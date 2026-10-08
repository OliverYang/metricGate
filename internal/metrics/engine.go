package metrics

// This is the only place a complexity number is decided; the language
// adapters only say what a construct *is*, never what it costs.
//
// The rules below are metricgate's own documented variant of the SonarSource
// white paper (G. Ann Campbell), not a literal implementation of it:
// docs/metrics.md is the spec, and the divergences listed there are deliberate.
// Changing a number here changes every stored verdict, so treat it as a policy
// change rather than a fix.
//
// Cognitive — "flat" means no nesting bonus and no nesting increase:
//
//	Branch (if)   +1 + nesting; condition, both bodies and the whole
//	              else / else-if chain nested one level deeper
//	Loop          +1 + nesting; header and body nested
//	Switch        +1 + nesting; scrutinee and arms nested
//	Catch         +1 flat
//	Logical run   +1 flat per run of identical operators
//	Lambda        nothing; body nested one level
//
// Cyclomatic — base 1 per function, then +1 each for: Branch, Loop,
// non-default switch arm, ternary, and every operator in a logical run.
//
// Deviations from the white paper, kept on purpose:
//   - an else-if chain pays the nesting increment at every link, so long
//     chains score above the flat switch this repo would rather you wrote;
//   - a ternary is a cyclomatic path but no cognitive decision;
//   - a catch clause adds no cyclomatic path, matching what the Java numbers
//     have always measured.
//
// Labeled jumps and recursion are cognitive points in the white paper and are
// not counted here (docs/metrics.md, "not counted in v0"); the adapters
// therefore do not lower them either.
func analyzeFunction(fn Function) FunctionReport {
	f := &frame{cyclomatic: 1} // McCabe base
	f.walkNodes(fn.Body)
	return FunctionReport{
		Name:       fn.Name,
		Kind:       fn.Kind,
		Line:       fn.Line,
		Cognitive:  f.cognitive,
		Cyclomatic: f.cyclomatic,
		MaxNesting: f.maxNesting,
		NPath:      npathNodes(fn.Body),
	}
}

// — NPATH (Nejmeh, CACM 31(2) 1988, doi 10.1145/42372.42379) —
//
// The number of acyclic execution paths through a function. Unlike the
// additive cyclomatic score, sequential statements MULTIPLY: two independent
// ternaries give 2×2, three 6-arm switches in a loop give 6³+1 — which is
// exactly the blow-up cyclomatic hides and why an NPATH limit (200, AT&T)
// catches the “every part looks small” monolith.
//
// Formulas follow the checkstyle reference table:
//
//	if (c) A            NP(A) + ops(c) + 1
//	if (c) A else B     NP(A) + NP(B) + ops(c)
//	for/while/do        NP(body) + ops(header) + 1
//	switch              Σ NP(arms) + ops(scrutinee)   (min 1: empty switch)
//	ternary             2 + ops(test) + NP-of-nested-ternaries
//	logical a&&b&&c     one point per operator, counted in condition slots
//	statement           1, times the NP of every ternary it contains
//
// Documented deviations (this repo's variant, like the cyclomatic ones
// above): a catch clause opens no extra path (same convention the stored
// cyclomatic numbers already use), a lambda body is scored into the
// enclosing function (paths multiply through it), and a ternary inside a
// condition slot contributes its 2 paths even though the original table
// counts only logical operators there — a value selection is a real path.

// npathSaturate caps the product so a pathological function cannot overflow;
// thresholds are three digits, so the exact value past the cap is irrelevant.
const npathSaturate = uint64(1) << 62

func npathMul(a, b uint64) uint64 {
	if a > npathSaturate/b {
		return npathSaturate
	}
	return a * b
}

// npathNodes returns the product of the sequence's statement factors — the
// “Function declaration = P(i=1..N) NP(Statement[i])” row of the table.
func npathNodes(nodes []Node) uint64 {
	np := uint64(1)
	for _, node := range nodes {
		np = npathMul(np, npathNode(node))
	}
	return np
}

func npathNode(n Node) uint64 {
	switch n := n.(type) {
	case Lambda:
		return npathNodes(n.Body)
	case Branch:
		np := npathNodes(n.Then) + npathCond(n.Test) + 1
		if n.Alternate != nil {
			np += npathNodes([]Node{n.Alternate}) - 1
		}
		return np
	case Loop:
		return npathNodes(n.Body) + npathCond(n.Init) + 1
	case Switch:
		sum := uint64(0)
		for _, tc := range n.Cases {
			sum += npathNodes(tc.Body)
		}
		if sum == 0 {
			sum = 1 // an empty switch still has exactly one path
		}
		return sum + npathCond(n.Init)
	case Conditional:
		return 2 + npathCond(n.Test) + npathNestedTernaries(n.Then) + npathNestedTernaries(n.Alternate)
	case Catch:
		return npathNodes(n.Body) // no extra path; see the deviation note
	case Group:
		return npathNodes([]Node(n))
	case Logical:
		return 1 // operators only count in condition slots (npathCond)
	}
	return 1
}

