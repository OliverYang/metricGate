package metrics

import (
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// ck measures a Java source file and indexes its classes by name.
func ck(t *testing.T, src string) map[string]ClassStats {
	t.Helper()
	tree, release := parsetest.Parse(t, parser.LangJava, src)
	defer release()
	stats := AnalyzeFile("X.java", []byte(src), tree, parser.LangJava)
	byName := map[string]ClassStats{}
	for _, c := range stats.Classes {
		byName[c.Name] = c
	}
	return byName
}

const ckSource = `
package a;

import java.util.HashMap;
import java.util.List;

public class Alpha extends Base implements Repo {
    private int used;              // shared by a and b
    private int shared;            // shared by b and c
    private int orphan;            // used by nobody
    private List<String> names;

    public Alpha(int u) { this.used = u; }

    public int a(HashMap<String, Integer> m) {
        int t = used;
        if (t > 0) {
            t++;
        }
        return t;
    }

    public int b() {
        int t = used + shared;
        for (String n : names) {
            t += n.length();
        }
        return t;
    }

    public int c() {
        return shared + orphan;
    }

    public class Inner {
        int deep() { return used; }
    }
}

class Base {}

interface Repo {
    String find(int id);
}
`

func TestClassMetrics(t *testing.T) {
	got := ck(t, ckSource)
	alpha := ckClass(t, got, "Alpha")
	inner := ckClass(t, got, "Inner")
	repo := ckClass(t, got, "Repo")

	if alpha.NOM != 4 {
		t.Errorf("NOM = %d, want 4", alpha.NOM)
	}
	if alpha.NOF != 4 {
		t.Errorf("NOF = %d, want 4", alpha.NOF)
	}
	// WMC: ctor(1) + a(2) + b(2) + c(1) = 6.
	if alpha.WMC != 6 {
		t.Errorf("WMC = %d, want 6", alpha.WMC)
	}
	// DIT: Base is declared in the same file with no parent -> 1.
	if alpha.DIT != 1 {
		t.Errorf("DIT = %d, want 1", alpha.DIT)
	}

	// Inner is measured separately and sees only its own scope: CBO 0
	// (used is a field reference, not a type), DIT 0 (no extends).
	if inner.CBO != 0 {
		t.Errorf("Inner CBO = %d, want 0", inner.CBO)
	}
	if inner.DIT != 0 {
		t.Errorf("Inner DIT = %d, want 0", inner.DIT)
	}

	if repo.Kind != "interface" {
		t.Errorf("Repo kind = %s, want interface", repo.Kind)
	}
	// A bodyless interface method contributes the WMC minimum of 1.
	if repo.WMC != 1 {
		t.Errorf("Repo WMC = %d, want 1", repo.WMC)
	}
}

func TestClassCBOAndLCOM(t *testing.T) {
	got := ck(t, ckSource)
	alpha := ckClass(t, got, "Alpha")

	// CBO: Base, Repo, HashMap, String, List, Integer = 6 (primitives and
	// the own name excluded).
	if alpha.CBO != 6 {
		t.Errorf("CBO = %d, want 6", alpha.CBO)
	}
	// Pairs over methods {ctor,a,b,c}: share(ctor,a)=used, share(a,b)=used,
	// share(b,c)=shared; the other three pairs share nothing -> P=3, Q=3.
	if alpha.LCOM != 0 {
		t.Errorf("LCOM = %d, want 0 (P=3 Q=3)", alpha.LCOM)
	}
}

// ckClass fetches one measured class, failing the test when absent.
func ckClass(t *testing.T, got map[string]ClassStats, name string) ClassStats {
	t.Helper()
	c, ok := got[name]
	if !ok {
		t.Fatalf("%s missing from %v", name, got)
	}
	return c
}

// The classic split-brain class: two method groups touching disjoint field
// sets must produce a high LCOM.
func TestClassLCOMSplitBrain(t *testing.T) {
	src := `
package a;

class Split {
    private int left;
    private int right;

    int readLeft()  { return left; }
    int addLeft(int d)  { left += d; return left; }
    int readRight() { return right; }
    int addRight(int d) { right += d; return right; }
}
`
	got := ck(t, src)
	s, ok := got["Split"]
	if !ok {
		t.Fatal("Split missing")
	}
	// Pairs: {rl,al}=L, {rr,ar}=R, {rl,rr},{rl,ar},{al,rr},{al,ar} disjoint.
	// Q=2, P=4 → LCOM=2.
	if s.LCOM != 2 {
		t.Errorf("LCOM = %d, want 2", s.LCOM)
	}
}

// Field-free classes and single-method classes never report cohesion debt.
func TestClassLCOMTrivialZeros(t *testing.T) {
	src := `
package a;

class NoFields {
    int one() { return 1; }
    int two() { return 2; }
}

class OneMethod {
    private int f;
    int get() { return f; }
}
`
	got := ck(t, src)
	if got["NoFields"].LCOM != 0 {
		t.Errorf("NoFields LCOM = %d, want 0", got["NoFields"].LCOM)
	}
	if got["OneMethod"].LCOM != 0 {
		t.Errorf("OneMethod LCOM = %d, want 0", got["OneMethod"].LCOM)
	}
}

// Inheritance depth resolves same-file chains and ignores cross-file
// superclasses (counted as one level), and cycles terminate.
func TestClassDIT(t *testing.T) {
	src := `
package a;

class Grandparent {}
class Parent extends Grandparent {}
class Child extends Parent {}
class Orphan extends External {}
interface IA {}
interface IB extends IA {}
class IfaceImpl implements IA {}
`
	got := ck(t, src)
	if got["Grandparent"].DIT != 0 {
		t.Errorf("Grandparent DIT = %d, want 0", got["Grandparent"].DIT)
	}
	if got["Child"].DIT != 2 {
		t.Errorf("Child DIT = %d, want 2", got["Child"].DIT)
	}
	// External is not in the file: depth 1.
	if got["Orphan"].DIT != 1 {
		t.Errorf("Orphan DIT = %d, want 1", got["Orphan"].DIT)
	}
	// An interface extending one interface has depth 1.
	if got["IB"].DIT != 1 {
		t.Errorf("IB DIT = %d, want 1", got["IB"].DIT)
	}
	// implements never adds depth.
	if got["IfaceImpl"].DIT != 0 {
		t.Errorf("IfaceImpl DIT = %d, want 0", got["IfaceImpl"].DIT)
	}
}

// Go contributes no class metrics.
func TestClassMetricsGoEmpty(t *testing.T) {
	src := "package a\n\nfunc F() int { return 1 }\n"
	tree, release := parsetest.Parse(t, parser.LangGo, src)
	defer release()
	stats := AnalyzeFile("x.go", []byte(src), tree, parser.LangGo)
	if len(stats.Classes) != 0 {
		t.Errorf("go classes = %d, want 0", len(stats.Classes))
	}
}
