// Package tree_sitter_tree_sitter_vb_dotnet exposes the VB.NET grammar to Go.
//
// This file is a local shim, not upstream's. Upstream
// (github.com/CodeAnt-AI/tree-sitter-vb-dotnet) ships a binding.go whose cgo
// prologue calls C.tree_sitter_tree_sitter_vb_dotnet, but src/parser.c exports
// tree_sitter_vb_dotnet — the call does not link:
//
//	binding.go:14:24: could not determine what C.tree_sitter_tree_sitter_vb_dotnet refers to
//
// Upstream's go.mod also declares module path
// github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet while the repository
// lives at github.com/CodeAnt-AI/tree-sitter-vb-dotnet, so `go get` on the
// repository path fails outright:
//
//	module declares its path as: github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet
//	        but was required as: github.com/CodeAnt-AI/tree-sitter-vb-dotnet
//
// The module path above therefore keeps upstream's declared name (so a
// `replace` in the main module can point at this directory) and only the C
// symbol is corrected. Nothing else about the grammar is changed; src/parser.c
// is byte-identical to upstream v0.0.0-20250728102902-cfca210ce8fd.
package tree_sitter_tree_sitter_vb_dotnet

// #cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/../../src
// #include "tree_sitter/parser.h"
// #include "parser.c"
import "C"

import "unsafe"

// Language returns the tree-sitter Language for VB.NET.
func Language() unsafe.Pointer { return unsafe.Pointer(C.tree_sitter_vb_dotnet()) }