// npathCond is the condition-slot term: one point per logical operator plus
// the paths of any ternaries selectioning inside the condition.
func npathCond(nodes []Node) uint64 {
	var ops, tern uint64
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, node := range nodes {
			switch n := node.(type) {
			case Logical:
				if len(n.Operands) > 1 {
					ops += uint64(len(n.Operands) - 1)
				}
				walk(n.Operands)
			case Conditional:
				tern += npathNode(n)
			case Group:
				walk([]Node(n))
			}
		}
	}
	walk(nodes)
	return ops + tern
}

// npathNestedTernaries sums the additive NP of ternaries nested in a
// ternary's arms (the NP(e2)/NP(e3) terms of the checkstyle formula).
func npathNestedTernaries(nodes []Node) uint64 {
	var sum uint64
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, node := range nodes {
			switch n := node.(type) {
			case Conditional:
				sum += npathNode(n)
			case Group:
				walk([]Node(n))
			case Logical:
				walk(n.Operands)
			}
		}
	}
	walk(nodes)
	return sum
}

// frame accumulates one function's score while the engine walks it.
type frame struct {
	cognitive  uint32
	cyclomatic uint32
	nesting    uint32
	maxNesting uint32
}

// enter raises the nesting level for the rest of a construct's subtree. The
// ceiling is recorded on the way in, so an empty body still counts as nested.
func (f *frame) enter() func() {
	f.nesting++
	if f.nesting > f.maxNesting {
		f.maxNesting = f.nesting
	}
	return func() { f.nesting-- }
}

func (f *frame) walkNodes(nodes []Node) {
	for _, node := range nodes {
		switch n := node.(type) {
		case Lambda:
			// Scoped by hand, not with defer: this is inside a loop, and a
			// deferred leave would only run when walkNodes returns — every
			// sibling after a closure would keep its nesting bonus.
			restore := f.enter()
			f.walkNodes(n.Body)
			restore()
		case Branch:
			f.branch(n)
		case Conditional:
			f.walkNodes(n.Test)
			f.cyclomatic++
			f.walkNodes(n.Then)
			f.walkNodes(n.Alternate)
		case Loop:
			f.decision(n.Init, n.Body, true)
		case Switch:
			f.switchStmt(n)
		case Catch:
			f.cognitive++ // flat: a handler is a decision, not another layer
			f.walkNodes(n.Body)
		case Logical:
			f.logical(n)
		case Group:
			f.walkNodes([]Node(n))
		}
	}
}

// decision scores a construct that is both a cognitive and a cyclomatic
// decision point, then walks its header and body one level deeper.
func (f *frame) decision(init, body []Node, cyclomatic bool) {
	f.cognitive += 1 + f.nesting
	if cyclomatic {
		f.cyclomatic++
	}
	defer f.enter()()
	f.walkNodes(init)
	f.walkNodes(body)
}

// branch scores an if and recurses through its else / else-if chain, so each
// link of the chain is measured one level below the last.
func (f *frame) branch(b Branch) {
	f.cognitive += 1 + f.nesting
	f.cyclomatic++

	defer f.enter()()
	f.walkNodes(b.Test)
	f.walkNodes(b.Then)
	if b.Alternate != nil {
		f.walkNodes([]Node{b.Alternate})
	}
}

func (f *frame) switchStmt(s Switch) {
	f.cognitive += 1 + f.nesting // the switch itself opens no extra path

	defer f.enter()()
	f.walkNodes(s.Init)
	for _, tc := range s.Cases {
		if !tc.IsDefault {
			f.cyclomatic++ // an arm is a decision point; `default` is not
		}
		f.walkNodes(tc.Body)
	}
}

// logical scores one folded run: a flat +1 however long the run is, and one
// cyclomatic path per operator (an n-operand run joins n−1 of them).
func (f *frame) logical(l Logical) {
	f.cognitive++
	if len(l.Operands) > 1 {
		f.cyclomatic += uint32(len(l.Operands) - 1)
	}
	f.walkNodes(l.Operands)
}
