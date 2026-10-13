// Package sources collects the paths of supported source files.
package sources

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/parser"
)

// Files returns every supported source file under root (root itself may be
// a single file), sorted by path. Directory walks skip .git.
func Files(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := parser.LanguageForFile(d.Name()); ok {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// IsTestPath reports whether a file lives in test code: Go's _test.go
// suffix, a Test.java/_test.py style name, or a test/ or tests/ path
// segment. The engine uses it to apply test-specific metric baselines and
// the gate uses the same answer for its test-coverage checks — one
// heuristic, so the two can never disagree about what a test is.
func IsTestPath(path string) bool {
	base := filepath.Base(path)
	for _, suffix := range []string{"_test.go", "Test.java", "_test.py", "test_.py", "_test.ts", "_test.tsx", "_test.js", ".test.ts", ".test.tsx", ".test.js", "_test.rs"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == "test" || seg == "tests" || seg == "__tests__" {
			return true
		}
	}
	return false
}
