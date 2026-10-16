// Class-level object-oriented metrics (the Chidamber–Kemerer family).
// Produced for every language that has a class-like declaration — the
// per-grammar spellings live in classes_lang.go. Go has no classes and
// contributes nothing. All measures are deliberately name-based ("lite"):
// no type resolution, no symbol tables. Every deviation from the strict CK
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
	Kind      string // class | interface | struct | trait | enum | record
	StartLine int    // 1-based
	EndLine   int    // 1-based inclusive
	WMC       int    // sum of method cyclomatic complexity
	NOM       int    // methods + constructors
	NOF       int    // declared fields (carried for tooling, not gated yet)
	LCOM      int    // original CK P−Q over method pairs sharing a field
	CBO       int    // distinct other type names referenced
	DIT       int    // inheritance depth, same-file resolution
}

// classMeasurer holds the per-file state of the class pass.
type classMeasurer struct {
	lang     parser.LanguageID
	spec     *classSpec
	src      []byte
	declared map[string]*sitter.Node   // every type name declared in the file
	impls    map[string][]*sitter.Node // Rust: methods indexed by owning type
	ditMemo  map[string]int
	ditBusy  map[string]bool // inheritance-cycle guard
}

// measureClasses fills fs.Classes for one parsed file. A language without a
// class spelling (Go) contributes nothing.
func measureClasses(lang parser.LanguageID, src []byte, root *sitter.Node, fs *FileStats) {
	spec := classSpecs[lang]
	if spec == nil {
		return
	}
	m := &classMeasurer{
		lang:     lang,
		spec:     spec,
		src:      src,
		declared: map[string]*sitter.Node{},
		impls:    map[string][]*sitter.Node{},
		ditMemo:  map[string]int{},
		ditBusy:  map[string]bool{},
	}
	m.collectDeclared(root)
	if spec.impls {
		m.collectImpls(root)
	}
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
		if _, ok := m.spec.decls[n.Kind()]; ok {
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

// collectImpls indexes Rust impl blocks by the type they extend. Rust keeps
// methods outside the type declaration, so `impl Svc { fn a() }` and
// `impl Draw for Svc { fn b() }` both belong to Svc: the type, not the
// trait, owns the method count. A trait's own default methods stay with the
// trait, which the declaration walk measures.
func (m *classMeasurer) collectImpls(root *sitter.Node) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Kind() == "impl_item" {
			name := lastDottedName(text(m.src, n.ChildByFieldName("type")))
			if name != "" {
				if body := n.ChildByFieldName("body"); body != nil {
					_, methods := m.bodyMembers(body)
					m.impls[name] = append(m.impls[name], methods...)
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

// walkClasses measures every class-like declaration exactly once. Nested
// types are measured on their own; the outer class excludes them.
func (m *classMeasurer) walkClasses(root *sitter.Node, fs *FileStats) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if _, ok := m.spec.decls[n.Kind()]; ok {
			fs.Classes = append(fs.Classes, m.measure(n))
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

// measure computes the whole CK family for one class-like declaration.
func (m *classMeasurer) measure(n *sitter.Node) ClassStats {
	c := ClassStats{
		Name:      text(m.src, n.ChildByFieldName("name")),
		Kind:      m.spec.decls[n.Kind()],
		StartLine: int(n.StartPosition().Row) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
	}
	m.score(n, &c)
	return c
}

// score fills the five CK measures for one declaration. Kept apart from
// measure so the counting is one readable block and neither function carries
// the whole family in its callee list.
func (m *classMeasurer) score(n *sitter.Node, c *ClassStats) {
	fields, methods := m.members(n)
	c.NOF = len(fields)
	c.NOM = len(methods)
	c.WMC = m.wmc(methods)
	c.LCOM = lcom(m.src, fields, methods)
	c.CBO = m.cbo(n, c.Name)
	c.DIT = m.ditOf(n)
}

// members lists the declaration's direct fields and methods. Nested type
// declarations are boundaries — their members belong to them.
func (m *classMeasurer) members(class *sitter.Node) (fields []string, methods []*sitter.Node) {
	body := class.ChildByFieldName("body")
	if body == nil {
		return nil, nil
	}
	fields, methods = m.bodyMembers(body)
	if name := text(m.src, class.ChildByFieldName("name")); name != "" {
		methods = append(methods, m.impls[name]...)
	}
	return fields, methods
}

// bodyMembers reads one type body through the language's member table.
// Kinds absent from the table are neither fields nor methods: access
// specifiers, comments, and nested type declarations.
func (m *classMeasurer) bodyMembers(body *sitter.Node) (fields []string, methods []*sitter.Node) {
	for i := uint(0); i < body.NamedChildCount(); i++ {
		member := body.NamedChild(i)
		read, ok := m.spec.members[member.Kind()]
		if !ok {
			continue
		}
		names, isMethod := read(m.src, member)
		if isMethod {
			methods = append(methods, member)
			continue
		}
		fields = append(fields, names...)
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
// method contributes 1, the minimum — the type still pays for the
// dispatch it promises.
func (m *classMeasurer) wmc(methods []*sitter.Node) int {
	total := 0
	lowerer := lowererFor(m.lang)
	for _, md := range methods {
		if body := md.ChildByFieldName("body"); body != nil && lowerer != nil {
			cyc, _, _ := irScore(lowerer, m.src, md)
			total += cyc
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
		if n.Kind() == "identifier" || n.Kind() == "field_identifier" ||
			n.Kind() == "property_identifier" || n.Kind() == "field_access" {
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

// cbo counts the distinct type names the declaration references outside
// itself. Name-based: platform types count too (coupling to the standard
// library is still coupling); primitives and the declaration's own name do
// not. Nested types are boundaries — their references are theirs.
func (m *classMeasurer) cbo(class *sitter.Node, own string) int {
	refs := map[string]bool{}
	m.collectTypeNames(class, refs, true)
	delete(refs, own)
	return len(refs)
}

// collectTypeNames walks one declaration subtree gathering type names via
// the language's collector. rootClass marks the starting node: nested
// declarations below it stop the walk because they are measured separately.
func (m *classMeasurer) collectTypeNames(n *sitter.Node, refs map[string]bool, rootClass bool) {
	m.collectTypeNamesFrom(nil, n, refs, rootClass)
}

// collectTypeNamesFrom carries the parent down so a collector can tell a type
// position from a declaration name.
func (m *classMeasurer) collectTypeNamesFrom(parent, n *sitter.Node, refs map[string]bool, rootClass bool) {
	if n == nil {
		return
	}
	if !rootClass && classDeclKinds[n.Kind()] {
		return
	}
	if m.spec.types != nil {
		m.spec.types(m.src, n, parent, refs)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		m.collectTypeNamesFrom(n, n.Child(i), refs, false)
	}
}

// ditOf resolves the inheritance depth within the file: a superclass
// declared in the same file contributes its own depth + 1; any other
// superclass counts as one level (cross-file resolution belongs to the
// symbol index, not to the per-file pass). Implementing an interface does
// not add depth; an interface extending one does. A language with no
// inheritance (Rust, C) always reports 0.
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

// superName returns the single parent type of a declaration, using the
// grammar's inheritance position. "" means the language has no inheritance
// or the declaration has no parent.
func (m *classMeasurer) superName(class *sitter.Node) string {
	if m.spec.super == nil {
		return ""
	}
	return m.spec.super(m.src, class)
}
