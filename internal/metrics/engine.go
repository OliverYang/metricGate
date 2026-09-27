package metrics

import (
	"sort"
)

// Engine implements language-agnostic complexity scoring.
// It walks the IR and computes Cognitive Complexity (SonarSource/G.Ann Campbell)
// and Cyclomatic Complexity (McCabe) according to standardized rules.
//
// Scoring Model:
// - Every Function is measured independently: nesting resets to 0 at function boundary
// - Nested functions are reported as children, not inflating parent's score
// - File total = module-level code + every function at all depths
//
// Cyclomatic Complexity (McCabe):
// - Base 1 per function
// - +1 for each branch (if/else if), ternary, loop, non-default case, catch, null guard
// - +1 cyclomatic per extra operand in Logical node
//
// Cognitive Complexity (SonarSource):
// - +1 plus nesting bonus for: branch, ternary, switch, loop, catch
// - +1 flat (no bonus) for: else/else-if, labeled jumps, logical sequence, recursion
// - Nesting increases inside branch/ternary/switch/loop/catch bodies and nested functions
type Engine struct{}

// NewEngine creates a new complexity engine.
func NewEngine() *Engine {
	return &Engine{}
}

// AnalyzeFile scores a single file's IR and returns detailed metrics.
func (e *Engine) AnalyzeFile(path string, nodes []Node) FileReport {
	frame := e.newModuleFrame()
	e.walkNodes(nodes, frame)
	
	functions := frame.children
	var cognitive, cyclomatic uint32
	
	// Sum all functions (all depths)
	cognitive = frame.cognitive
	cyclomatic = frame.cyclomatic
	e.sumTree(functions, &cognitive, &cyclomatic)
	
	return FileReport{
		Path:       path,
		Cognitive:  cognitive,
		Cyclomatic: cyclomatic,
		Functions:  functions,
	}
}

// AnalyzeFiles scores multiple files and returns project-wide summary.
func (e *Engine) AnalyzeFiles(files map[string][]Node) Summary {
	fileReports := make([]FileReport, 0, len(files))
	
	for path, nodes := range files {
		report := e.AnalyzeFile(path, nodes)
		fileReports = append(fileReports, report)
	}
	
	return e.computeSummary(fileReports)
}

// Module frame accumulator for scoring.
type frame struct {
	name       string
	cognitive  uint32
	cyclomatic uint32
	nesting    uint32
	children   []FunctionReport
}

func (e *Engine) newModuleFrame() *frame {
	return &frame{
		name:    "<module>",
		nesting: 0,
	}
}

func (e *Engine) enterNesting(f *frame) {
	f.nesting++
}

func (e *Engine) leaveNesting(f *frame) {
	if f.nesting > 0 {
		f.nesting--
	}
}

func (e *Engine) addCognitive(f *frame, amount uint32) {
	f.cognitive += amount
}

func (e *Engine) addCyclomatic(f *frame) {
	f.cyclomatic++
}

func (e *Engine) walkNodes(nodes []Node, f *frame) {
	for _, node := range nodes {
		e.visit(node, f)
	}
}

func (e *Engine) visit(node Node, f *frame) {
	switch n := node.(type) {
	case Function:
		e.visitFunction(n, f)
	case Branch:
		e.visitBranch(n, f)
	case Conditional:
		e.visitConditional(n, f)
	case Loop:
		e.nested(true, n.Body, f)
	case Switch:
		e.visitSwitch(n, f)
	case Catch:
		e.nested(true, n.Body, f)
	case Jump:
		if n.Labeled {
			e.addCognitive(f, 1)
		}
	case Logical:
		e.visitLogical(n, f)
	case NullGuard:
		e.addCyclomatic(f)
		e.walkNodes(n.Body, f)
	case Call:
		e.visitCall(n, f)
	case Group:
		e.walkNodes([]Node(n), f)
	case ModuleLevel:
		e.walkNodes([]Node(n), f)
	default:
		// Unknown node type: treat as transparent group
	}
}

func (e *Engine) visitFunction(fn Function, parent *frame) {
	childFrame := &frame{
		name:       fn.Name,
		cognitive:  0,
		cyclomatic: 1, // McCabe base
		nesting:    0,
	}
	
	e.walkNodes(fn.Body, childFrame)
	
	report := FunctionReport{
		Name:       fn.Name,
		Kind:       fn.Kind,
		Line:       fn.Line,
		Cognitive:  childFrame.cognitive,
		Cyclomatic: childFrame.cyclomatic,
		Children:   childFrame.children,
	}
	
	parent.children = append(parent.children, report)
}

// Visit a branch (if/else). Consequent gets nesting bonus; alternate scored flat.
func (e *Engine) visitBranch(b Branch, f *frame) {
	nesting := f.nesting
	
	// Test condition (walk for nested constructs)
	e.walkNodes(b.Test, f)
	
	// Then body: +1 cognitive + nesting bonus
	e.addCognitive(f, 1+nesting)
	e.addCyclomatic(f)
	e.enterNesting(f)
	e.walkNodes(b.Then, f)
	e.leaveNesting(f)
	
	// Alternate: flat cognitive +1, no nesting bonus
	if b.Alternate != nil {
		e.visitAlternate(b.Alternate, f)
	}
}

