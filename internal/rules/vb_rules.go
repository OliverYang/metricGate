package rules

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// VBRules is the built-in VB.NET rule pack. Banned APIs live in policy
// (languages.vbnet.banned) and are compiled by the engine, not here.
//
// The grammar carries no fields on most statement nodes — a method body is a
// flat list of `statement` children, and `If`/`While`/`Do` keep their
// condition as an ordinary named child — so emptiness and chain walks go by
// child kind rather than by field. Logical operators are deliberately absent
// from this pack: the grammar's `binary_expression` drops the operator token
// for `AndAlso`/`OrElse`, so a complexity contribution counted from it would
// be a guess. docs/metrics.md records the gap.
func VBRules() []*Rule {
	return []*Rule{
		vbEmptyBlock(),
		todoCommentRule("(comment) @c"),
		VBUnknownDependency(),
		vbChainDepth(),
	}
}

// vbEmptyBlock flags an empty control-flow body. The stock emptiness test
// (no named children) never fires here: a VB `If` keeps its condition as a
// named child, and a `For` keeps its loop variable, bounds and step, so
// every empty construct has named children. Only a nested `statement`
// counts as work.
func vbEmptyBlock() *Rule {
	return emptyBlockRule(`
(if_statement) @blk
(while_statement) @blk
(do_statement) @blk
(for_statement) @blk
(for_each_statement) @blk
`, vbBlockIsEmpty)
}

// vbBlockIsEmpty reports whether a control-flow construct has no statement
// of its own. The condition expression, the loop variable and the bounds are
// named children too, so child count says nothing.
func vbBlockIsEmpty(blk *sitter.Node) bool {
	for i := uint(0); i < blk.NamedChildCount(); i++ {
		switch blk.NamedChild(i).Kind() {
		case "statement", "elseif_clause", "else_clause", "case_block", "case_else_block":
			return false
		}
	}
	return true
}

// VBUnknownDependency flags an Imports namespace that is not a declared
// dependency. An aliased import (`Imports X = Y`) does not parse in this
// grammar, so only the plain form is reachable; the query captures the
// namespace as written and the stdlib test recognises the BCL roots.
func VBUnknownDependency() *Rule {
	return unknownDepRule(
		`[(imports_statement namespace: (namespace_name) @path)]`,
		func(raw string) string { return strings.TrimSpace(raw) },
		vbStdlibImport,
	)
}

// vbStdlibImport reports whether a namespace belongs to the .NET base class
// library. VB.NET imports the same namespaces as C#, so this is the same
// decision, kept in step with the C# rule's list.
func vbStdlibImport(path string) bool {
	return csBCLNamespace(path)
}

// vbChainDepth flags call chains deeper than max_depth. The grammar nests
// each hop inside the receiver: a.B().C() is a `member_access` whose object
// is the inner invocation, and the whole expression is wrapped in a
// `binary_expression`-shaped `expression`, so the depth walk crosses
// `member_access.object` and nestedInChain keeps only the outermost link
// reportable.
func vbChainDepth() *Rule {
	return chainDepthRule(`(invocation) @chain`, func(ctx *MatchCtx) int {
		// The matched node is itself a call: count it, then each call the
		// receiver chain descends into. a.B().C().D() = 3.
		depth := 1
		for cur := vbInnerCall(ctx.Captures["chain"]); cur != nil; cur = vbInnerCall(cur) {
			depth++
		}
		return depth
	})
}

// vbInnerCall returns the call nested one hop inside n through its receiver,
// or nil. A method call keeps its receiver in the `target` field's
// `member_access.object`; a bare call has no receiver.
func vbInnerCall(n *sitter.Node) *sitter.Node {
	if n == nil {
		return nil
	}
	target := n.ChildByFieldName("target")
	if target == nil || target.Kind() != "member_access" {
		return nil
	}
	obj := target.ChildByFieldName("object")
	if obj == nil {
		return nil
	}
	if obj.Kind() == "invocation" {
		return obj
	}
	return firstDescendantKind(obj, "invocation")
}

// The call and import spellings, verified against the grammar. A bare call
// keeps its callee as the `target` identifier; a method call keeps it behind
// a `member_access` receiver. `New X(...)` is a `new_expression`, which is
// where a constructor ban points.
const (
	vbCallBare = `(invocation
    target: (identifier) @fn
    arguments: (argument_list) @args) @call`
	vbCallRecv = `(invocation
    target: (member_access
        object: (_) @recv
        member: (identifier) @fn)
    arguments: (argument_list) @args) @call`
	vbNew    = `(new_expression type: (_) @fn) @call`
	vbImport = `[(imports_statement namespace: (namespace_name) @path)]`
)

// vbCallBan builds a call ban for VB's `invocation` node. A bare policy
// entry matches only the unqualified spelling and a dotted one only the
// receiver-qualified spelling, exactly as the grammar separates them.
func vbCallBan(b Ban) *Rule {
	return langCallBan(b, vbCallBare, vbCallRecv)
}

// vbConstructorBan builds a `New X(...)` ban. The type is a named child of
// `new_expression` rather than a field, so the callee text is read straight
// from the capture and reduced to its last dotted segment before comparing —
// `New List(Of Integer)` and `New System.Text.StringBuilder()` both land on
// the type name the policy spells.
func vbConstructorBan(b Ban) *Rule {
	return constructorRule(b, vbNew)
}

// vbImportBan builds an Imports ban. The namespace is compared as written;
// the stdlib test is separate and belongs to the unknown-dependency rule.
func vbImportBan(b Ban) *Rule {
	return langImportBan(b, vbImport, trimImportDelims)
}
