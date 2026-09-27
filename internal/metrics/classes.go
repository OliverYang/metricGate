// Class-level object-oriented metrics (the Chidamber–Kemerer family).
// Java only today — Go has no classes; a package-level aggregation is a
// later extension. All measures are deliberately name-based ("lite"): no
// type resolution, no symbol tables. Every deviation from the strict CK
// definitions is documented in docs/metrics.md and is biased to be
// conservative (under-report) rather than noisy.
package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// ClassStats carries every per-class measurement in one pass.
type ClassStats struct {
	Name      string
	Kind      string // class | interface
	StartLine int    // 1-based
	EndLine   int    // 1-based inclusive
	WMC       int    // sum of method cyclomatic complexity
	NOM       int    // methods + constructors
	NOF       int    // declared fields (carried for tooling, not gated yet)
	LCOM      int    // original CK P−Q over method pairs sharing a field
	CBO       int    // distinct other type names referenced
	DIT       int    // inheritance depth, same-file resolution
}

// javaPrimitives are the names that do not count as object coupling.
var javaPrimitives = map[string]bool{
	"int": true, "long": true, "short": true, "byte": true,
	"char": true, "boolean": true, "float": true, "double": true,
	"void": true, "var": true,
}

// classMeasurer holds the per-file state of the class pass.
type classMeasurer struct {
	src      []byte
	prof     *LangProfile
	declared map[string]*sitter.Node // every type name declared in the file
	ditMemo  map[string]int
	ditBusy  map[string]bool // inheritance-cycle guard
}

// measureClasses fills fs.Classes for one parsed Java file. Other languages
// contribute nothing: the class family is Java-only by design for now.
func measureClasses(lang parser.LanguageID, src []byte, root *sitter.Node, fs *FileStats, prof *LangProfile) {
	if lang != parser.LangJava {
		return
	}
	m := &classMeasurer{
		src:      src,
		prof:     prof,
		declared: map[string]*sitter.Node{},
		ditMemo:  map[string]int{},
		ditBusy:  map[string]bool{},
	}
	m.collectDeclared(root)
	m.walkClasses(root, fs)
}

