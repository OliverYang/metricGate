package cockpit

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"metricgate/internal/accept"
	"metricgate/internal/store"
)

// Source, activity API ------------------------------------------------------
//
// GET /api/source returns the code window around one located finding. This is
// the one endpoint that reads the operator's source, so the boundaries are the
// point (docs/design/cockpit-sonarqube-parity.md §4.4):
//
//   - A finding names its file, and that file is read. Nothing else is: no
//     directory traversal, no listing, no search. A request for a file no
//     finding points at is refused.
//   - The window is bounded (+- sourceWindowLines) and the anchor is a byte
//     span, so a shift in the file moves the window but does not widen it.
//   - Only a finding whose identity resolves to a row in this store is served,
//     which is what stops the endpoint from being an arbitrary file reader.
type SourceView struct {
	Fingerprint string       `json:"fingerprint"`
	File        string       `json:"file"`
	Language    string       `json:"language"`
	StartLine   int          `json:"startLine"`
	EndLine     int          `json:"endLine"`
	TotalLines  int          `json:"totalLines"`
	AnchorLine  int          `json:"anchorLine"`
	AnchorStart int          `json:"anchorStart"`
	AnchorEnd   int          `json:"anchorEnd"`
	Lines       []SourceLine `json:"lines"`
	Truncated   bool         `json:"truncated"`
	Reason      string       `json:"reason,omitempty"`
}

// SourceLine is one numbered line of the window. Highlight is deliberately
// three classes (key/comment/string) computed here rather than with a bundled
// highlighter: a stored XSS-free colored read is worth ten lines and not worth
// a second dependency.
type SourceLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	Class  string `json:"class,omitempty"`
}

// sourceWindowLines is half the window returned around an anchored finding.
const sourceWindowLines = 15

// sourceMaxBytes caps one read so a minified bundle cannot turn a code view
// into a whole-file dump.
const sourceMaxBytes = 512 * 1024

// sourceAPI serves GET /api/source?fingerprint=...
func sourceAPI(dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		fingerprint := strings.TrimSpace(r.URL.Query().Get("fingerprint"))
		if fingerprint == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fingerprint is required"})
			return
		}
		view, err := sourceView(dbPath, fingerprint)
		if err != nil {
			if errors.Is(err, errNoSuchFinding) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// errNoSuchFinding is what the source endpoint answers when the fingerprint is
// unknown: the caller asked for a file no finding points at, and a 404 is the
// honest answer rather than a read of whatever path arrived.
var errNoSuchFinding = errors.New("no stored finding with that fingerprint")

// sourceView resolves one fingerprint to a file and reads the window around it.
func sourceView(dbPath, fingerprint string) (SourceView, error) {
	view := SourceView{Fingerprint: fingerprint, Language: "text"}
	if dbPath == "" {
		return view, errors.New("history store is disabled; source view is not available")
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return view, err
	}
	defer st.Close()
	ledger, _ := accept.Load(accept.DefaultPath)
	// One window, one query: the fingerprint is matched in memory because
	// IssueQuery has no fingerprint filter, and adding one would put a lookup
	// endpoint's semantics into the query the list view uses. A window of the
	// most recent runs is enough because a fingerprint's first occurrence is
	// the identity being resolved, not the newest one.
	rows, err := st.IssuesForQuery(store.IssueQuery{Window: store.Window{Limit: defaultWindowLimit}}, ledger)
	if err != nil {
		return view, err
	}
	for _, issue := range rows.Issues {
		if issue.Fingerprint != fingerprint {
			continue
		}
		return buildSourceView(view, st, issue)
	}
	return view, errNoSuchFinding
}

// resolveSourcePath turns a finding's stored path into one this process can
// read. The engine records a path relative to the project root it scanned, so a
// relative path is joined to that run's root — without this, every relative
// finding (the overwhelming majority of real runs) reports "finding has no
// readable file path" and the code window never opens. An absolute path is
// taken as-is, which is what runs recorded with an absolute root produce. A
// run that cannot be found is a reason, not a guess: joining against this
// process's working directory would show the operator a same-named file from an
// unrelated project and present it as this finding's source.
func resolveSourcePath(st *store.Store, issue store.IssueRow) (string, error) {
	if filepath.IsAbs(issue.File) {
		return issue.File, nil
	}
	if issue.File == "" {
		return "", errors.New("finding has no file path")
	}
	run, ok, err := st.RunByID(issue.LastRunID)
	if err != nil {
		return "", err
	}
	if !ok || run.ProjectRoot == "" {
		return "", errors.New("finding has no readable file path")
	}
	return filepath.Join(run.ProjectRoot, filepath.FromSlash(issue.File)), nil
}

// readSourceWindow reads one file, capped. A path that is not a regular
// readable file is reported as a reason rather than an error: a finding can
// point at something deleted or renamed since the run, and that is a state the
// operator should see, not a failed request.
func readSourceWindow(path string) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("finding has no readable file path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("file is no longer readable: %v", err)
	}
	if !info.Mode().IsRegular() || info.Size() > sourceMaxBytes {
		return nil, fmt.Errorf("file is not a regular readable source file")
	}
	return os.ReadFile(path)
}

