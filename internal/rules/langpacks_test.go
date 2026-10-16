package rules

import (
	"testing"

	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// The three rule packs added for SQL, Verilog and VB.NET. Each one is
// exercised through ForLanguage (via runRules), because the dispatch is the
// place a new language is most likely to be forgotten in, and each rule's
// query is written against a grammar that differs sharply from the ones the
// other ten languages use — SQL has no block node, Verilog has no fields at
// all, and VB.NET hides its condition as an ordinary child.
//
// Every fixture below was first checked against the grammar with the
// throwaway probe (go run ./tmpprobe2), because a query that does not match
// is a rule that silently never fires — the one failure mode a passing test
// suite will not catch on its own.

// --- SQL -------------------------------------------------------------------

// SQL's empty routine body is a grammar hole: `BEGIN END` with nothing
// between parses as ERROR, so there is nothing for the rule to see. The
// reachable empty-block shape is an empty WHILE, which is what the fixture
// uses. A body holding only a comment stays quiet too, because the rule
// counts statements rather than named children.
const sqlEmptyWhileSrc = `CREATE FUNCTION f(a int, b int) RETURNS int AS $$
BEGIN
    WHILE a < b BEGIN
    END;
    RETURN a;
END;
$$ LANGUAGE sql;`

const sqlFilledBodySrc = `CREATE FUNCTION f(a int) RETURNS int AS $$
BEGIN
    RETURN a;
END;
$$ LANGUAGE sql;`

func TestSQLEmptyBlock(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangSQL, sqlEmptyWhileSrc), "empty-block")
	if len(fs) != 1 {
		t.Fatalf("empty-block = %d, want 1 (the empty WHILE); got %+v", len(fs), fs)
	}
	// A body whose only work is a RETURN must stay quiet too: the grammar
	// keeps RETURN outside `statement`, so this is the case a
	// statement-only emptiness test gets wrong (it would flag the routine
	// in sqlFilledBodySrc).
	fs = findingsByRule(runRules(t, parser.LangSQL, sqlFilledBodySrc), "empty-block")
	if len(fs) != 0 {
		t.Fatalf("non-empty body flagged: %+v", fs)
	}
}

// SQL spells its two comment forms as two different node kinds, so a todo
// rule that only knows `comment` would miss every block comment.
// TestSQLTodoComment turns the rule on explicitly: todo-comment ships off by
// default, so runRules (policy nil) would silently return nothing and the
// test would pass while the query was broken.
func TestSQLTodoComment(t *testing.T) {
	src := `-- TODO: drop this
CREATE TABLE t (id int);

/* FIXME: index later */
CREATE VIEW v AS SELECT 1;`
	rules := SQLRules()
	fs := findingsByRule(runnerFor(t, parser.LangSQL, rules, allEnabled{})(t, src), "todo-comment")
	if len(fs) != 2 {
		t.Fatalf("todo-comment = %d, want 2 (line and block comment); got %+v", len(fs), fs)
	}
}

func TestSQLChainDepth(t *testing.T) {
	// Four nested calls: the outermost exceeds the default max_depth of 3.
	src := `CREATE FUNCTION f(a int) RETURNS int AS $$
BEGIN
    RETURN one(two(three(four(a))));
END;
$$ LANGUAGE sql;`
	fs := findingsByRule(runRules(t, parser.LangSQL, src), "call-chain-depth")
	if len(fs) != 1 {
		t.Fatalf("call-chain-depth = %d, want 1; got %+v", len(fs), fs)
	}
}

