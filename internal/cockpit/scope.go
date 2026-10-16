package cockpit

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/calllog"
	"metricgate/internal/store"
)

// The scope token is how every workspace endpoint decides which runs it is
// allowed to talk about.
//
// Without it the server answers with whatever directory happened to own the
// process: the gate badge, the measures table and the policy editor all read
// the same local history database, so on a machine where several projects
// share one database every view silently describes the wrong project. An
// operator who then reads "gate passed" has no way to know it graded
// somebody else's code, which is worse than an error.
const scopeParam = "scope"

// scopeAll is the explicit form of "no scope": every project and the
// unassigned runs together. It exists because the UI has to send something to
// clear a previously-selected scope, and an empty query parameter is dropped
// by the fetch layer before it reaches the server.
const scopeAll = "all"

// scopeUnassigned is the run-history bucket with no project identity — runs
// recorded before project roots existed, or recorded outside any git root.
// It is a name, not a path, precisely so that it cannot be confused with a
// project the operator can type.
const scopeUnassigned = "unassigned"

// errUnknownScope is the base refusal every scope endpoint turns into HTTP 400.
// Its text is the response body, so it has to read as an answer, not a log
// line: the UI shows it verbatim in the error banner.
var errUnknownScope = fmt.Errorf("unknown scope")

// unknownScopeError names the rejected token and what may be chosen instead.
//
// The list is capped because the message is shown in a banner, not written to
// a log: an operator with 40 projects needs to know that the path they typed
// is not one the cockpit has seen, not a wall of directories. The cap keeps
// the refusal readable while still making the common mistake — a stale or
// hand-edited bookmark — self-explanatory.
func unknownScopeError(token string, roots []string) error {
	available := scopeList(roots, 5)
	if available == "" {
		return fmt.Errorf("%w: %s", errUnknownScope, token)
	}
	return fmt.Errorf("%w: %s (known projects: %s)", errUnknownScope, token, available)
}

// scopeList renders the whitelist for an operator-facing message, sorted so
// the same database always produces the same sentence.
func scopeList(roots []string, max int) string {
	items := make([]string, 0, len(roots))
	for _, root := range roots {
		if strings.TrimSpace(root) != "" {
			items = append(items, root)
		}
	}
	sort.Strings(items)
	if max > 0 && len(items) > max {
		return strings.Join(items[:max], ", ") + fmt.Sprintf(" and %d more", len(items)-max)
	}
	return strings.Join(items, ", ")
}

// resolveScope turns ?scope= into the run window the endpoint should query.
//
// The contract is a whitelist, not a parser: the token is either one of the
// two reserved names or a project root the server has already seen. Anything
// else is refused even when the path exists on disk, because a
// browser-reachable endpoint that accepts arbitrary directories is a way to
// read files outside the workspace.
//
// extraRoots is the second half of the whitelist — the projects the dashboard
// shows, which come from the local call log rather than from the run history.
// It is variadic because a scope resolver whose second source is missing
// would be worse than one that has both: a project card rendered from the
// call log would lead to a 400 "unknown scope" on a project the operator can
// plainly see. Callers that have no call log simply omit it.
func resolveScope(r *http.Request, dbPath string, extraRoots ...string) (store.Window, error) {
	token := strings.TrimSpace(r.URL.Query().Get(scopeParam))
	switch {
	case token == "" || token == scopeAll:
		// The zero window is "every run", which is what "all" means.
		return store.Window{}, nil
	case token == scopeUnassigned:
		return store.Window{Unassigned: true}, nil
	}
	roots, err := knownProjectRoots(dbPath, extraRoots)
	if err != nil {
		return store.Window{}, err
	}
	if match, ok := matchScopeRoot(roots, token); ok {
		return store.Window{ProjectRoot: match}, nil
	}
	return store.Window{}, unknownScopeError(token, roots)
}

// applyScope writes the scope onto a window that already carries a run
// window. It is separate from resolveScope because the endpoints do not want
// the same thing: the gate and the measures table want one run, so they take
// the window as the whole query, while the issue list wants the last N runs
// *of the selected project*, so it must keep its own limit and lower bound
// instead of inheriting the scope's.
func applyScope(dst *store.Window, src store.Window) {
	dst.ProjectRoot = src.ProjectRoot
	dst.Unassigned = src.Unassigned
}

