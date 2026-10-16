package metrics

import (
	"testing"

	"metricgate/internal/parser"
)

// SQL is the odd one out among the supported languages: a routine's body is a
// child node (function_body / procedure_body) rather than a `body` field, its
// name lives inside an object_reference rather than a `name` field, and its
// only two control-flow constructs are WHILE and CASE. Each of those is a
// place where a generic path would silently produce zero, so each gets a test.

// cyclomatic = 1 (base) + 1 while + 2 case arms (the ELSE arm is the default)
// cognitive = while(1) + case(1)
// maxNesting = 1 (the case sits inside the while's body)
const sqlRoutineSrc = `CREATE FUNCTION grade(score int) RETURNS text AS $$
BEGIN
  WHILE score < 0 BEGIN RETURN 'F'; END;
  RETURN CASE WHEN score >= 90 THEN 'A' WHEN score >= 80 THEN 'B' ELSE 'F' END;
END;
$$ LANGUAGE sql;
`

func TestSQLFunctionMetrics(t *testing.T) {
	fs := analyze(t, parser.LangSQL, sqlRoutineSrc)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	fn := fs.Functions[0]
	if fn.Name != "grade" {
		t.Errorf("name = %q, want grade (it lives in object_reference, not a name field)", fn.Name)
	}
	if fn.Params != 1 {
		t.Errorf("params = %d, want 1", fn.Params)
	}
	if fn.Cyclomatic != 4 {
		t.Errorf("cyclomatic = %d, want 4", fn.Cyclomatic)
	}
	if fn.Cognitive != 2 {
		t.Errorf("cognitive = %d, want 2", fn.Cognitive)
	}
	if fn.MaxNesting != 1 {
		t.Errorf("maxNesting = %d, want 1", fn.MaxNesting)
	}
	if fn.NPath == 0 {
		t.Error("npath = 0; the routine body was not lowered")
	}
	if fn.Volume == 0 {
		t.Error("volume = 0; the routine body was not found")
	}
}

// A table is a schema declaration, not behaviour: it must not be measured as a
// function, and it must not be counted as a class either — CK is not
// advertised for SQL.
func TestSQLDeclarationsAreNotFunctions(t *testing.T) {
	src := `CREATE TABLE users (id int, name text);
CREATE INDEX idx_users_name ON users (name);
CREATE VIEW active_users AS SELECT id FROM users;
CREATE PROCEDURE bump(IN n int) AS $$
BEGIN
  DELETE FROM audit WHERE id = n;
END;
$$ LANGUAGE sql;
`
	fs := analyze(t, parser.LangSQL, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1 (the procedure); got %+v", len(fs.Functions), fs.Functions)
	}
	if fs.Functions[0].Name != "bump" {
		t.Errorf("name = %q, want bump", fs.Functions[0].Name)
	}
	if len(fs.Classes) != 0 {
		t.Errorf("classes = %d, want 0", len(fs.Classes))
	}
	if HasClasses("sql") {
		t.Error("HasClasses(sql) = true; SQL has no class construct to measure")
	}
}

// Fan-out has to read the callee off the first named child: an invocation
// spelled `fn(a)` leaves the `unit` field unset, and only the FROM-variant of
// the grammar fills it.
func TestSQLFanOut(t *testing.T) {
	src := `CREATE FUNCTION f(uid int) RETURNS int AS $$
BEGIN
  RETURN normalize(uid) + normalize(uid);
END;
$$ LANGUAGE sql;
`
	fs := analyze(t, parser.LangSQL, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	if fs.Functions[0].FanOut != 1 {
		t.Errorf("fanOut = %d, want 1 (one distinct callee called twice)", fs.Functions[0].FanOut)
	}
}
