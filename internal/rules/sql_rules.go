package rules

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// SQLRules is the built-in SQL rule pack. Banned APIs live in policy
// (languages.sql.banned) and are compiled by the engine, not here.
//
// SQL's grammar is the weakest of the languages here: `IF ... THEN` does
// not parse at all, `PERFORM`/`CALL`/`SET`/`RAISE` inside a routine body
// fall out as ERROR, and only `WHILE` and the `CASE` expression are real
// control flow. The pack therefore carries only the rules whose queries
// were verified against the grammar; the missing coverage is recorded in
// docs/metrics.md rather than papered over with a rule that can never fire.
func SQLRules() []*Rule {
	return []*Rule{
		sqlEmptyBlock(),
		todoCommentRule("[(comment) (marginalia)] @c"),
		sqlChainDepth(),
	}
}

// sqlEmptyBlock flags routine bodies that contain no statement. The stock
// emptiness test (no named children) is wrong here twice over: SQL has no
// block node at all — a routine body is one `function_body` /
// `procedure_body` node that also holds `keyword_as`, the dollar quoting
// and the BEGIN/END keywords — so emptiness has to be counted from
// `statement` children instead. A body holding only a comment still counts
// as empty.
func sqlEmptyBlock() *Rule {
	return emptyBlockRule(`
(function_body) @blk
(procedure_body) @blk
(while_statement) @blk
`, sqlBlockIsEmpty)
}

// sqlBlockIsEmpty reports whether a SQL routine body or WHILE statement has
// no statement of its own. Keywords and the condition expression are named
// children too, so child count says nothing; only a nested statement or a
// RETURN counts as work. RETURN needs naming separately because the grammar
// keeps it outside `statement` (a body holding only `RETURN a;` is otherwise
// indistinguishable from `BEGIN END`).
func sqlBlockIsEmpty(blk *sitter.Node) bool {
	for i := uint(0); i < blk.NamedChildCount(); i++ {
		switch blk.NamedChild(i).Kind() {
		case "statement", "block", "keyword_return":
			return false
		}
	}
	return true
}

// sqlChainDepth flags nested call chains deeper than max_depth. SQL spells
// a call `invocation`; a nested call hides inside the `term` wrapper of the
// outer invocation's parameter field, so the depth walk crosses both hops
// and nestedInChain keeps only the outermost link reportable.
func sqlChainDepth() *Rule {
	return chainDepthRule(`
(invocation) @chain
`, func(ctx *MatchCtx) int {
		// The matched node is itself a call: count it, then each inner
		// invocation the chain descends into. upper(trim(lower(x))) = 3.
		depth := 1
		for cur := sqlInnerCall(ctx.Captures["chain"]); cur != nil; cur = sqlInnerCall(cur) {
			depth++
		}
		return depth
	})
}

// sqlInnerCall returns the invocation nested in a call's argument, or nil.
// The argument is wrapped in a `term` node, so the walk goes
// parameter -> term -> value -> invocation.
func sqlInnerCall(n *sitter.Node) *sitter.Node {
	param := n.ChildByFieldName("parameter")
	if param == nil {
		return nil
	}
	if param.Kind() == "invocation" {
		return param
	}
	for i := uint(0); i < param.NamedChildCount(); i++ {
		if c := param.NamedChild(i); c.Kind() == "invocation" {
			return c
		}
	}
	return nil
}

// sqlCallBare and sqlCallRecv are the two spellings a SQL call can take.
// The grammar puts the callee in a single `object_reference` whose `schema`
// field is filled only when the source wrote the qualified form, so one
// query per spelling with `!schema` / `schema:` separating them is exact.
// The argument is the `term` wrapper around whatever was passed.
const (
	sqlCallBare = `(invocation
    (object_reference !schema name: (identifier) @fn)
    (term) @args) @call`
	sqlCallRecv = `(invocation
    (object_reference schema: (identifier) @recv name: (identifier) @fn)
    (term) @args) @call`
)

// sqlCallBan builds a call ban for SQL's `invocation` node. A bare policy
// entry matches only the unqualified spelling and a dotted one only the
// schema-qualified spelling, which is exactly how the grammar separates
// them — one query per shape keeps the receiver comparison identical to
// every other language's.
func sqlCallBan(b Ban) *Rule {
	return langCallBan(b, sqlCallBare, sqlCallRecv)
}

// sqlImportBan reports nothing: SQL has no import statement, and a schema
// reference is not one. Registered all the same so a policy that names SQL
// imports fails at compile time rather than silently matching nothing.
func sqlImportBan(b Ban) *Rule {
	return callRule(b, `(create_schema name: (identifier) @fn) @call`, func(ctx *MatchCtx) (string, bool) {
		return matchedName(ctx, b.Match, b)
	})
}
