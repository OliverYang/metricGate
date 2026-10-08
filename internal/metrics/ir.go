// Package metrics provides language-agnostic complexity metrics.
// The IR (Intermediate Representation) layer decouples AST parsing from
// scoring logic, so a language's grammar knowledge lives in exactly one
// adapter and every scoring rule lives in exactly one engine.
package metrics

// SwitchCase represents one arm of a switch statement.
//
// IsDefault marks the arm that opens no new decision path (Go default_case,
// Java `default:` switch_label).
type SwitchCase struct {
	IsDefault bool
	Body      []Node
}

// Node is the language-agnostic intermediate representation for complexity
// analysis. Nodes carry shape only — every scoring rule lives in engine.go, so
// a construct's cost is defined exactly once for all languages.
//
// Adapters must lower every child of every AST node: a construct an adapter
// does not recognize has to surface as Group, or inside another node's Test,
// Init or Body — never as nothing. Dropped children silently lose complexity,
// which is how the first draft of these adapters under-scored every switch.
type Node interface {
	isNode()
}

// Function is one lowered function-like unit: what an adapter produces from a
// declaration node and what the Engine scores. It is deliberately not a Node —
// a unit is the thing being measured, never something measured inside another.
// Declarations nested inside a body (a Java anonymous class's method, say)
// lower transparently, so their statements are charged to the enclosing unit.
type Function struct {
	Name string // function name, as the report labels it
	Kind string // "function", "method", "constructor", ...
	Line uint32 // 1-based line where the declaration starts
	Body []Node
}

// Lambda represents an inline function body (Go func_literal, Java
// lambda_expression) whose statements are scored into the *enclosing*
// function, one nesting level deeper.
type Lambda struct {
	Body []Node
}

func (Lambda) isNode() {}

// Branch represents an if statement, with its else / else-if chain reachable
// through Alternate (a nested Branch for `else if`, otherwise the else body).
type Branch struct {
	Test      []Node
	Then      []Node
	Alternate Node // nil when the if has no else
}

func (Branch) isNode() {}

// Conditional represents an expression-level choice whose value is one of two
// arms: the Java ternary, and in future languages a match expression.
type Conditional struct {
	Test      []Node
	Then      []Node
	Alternate []Node
}

func (Conditional) isNode() {}

// Loop represents any loop construct (for/while/do-while/foreach/range).
// Init carries the header expressions (iterator, condition, range source);
// they sit inside the loop's nesting level, like the body.
type Loop struct {
	Init []Node
	Body []Node
}

func (Loop) isNode() {}

// Switch represents a switch/select/match statement. Init carries the
// scrutinee and any initializer, which sit inside the switch's nesting level.
type Switch struct {
	Init  []Node
	Cases []SwitchCase
}

func (Switch) isNode() {}

// Catch represents an exception handler (catch/except/rescue clause).
type Catch struct {
	Body []Node
}

func (Catch) isNode() {}

// Logical represents a run of like logical operators, already folded: `a &&
// b && c` is one Logical holding three operands. A nested run of a *different*
// operator appears as an operand that is itself a Logical. The operator itself
// is not carried: every run costs the same whatever it joins.
type Logical struct {
	Operands []Node // at least 2 operands
}

func (Logical) isNode() {}

// Group is a transparent container: the engine recurses into it and scores
// nothing. Adapters use it for statements, blocks and expression wrappers.
type Group []Node

func (Group) isNode() {}

// FunctionReport is the engine's verdict on one Function.
type FunctionReport struct {
	Name       string
	Kind       string
	Line       uint32 // 1-based
	Cognitive  uint32
	Cyclomatic uint32
	MaxNesting uint32
	NPath      uint64 // acyclic execution paths (Nejmeh 1988)
}
