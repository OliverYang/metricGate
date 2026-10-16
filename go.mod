module metricgate

go 1.26.2

require (
	github.com/DerekStride/tree-sitter-sql v0.0.0-20260919000438-39fdb0064037
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/tree-sitter/tree-sitter-c v0.23.4
	github.com/tree-sitter/tree-sitter-c-sharp v0.23.5
	github.com/tree-sitter/tree-sitter-cpp v0.23.4
	github.com/tree-sitter/tree-sitter-go v0.25.0
	github.com/tree-sitter/tree-sitter-java v0.23.5
	github.com/tree-sitter/tree-sitter-javascript v0.23.1
	github.com/tree-sitter/tree-sitter-python v0.25.0
	github.com/tree-sitter/tree-sitter-rust v0.23.2
	github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet v0.0.0
	github.com/tree-sitter/tree-sitter-typescript v0.23.2
	github.com/tree-sitter/tree-sitter-verilog v1.0.3
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.59.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	modernc.org/libc v1.75.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

// The VB.NET grammar has no usable upstream Go module: its go.mod declares the
// module path github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet while
// the repository lives at github.com/CodeAnt-AI/tree-sitter-vb-dotnet, and its
// bindings/go/binding.go calls a C symbol (tree_sitter_tree_sitter_vb_dotnet)
// that src/parser.c does not export (it exports tree_sitter_vb_dotnet).
// third_party/tree-sitter-vb-dotnet keeps upstream's declared module path,
// vendors the byte-identical src/parser.c, and fixes only the cgo symbol.
replace github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet => ./third_party/tree-sitter-vb-dotnet
