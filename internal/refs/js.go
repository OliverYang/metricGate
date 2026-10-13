package refs

import sitter "github.com/tree-sitter/go-tree-sitter"

// walkJS is the JavaScript/TypeScript arm of the extraction walk. The name of
// a function/method declaration is an `identifier` (methods: property_identifi
// er) and a call's callee is reached through the call_expression, never from
// the bare identifier, so declaration names are recorded in skip.
func (w *walker) walkJS(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "function_declaration", "generator_function_declaration", "function_expression", "arrow_function", "method_definition":
		enclosing = w.declName(n, enclosing)
	case "class_declaration", "abstract_class_declaration":
		w.jsClass(n, enclosing)
	case "call_expression":
		w.jsCall(n, enclosing)
	case "new_expression":
		w.jsNew(n, enclosing)
	case "type_identifier":
		// TypeScript type positions (annotations, type arguments).
		w.typeRef(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkJS(n.Child(i), enclosing)
	}
}

// declName marks a declaration's name as a definition (so it is never read as
// a use) and returns it as the new enclosing scope. An unnamed declaration —
// an anonymous function_expression — keeps the outer scope.
func (w *walker) declName(n *sitter.Node, enclosing string) string {
	name := n.ChildByFieldName("name")
	if name == nil {
		return enclosing
	}
	w.skip[name.StartByte()] = true
	return w.text(name)
}

// jsClass records a class declaration's own name as a definition and its base
// types as type references. Both come from the same node, so they share one
// branch of the walk rather than widening it with two more calls.
func (w *walker) jsClass(n *sitter.Node, enclosing string) {
	w.skipName(n)
	w.heritageRefs(jsHeritage(n), enclosing)
}

// jsHeritage returns the base types of a class declaration. TypeScript and
// JavaScript both put them in a `class_heritage` child — a positional child,
// not a named field, so it is found by kind. TypeScript wraps each clause
// (`extends_clause` carries the base in its value field, `implements_clause`
// carries one type per child); JavaScript spells `extends X` with no wrapper
// at all, so the base is a direct named child. A class with no heritage
// yields nil.
func jsHeritage(n *sitter.Node) []*sitter.Node {
	heritage := childByKind(n, "class_heritage")
	if heritage == nil {
		return nil
	}
	var out []*sitter.Node
	for _, clause := range namedChildren(heritage) {
		switch clause.Kind() {
		case "extends_clause":
			if value := clause.ChildByFieldName("value"); value != nil {
				out = append(out, value)
			}
		case "implements_clause":
			out = append(out, namedChildren(clause)...)
		default:
			out = append(out, clause)
		}
	}
	return out
}

// childByKind returns the first named child of the given kind, or nil. Some
// grammars leave a construct positional rather than naming a field for it
// (class_heritage on a TypeScript class_declaration is one), so the field
// lookup cannot reach it.
func childByKind(n *sitter.Node, kind string) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// namedChildren returns every named child of n, skipping nils.
func namedChildren(n *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// jsCall records the callee of a call_expression: a bare identifier (f(...))
// or the property of a member expression (obj.f(...)).
func (w *walker) jsCall(n *sitter.Node, enclosing string) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier":
		w.add(w.text(fn), KindCall, "", enclosing, n)
	case "member_expression":
		prop := fn.ChildByFieldName("property")
		if prop == nil {
			return
		}
		recv := ""
		if obj := fn.ChildByFieldName("object"); obj != nil {
			recv = oneline(w.text(obj), 40)
		}
		w.add(w.text(prop), KindCall, recv, enclosing, n)
	}
}

// jsNew records one constructor invocation `new Foo(...)`: the constructor
// expression is the callee, the receiver its textual object for members.
func (w *walker) jsNew(n *sitter.Node, enclosing string) {
	ctor := n.ChildByFieldName("constructor")
	if ctor == nil {
		return
	}
	name := w.text(ctor)
	recv := ""
	if ctor.Kind() == "member_expression" {
		if prop := ctor.ChildByFieldName("property"); prop != nil {
			name = w.text(prop)
		}
		if obj := ctor.ChildByFieldName("object"); obj != nil {
			recv = oneline(w.text(obj), 40)
		}
	}
	w.add(name, KindType, recv, enclosing, n)
}
