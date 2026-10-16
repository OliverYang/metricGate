// Symbol extraction for the ten languages whose declaration shapes differ
// from Go's and Java's (see extractGo/extractJava in facts.go).
//
// The extraction is table-driven: each language declares which node kinds are
// declarations worth indexing, which node kind holds a type's members, and how
// visibility is decided. One shared walker then descends the declaration
// structure — never into function bodies, so a local variable or a nested
// closure cannot masquerade as API surface.
//
// This is what makes `symbols`, `find_symbols`, `impact`, `get_constraints`
// and the constraint digest work outside Go and Java: without it the index is
// empty and `impact` answers "no definition in this tree; the symbol is
// external" for code that is right there.
package facts

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// declSpec describes one declaration node kind: what symbol kind it produces,
// which field holds its name, and which field holds its members.
type declSpec struct {
	Kind string // symbol kind reported (function|method|class|struct|…)
	// Name is the field holding the name. When it is empty the name is read
	// from the declarator chain (C/C++ style) instead.
	Name string
	// Members is the field holding the member container, when the declaration
	// owns one (class body, struct body, enum body). Members become children.
	Members string
	// NameFromType reads the name from the `type` field instead, for Rust's
	// `impl Foo {}` which has no name field at all.
	NameFromType bool
	// FuncValue records the declaration only when its `value` field is a
	// function. It keeps `const helper = () => …` in the index without
	// indexing every other module-level constant as API surface.
	FuncValue bool
	// NameFunc resolves the name when the grammar keeps it outside a field:
	// inside a wrapper node (SQL's object_reference, Verilog's module_header),
	// or absent altogether. It takes precedence over Name.
	NameFunc func(src []byte, n *sitter.Node) string
	// Recurse descends into the declaration as well as recording it, for
	// grammars whose container is itself a declaration: a Verilog module holds
	// functions and tasks, a VB class holds methods. The declaration's own
	// name becomes the receiver of everything inside it.
	Recurse bool
	// BodyFunc locates the body when the grammar has no `body` field, so the
	// signature can stop where the body starts. SQL and Verilog wrap the body
	// in a named child; VB simply follows the header with `statement` nodes.
	BodyFunc func(n *sitter.Node) *sitter.Node
}

// langSpec is one language's extraction table.
type langSpec struct {
	Decls map[string]declSpec
	// Containers are node kinds the walker descends into without recording a
	// symbol: namespace/template wrappers, member lists, decorator wrappers.
	Containers map[string]bool
	// OwnerKinds are containers that re-anchor the owner of everything inside
	// them (Rust's `impl Point {}` makes Point the receiver of its methods).
	OwnerKinds map[string]string
	Vis        func(src []byte, n *sitter.Node, name string) string
}

// tsCommon is the declaration table shared by TypeScript and JavaScript. The
// two grammars use the same node names for these; TypeScript adds the
// signature/abstract kinds below.
func tsDecls() map[string]declSpec {
	return map[string]declSpec{
		"function_declaration":           {Kind: "function", Name: "name"},
		"generator_function_declaration": {Kind: "function", Name: "name"},
		"class_declaration":              {Kind: "class", Name: "name", Members: "body"},
		"method_definition":              {Kind: "method", Name: "name"},
		"public_field_definition":        {Kind: "field", Name: "name"},
		"field_definition":               {Kind: "field", Name: "property"},
		// const helper = () => … is how a large share of TS/JS functions are
		// declared, so function-valued declarators are indexed while plain
		// constants are not.
		"variable_declarator": {Kind: "function", Name: "name", FuncValue: true},
	}
}

// tsExtra are the TypeScript-only declaration kinds.
func tsExtra() map[string]declSpec {
	return map[string]declSpec{
		"abstract_class_declaration": {Kind: "class", Name: "name", Members: "body"},
		"interface_declaration":      {Kind: "interface", Name: "name", Members: "body"},
		"enum_declaration":           {Kind: "enum", Name: "name", Members: "body"},
		"type_alias_declaration":     {Kind: "type", Name: "name"},
		"function_signature":         {Kind: "function", Name: "name"},
		"method_signature":           {Kind: "method", Name: "name"},
		"abstract_method_signature":  {Kind: "method", Name: "name"},
		"property_signature":         {Kind: "field", Name: "name"},
	}
}

