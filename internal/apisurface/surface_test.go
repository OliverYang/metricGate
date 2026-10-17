package apisurface

import (
	"encoding/json"
	"strings"
	"testing"

	"metricgate/internal/facts"
)

func TestCompareRemovedAndAdded(t *testing.T) {
	old := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {
			{Name: "Keep", Signature: "func Keep() int", File: "api/a.go", Line: 1},
			{Name: "Gone", Signature: "func Gone(x int) error", File: "api/a.go", Line: 5},
		},
	}}
	new := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {
			{Name: "Keep", Signature: "func Keep() int", File: "api/a.go", Line: 1},
			{Name: "Fresh", Signature: "func Fresh() string", File: "api/b.go", Line: 9},
		},
	}}
	d := Compare(old, new)
	if len(d.Breaking) != 1 || d.Breaking[0].Kind != "removed" || d.Breaking[0].Name != "Gone" {
		t.Errorf("breaking = %+v, want one removed Gone", d.Breaking)
	}
	if len(d.Added) != 1 || d.Added[0].Name != "Fresh" {
		t.Errorf("added = %+v, want Fresh", d.Added)
	}
	if !d.Changed() {
		t.Error("Changed = false, want true")
	}
}

func TestCompareSignatureChangeIsBreakingWhitespaceIsNot(t *testing.T) {
	old := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Run", Signature: "func Run(  root string )\n(*Summary, error)", File: "a.go", Line: 1}},
	}}
	same := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Run", Signature: "func Run (root string) (*Summary, error)", File: "a.go", Line: 1}},
	}}
	if d := Compare(old, same); len(d.Breaking) != 0 {
		t.Errorf("whitespace-only change flagged breaking: %+v", d.Breaking)
	}
	changed := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Run", Signature: "func Run(root string, verbose bool) (*Summary, error)", File: "a.go", Line: 1}},
	}}
	d := Compare(old, changed)
	if len(d.Breaking) != 1 || d.Breaking[0].Kind != "signature-changed" {
		t.Errorf("breaking = %+v, want one signature-changed", d.Breaking)
	}
}

func TestCompareReceiverIsIdentity(t *testing.T) {
	old := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {
			{Name: "Get", Receiver: "A", Signature: "func (a A) Get() int", File: "a.go", Line: 1},
			{Name: "Get", Receiver: "B", Signature: "func (b B) Get() int", File: "a.go", Line: 5},
		},
	}}
	same := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {
			{Name: "Get", Receiver: "A", Signature: "func (a A) Get() int", File: "a.go", Line: 1},
			{Name: "Get", Receiver: "B", Signature: "func (b B) Get() int", File: "a.go", Line: 5},
		},
	}}
	if d := Compare(old, same); d.Changed() {
		t.Errorf("same surface diffed as changed: %+v", d)
	}
	// Dropping one receiver's method is breaking even though the name survives.
	dropB := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Get", Receiver: "A", Signature: "func (a A) Get() int", File: "a.go", Line: 1}},
	}}
	d := Compare(old, dropB)
	if len(d.Breaking) != 1 || d.Breaking[0].Name != "Get" || d.Breaking[0].Old == "" {
		t.Errorf("breaking = %+v, want removed Get with its old signature", d.Breaking)
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	snap := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Keep", Signature: "func Keep() int", File: "api/a.go", Line: 1}},
	}}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Packages["api"]) != 1 || back.Packages["api"][0].Name != "Keep" {
		t.Errorf("round trip lost data: %s", data)
	}
	if !strings.Contains(string(data), `"version":1`) {
		t.Errorf("json = %s", data)
	}
}

func TestCurrentExcludesTestsAndFixtures(t *testing.T) {
	files := []*facts.FileFacts{
		{File: "internal/pkg/pkg.go", Package: "pkg", Language: "go", Symbols: []facts.Symbol{{Name: "Run", Signature: "func Run()", Visibility: "exported", StartLine: 1}}},
		{File: "internal/pkg/pkg_test.go", Package: "pkg", Language: "go", Symbols: []facts.Symbol{{Name: "TestRun", Signature: "func TestRun(t *testing.T)", Visibility: "exported", StartLine: 1}}},
		{File: "internal/pkg/testdata/fixture.go", Package: "pkg", Language: "go", Symbols: []facts.Symbol{{Name: "Fixture", Signature: "func Fixture()", Visibility: "exported", StartLine: 1}}},
	}
	snap := Current(files)
	entries := snap.Packages["pkg"]
	if len(entries) != 1 || entries[0].Name != "Run" {
		t.Errorf("surface = %+v, want only production Run", entries)
	}
}

func TestCurrentIncludesExportedJavaMembers(t *testing.T) {
	files := []*facts.FileFacts{{
		File: "src/Store.java", Package: "a", Language: "java",
		Symbols: []facts.Symbol{{
			Name: "Store", Kind: "class", Visibility: "exported", Signature: "public class Store",
			Children: []facts.Symbol{
				{Name: "size", Kind: "method", Visibility: "exported", Signature: "public int size()", StartLine: 3},
				{Name: "secret", Kind: "method", Visibility: "private", Signature: "private void secret()", StartLine: 4},
			},
		}},
	}}
	snap := Current(files)
	entries := snap.Packages["a"]
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want public Store + size only", entries)
	}
	if entries[0].Name != "Store" || entries[1].Name != "size" || entries[1].Receiver != "Store" {
		t.Errorf("Java API entries = %+v", entries)
	}
}

// The "(root)" bucket collects every file that declares no package. Two
// unrelated scripts defining the same function name are two symbols, not one
// symbol that changed: keying the bucket by name alone made Compare report a
// signature change for a tree diffed against itself.
func TestRootBucketKeysByNameAndFile(t *testing.T) {
	snap := &Snapshot{Version: 1, Packages: map[string][]Entry{
		rootPackage: {
			{Name: "main", Signature: "def main() -> None:", File: "scripts/a.py", Line: 1},
			{Name: "main", Signature: "def main(args) -> int:", File: "scripts/b.py", Line: 1},
		},
	}}
	d := Compare(snap, snap)
	if d.Changed() {
		t.Errorf("a tree diffed against itself changed: breaking=%+v added=%+v moved=%+v", d.Breaking, d.Added, d.Moved)
	}
}

// A real package keeps pairing by name across files, so moving a symbol
// between files in the same package is still reported as moved rather than as
// one removal plus one addition.
func TestNamedPackageStillPairsAcrossFiles(t *testing.T) {
	old := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Run", Signature: "func Run()", File: "api/a.go", Line: 1}},
	}}
	moved := &Snapshot{Version: 1, Packages: map[string][]Entry{
		"api": {{Name: "Run", Signature: "func Run()", File: "api/b.go", Line: 1}},
	}}
	d := Compare(old, moved)
	if len(d.Breaking) != 0 {
		t.Errorf("moving a symbol inside its package is not breaking: %+v", d.Breaking)
	}
	if len(d.Moved) != 1 || d.Moved[0].New != "api/b.go" {
		t.Errorf("moved = %+v, want one move to api/b.go", d.Moved)
	}
}