// A SQL call ban matches the bare and the schema-qualified spelling
// separately, because the grammar marks the difference with the presence of
// the `schema` field rather than with text.
func TestSQLCallBanSpellings(t *testing.T) {
	src := `CREATE FUNCTION f(a int) RETURNS int AS $$
BEGIN
    RETURN drop_all(a) + pg_catalog.drop_all(a);
END;
$$ LANGUAGE sql;`

	bare := BannedRules("sql", []Ban{{Kind: BanCall, Match: "drop_all"}})
	if len(bare) != 1 {
		t.Fatalf("bare ban rules = %d, want 1", len(bare))
	}
	fs := newTestRunner(t, parser.LangSQL, bare)(t, src)
	if len(fs) != 1 {
		t.Fatalf("bare ban fired %d times, want 1 (the unqualified call only); got %+v", len(fs), fs)
	}

	qualified := BannedRules("sql", []Ban{{Kind: BanCall, Match: "pg_catalog.drop_all"}})
	if len(qualified) != 1 {
		t.Fatalf("qualified ban rules = %d, want 1", len(qualified))
	}
	fs = newTestRunner(t, parser.LangSQL, qualified)(t, src)
	if len(fs) != 1 {
		t.Fatalf("qualified ban fired %d times, want 1; got %+v", len(fs), fs)
	}
}

// --- Verilog ---------------------------------------------------------------

const verilogEmptyBlockSrc = `module m;
  function automatic int f(int a);
    if (a > 0) begin
    end
    return a;
  endfunction
endmodule`

func TestVerilogEmptyBlock(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangVerilog, verilogEmptyBlockSrc), "empty-block")
	if len(fs) != 1 {
		t.Fatalf("empty-block = %d, want 1; got %+v", len(fs), fs)
	}
}

const verilogIncludeSrc = "`include \"local.vh\"\n`include <vendor_lib.svh>\n\nmodule m;\nendmodule\n"

// The quoted include is a file beside the source and must never be reported
// as an undeclared dependency; the angle-bracket form is the one a policy
// can name, so it is the one the rule flags.
func TestVerilogUnknownInclude(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangVerilog, verilogIncludeSrc), "unknown-dependency")
	if len(fs) != 1 {
		t.Fatalf("unknown-dependency = %d, want 1 (the angle-bracket include); got %+v", len(fs), fs)
	}
	if !contains(fs[0].Message, "vendor_lib") {
		t.Errorf("message %q should name the angle-bracket header", fs[0].Message)
	}
}

func TestVerilogChainDepth(t *testing.T) {
	src := `module m;
  function automatic int f(int a);
    return one(two(three(four(a))));
  endfunction
endmodule`
	fs := findingsByRule(runRules(t, parser.LangVerilog, src), "call-chain-depth")
	if len(fs) != 1 {
		t.Fatalf("call-chain-depth = %d, want 1; got %+v", len(fs), fs)
	}
}

// A Verilog ban reaches the system tasks too: `$display` is spelled with a
// leading $ that the ban spelling does not carry.
func TestVerilogCallBanSystemTask(t *testing.T) {
	src := `module m;
  function automatic int f(int a);
    $display("a=%d", a);
    return a;
  endfunction
endmodule`
	rules := BannedRules("verilog", []Ban{{Kind: BanCall, Match: "display"}})
	if len(rules) != 1 {
		t.Fatalf("ban rules = %d, want 1", len(rules))
	}
	fs := newTestRunner(t, parser.LangVerilog, rules)(t, src)
	if len(fs) != 1 {
		t.Fatalf("$display ban fired %d times, want 1; got %+v", len(fs), fs)
	}
}

// --- VB.NET ----------------------------------------------------------------

const vbEmptyBlockSrc = `Module M
    Sub S(x As Integer)
        If x > 0 Then
        End If
        While x > 0
        End While
    End Sub
End Module`

// A VB `If` keeps its condition as a named child, so the stock emptiness
// test would never fire; both constructs here must be reported.
func TestVBEmptyBlock(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangVB, vbEmptyBlockSrc), "empty-block")
	if len(fs) != 2 {
		t.Fatalf("empty-block = %d, want 2 (If and While); got %+v", len(fs), fs)
	}
}

const vbImportSrc = `Imports System
Imports System.Collections.Generic
Imports MyApp.Core

Module M
End Module`