// windowBounds centres the window on the anchor and clamps it to the file.
func windowBounds(line, total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if line < 1 {
		line = 1
	}
	if line > total {
		line = total
	}
	first := line - sourceWindowLines
	if first < 1 {
		first = 1
	}
	last := line + sourceWindowLines
	if last > total {
		last = total
	}
	return first, last
}

// splitLines splits on newlines and drops the trailing empty element a final
// newline produces, so the reported total is what a human would count.
func splitLines(data []byte) []string {
	text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// lineClass gives a line one of three highlight classes. It is the whole syntax
// colouring in this tool: enough to read a finding's context, small enough to be
// obviously correct by looking at it. A bundled highlighter would cost more
// bytes than the rest of the UI.
func lineClass(line string) string {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "//"), strings.HasPrefix(trimmed, "#"),
		strings.HasPrefix(trimmed, "/*"), strings.HasPrefix(trimmed, "*"),
		strings.HasPrefix(trimmed, "<!--"):
		return "comment"
	case strings.HasPrefix(trimmed, "\""), strings.HasPrefix(trimmed, "'"),
		strings.HasPrefix(trimmed, "`"):
		return "string"
	case isKeywordStart(trimmed):
		return "key"
	}
	return ""
}

// isKeywordStart reports whether a line starts on a keyword. Only whole words
// count: "returned" is a value, not `return`.
func isKeywordStart(trimmed string) bool {
	word := trimmed
	for i, r := range trimmed {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			word = trimmed[:i]
			break
		}
	}
	switch word {
	case "return", "if", "else", "for", "range", "func", "fn", "function", "var",
		"let", "const", "class", "def", "import", "export", "package", "type",
		"struct", "interface", "public", "private", "static", "void", "new":
		return true
	}
	return false
}

// buildSourceView fills the window for one resolved finding.
func buildSourceView(view SourceView, st *store.Store, issue store.IssueRow) (SourceView, error) {
	view.File, view.AnchorLine = issue.File, issue.StartLine
	view.AnchorStart, view.AnchorEnd = issue.StartColumn, issue.EndColumn
	path, err := resolveSourcePath(st, issue)
	if err != nil {
		view.Reason = err.Error()
		return view, nil
	}
	data, err := readSourceWindow(path)
	if err != nil {
		view.Reason = err.Error()
		return view, nil
	}
	lines := splitLines(data)
	view.TotalLines = len(lines)
	view.Language = languageOf(issue.File)
	// An empty file is an empty window. Falling through would run the window
	// loop with bounds (0,0) and index lines[-1] — the file exists and was
	// read, it just has no lines, and that is a state the client renders as
	// "代码窗口为空". Lines is an empty slice rather than nil so the payload
	// carries "lines":[] and the client's emptiness test reads data.
	if len(lines) == 0 {
		view.Lines = []SourceLine{}
		return view, nil
	}
	first, last := windowBounds(issue.StartLine, len(lines))
	view.StartLine, view.EndLine, view.Truncated = first, last, first > 1 || last < len(lines)
	for n := first; n <= last; n++ {
		view.Lines = append(view.Lines, SourceLine{
			Number: n,
			Text:   lines[n-1],
			Class:  lineClass(lines[n-1]),
		})
	}
	// The anchor's byte span only means something for a column-aware finding;
	// a line-anchored finding is highlighted whole rather than pretending a
	// column the anchor never claimed.
	if issue.Anchor != "exact" {
		view.AnchorStart, view.AnchorEnd = 1, 0
	}
	return view, nil
}

// languageOf names a language by extension, for the view's own label. It maps
// to the same vocabulary the parser reports, so a view that says "go" and a
// check that checks go agree.
func languageOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cs":
		return "cs"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	}
	return "text"
}