// csDecls is C#'s declaration table. Members of a type are reached through the
// type's `body` (a declaration_list), so declaration_list is a container too —
// nested types and members are found at any depth.
func csDecls() map[string]declSpec {
	return map[string]declSpec{
		"method_declaration":       {Kind: "method", Name: "name"},
		"constructor_declaration":  {Kind: "constructor", Name: "name"},
		"destructor_declaration":   {Kind: "destructor", Name: "name"},
		"operator_declaration":     {Kind: "operator", Name: "operator"},
		"local_function_statement": {Kind: "function", Name: "name"},
		"property_declaration":     {Kind: "property", Name: "name"},
		"field_declaration":        {Kind: "field"},
		"enum_member_declaration":  {Kind: "field", Name: "name"},
		"class_declaration":        {Kind: "class", Name: "name", Members: "body"},
		"interface_declaration":    {Kind: "interface", Name: "name", Members: "body"},
		"struct_declaration":       {Kind: "struct", Name: "name", Members: "body"},
		"record_declaration":       {Kind: "record", Name: "name", Members: "body"},
		"enum_declaration":         {Kind: "enum", Name: "name", Members: "body"},
	}
}

// rustDecls is Rust's declaration table. `impl Foo {}` is not listed: it owns
// no name of its own, so it is handled as an owner container instead.
func rustDecls() map[string]declSpec {
	return map[string]declSpec{
		"function_item": {Kind: "function", Name: "name"},
		"struct_item":   {Kind: "struct", Name: "name", Members: "body"},
		"enum_item":     {Kind: "enum", Name: "name", Members: "body"},
		"trait_item":    {Kind: "trait", Name: "name", Members: "body"},
		"mod_item":      {Kind: "module", Name: "name", Members: "body"},
		"const_item":    {Kind: "const", Name: "name"},
		"static_item":   {Kind: "static", Name: "name"},
		"type_item":     {Kind: "type", Name: "name"},
	}
}

// cDecls is C's table; cppDecls extends it with the class and namespace kinds.
func cDecls() map[string]declSpec {
	return map[string]declSpec{
		"function_definition": {Kind: "function"},
		"struct_specifier":    {Kind: "struct", Name: "name", Members: "body"},
		"union_specifier":     {Kind: "union", Name: "name", Members: "body"},
		"enum_specifier":      {Kind: "enum", Name: "name", Members: "body"},
		"type_definition":     {Kind: "type"},
		"field_declaration":   {Kind: "field"},
	}
}

func cppDecls() map[string]declSpec {
	d := cDecls()
	d["class_specifier"] = declSpec{Kind: "class", Name: "name", Members: "body"}
	d["namespace_definition"] = declSpec{Kind: "namespace", Name: "name", Members: "body"}
	return d
}

// sqlDecls is SQL's table: the DDL objects that name something plus the two
// procedural units. A column is not API surface of a database the way a
// function is, so table contents are not indexed. Both `create_function` and
// `create_procedure` keep their name in an object_reference child and their
// body in a sibling node, hence NameFunc/BodyFunc.
func sqlDecls() map[string]declSpec {
	return map[string]declSpec{
		"create_function":  {Kind: "function", NameFunc: sqlDeclName, BodyFunc: sqlBodyOf},
		"create_procedure": {Kind: "procedure", NameFunc: sqlDeclName, BodyFunc: sqlBodyOf},
		"create_table":     {Kind: "table", NameFunc: sqlDeclName},
		"create_view":      {Kind: "view", NameFunc: sqlDeclName},
		"create_schema":    {Kind: "schema", NameFunc: sqlSchemaName},
		"create_index":     {Kind: "index", NameFunc: sqlIndexName},
	}
}

// verilogDecls is SystemVerilog's table. The grammar attaches no name field to
// any declaration, so every name comes from NameFunc. Modules, packages,
// programs, interfaces and classes recurse: they hold the functions and tasks
// that are the units of behaviour.
func verilogDecls() map[string]declSpec {
	return map[string]declSpec{
		"module_declaration":            {Kind: "module", NameFunc: verilogDeclName, Recurse: true},
		"interface_declaration":         {Kind: "interface", NameFunc: verilogDeclName, Recurse: true},
		"package_declaration":           {Kind: "package", NameFunc: verilogDeclName, Recurse: true},
		"program_declaration":           {Kind: "program", NameFunc: verilogDeclName, Recurse: true},
		"class_declaration":             {Kind: "class", NameFunc: verilogDeclName, Recurse: true},
		"function_declaration":          {Kind: "function", NameFunc: verilogDeclName, BodyFunc: verilogBodyOf},
		"task_declaration":              {Kind: "task", NameFunc: verilogDeclName, BodyFunc: verilogBodyOf},
		"class_constructor_declaration": {Kind: "constructor", NameFunc: verilogCtorName, BodyFunc: verilogBodyOf},
	}
}

