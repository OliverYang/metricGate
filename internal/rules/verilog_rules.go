package rules

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// VerilogRules is the built-in SystemVerilog/Verilog rule pack. Banned APIs
// live in policy (languages.verilog.banned) and are compiled by the engine,
// not here.
//
// The tree-sitter-verilog grammar carries no fields on its declaration and
// statement nodes, so every rule below walks child kinds instead of fields.
// Two shapes the grammar gets wrong are left out rather than approximated:
// `obj.method(1);` inside a task body parses as ERROR, and a class
// constructor's `new` is a declaration, not a call, so neither is reachable
// by a call ban.
func VerilogRules() []*Rule {
	return []*Rule{
		verilogEmptyBlock(),
		todoCommentRule("(comment) @c"),
		VerilogUnknownInclude(),
		verilogChainDepth(),
	}
}

// verilogEmptyBlock flags `begin ... end` blocks with nothing in them. The
// stock emptiness test applies here: a seq_block has no named children when
// it is empty, and the grammar wraps every if/loop/case arm body in one.
func verilogEmptyBlock() *Rule {
	return newEmptyBlockRule(`(seq_block) @blk`)
}

// VerilogUnknownInclude flags a “ `include <...> “ whose header is not a
// declared dependency. The angle-bracket form names a tool/library header
// (the quoted form is a file next to the source, like C's `"..."`), and the
// grammar hands the two back as different kinds, so the query captures both
// and the extractor tells them apart.
func VerilogUnknownInclude() *Rule {
	return unknownDepRule(
		`(include_compiler_directive
    [(double_quoted_string) @path
     (include_compiler_directive_standard) @path])`,
		verilogIncludePath,
		verilogStdInclude,
	)
}

// verilogIncludePath reduces an include to the path a policy can name. The
// quoted form is a header beside the source — a repository file, not a
// dependency — so it yields "" and the rule stays quiet. The angle form is
// what a dependency ban can meaningfully match.
func verilogIncludePath(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, `"`) {
		return ""
	}
	return strings.Trim(s, "<>")
}

// verilogStdInclude reports whether an angle-bracket include names a
// standard header. Verilog and SystemVerilog ship no standard include files
// — the language has no analogue of <stdio.h> — so nothing is inherently
// standard and the allow list in policy carries the whole decision.
func verilogStdInclude(string) bool { return false }

// verilogChainDepth flags call chains deeper than max_depth. The grammar
// wraps every call in subroutine_call -> function_subroutine_call and puts
// the receiver of the next hop either in a `primary` (method calls) or in
// the argument list (function calls); the walk follows both, and
// nestedInChain keeps only the outermost link reportable.
func verilogChainDepth() *Rule {
	return chainDepthRule(`
[(tf_call) (method_call)] @chain
`, func(ctx *MatchCtx) int {
		// The matched node is itself a call: count it, then each inner call
		// the chain descends into. a.b().c().d() = 4, helper(add_one(x)) = 2.
		depth := 1
		for cur := verilogInnerCall(ctx.Captures["chain"]); cur != nil; cur = verilogInnerCall(cur) {
			depth++
		}
		return depth
	})
}

// verilogInnerCall returns the call nested one hop inside n, or nil. A
// method call keeps its receiver in a `primary` child; a function call keeps
// its arguments in a `list_of_arguments_parent`, whose expression holds the
// inner call.
func verilogInnerCall(n *sitter.Node) *sitter.Node {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "method_call":
		return firstDescendantKind(firstChildKindOf(n, "primary"), "method_call", "tf_call")
	case "tf_call", "system_tf_call":
		return firstDescendantKind(firstChildKindOf(n, "list_of_arguments_parent"), "method_call", "tf_call")
	}
	return nil
}

// The call and include spellings, verified against the grammar. A function
// call keeps its callee as a bare simple identifier; a method call keeps it
// one level down in method_call_body, behind the receiver. A system task is
// its own node kind and its identifier carries the leading `$`.
const (
	verilogCallBare = `(tf_call
    (simple_identifier) @fn
    (list_of_arguments_parent) @args) @call`
	verilogCallRecv = `(method_call
    (_) @recv
    (method_call_body
        (method_identifier) @fn
        (list_of_arguments_parent) @args)) @call`
	verilogSystemCall = `(system_tf_call
    (system_tf_identifier) @fn
    (list_of_arguments_parent) @args) @call`
	verilogInclude = `(include_compiler_directive
    [(double_quoted_string) @path
     (include_compiler_directive_standard) @path])`
)

// verilogCallBan builds a call ban for the grammar's two call shapes: a
// function call (`tf_call`) and a method call (`method_call`), whose callee
// is the first simple identifier in its method_call_body. Both compare the
// receiver by source spelling, as every other language's ban does.
func verilogCallBan(b Ban) *Rule {
	rule := langCallBan(b, verilogCallBare, verilogCallRecv)
	// A system task (`$display`, `$fatal`) is spelled with a leading $, so
	// the same ban spelling matches it as a bare name once the prefix is
	// folded away.
	sys := callRule(b, verilogSystemCall, func(ctx *MatchCtx) (string, bool) {
		got := strings.TrimPrefix(flatText(ctx, "fn"), "$")
		if !nameMatch(got, b.Match, b.Prefix) {
			return "", false
		}
		return got, true
	})
	rule.Alternate = sys
	return rule
}

// verilogImportBan builds an include ban: the path compared is the header
// name, quotes and angle brackets stripped, with the repository-local quoted
// form yielding "" so it never matches a dependency spelling.
func verilogImportBan(b Ban) *Rule {
	return langImportBan(b, verilogInclude, trimImportDelims)
}

// firstChildKindOf returns the first direct child of one of the given kinds.
func firstChildKindOf(n *sitter.Node, kinds ...string) *sitter.Node {
	if n == nil {
		return nil
	}
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

// firstDescendantKind returns the first node of one of the given kinds in
// source order, searching the whole subtree, or nil.
func firstDescendantKind(n *sitter.Node, kinds ...string) *sitter.Node {
	if n == nil {
		return nil
	}
	for _, k := range kinds {
		if n.Kind() == k {
			return n
		}
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if found := firstDescendantKind(n.NamedChild(i), kinds...); found != nil {
			return found
		}
	}
	return nil
}
