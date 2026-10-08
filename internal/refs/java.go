package refs

import sitter "github.com/tree-sitter/go-tree-sitter"

// walkJava is the Java arm of the extraction walk. The name of a method
// declaration and the name of a method invocation are both plain `identifier`
// nodes, so a declaration name is recorded in skip; invocations are read from
// their method_invocation parent, never from the bare identifier.
func (w *walker) walkJava(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "method_declaration", "constructor_declaration":
		if name := n.ChildByFieldName("name"); name != nil {
			w.skip[name.StartByte()] = true
			enclosing = w.text(name)
		}
	case "method_invocation":
		w.javaCall(n, enclosing)
	case "type_identifier":
		w.typeRef(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkJava(n.Child(i), enclosing)
	}
}

// javaCall records one method_invocation. The receiver is the textual object
// expression (empty for an unqualified call in the same type); it is not
// resolved to a declared type — that would need the type system.
func (w *walker) javaCall(n *sitter.Node, enclosing string) {
	name := n.ChildByFieldName("name")
	if name == nil {
		return
	}
	recv := ""
	if obj := n.ChildByFieldName("object"); obj != nil {
		recv = oneline(w.text(obj), 40)
	}
	w.add(w.text(name), KindCall, recv, enclosing, n)
}
