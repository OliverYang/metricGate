// Package metrics provides language-agnostic complexity metrics.
// The IR (Intermediate Representation) layer decouples AST parsing from
// scoring logic, enabling easy addition of new languages.
package metrics

// LogicalOp represents normalized logical operators across languages.
type LogicalOp string

const (
	LogicalAnd        LogicalOp = "&&"
	LogicalOr         LogicalOp = "||"
	LogicalCoalesce   LogicalOp = "??"
)

// SwitchCase represents one arm of a switch statement.
type SwitchCase struct {
	IsDefault bool   // true for default/arm without decision
	Body      []Node // body nodes
}

// Node is the language-agnostic intermediate representation for complexity analysis.
// Each node type maps to specific cognitive/cyclomatic scoring rules defined in
// the engine package. All scoring logic lives in engine.Analyze(); this IR carries
// no scores itself.
//
// Design principles:
// - Every construct that affects complexity has its own variant
// - Transparent containers (Group) recurse without scoring
// - Functions are measured independently (nesting resets at function boundary)
// - Nested functions appear as children, not inflating parent's score
type Node interface {
	isNode()
}

// Function represents a function-like unit (function, method, closure, lambda, etc.).
// The engine scores each independently; nesting resets to 0 at this boundary.
type Function struct {
	Name string   // function name (opaque to engine)
	Kind string   // "function", "method", "closure", "lambda", etc.
	Line uint32   // 1-based line number where function starts
	Body []Node   // body nodes
}

func (Function) isNode() {}

// Branch represents an if/else or else-if chain.
// Scoring: +1 cognitive + nesting bonus for 'then', +1 flat cognitive for 'alternate'.
type Branch struct {
	Test      []Node // condition expression
	Then      []Node // consequent body (nested)
	Alternate Node   // else or else-if (nil if none)
}

func (Branch) isNode() {}

// Conditional represents a ternary expression (?:).
// Scoring: +1 cognitive + nesting bonus.
type Conditional struct {
	Test      []Node // condition
	Then      []Node // true branch
	Alternate []Node // false branch
}

func (Conditional) isNode() {}

// Loop represents any loop construct (for/while/do-while/foreach/range).
// Scoring: +1 cognitive + nesting bonus, +1 cyclomatic.
type Loop struct {
	Body []Node
}

func (Loop) isNode() {}

// Switch represents a switch/select/match statement.
// Scoring: +1 cognitive + nesting bonus (switch itself is NOT cyclomatic);
// each non-default case adds +1 cyclomatic.
type Switch struct {
	Cases []SwitchCase
}

func (Switch) isNode() {}

// Catch represents an exception handler (try/catch/except).
// Scoring: +1 cognitive + nesting bonus, +1 cyclomatic.
type Catch struct {
	Body []Node
}

func (Catch) isNode() {}

// Jump represents a labeled break/continue/goto.
// Scoring: +1 cognitive only if labeled.
type Jump struct {
	Labeled bool // true for labeled jumps
}

func (Jump) isNode() {}

// Logical represents a sequence of like logical operators.
// Scoring: +1 cognitive per node, +1 cyclomatic per extra operand.
// Adapters must fold runs of same-operator into single node (e.g., a && b && c -> one Logical with 3 operands).
type Logical struct {
	Op       LogicalOp
	Operands []Node // at least 2 operands
}

func (Logical) isNode() {}

// NullGuard represents explicit null-dependent control flow splits.
// Examples: optional chaining (?->), safe navigation (&.), null-aware access.
// Scoring: +1 cyclomatic per guard, NO cognitive/nesting impact.
type NullGuard struct {
	Body []Node
}

func (NullGuard) isNode() {}

// Call represents a function call for recursion detection.
// Scoring: +1 cognitive if callee matches enclosing function name (recursion).
type Call struct {
	Callee *string // function name being called (nil if unknown)
}

func (Call) isNode() {}

// Group is a transparent container for constructs that hold children but carry
// no score of their own (statements, expressions, blocks). The engine simply
// recurses into it.
type Group []Node

func (Group) isNode() {}

// ModuleLevel represents top-level code in a file (module scope).
// The engine wraps module-level nodes under an implicit "module" frame.
type ModuleLevel []Node

func (ModuleLevel) isNode() {}

// FileReport represents the analysis result for a single source file.
type FileReport struct {
	Path            string // file path (relative to project root)
	Cognitive       uint32 // total cognitive complexity
	Cyclomatic      uint32 // total cyclomatic complexity
	Functions       []FunctionReport // all functions at all nesting depths
	ParseErrors     []string // parser errors (if any)
}

// FunctionReport represents metrics for a single function-like unit.
type FunctionReport struct {
	Name       string            // function name
	Kind       string            // "function", "method", "closure", etc.
	Line       uint32            // 1-based line number
	Cognitive  uint32            // cognitive complexity score
	Cyclomatic uint32            // cyclomatic complexity score
	Children   []FunctionReport  // nested functions (independent scores)
}

// Summary represents project-wide rollup statistics across all files.
// Complexity is right-skewed, so percentiles (not mean/stddev) describe the tail.
type Summary struct {
	FileCount         int
	FunctionCount     int
	ParseErrorCount   int
	ParseErrorFileCount int
	ParseErrorFiles   []string // paths of files with parse errors
	Cognitive         MetricSummary
	Cyclomatic        MetricSummary
}

// MetricSummary describes distribution of one metric over all functions.
type MetricSummary struct {
	Sum      uint32 // total sum
	Max      uint32 // maximum value
	Median   uint32 // 50th percentile
	P90      uint32 // 90th percentile
	P95      uint32 // 95th percentile
}
