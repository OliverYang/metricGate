package refs

import sitter "github.com/tree-sitter/go-tree-sitter"

// walkC is the C/C++ arm of the extraction walk. The C grammar names a
// function definition through its declarator chain (no name field on the
// function_definition itself), and a declaration name is a bare identifier
// reached only through the declarator — so declaration names are recorded in
// skip and calls are read from the call_expression, never from the identifier.
func (w *walker) walkC(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "function_definition":
		if name := cDeclaratorName(w, n); name != nil {
			w.skip[name.StartByte()] = true
			enclosing = w.text(name)
		}
	case "call_expression":
		w.cCall(n, enclosing)
	case "type_identifier":
		// Type uses (declarations, casts, sizeof) that name a type.
		w.typeRef(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkC(n.Child(i), enclosing)
	}
}

// cDeclaratorName resolves the name a function_definition declares: the
// declarator chain down to the function_declarator's declarator (a plain
// identifier). Returns nil for function pointers whose name is elsewhere.
func cDeclaratorName(w *walker, fn *sitter.Node) *sitter.Node {
	decl := fn.ChildByFieldName("declarator")
	for decl != nil && decl.Kind() == "pointer_declarator" {
		decl = decl.NamedChild(0)
	}
	if decl == nil || decl.Kind() != "function_declarator" {
		return nil
	}
	return decl.ChildByFieldName("declarator")
}

// cCall records the callee of a call_expression: a bare identifier (f(...)),
// the attribute of a field expression (obj.f(...) / PTR->f(...)), or a
// parenthesized function-pointer call whose inner callee is used as-is.
func (w *walker) cCall(n *sitter.Node, enclosing string) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier":
		w.add(w.text(fn), KindCall, "", enclosing, n)
	case "field_expression":
		// a.b(...) / a->b(...): the field (last component) is the callee.
		if field := fn.ChildByFieldName("field"); field != nil {
			recv := ""
			if arg := fn.ChildByFieldName("argument"); arg != nil {
				recv = oneline(w.text(arg), 40)
			}
			w.add(w.text(field), KindCall, recv, enclosing, n)
		}
	}
}

// walkCS is the C# arm of the extraction walk. A method's name is an
// identifier field on the declaration, and a call's callee is reached through
// the invocation_expression, never from the bare identifier, so declaration
// names are recorded in skip.
func (w *walker) walkCS(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "method_declaration", "constructor_declaration", "destructor_declaration", "operator_declaration", "local_function_statement":
		if name := n.ChildByFieldName("name"); name != nil {
			w.skip[name.StartByte()] = true
			enclosing = w.text(name)
		}
	case "invocation_expression":
		w.csCall(n, enclosing)
	case "object_creation_expression":
		w.csNew(n, enclosing)
	case "identifier":
		// Namespace and type usages (using directives, declared types).
		w.typeRef(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkCS(n.Child(i), enclosing)
	}
}

// csCall records the callee of an invocation_expression: a bare identifier
// (F(...)) or the name of a member access chain (obj.F(...)).
func (w *walker) csCall(n *sitter.Node, enclosing string) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier", "generic_name":
		w.add(oneline(w.text(fn), 40), KindCall, "", enclosing, n)
	case "member_access_expression":
		name := fn.ChildByFieldName("name")
		if name == nil {
			return
		}
		recv := ""
		if obj := fn.ChildByFieldName("expression"); obj != nil {
			recv = oneline(w.text(obj), 40)
		}
		w.add(w.text(name), KindCall, recv, enclosing, n)
	}
}

// csNew records one constructor invocation `new T(...)`: the type is the
// callee, its textual generic suffix kept in the name (List<int>).
func (w *walker) csNew(n *sitter.Node, enclosing string) {
	typ := n.ChildByFieldName("type")
	if typ == nil {
		return
	}
	w.add(oneline(w.text(typ), 40), KindType, "", enclosing, n)
}

// walkRust is the Rust arm of the extraction walk. A function's name is an
// identifier field on the function_item, and a call's callee is reached
// through the call_expression, never from the bare identifier, so
// declaration names are recorded in skip.
func (w *walker) walkRust(n *sitter.Node, enclosing string) {
	switch n.Kind() {
	case "function_item":
		if name := n.ChildByFieldName("name"); name != nil {
			w.skip[name.StartByte()] = true
			enclosing = w.text(name)
		}
	case "call_expression":
		w.rustCall(n, enclosing)
	case "field_expression":
		// Method-position field expressions are handled by rustCall via the
		// enclosing call; a bare field_expression is a value use, skipped.
	case "type_identifier":
		// Type positions (annotations, generics, impl targets).
		w.typeRef(n, enclosing)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walkRust(n.Child(i), enclosing)
	}
}

// rustCall records the callee of a call_expression: a bare identifier
// (f(...)) or the field of a field expression (obj.f(...) /
// Type::f(...) — the scope of a scoped_identifier counts as the receiver).
func (w *walker) rustCall(n *sitter.Node, enclosing string) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier":
		w.add(w.text(fn), KindCall, "", enclosing, n)
	case "field_expression":
		if field := fn.ChildByFieldName("field"); field != nil {
			recv := ""
			if val := fn.ChildByFieldName("value"); val != nil {
				recv = oneline(w.text(val), 40)
			}
			w.add(w.text(field), KindCall, recv, enclosing, n)
		}
	case "scoped_identifier":
		// Type::method(...): the path is the callee, truncated to one line.
		w.add(oneline(w.text(fn), 40), KindCall, "", enclosing, n)
	}
}
