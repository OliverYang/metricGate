package refs

import sitter "github.com/tree-sitter/go-tree-sitter"

// walkGo is the Go arm of the extraction walk: it records call sites and type
// references, and tracks the declaration names that must not be mistaken for
// either (a type_spec's name is a type_identifier, the same node kind a
// reference uses).
func (w *walker) walkGo(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "function_declaration", "method_declaration":
		if name := n.ChildByFieldName("name"); name != nil {
			w.skip[name.StartByte()] = true
			enclosing = w.text(name)
		}
	case "type_spec":
		w.skipName(n)
	case "call_expression":
		w.goCall(n, enclosing)
	case "type_identifier":
		w.typeRef(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkGo(n.Child(i), enclosing)
	}
}

// goCall records the callee of a call_expression: a bare identifier
// (f(...)) or the field of a selector (recv.f(...)).
func (w *walker) goCall(n *sitter.Node, enclosing string) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier":
		w.add(w.text(fn), KindCall, "", enclosing, n)
	case "selector_expression":
		field := fn.ChildByFieldName("field")
		if field == nil {
			return
		}
		recv := ""
		if op := fn.ChildByFieldName("operand"); op != nil {
			recv = oneline(w.text(op), 40)
		}
		w.add(w.text(field), KindCall, recv, enclosing, n)
	}
}