// vbDecls is VB.NET's table. Blocks are declarations that recurse, so a class's
// methods are indexed with the class as their receiver; a field's name is one
// level down in its variable_declarator, which varDeclaratorName reaches.
func vbDecls() map[string]declSpec {
	return map[string]declSpec{
		"class_block":             {Kind: "class", Name: "name", Recurse: true},
		"module_block":            {Kind: "module", Name: "name", Recurse: true},
		"structure_block":         {Kind: "struct", Name: "name", Recurse: true},
		"interface_block":         {Kind: "interface", Name: "name", Recurse: true},
		"enum_block":              {Kind: "enum", Name: "name", Recurse: true},
		"enum_member":             {Kind: "field", Name: "name"},
		"method_declaration":      {Kind: "function", Name: "name", BodyFunc: vbBodyOf},
		"constructor_declaration": {Kind: "constructor", NameFunc: vbCtorName, BodyFunc: vbBodyOf},
		"property_declaration":    {Kind: "property", Name: "name"},
		"field_declaration":       {Kind: "field"},
		"delegate_declaration":    {Kind: "delegate", Name: "name"},
		"event_declaration":       {Kind: "event", Name: "name"},
		"const_declaration":       {Kind: "const", Name: "name"},
	}
}

// langSpecs is the extraction table for every language that has one. Go and
// Java keep their dedicated extractors (they carry receiver and modifier
// semantics a table cannot express).
var langSpecs = map[parser.LanguageID]langSpec{
	parser.LangTS: {
		Decls:      mergeDecls(tsDecls(), tsExtra()),
		Containers: setOf("export_statement", "lexical_declaration", "ambient_declaration", "internal_module", "module"),
		Vis:        tsVis,
	},
	// TSX indexes symbols through the TypeScript table: JSX adds element
	// nodes inside function bodies, and the extractor never enters them.
	parser.LangTSX: {
		Decls:      mergeDecls(tsDecls(), tsExtra()),
		Containers: setOf("export_statement", "lexical_declaration", "ambient_declaration", "internal_module", "module"),
		Vis:        tsVis,
	},
	parser.LangJS: {
		Decls:      tsDecls(),
		Containers: setOf("export_statement", "lexical_declaration"),
		Vis:        tsVis,
	},
	parser.LangPy: {
		Decls: map[string]declSpec{
			"function_definition": {Kind: "function", Name: "name"},
			"class_definition":    {Kind: "class", Name: "name", Members: "body"},
		},
		Containers: setOf("decorated_definition"),
		Vis:        pyVis,
	},
	parser.LangC: {
		Decls: cDecls(),
		Vis:   cVis,
	},
	parser.LangCpp: {
		Decls:      cppDecls(),
		Containers: setOf("template_declaration", "linkage_specification"),
		Vis:        cVis,
	},
	parser.LangCS: {
		Decls:      csDecls(),
		Containers: setOf("declaration_list", "namespace_declaration"),
		Vis:        csVis,
	},
	parser.LangRust: {
		Decls:      rustDecls(),
		Containers: setOf("declaration_list", "impl_item"),
		OwnerKinds: map[string]string{"impl_item": "type"},
		Vis:        rustVis,
	},
	// SQL is DDL plus a thin procedural layer (see sqlDecls). A declaration is
	// wrapped in a `statement` node, so that has to be a container or the
	// walker never reaches `create_function`.
	parser.LangSQL: {
		Decls:      sqlDecls(),
		Containers: setOf("statement"),
		Vis:        sqlVis,
	},
	// SystemVerilog has no name fields at all (see verilogDecls).
	parser.LangVerilog: {
		Decls: verilogDecls(),
		Containers: setOf(
			"module_or_generate_item", "package_or_generate_item_declaration",
			"non_port_program_item", "interface_or_generate_item",
			"module_common_item", "class_item", "class_method", "generate_region",
		),
		Vis: verilogVis,
	},
	// VB.NET blocks recurse, so a class's members carry the class as receiver.
	parser.LangVB: {
		Decls:      vbDecls(),
		Containers: setOf("namespace_block", "type_declaration", "declaration_list"),
		Vis:        vbVis,
	},
}