// knownProjectRoots is the scope whitelist: the project roots in the run
// history, plus the roots the caller saw somewhere else it trusts (the
// dashboard's call-log projects).
//
// Both halves are needed. Store roots alone would refuse a project the
// project picker renders from the call log; call-log roots alone would accept
// a directory that has never been checked, where "no findings" and "no such
// project" are indistinguishable. A call log that cannot be read contributes
// nothing rather than failing the request: one broken source of a whitelist
// must not turn a working endpoint into a 500.
func knownProjectRoots(dbPath string, extraRoots []string) ([]string, error) {
	var roots []string
	if strings.TrimSpace(dbPath) != "" {
		st, err := store.Open(dbPath)
		if err != nil {
			return nil, err
		}
		defer func() { _ = st.Close() }()
		dbRoots, err := st.ProjectRoots()
		if err != nil {
			return nil, err
		}
		roots = append(roots, dbRoots...)
	}
	roots = append(roots, extraRoots...)
	return dedupePaths(roots), nil
}

// dedupePaths drops repeated spellings of one directory so a root recorded
// both in the run history and in the call log is listed once.
func dedupePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		key := canonicalScopePath(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
	}
	return out
}

// callLogProjectRoots is the call-log half of the whitelist: the distinct
// project paths the local MCP calls were made under.
//
// /api/dashboard already reads this same log on every request, so this is not
// a new class of cost — it is the same fact handed to the three endpoints that
// currently cannot name the projects the dashboard shows them.
func callLogProjectRoots(logPath string) []string {
	if strings.TrimSpace(logPath) == "" {
		return nil
	}
	records, _, err := calllog.Read(logPath)
	if err != nil {
		return nil
	}
	roots := make([]string, 0, len(records))
	for _, record := range records {
		roots = append(roots, record.ProjectPath)
	}
	return dedupePaths(roots)
}

// matchScopeRoot reports the recorded root a token names, if any.
//
// Comparison is on canonical form, equality in the recorded spelling is what
// goes into the query: SQLite filters runs with `project_root = ?`, so passing
// the canonical form would find nothing when a recorded root itself contains
// a symlink. Two spellings of one directory (/tmp/x and /private/tmp/x on
// macOS) must therefore resolve to the same recorded row.
func matchScopeRoot(roots []string, token string) (string, bool) {
	needle := canonicalScopePath(token)
	for _, root := range roots {
		if canonicalScopePath(root) == needle {
			return root, true
		}
	}
	return "", false
}

// canonicalScopePath normalises a path for scope comparison: absolute, then
// symlink-resolved, falling back to the cleaned spelling when the OS refuses.
// A token that cannot be resolved simply cannot match a recorded root, so the
// refusal still happens — normalisation never turns a wrong path into a hit.
func canonicalScopePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		absolute = trimmed
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(absolute)
}

// scopeWindow is the one-line form for a handler that has already read its own
// window: the scope may only narrow it, never widen it.
//
// That restriction is the whole point. The issue list arrives with a run window
// of its own — the last 60 runs — and a scope that replaced it would turn
// "the recent issues of this project" into "every issue this project ever had",
// which is a different question and a much slower one. Clearing the window for
// the global scope would be the same mistake in the other direction: "all
// projects" is a statement about which runs qualify, not permission to read
// every run the database has ever recorded, and on a shared history file that
// is the difference between a page that loads and one that times out. So the
// scope contributes a filter and nothing else.
func scopeWindow(dst *store.Window, src store.Window) {
	applyScope(dst, src)
}

// scopeRunWindow is the one-run lookup the gate and the measures table need:
// the newest run inside a scope, whatever that scope is.
//
// The unassigned flag is carried because "the newest run with no project
// identity" is a different record from "the newest run anywhere", and reading
// the wrong one puts somebody else's verdict on the badge.
func scopeRunWindow(scope store.Window) store.Window {
	return store.Window{Limit: 1, ProjectRoot: scope.ProjectRoot, Unassigned: scope.Unassigned}
}

// scopeOrigin answers "which directory should policy discovery start in".
//
// The fallback to the process working directory is what keeps an old bookmark
// working: a request with no scope, or with scope=all, is exactly the request
// the cockpit served before scopes existed, and it must land on the repository
// the server was started in. An unassigned scope has no directory of its own —
// it is a bucket of runs with no project identity — so it reads the same policy
// as the global view rather than inventing one.
func scopeOrigin(w store.Window) string {
	if w.ProjectRoot != "" {
		return w.ProjectRoot
	}
	return policyRoot()
}