func TestVBUnknownDependency(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangVB, vbImportSrc), "unknown-dependency")
	if len(fs) != 1 {
		t.Fatalf("unknown-dependency = %d, want 1 (MyApp.Core); got %+v", len(fs), fs)
	}
	if !contains(fs[0].Message, "MyApp.Core") {
		t.Errorf("message %q should name the namespace", fs[0].Message)
	}
}

func TestVBChainDepth(t *testing.T) {
	src := `Module M
    Sub S()
        Dim r = a.B().C().D().E()
    End Sub
End Module`
	fs := findingsByRule(runRules(t, parser.LangVB, src), "call-chain-depth")
	if len(fs) != 1 {
		t.Fatalf("call-chain-depth = %d, want 1; got %+v", len(fs), fs)
	}
}

// A bare ban matches only the unqualified spelling, exactly as it does in
// every other language; reaching a qualified call needs the receiver in the
// policy entry. Both halves are asserted so the split cannot drift.
func TestVBCallBanBareAndReceiver(t *testing.T) {
	src := `Module M
    Sub S()
        Danger()
        Me.Danger()
        Safe()
    End Sub
End Module`

	bare := BannedRules("vbnet", []Ban{{Kind: BanCall, Match: "Danger"}})
	fs := newTestRunner(t, parser.LangVB, bare)(t, src)
	if len(fs) != 1 {
		t.Fatalf("bare Danger ban fired %d times, want 1 (the unqualified call only); got %+v", len(fs), fs)
	}

	qualified := BannedRules("vbnet", []Ban{{Kind: BanCall, Match: "Me.Danger"}})
	fs = newTestRunner(t, parser.LangVB, qualified)(t, src)
	if len(fs) != 1 {
		t.Fatalf("Me.Danger ban fired %d times, want 1; got %+v", len(fs), fs)
	}
}

func TestVBConstructorBan(t *testing.T) {
	src := `Module M
    Sub S()
        Dim x = New ForbiddenThing()
        Dim y = New AllowedThing()
    End Sub
End Module`
	rules := BannedRules("vbnet", []Ban{{Kind: BanConstructor, Match: "ForbiddenThing"}})
	if len(rules) != 1 {
		t.Fatalf("ban rules = %d, want 1", len(rules))
	}
	fs := newTestRunner(t, parser.LangVB, rules)(t, src)
	if len(fs) != 1 {
		t.Fatalf("constructor ban fired %d times, want 1; got %+v", len(fs), fs)
	}
}

// newTestRunner compiles a hand-built rule list against a language's grammar
// and returns a function that runs it over a source string. The compiled-ban
// tests need this because BannedRules takes bans directly rather than a
// policy config, and going through runRules would re-add the built-in pack.
func newTestRunner(t *testing.T, lang parser.LanguageID, all []*Rule) func(*testing.T, string) []model.Finding {
	t.Helper()
	return runnerFor(t, lang, all, nil)
}

// allEnabled is a Policy that turns every rule on, including the ones that
// ship off by default (todo-comment).
type allEnabled struct{}

func (allEnabled) RuleSeverity(lang, ruleID string) string { return "" }
func (allEnabled) RuleEnabled(lang, ruleID string) *bool {
	on := true
	return &on
}
func (allEnabled) RuleOptions(lang, ruleID string) map[string]any { return nil }

// runnerFor compiles rules and returns a run function with the given policy.
func runnerFor(t *testing.T, lang parser.LanguageID, all []*Rule, p Policy) func(*testing.T, string) []model.Finding {
	t.Helper()
	p2, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p2.Close)
	runner, err := NewRunner(string(lang), p2.Lang, all)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Close)
	return func(t *testing.T, src string) []model.Finding {
		t.Helper()
		tree, release := parsetest.Parse(t, lang, src)
		defer release()
		return runner.Run("t."+string(lang), []byte(src), tree, p)
	}
}
