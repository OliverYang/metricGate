package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/arch"
	"metricgate/internal/facts"
)

// writeFactsFile drops a minimal Go file at rel so the collector has
// something to find; the body is irrelevant to exclusion, which is decided by
// path alone.
func writeFactsFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCollectForArchAppliesPolicyExcludesWithoutConfig is the regression guard
// for the failure that made arch drift report 20 phantom errors: a bare
// `arch snapshot` (no -config) fell back to the unfiltered facts, so the
// M2 raw-data snapshots under .data/m2b-raw/ were captured as architecture and
// every new dependency direction into them was reported as drift. A discovered
// policy must apply its exclude list whether or not -config was passed.
func TestCollectForArchAppliesPolicyExcludesWithoutConfig(t *testing.T) {
	root := t.TempDir()
	writeFactsFile(t, root, ".metricgate.yaml", "exclude: [\".data\"]\n")
	writeFactsFile(t, root, "internal/keep/keep.go", "package keep\nfunc Keep() {}\n")
	writeFactsFile(t, root, ".data/m2b-raw/m2b_sandbox/internal/dist/dist.go", "package dist\nfunc Keep() {}\n")

	list, err := collectForArch(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("collectForArch without -config kept %d files, want 1 (only internal/keep/keep.go): %s", len(list), fileNames(list))
	}
	if got := fileNames(list); !strings.HasSuffix(got, "internal/keep/keep.go") {
		t.Errorf("kept %s, want the tree's only non-excluded file", got)
	}
}

// TestCollectForArchWithConfigMatchesDiscovered pins the other half of the
// contract: passing -config explicitly must produce exactly the same capture
// as discovery. If the two paths ever disagree, "bless the snapshot" becomes
// unreproducible because the answer depends on how the command was spelled.
func TestCollectForArchWithConfigMatchesDiscovered(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "custom.yaml")
	// Both a discoverable policy and an explicit one must be present: the point
	// is that spelling the -config flag does not change what gets captured.
	writeFactsFile(t, root, ".metricgate.yaml", "exclude: [\".data\"]\n")
	writeFactsFile(t, root, "custom.yaml", "exclude: [\".data\"]\n")
	writeFactsFile(t, root, "internal/keep/keep.go", "package keep\nfunc Keep() {}\n")
	writeFactsFile(t, root, ".data/m2b-raw/m2b_sandbox/internal/dist/dist.go", "package dist\nfunc Keep() {}\n")

	discovered, err := collectForArch(root, "")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := collectForArch(root, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fileNames(explicit), fileNames(discovered); got != want {
		t.Errorf("explicit -config captured\n  %s\ndiscovery captured\n  %s\nthey must agree", got, want)
	}
}

// TestCollectForArchWithoutPolicyKeepsEverything guards the no-policy case:
// nothing discovered means the built-in defaults carry no exclude, so the
// capture must stay unfiltered rather than guessing one.
func TestCollectForArchWithoutPolicyKeepsEverything(t *testing.T) {
	root := t.TempDir() // no .metricgate.yaml anywhere above it
	writeFactsFile(t, root, "internal/keep/keep.go", "package keep\nfunc Keep() {}\n")
	writeFactsFile(t, root, ".data/m2b-raw/m2b_sandbox/internal/dist/dist.go", "package dist\nfunc Keep() {}\n")

	list, err := collectForArch(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("kept %d files, want 2 when no policy excludes anything: %s", len(list), fileNames(list))
	}
}

// fileNames renders the captured files as one line for readable failure diffs.
func fileNames(list []*facts.FileFacts) string {
	names := make([]string, len(list))
	for i, f := range list {
		names[i] = f.File
	}
	return strings.Join(names, ", ")
}

// TestArchSnapshotCapturesJavaDirections is the regression guard for the
// capture that froze an empty architecture. `arch snapshot` slashed the Java
// package ("com.x.ctrl" -> "com/x/ctrl") but left the import statement dotted,
// so an import was one segment while the package was three. matchDir matches
// Java imports as a leading sequence and bails out as soon as the import is
// shorter than the package, so every Java import resolved to nothing: a
// 1,966-file Java+Python workspace captured 200 packages and 0 dependency
// directions while `check` reported layer violations on the same tree. The
// snapshot and the gate must describe one graph, not two.
func TestArchSnapshotCapturesJavaDirections(t *testing.T) {
	root := t.TempDir()
	writeFactsFile(t, root, "src/main/java/com/x/ctrl/C.java",
		"package com.x.ctrl;\nimport com.x.dao.D;\npublic class C { D d; }\n")
	writeFactsFile(t, root, "src/main/java/com/x/dao/D.java",
		"package com.x.dao;\npublic class D {}\n")
	writeFactsFile(t, root, "src/main/java/com/x/svc/S.java",
		"package com.x.svc;\nimport com.x.dao.D;\npublic class S { D d; }\n")

	list, err := collectForArch(root, "")
	if err != nil {
		t.Fatal(err)
	}
	snap := arch.Capture(arch.Build(archFiles(list)))
	if len(snap.Edges) != 2 {
		t.Fatalf("java edges = %d, want 2 (ctrl->dao, svc->dao); packages=%v edges=%+v",
			len(snap.Edges), snap.Packages, snap.Edges)
	}
	for _, e := range snap.Edges {
		if !strings.HasSuffix(e.From, "ctrl") && !strings.HasSuffix(e.From, "svc") {
			t.Errorf("unexpected edge %+v", e)
		}
		if e.To != "com/x/dao" {
			t.Errorf("edge To = %q, want %q", e.To, "com/x/dao")
		}
	}
}