func mergeDecls(a, b map[string]declSpec) map[string]declSpec {
	out := make(map[string]declSpec, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func setOf(kinds ...string) map[string]bool {
	out := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		out[k] = true
	}
	return out
}

// extractTable is the entry point for every table-driven language. It returns
// false when the language has no table, so Extract can fall through.
func extractTable(f *FileFacts, src []byte, root *sitter.Node, lang parser.LanguageID) bool {
	spec, ok := langSpecs[lang]
	if !ok {
		return false
	}
	w := &langWalker{spec: spec, src: src, f: f}
	w.descend(root, "")
	return true
}

// langWalker walks declaration containers. It never enters a function body:
// only containers and member lists are descended into, so a local class or a
// nested closure stays out of the index.
type langWalker struct {
	spec langSpec
	src  []byte
	f    *FileFacts
}

// descend records every declaration among n's named children and recurses
// into the children that are containers.
func (w *langWalker) descend(n *sitter.Node, owner string) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if field, ok := w.spec.OwnerKinds[c.Kind()]; ok {
			w.descend(c, w.ownerOf(c, field, owner))
			continue
		}
		if d, ok := w.spec.Decls[c.Kind()]; ok {
			if sym, keep := w.symbol(c, d, owner); keep {
				w.f.Symbols = append(w.f.Symbols, sym)
			}
			if d.Recurse {
				// The container is itself a declaration: its name owns the
				// declarations inside it (a Verilog module's functions, a VB
				// class's methods).
				if name := w.name(c, d); name != "" {
					w.descend(c, name)
				}
			}
			continue
		}
		if w.spec.Containers[c.Kind()] {
			w.descend(c, owner)
		}
	}
}

// ownerOf reads the owner name an owner container re-anchors to, falling back
// to the surrounding owner when the field is absent.
func (w *langWalker) ownerOf(n *sitter.Node, field, fallback string) string {
	name := oneline(text(w.src, n.ChildByFieldName(field)), 60)
	if name == "" {
		return fallback
	}
	return name
}

// symbol builds one declaration's Symbol, including its members when the kind
// owns a member container. keep is false for declarations the table filters
// out (a non-function `const`, an anonymous default export).
func (w *langWalker) symbol(n *sitter.Node, d declSpec, owner string) (Symbol, bool) {
	if d.FuncValue && !isFuncValue(w.src, n) {
		return Symbol{}, false
	}
	name := w.name(n, d)
	if name == "" {
		return Symbol{}, false
	}
	sym := Symbol{
		Kind:       d.Kind,
		Name:       name,
		Receiver:   owner,
		Signature:  w.signature(n, d),
		Visibility: w.spec.Vis(w.src, n, name),
		StartLine:  line(n),
	}
	if d.Members != "" {
		if body := n.ChildByFieldName(d.Members); body != nil {
			sym.Children = w.members(body, name)
		}
	}
	return sym, true
}

// isFuncValue reports whether a declarator's value is a function literal, so
// `const helper = () => …` is API surface while `const MAX = 10` is not.
func isFuncValue(src []byte, n *sitter.Node) bool {
	v := n.ChildByFieldName("value")
	if v == nil {
		return false
	}
	switch v.Kind() {
	case "arrow_function", "function_expression", "generator_function":
		return true
	}
	return false
}

// members collects a type's member declarations, each carrying the owning type
// as its receiver — the same shape Go's interface methods and Java's class
// members already have.
func (w *langWalker) members(body *sitter.Node, owner string) []Symbol {
	var out []Symbol
	for i := uint(0); i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		d, ok := w.spec.Decls[c.Kind()]
		if !ok {
			continue
		}
		if sym, keep := w.symbol(c, d, owner); keep {
			out = append(out, sym)
		}
	}
	return out
}

// name resolves a declaration's name: a language-supplied resolver when the
// grammar keeps it outside a field, the named field when the grammar has one,
// the `type` field for owner containers, the declarator chain for C/C++
// (where the name lives inside a nested declarator), and the first variable
// declarator for C#/VB fields (whose declaration node has no fields at all).
func (w *langWalker) name(n *sitter.Node, d declSpec) string {
	if d.NameFunc != nil {
		return d.NameFunc(w.src, n)
	}
	if d.NameFromType {
		return oneline(text(w.src, n.ChildByFieldName("type")), 60)
	}
	if d.Name != "" {
		return text(w.src, n.ChildByFieldName(d.Name))
	}
	if name := declaratorName(w.src, n); name != "" {
		return name
	}
	return varDeclaratorName(w.src, n)
}

