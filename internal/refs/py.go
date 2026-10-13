package refs

import sitter "github.com/tree-sitter/go-tree-sitter"

// walkPy is the Python arm of the extraction walk. A definition's name is an
// `identifier` child of function_definition / class_definition, and a call's
// callee lives in the call node's function field, so declaration names are
// recorded in skip and never read as call references.
func (w *walker) walkPy(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "function_definition":
		enclosing = w.declName(n, enclosing)
	case "class_definition":
		enclosing = w.declName(n, enclosing)
		w.heritageRefs(pySuperclasses(n), enclosing)
	case "decorated_definition":
		// Transparent: the decorators lower with the children below.
	case "call":
		w.pyCall(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkPy(n.Child(i), enclosing)
	}
}

// pySuperclasses returns the base-class expressions of a class_definition.
// Python puts them in an `argument_list` under the `superclasses` field, one
// positional argument per base; a keyword argument (`metaclass=...`) is not a
// base and is left out.
func pySuperclasses(n *sitter.Node) []*sitter.Node {
	list := n.ChildByFieldName("superclasses")
	if list == nil {
		return nil
	}
	var out []*sitter.Node
	for i := uint(0); i < list.NamedChildCount(); i++ {
		arg := list.NamedChild(i)
		if arg == nil || arg.Kind() == "keyword_argument" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// pyCall records the callee of a call: a bare identifier (f(...)) or the
// attribute of an attribute chain (obj.f(...)). The receiver is the textual
// object of the attribute, truncated like the other walkers.
func (w *walker) pyCall(n *sitter.Node, enclosing string) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier":
		w.add(w.text(fn), KindCall, "", enclosing, n)
	case "attribute":
		attr := fn.ChildByFieldName("attribute")
		if attr == nil {
			return
		}
		recv := ""
		if obj := fn.ChildByFieldName("object"); obj != nil {
			recv = oneline(w.text(obj), 40)
		}
		w.add(w.text(attr), KindCall, recv, enclosing, n)
	}
}