// collectDeclared indexes every type declaration in the file (first wins)
// so DIT can resolve superclass chains without leaving the file.
func (m *classMeasurer) collectDeclared(root *sitter.Node) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "class_declaration", "interface_declaration":
			name := text(m.src, n.ChildByFieldName("name"))
			if name != "" && m.declared[name] == nil {
				m.declared[name] = n
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

// walkClasses measures every class and interface declaration exactly once.
// Nested types are measured on their own; the outer class excludes them.
func (m *classMeasurer) walkClasses(root *sitter.Node, fs *FileStats) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "class_declaration", "interface_declaration":
			fs.Classes = append(fs.Classes, m.measure(n))
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

// measure computes the whole CK family for one class or interface.
func (m *classMeasurer) measure(n *sitter.Node) ClassStats {
	c := ClassStats{
		Name:      text(m.src, n.ChildByFieldName("name")),
		Kind:      "class",
		StartLine: int(n.StartPosition().Row) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
	}
	if n.Kind() == "interface_declaration" {
		c.Kind = "interface"
	}
	fields, methods := m.members(n)
	c.NOF = len(fields)
	c.NOM = len(methods)
	c.WMC = m.wmc(methods)
	c.LCOM = lcom(m.src, fields, methods)
	c.CBO = m.cbo(n, c.Name)
	c.DIT = m.ditOf(n)
	return c
}

// members lists the class's direct fields and methods; nested type
// declarations are boundaries — their members belong to them.
func (m *classMeasurer) members(class *sitter.Node) (fields []string, methods []*sitter.Node) {
	body := class.ChildByFieldName("body")
	if body == nil {
		return nil, nil
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		switch member.Kind() {
		case "field_declaration":
			fields = append(fields, declaratorNames(m.src, member)...)
		case "method_declaration", "constructor_declaration":
			methods = append(methods, member)
		}
	}
	return fields, methods
}

// declaratorNames expands one field_declaration into its declared names
// (`int a, b;` declares two fields).
func declaratorNames(src []byte, fieldDecl *sitter.Node) []string {
	var names []string
	for i := uint(0); i < fieldDecl.NamedChildCount(); i++ {
		d := fieldDecl.NamedChild(i)
		if d.Kind() != "variable_declarator" {
			continue
		}
		if name := d.ChildByFieldName("name"); name != nil {
			names = append(names, text(src, name))
		}
	}
	return names
}

// wmc sums method cyclomatic complexity. A bodyless (abstract/interface)
// method contributes 1, the minimum — the class still pays for the
// dispatch it promises.
func (m *classMeasurer) wmc(methods []*sitter.Node) int {
	total := 0
	for _, md := range methods {
		if body := md.ChildByFieldName("body"); body != nil {
			total += cyclomatic(m.prof, body, m.src)
			continue
		}
		total++
	}
	return total
}

// lcom implements the original Chidamber–Kemerer definition: P is the
// number of method pairs sharing no field, Q the pairs sharing at least
// one; the metric is max(0, P−Q). Field usage is name-based, so shadowing
// a field name with a local can only inflate sharing — which biases the
// measure toward cohesion (lower), the conservative direction.
func lcom(src []byte, fields []string, methods []*sitter.Node) int {
	if len(fields) == 0 || len(methods) < 2 {
		return 0
	}
	used := make([]map[string]bool, len(methods))
	for i, md := range methods {
		used[i] = usedFieldNames(src, md, fields)
	}
	p, q := 0, 0
	for i := 0; i < len(used); i++ {
		for j := i + 1; j < len(used); j++ {
			if sharesAny(used[i], used[j]) {
				q++
			} else {
				p++
			}
		}
	}
	if p > q {
		return p - q
	}
	return 0
}

// usedFieldNames returns the subset of fields whose name appears as an
// identifier anywhere in the method (constructor included).
func usedFieldNames(src []byte, method *sitter.Node, fields []string) map[string]bool {
	wanted := make(map[string]bool, len(fields))
	for _, f := range fields {
		wanted[f] = true
	}
	used := map[string]bool{}
	body := method.ChildByFieldName("body")
	if body == nil {
		return used
	}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Kind() == "identifier" {
			name := text(src, n)
			if wanted[name] {
				used[name] = true
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return used
}

func sharesAny(a, b map[string]bool) bool {
	for f := range a {
		if b[f] {
			return true
		}
	}
	return false
}

// cbo counts the distinct type names the class references outside itself.
// Name-based: JDK types count too (coupling to the platform is still
// coupling); primitives and the class's own name do not. Nested types are
// boundaries — their references are theirs.
func (m *classMeasurer) cbo(class *sitter.Node, own string) int {
	refs := map[string]bool{}
	m.collectTypeNames(class, refs, true)
	delete(refs, own)
	return len(refs)
}

// collectTypeNames walks one class subtree gathering type_identifier texts.
// rootClass marks the starting node: nested declarations below it stop the
// walk because they are measured separately.
func (m *classMeasurer) collectTypeNames(n *sitter.Node, refs map[string]bool, rootClass bool) {
	if n == nil {
		return
	}
	if !rootClass {
		switch n.Kind() {
		case "class_declaration", "interface_declaration":
			return
		case "type_identifier":
			name := text(m.src, n)
			if !javaPrimitives[name] {
				refs[name] = true
			}
		}
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		m.collectTypeNames(n.Child(i), refs, false)
	}
}

// ditOf resolves the inheritance depth within the file: a superclass
// declared in the same file contributes its own depth + 1; any other
// superclass counts as one level (cross-file resolution belongs to the
// symbol index, not to the per-file pass). Implementing an interface does
// not add depth; an interface extending one does.
func (m *classMeasurer) ditOf(class *sitter.Node) int {
	name := text(m.src, class.ChildByFieldName("name"))
	if d, ok := m.ditMemo[name]; ok {
		return d
	}
	if m.ditBusy[name] {
		return 1 // cycle: stop counting rather than loop forever
	}
	m.ditBusy[name] = true
	defer delete(m.ditBusy, name)

	d := 0
	if super := m.superName(class); super != "" {
		d = 1
		if superNode, ok := m.declared[super]; ok {
			d += m.ditOf(superNode)
		}
	}
	m.ditMemo[name] = d
	return d
}

// superName returns the single parent type of a declaration: the
// superclass of a class, or the first extended interface of an interface.
func (m *classMeasurer) superName(class *sitter.Node) string {
	if sc := class.ChildByFieldName("superclass"); sc != nil {
		return firstTypeName(m.src, sc)
	}
	for i := uint(0); i < class.NamedChildCount(); i++ {
		child := class.NamedChild(i)
		if child.Kind() == "extends_interfaces" {
			return firstTypeName(m.src, child)
		}
	}
	return ""
}

// firstTypeName returns the first type_identifier inside a node.
func firstTypeName(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind() == "type_identifier" {
		return text(src, n)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if name := firstTypeName(src, n.Child(i)); name != "" {
			return name
		}
	}
	return ""
}
