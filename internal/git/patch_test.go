package git

import (
	"testing"
)

// Tests for the batched-patch path split in patch.go: hunks from one `git diff`
// must land on the change their paths name, even when git C-quotes those paths.

// specialBaseFiles are the fixtures for the special-path test: one file per path
// shape, each with a distinct body, so a mis-attributed hunk cannot hide inside
// a file that git reported as unchanged.
var specialBaseFiles = map[string]string{
	"back\\slash.go": "package x\nfunc A() int { return 1 }\n",
	"quo\"te.go":     "package x\nfunc B() int { return 1 }\n",
	"tab\tname.go":   "package x\nfunc C() int { return 1 }\n",
	"bin.dat":        "\x00\x01\x02\x03\n",
	"old-name.go":    "package x\nfunc Renamed() int {\n\treturn 1\n}\n",
}

// TestLoadChangesBatchedPatchAttributesSpecialPaths is the regression pin for
// the single-batch diff: git C-quotes a path in the patch header whenever it
// contains a backslash, a double quote or a tab, and core.quotePath only
// covers non-ASCII. Attribution goes through the verbatim `--name-status -z`
// paths, so these three must still land their hunks on the right change.
func TestLoadChangesBatchedPatchAttributesSpecialPaths(t *testing.T) {
	root := initGitRepo(t)
	backslash := "back\\slash.go"
	quote := `quo"te.go`
	tab := "tab\tname.go"
	binary := "bin.dat"
	renameFrom := "old-name.go"
	for name, body := range specialBaseFiles {
		writeRepoFile(t, root, name, body)
	}
	gitRun(t, root, "add", "--all")
	gitRun(t, root, "commit", "-qm", "base")

	writeRepoFile(t, root, backslash, "package x\nfunc A() int { return 2 }\n")
	writeRepoFile(t, root, quote, "package x\nfunc B() int { return 2 }\n")
	writeRepoFile(t, root, tab, "package x\nfunc C() int { return 2 }\n")
	writeRepoFile(t, root, binary, "\x00\x01\x02\x03\x04\n")
	// rename + content change: the section carries both --- old / +++ new and
	// the rename pair, so both paths have to resolve to the same change.
	gitRun(t, root, "mv", renameFrom, "new-name.go")
	writeRepoFile(t, root, "new-name.go", "package x\nfunc Renamed() int {\n\treturn 2\n}\n")

	changes, err := LoadChanges(root, ChangeRequest{Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(changes)
	for _, path := range []string{backslash, quote, tab, "new-name.go"} {
		c, ok := got[path]
		if !ok {
			t.Fatalf("missing change %q; paths=%v", path, keys(got))
		}
		if len(c.Hunks) == 0 {
			t.Errorf("%q lost its hunks in the batched patch", path)
		}
	}
	if c := got["new-name.go"]; c.Status != "R" || c.OldPath != renameFrom {
		t.Errorf("rename = %+v, want status R with oldPath %q", c, renameFrom)
	}
	// A binary change has no text hunks on either side, so it must stay hunk-free
	// rather than inherit another file's hunks.
	if c := got[binary]; len(c.Hunks) != 0 {
		t.Errorf("binary hunks = %+v, want none", c.Hunks)
	}
}

// TestLoadChangesIgnoresUserDiffConfig covers the config a batched patch reads
// but the per-file path never did: diff.mnemonicPrefix renames the header
// prefixes to c/ + w/ and diff.noprefix drops them. --src-prefix/--dst-prefix
// pin them so hunk attribution cannot silently vanish for a whole repo.
func TestLoadChangesIgnoresUserDiffConfig(t *testing.T) {
	for _, setting := range [][2]string{{"diff.mnemonicprefix", "true"}, {"diff.noprefix", "true"}} {
		root := initGitRepo(t)
		gitRun(t, root, "config", setting[0], setting[1])
		writeRepoFile(t, root, "a.go", "package x\nfunc A() int { return 1 }\n")
		gitRun(t, root, "add", "--all")
		gitRun(t, root, "commit", "-qm", "base")
		writeRepoFile(t, root, "a.go", "package x\nfunc A() int { return 2 }\n")

		changes, err := LoadChanges(root, ChangeRequest{Mode: ModeWorkspace})
		if err != nil {
			t.Fatalf("%s=%s: %v", setting[0], setting[1], err)
		}
		if len(changes) != 1 || len(changes[0].Hunks) == 0 {
			t.Errorf("%s=%s: changes=%+v, want a.go with hunks", setting[0], setting[1], changes)
		}
	}
}
