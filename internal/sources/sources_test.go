package sources

import "testing"

func TestIsTestPath(t *testing.T) {
	yes := []string{
		"pkg/a_test.go", "internal/x/y_test.go",
		"src/App.test.tsx", "src/App.test.js", "src/App.test.ts",
		"pkg/mod_test.py", "pkg/test_.py",
		"src/lib_test.rs",
		"tests/e2e/main.go", "test/helper.java", "web/__tests__/app.ts",
		"java/src/ThingTest.java",
	}
	no := []string{
		"pkg/a.go", "src/App.tsx", "src/main.rs", "py/mod.py",
		"attic/test_helpers.go", // "test" as name prefix, not segment
		"internal/protest/x.go", // segment contains "test" but isn't it
	}
	for _, p := range yes {
		if !IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = true, want false", p)
		}
	}
}