// varDeclaratorName finds the first variable declarator below n and returns
// its name. C#'s field_declaration carries no fields of its own, so the name of
// `private double r;` only exists one level down, inside the
// variable_declaration the declaration wraps.
func varDeclaratorName(src []byte, n *sitter.Node) string {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "variable_declarator":
			return text(src, c.ChildByFieldName("name"))
		case "variable_declaration":
			if name := varDeclaratorName(src, c); name != "" {
				return name
			}
		}
	}
	return ""
}

// signature is the declaration text up to (excluding) its body, on one line.
func (w *langWalker) signature(n *sitter.Node, d declSpec) string {
	end := n.EndByte()
	if body := n.ChildByFieldName("body"); body != nil {
		end = body.StartByte()
	} else if d.BodyFunc != nil {
		if body := d.BodyFunc(n); body != nil {
			end = body.StartByte()
		}
	}
	return oneline(string(w.src[n.StartByte():end]), 160)
}

// declaratorName walks a C/C++ declarator chain to the identifier that names
// the declaration: function_definition → function_declarator → identifier,
// with any number of pointer/parenthesised declarators in between.
func declaratorName(src []byte, n *sitter.Node) string {
	cur := n
	for depth := 0; depth < 8 && cur != nil; depth++ {
		switch cur.Kind() {
		case "identifier", "field_identifier", "type_identifier":
			return text(src, cur)
		}
		if next := cur.ChildByFieldName("declarator"); next != nil {
			cur = next
			continue
		}
		cur = firstIdentChild(cur)
	}
	return ""
}

// firstIdentChild returns the first named child that is or contains an
// identifier, for declarators the field walk cannot reach.
func firstIdentChild(n *sitter.Node) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "identifier", "field_identifier", "type_identifier":
			return c
		}
	}
	return nil
}

// modifiersText is the concatenated modifier text of a declaration, used by
// the C#/TypeScript visibility rules.
func modifiersText(src []byte, n *sitter.Node) string {
	var b strings.Builder
	for i := uint(0); i < n.ChildCount(); i++ {
		switch c := n.Child(i); c.Kind() {
		case "modifiers", "accessibility_modifier", "modifier":
			b.WriteString(text(src, c))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// tsVis: TypeScript/JavaScript visibility. A `#private` name is private, an
// explicit private/protected modifier is private, everything else is public
// surface.
func tsVis(src []byte, n *sitter.Node, name string) string {
	if strings.HasPrefix(name, "#") {
		return "private"
	}
	mods := modifiersText(src, n)
	if strings.Contains(mods, "private") || strings.Contains(mods, "protected") {
		return "private"
	}
	return "exported"
}

// pyVis: Python's only visibility convention is the leading underscore. Dunder
// names (`__init__`) are protocol methods, not private helpers.
func pyVis(src []byte, n *sitter.Node, name string) string {
	if !strings.HasPrefix(name, "_") {
		return "exported"
	}
	if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
		return "exported"
	}
	return "private"
}

// cVis: C/C++ have no visibility, so `static` linkage is the one real signal
// that a declaration is file-local.
func cVis(src []byte, n *sitter.Node, name string) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == "storage_class_specifier" && text(src, c) == "static" {
			return "private"
		}
	}
	return "exported"
}

// csVis: C# modifiers. An unmarked declaration is reported as exported rather
// than guessed at: the default differs between top-level types (internal) and
// members (private), and claiming something is hidden when it is not would
// hide real API surface from the constraint pack.
func csVis(src []byte, n *sitter.Node, name string) string {
	mods := modifiersText(src, n)
	switch {
	case strings.Contains(mods, "public"):
		return "exported"
	case strings.Contains(mods, "protected"), strings.Contains(mods, "internal"):
		return "package"
	case strings.Contains(mods, "private"):
		return "private"
	default:
		return "exported"
	}
}

// rustVis: `pub` is the only thing that widens visibility in Rust.
func rustVis(src []byte, n *sitter.Node, name string) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == "visibility_modifier" {
			return "exported"
		}
	}
	return "private"
}

// sqlVis: SQL has no visibility modifiers. Every object the grammar names at
// the top level is part of the schema's surface, so nothing is hidden.
func sqlVis(src []byte, n *sitter.Node, name string) string { return "exported" }

// sqlDeclName reads the name of a SQL declaration. `CREATE FUNCTION pick …`
// puts the name in an object_reference child rather than in a field of the
// declaration, and object_reference itself keeps it in the `name` field
// (schema and database are separate fields).
func sqlDeclName(src []byte, n *sitter.Node) string {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() == "object_reference" {
			if name := text(src, c.ChildByFieldName("name")); name != "" {
				return name
			}
		}
	}
	return ""
}

