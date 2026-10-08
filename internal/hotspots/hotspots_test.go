package hotspots

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseNumstat(t *testing.T) {
	got := parseNumstat("\x01\n10\t2\tinternal/a.go\n-\t-\timage.png\n3\t4\tinternal/a.go\n0\t8\tinternal/b.go\n")
	want := map[string]int{"internal/a.go": 19, "internal/b.go": 8}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNumstat = %v, want %v", got, want)
	}
}

func TestHotspotFormulaAndOrdering(t *testing.T) {
	// max churn 200. a.go churn=100 MI=50 -> .5*50=25;
	// b.go churn=200 MI=90 -> 1*10=10; c.go churn=50 no MI -> .25*100=25.
	got := scoreFiles(map[string]int{"a.go": 100, "b.go": 200, "c.go": 50}, map[string]float64{"a.go": 50, "b.go": 90}, nil, 10)
	if len(got) != 3 {
		t.Fatalf("got %d hotspots", len(got))
	}
	if got[0].File != "a.go" || got[0].Score != 25 {
		t.Errorf("top = %+v, want a.go score 25 (tie broken by file)", got[0])
	}
	if got[1].File != "c.go" || got[1].Score != 25 {
		t.Errorf("second = %+v, want c.go score 25", got[1])
	}
	if got[2].File != "b.go" || got[2].Score != 10 {
		t.Errorf("third = %+v, want b.go score 10", got[2])
	}
}

// Risk annotates a hotspot without changing its churn-based score or order.
func TestRiskAnnotatesWithoutReordering(t *testing.T) {
	churn := map[string]int{"a.go": 100, "b.go": 200}
	got := scoreFiles(churn, nil, map[string]float64{"a.go": 42.5}, 10)
	if len(got) != 2 || got[0].File != "b.go" {
		t.Fatalf("order changed: %+v", got)
	}
	if got[1].File != "a.go" || got[1].Risk != 42.5 || got[0].Risk != 0 {
		t.Errorf("risk annotation wrong: %+v", got)
	}
}

func TestHotspotTopCap(t *testing.T) {
	got := scoreFiles(map[string]int{"a": 10, "b": 9, "c": 8}, nil, nil, 2)
	if len(got) != 2 || got[0].File != "a" || got[1].File != "b" {
		t.Errorf("top cap = %+v", got)
	}
}

func TestComputeRejectsNonGitDirectory(t *testing.T) {
	_, err := Compute(Options{Root: t.TempDir()})
	if err == nil {
		t.Fatal("Compute in a non-git directory must fail")
	}
}

func TestComputeFromTinyGitRepo(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	file := filepath.Join(dir, "hot.go")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "hot.go")
	run("commit", "-qm", "base")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-qam", "grow")
	got, err := Compute(Options{Root: dir, MIs: map[string]float64{"hot.go": 50}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].File != "hot.go" || got[0].Churn != 5 || got[0].Score != 50 {
		t.Errorf("hotspots = %+v, want hot.go churn=5 score=50", got)
	}
}
