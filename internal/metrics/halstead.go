// Halstead software-science measures and the Maintainability Index built
// on them (Phase B-2 of docs/phase-b-design.md).
//
// Counting follows the classic operator/operand split, read off the
// tree-sitter leaves: any named leaf is an operand (identifiers, literals),
// any anonymous leaf is an operator (keywords and punctuation keep their
// exact text). Distinct counts are η1+η2; totals are N1+N2. This is the
// "MI(Halstead-V) variant" agreed in design round 3: only the volume term
// feeds the index, not the full difficulty/effort family.
package metrics

import (
	"math"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Halstead is the raw software-science census of one file.
type Halstead struct {
	N1        int // total operators
	N2        int // total operands
	Distinct1 int // distinct operators (η1)
	Distinct2 int // distinct operands (η2)
}

// Volume is Halstead's V: the information content of the program in bits.
func (h Halstead) Volume() float64 {
	eta := float64(h.Distinct1 + h.Distinct2)
	n := float64(h.N1 + h.N2)
	if eta == 0 || n == 0 {
		return 0
	}
	return n * math.Log2(eta)
}

// CountHalstead walks one file's tree and returns its census. It takes several
// roots because a declaration's body is not always one node: VB.NET follows a
// method header with a flat run of statements and SystemVerilog splits
// declarations from statements, so the census has to span them all.
func CountHalstead(src []byte, roots ...*sitter.Node) Halstead {
	var h Halstead
	ops := map[string]int{} // operators by exact text
	rands := map[string]int{}

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || n.IsExtra() {
			return
		}
		if n.ChildCount() == 0 { // a leaf token
			if n.IsNamed() {
				h.N2++
				rands["id"]++ // operands normalise to one shape (Type-2 style)
			} else {
				t := text(src, n)
				h.N1++
				ops[t]++
			}
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	for _, root := range roots {
		walk(root)
	}
	h.Distinct1 = len(ops)
	h.Distinct2 = len(rands)
	return h
}