func (e *Engine) visitAlternate(node Node, f *frame) {
	switch n := node.(type) {
	case Branch:
		// else-if: flat cognitive +1, cyclomatic +1, then recurse
		e.addCognitive(f, 1)
		e.addCyclomatic(f)
		
		e.walkNodes(n.Test, f)
		e.enterNesting(f)
		e.walkNodes(n.Then, f)
		e.leaveNesting(f)
		
		if n.Alternate != nil {
			e.visitAlternate(n.Alternate, f)
		}
	default:
		// Plain else: flat cognitive +1, body nested
		e.addCognitive(f, 1)
		e.enterNesting(f)
		e.visit(node, f)
		e.leaveNesting(f)
	}
}

func (e *Engine) visitConditional(c Conditional, f *frame) {
	nesting := f.nesting
	
	// Test
	e.walkNodes(c.Test, f)
	
	// +1 cognitive + nesting bonus
	e.addCognitive(f, 1+nesting)
	e.addCyclomatic(f)
	
	// Both branches nested
	e.enterNesting(f)
	e.walkNodes(c.Then, f)
	e.walkNodes(c.Alternate, f)
	e.leaveNesting(f)
}

func (e *Engine) visitSwitch(s Switch, f *frame) {
	nesting := f.nesting
	
	// +1 cognitive + nesting bonus (switch itself NOT cyclomatic)
	e.addCognitive(f, 1+nesting)
	e.enterNesting(f)
	
	for _, tc := range s.Cases {
		if !tc.IsDefault {
			e.addCyclomatic(f) // non-default case is decision point
		}
		e.walkNodes(tc.Body, f)
	}
	
	e.leaveNesting(f)
}

func (e *Engine) visitLogical(l Logical, f *frame) {
	// +1 cognitive for the sequence
	e.addCognitive(f, 1)
	
	// +1 cyclomatic per extra operand (operands >= 2)
	if len(l.Operands) > 1 {
		e.addCyclomatic(f)
	}
	
	// Walk operands for nested constructs
	for _, op := range l.Operands {
		e.visit(op, f)
	}
}

func (e *Engine) visitCall(c Call, f *frame) {
	// Recursion detection: call matches enclosing function name
	if c.Callee != nil && f.name != "<module>" && f.name == *c.Callee {
		e.addCognitive(f, 1)
	}
}

// Nested construct: +1 cognitive + nesting bonus, optionally +1 cyclomatic, then body with nesting raised.
func (e *Engine) nested(addCyclomatic bool, body []Node, f *frame) {
	nesting := f.nesting
	
	e.addCognitive(f, 1+nesting)
	if addCyclomatic {
		e.addCyclomatic(f)
	}
	
	e.enterNesting(f)
	e.walkNodes(body, f)
	e.leaveNesting(f)
}

// Sum every function (all depths) into running totals.
func (e *Engine) sumTree(functions []FunctionReport, cog *uint32, cyc *uint32) {
	for _, fn := range functions {
		*cog += fn.Cognitive
		*cyc += fn.Cyclomatic
		e.sumTree(fn.Children, cog, cyc)
	}
}

// Compute project-wide summary from file reports.
func (e *Engine) computeSummary(reports []FileReport) Summary {
	var cogValues, cycValues []uint32
	var parseErrorCount int
	var parseErrorFiles []string
	
	for _, r := range reports {
		e.collectMetrics(r.Functions, &cogValues, &cycValues)
		
		if len(r.ParseErrors) > 0 {
			parseErrorCount += len(r.ParseErrors)
			parseErrorFiles = append(parseErrorFiles, r.Path)
		}
	}
	
	return Summary{
		FileCount:           len(reports),
		FunctionCount:       len(cogValues),
		ParseErrorCount:     parseErrorCount,
		ParseErrorFileCount: len(parseErrorFiles),
		ParseErrorFiles:     parseErrorFiles,
		Cognitive:           e.metricSummary(cogValues),
		Cyclomatic:          e.metricSummary(cycValues),
	}
}

func (e *Engine) collectMetrics(functions []FunctionReport, cog *[]uint32, cyc *[]uint32) {
	for _, fn := range functions {
		*cog = append(*cog, fn.Cognitive)
		*cyc = append(*cyc, fn.Cyclomatic)
		e.collectMetrics(fn.Children, cog, cyc)
	}
}

func (e *Engine) metricSummary(values []uint32) MetricSummary {
	if len(values) == 0 {
		return MetricSummary{}
	}
	
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})
	
	n := len(values)
	return MetricSummary{
		Sum:      0,
		Max:      values[n-1],
		Median:   e.percentile(values, 50.0),
		P90:      e.percentile(values, 90.0),
		P95:      e.percentile(values, 95.0),
	}
}

func (e *Engine) percentile(sorted []uint32, p float64) uint32 {
	if len(sorted) == 0 {
		return 0
	}
	
	rank := ((p / 100.0) * float64(len(sorted)))
	rankCeil := uint64(rank)
	if rank > float64(rankCeil) {
		rankCeil++
	}
	
	idx := int(rankCeil) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	
	// Sum calculation
	sum := uint32(0)
	for _, v := range sorted {
		sum += v
	}
	
	return sorted[idx]
}
