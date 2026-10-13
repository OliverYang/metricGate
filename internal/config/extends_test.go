package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExtendsPolicy(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtendsMergesChildOverParent(t *testing.T) {
	dir := t.TempDir()
	base := writeExtendsPolicy(t, dir, "base.yaml", `version: 1
gate:
  fail_on: error
languages:
  go:
    metrics:
      cyclomatic-complexity:
        warn: 5
        error: 8
`)

	child := writeExtendsPolicy(t, dir, "child.yaml", `version: 2
extends: base.yaml
languages:
  go:
    metrics:
      cyclomatic-complexity:
        error: 12
`)
	cfg, err := LoadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	_ = base // the parent is referenced by name in the child
	if cfg.Version != 2 {
		t.Errorf("child scalar must win, got %d", cfg.Version)
	}
	if cfg.Gate.FailOn != "error" {
		t.Errorf("parent scalar must survive, got %q", cfg.Gate.FailOn)
	}
	warn, errAt, _ := cfg.MetricThresholds("go", "cyclomatic-complexity")
	if warn != 5 {
		t.Errorf("parent warn must survive, got %d", warn)
	}
	if errAt != 12 {
		t.Errorf("child error must win, got %d", errAt)
	}
}

func TestExtendsBlankDocumentIsUntouched(t *testing.T) {
	cfg, err := Load([]byte("version: 1\ngate:\n  fail_on: warning\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gate.FailOn != "warning" {
		t.Errorf("fail_on = %q", cfg.Gate.FailOn)
	}
	if cfg.Extends() != nil {
		t.Error("a document without extends must report no chain")
	}
	if cfg.PolicyDocument() != nil {
		t.Error("a document without extends must have no effective document")
	}
}

func TestExtendsSequencesReplaceNotConcatenate(t *testing.T) {
	dir := t.TempDir()
	writeExtendsPolicy(t, dir, "base.yaml", `version: 1
exclude:
  - vendor
  - node_modules
`)
	child := writeExtendsPolicy(t, dir, "child.yaml", `extends: base.yaml
exclude:
  - .data
`)
	cfg, err := LoadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Exclude) != 1 || cfg.Exclude[0] != ".data" {
		t.Errorf("a sequence must be replaced wholesale, got %v", cfg.Exclude)
	}
}

func TestExtendsCycleIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeExtendsPolicy(t, dir, "a.yaml", "extends: b.yaml\nversion: 1\n")
	writeExtendsPolicy(t, dir, "b.yaml", "extends: a.yaml\nversion: 1\n")
	_, err := LoadFile(filepath.Join(dir, "a.yaml"))
	if err == nil {
		t.Fatal("a cycle must not load")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error must name the cycle, got %q", err)
	}
}

func TestExtendsDepthLimitIsAnError(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxExtendsDepth+2; i++ {
		writeExtendsPolicy(t, dir, "p"+string(rune('a'+i))+".yaml",
			"extends: p"+string(rune('a'+i+1))+".yaml\nversion: 1\n")
	}
	_, err := LoadFile(filepath.Join(dir, "pa.yaml"))
	if err == nil {
		t.Fatal("a chain past the limit must not load")
	}
	if !strings.Contains(err.Error(), "deeper than") {
		t.Errorf("error must name the limit, got %q", err)
	}
}

func TestExtendsMissingParentNamesThePath(t *testing.T) {
	dir := t.TempDir()
	child := writeExtendsPolicy(t, dir, "child.yaml", "extends: nope.yaml\nversion: 1\n")
	_, err := LoadFile(child)
	if err == nil {
		t.Fatal("a missing parent must not load")
	}
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Errorf("error must name the missing parent, got %q", err)
	}
}

func TestExtendsRecordsProvenancePerKey(t *testing.T) {
	dir := t.TempDir()
	base := writeExtendsPolicy(t, dir, "base.yaml", "version: 1\ngate:\n  fail_on: error\n")
	child := writeExtendsPolicy(t, dir, "child.yaml", "extends: base.yaml\nversion: 2\n")
	cfg, err := LoadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	src, path, explicit := cfg.ValueSource("version", "builtin")
	if src != "policy_file" || !explicit {
		t.Errorf("a key the child wrote must report policy_file, got %s/%s/%v", src, path, explicit)
	}
	src, path, explicit = cfg.ValueSource("gate.fail_on", "builtin")
	if src != "extends" || !explicit || path != base {
		t.Errorf("an inherited key must report the parent path, got %s/%s/%v", src, path, explicit)
	}
}

func TestExtendsSequenceResolvesRelativeToTheBase(t *testing.T) {
	dir := t.TempDir()
	org := filepath.Join(dir, "org")
	if err := os.MkdirAll(org, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExtendsPolicy(t, org, "base.yaml", "version: 1\ngate:\n  fail_on: error\n")
	child := writeExtendsPolicy(t, dir, "child.yaml", "extends: org/base.yaml\nversion: 2\n")
	cfg, err := LoadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gate.FailOn != "error" {
		t.Errorf("a relative parent must resolve against the policy's own directory, got %q", cfg.Gate.FailOn)
	}
}

func TestExtendsDigestDiffersFromTheChildFile(t *testing.T) {
	dir := t.TempDir()
	writeExtendsPolicy(t, dir, "base.yaml", "version: 1\ngate:\n  fail_on: error\nlanguages:\n  go:\n    metrics:\n      cyclomatic-complexity:\n        warn: 9\n")
	child := writeExtendsPolicy(t, dir, "child.yaml", "extends: base.yaml\nversion: 2\n")
	cfg, err := LoadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PolicyDocument() == nil {
		t.Fatal("an extending policy must expose its effective document")
	}
	if !strings.Contains(string(cfg.PolicyDocument()), "fail_on: error") {
		t.Errorf("the effective document must carry the parent's content:\n%s", cfg.PolicyDocument())
	}
	if strings.Contains(string(cfg.PolicyDocument()), "extends:") {
		t.Errorf("the effective document must not carry the extends key:\n%s", cfg.PolicyDocument())
	}
}