// sqlSchemaName reads the name of `CREATE SCHEMA app`, which the grammar
// stores as a bare identifier child.
func sqlSchemaName(src []byte, n *sitter.Node) string {
	return firstKind(src, n, "identifier")
}

// sqlIndexName reads the name of `CREATE INDEX idx ON t (c)`. The grammar puts
// the index's own name in the `column` field of create_index, which reads
// oddly but is the field the name is in; the table it indexes is a separate
// object_reference.
func sqlIndexName(src []byte, n *sitter.Node) string {
	return text(src, n.ChildByFieldName("column"))
}

// verilogVis: SystemVerilog's only access control is the `local`/`protected`
// qualifier on a class member. `localparam` is a constant, not a qualifier on a
// declaration, so it is deliberately not matched here.
func verilogVis(src []byte, n *sitter.Node, name string) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		switch c := n.Child(i); c.Kind() {
		case "class_item_qualifier", "method_qualifier":
			switch text(src, c) {
			case "local", "protected":
				return "private"
			}
		}
	}
	return "exported"
}

// verilogDeclName reads the name of a SystemVerilog declaration. The grammar
// attaches no name field to any of them: the name sits one or two wrapper
// levels down in a `simple_identifier`, and each declaration kind has its own
// wrapper (`module_header`, `function_identifier`, `task_identifier`,
// `class_identifier`, `interface_identifier`, …). Rather than enumerate the
// wrappers this takes the first simple_identifier in the subtree, which is
// always the declaration's own name: ports and body items come later.
func verilogDeclName(src []byte, n *sitter.Node) string {
	return firstKind(src, n, "simple_identifier")
}

// firstKind returns the text of the first node of the given kind anywhere
// below n, in source order.
func firstKind(src []byte, n *sitter.Node, kind string) string {
	if n == nil {
		return ""
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() == kind {
			return text(src, c)
		}
		if name := firstKind(src, c, kind); name != "" {
			return name
		}
	}
	return ""
}

// vbVis: VB's modifiers. `Friend` is assembly-scoped, which maps onto the
// package visibility the other languages use. An unmarked declaration is
// reported as exported rather than guessed at, for the same reason csVis does
// it.
func vbVis(src []byte, n *sitter.Node, name string) string {
	mods := modifiersText(src, n)
	switch {
	case strings.Contains(mods, "Private"):
		return "private"
	case strings.Contains(mods, "Protected"), strings.Contains(mods, "Friend"):
		return "package"
	default:
		return "exported"
	}
}

// vbCtorName names a VB constructor. `Sub New` carries no name field, so the
// convention `New` is used; the receiver (the enclosing class) already
// disambiguates it, which is how the other languages' constructors read too.
func vbCtorName(src []byte, n *sitter.Node) string { return "New" }

// verilogCtorName names a SystemVerilog class constructor. `function new(…)`
// has no name of its own, so the convention `new` is used; the receiver (the
// class) disambiguates it.
func verilogCtorName(src []byte, n *sitter.Node) string { return "new" }

// sqlBodyOf returns the body child of a SQL routine: the grammar puts the
// body in a `function_body`/`procedure_body` child rather than in a field.
func sqlBodyOf(n *sitter.Node) *sitter.Node {
	return firstChildOfKind(n, "function_body", "procedure_body")
}

// verilogBodyOf returns the first body item of a SystemVerilog routine. The
// declaration wraps a `*_body_declaration` that holds both the header (data
// type, name, ports) and the statements, so the signature must start inside it:
// returning the wrapper itself would cut the signature off before the name.
func verilogBodyOf(n *sitter.Node) *sitter.Node {
	decl := firstChildOfKind(n, "function_body_declaration", "task_body_declaration")
	if decl == nil {
		return nil
	}
	return firstChildOfKind(decl, "function_statement_or_null", "statement_or_null", "block_item_declaration")
}

// vbBodyOf returns the first statement of a VB routine. VB has no body node:
// a method is its header followed by a flat list of `statement` children.
func vbBodyOf(n *sitter.Node) *sitter.Node {
	return firstChildOfKind(n, "statement")
}

// firstChildOfKind returns the first child of one of the given kinds.
func firstChildOfKind(n *sitter.Node, kinds ...string) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		for _, k := range kinds {
			if c.Kind() == k {
				return c
			}
		}
	}
	return nil
}
