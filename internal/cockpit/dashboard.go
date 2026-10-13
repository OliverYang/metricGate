package cockpit

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/calllog"
	"metricgate/internal/store"
)

//go:embed web/*
var webFS embed.FS

// Dashboard is the stable payload consumed by the audit UI.
type Dashboard struct {
	GeneratedAt      string             `json:"generatedAt"`
	LogPath          string             `json:"logPath"`
	Calls            []DashboardCall    `json:"calls"`
	Summary          calllog.Summary    `json:"summary"`
	Runs             []store.RunRecord  `json:"runs"`
	ArchivedRuns     []store.RunRecord  `json:"archivedRuns"`
	Trend            store.TrendReport  `json:"trend"`
	MetricTrend      store.MetricTrend  `json:"metricTrend,omitempty"`
	ArchivedTrend    store.TrendReport  `json:"archivedTrend"`
	Findings         []DashboardFinding `json:"findings"`
	ArchivedFindings []DashboardFinding `json:"archivedFindings"`
	Projects         []ProjectSummary   `json:"projects"`
	UnassignedCalls  int                `json:"unassignedCalls"`
	Accepted         []accept.Entry     `json:"accepted,omitempty"`
}

// DashboardCall adds only a verified project root to the original local call record.
type DashboardCall struct {
	calllog.Record
	ProjectRoot string `json:"projectRoot,omitempty"`
}

// ProjectSummary groups calls only when their absolute target resolves inside a Git worktree.
type ProjectSummary struct {
	ID               string             `json:"id"`
	Root             string             `json:"root"`
	Name             string             `json:"name"`
	Calls            int                `json:"calls"`
	Flagged          int                `json:"flagged"`
	Errors           int                `json:"errors"`
	Warnings         int                `json:"warnings"`
	LatestActivity   string             `json:"latestActivity"`
	Runs             []store.RunRecord  `json:"runs"`
	ArchivedRuns     []store.RunRecord  `json:"archivedRuns"`
	Trend            store.TrendReport  `json:"trend"`
	ArchivedTrend    store.TrendReport  `json:"archivedTrend"`
	Findings         []DashboardFinding `json:"findings"`
	ArchivedFindings []DashboardFinding `json:"archivedFindings"`
}

var targetPathPattern = regexp.MustCompile(`(^|[[:space:]])path=(.*?)([[:space:]]+(store|query|metric|name)=|$)`)

// DashboardFinding keeps the latest findings available without exposing source code.
type DashboardFinding struct {
	RunID int64       `json:"runId"`
	Item  interface{} `json:"item"`
}

// NewDashboard reads the local call log and history store. Missing optional
// files produce an empty dashboard so the UI remains useful before first run.
func NewDashboard(logPath, dbPath string) (Dashboard, error) {
	d := Dashboard{LogPath: logPath, Calls: []DashboardCall{}, Runs: []store.RunRecord{}, ArchivedRuns: []store.RunRecord{}, Findings: []DashboardFinding{}, ArchivedFindings: []DashboardFinding{}}
	// Concession ledger: read-only display. A missing file is normal
	// (nothing ever accepted); other read errors surface in the UI as a
	// note rather than failing the whole dashboard.
	if l, err := accept.Load(accept.DefaultPath); err == nil && len(l.Entries) > 0 {
		d.Accepted = l.Entries
	}
	recs, skipped, err := calllog.Read(logPath)
	if err != nil && !os.IsNotExist(err) {
		return d, err
	}
	d.Calls, d.Projects, d.UnassignedCalls = groupCallsByProject(recs)
	d.Summary = calllog.Summarize(recs)
	d.Summary.Log = logPath
	d.Summary.Skipped = skipped
	if dbPath == "" {
		return d, nil
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return d, err
	}
	defer st.Close()
	d.Runs, err = st.LatestUnassignedRuns(20)
	if err != nil {
		return d, err
	}
	d.Trend, err = st.ReportUnassigned(20)
	if err != nil {
		return d, err
	}
	d.MetricTrend, err = st.MetricTrend(20)
	if err != nil {
		return d, err
	}
	d.ArchivedRuns, err = st.LatestArchivedUnassignedRuns(100)
	if err != nil {
		return d, err
	}
	d.ArchivedTrend, err = st.ReportArchivedUnassigned(100)
	if err != nil {
		return d, err
	}
	if err := attachProjectRuns(st, &d); err != nil {
		return d, err
	}
	if err := attachRunFindings(st, &d.Findings, d.Runs); err != nil {
		return d, err
	}
	if err := attachRunFindings(st, &d.ArchivedFindings, d.ArchivedRuns); err != nil {
		return d, err
	}
	return d, nil
}

func attachProjectRuns(st *store.Store, dashboard *Dashboard) error {
	known := make(map[string]bool, len(dashboard.Projects))
	for i := range dashboard.Projects {
		known[dashboard.Projects[i].Root] = true
	}
	roots, err := st.ProjectRoots()
	if err != nil {
		return err
	}
	for _, root := range roots {
		if !known[root] {
			dashboard.Projects = append(dashboard.Projects, ProjectSummary{ID: root, Root: root, Name: filepath.Base(root)})
		}
	}
	sort.Slice(dashboard.Projects, func(i, j int) bool { return dashboard.Projects[i].Name < dashboard.Projects[j].Name })
	for i := range dashboard.Projects {
		project := &dashboard.Projects[i]
		runs, err := st.LatestRunsForProject(20, project.Root)
		if err != nil {
			return err
		}
		project.Runs = runs
		project.Trend, err = st.ReportForProject(20, project.Root)
		if err != nil {
			return err
		}
		project.ArchivedRuns, err = st.LatestArchivedRunsForProject(100, project.Root)
		if err != nil {
			return err
		}
		project.ArchivedTrend, err = st.ReportArchivedForProject(100, project.Root)
		if err != nil {
			return err
		}
		if err := attachRunFindings(st, &project.Findings, runs); err != nil {
			return err
		}
		if err := attachRunFindings(st, &project.ArchivedFindings, project.ArchivedRuns); err != nil {
			return err
		}
	}
	return nil
}

func attachRunFindings(st *store.Store, destination *[]DashboardFinding, runs []store.RunRecord) error {
	for _, run := range runs {
		items, err := st.RunFindings(run.ID)
		if err != nil {
			return err
		}
		for _, item := range items {
			*destination = append(*destination, DashboardFinding{RunID: run.ID, Item: item})
		}
	}
	return nil
}

func groupCallsByProject(records []calllog.Record) ([]DashboardCall, []ProjectSummary, int) {
	calls := make([]DashboardCall, 0, len(records))
	projects := make(map[string]*ProjectSummary)
	unassigned := 0
	for _, record := range records {
		call := DashboardCall{Record: record, ProjectRoot: record.ProjectPath}
		root := record.ProjectPath
		if root == "" {
			root = gitRootForPath(targetPath(record.Target))
		}
		if root == "" {
			unassigned++
		} else {
			call.ProjectRoot = root
			updateProjectSummary(projects, root, record)
		}
		calls = append(calls, call)
	}
	return calls, sortedProjects(projects), unassigned
}

func updateProjectSummary(projects map[string]*ProjectSummary, root string, record calllog.Record) {
	project := projects[root]
	if project == nil {
		project = &ProjectSummary{ID: root, Root: root, Name: filepath.Base(root)}
		projects[root] = project
	}
	project.Calls++
	if record.Verdict != "" && !strings.HasPrefix(strings.ToLower(record.Verdict), "pass") {
		project.Flagged++
	}
	project.Errors += record.Errors
	project.Warnings += record.Warnings
	if record.Time.After(parseActivityTime(project.LatestActivity)) {
		project.LatestActivity = record.Time.Format(time.RFC3339)
	}
}

func sortedProjects(projects map[string]*ProjectSummary) []ProjectSummary {
	result := make([]ProjectSummary, 0, len(projects))
	for _, project := range projects {
		result = append(result, *project)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func targetPath(target string) string {
	matches := targetPathPattern.FindStringSubmatch(target)
	if len(matches) != 5 || !filepath.IsAbs(matches[2]) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(matches[2])))
	if err != nil {
		return ""
	}
	return resolved
}

func gitRootForPath(target string) string {
	info, err := os.Stat(target)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		target = filepath.Dir(target)
	}
	for dir := target; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

func parseActivityTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

// Handler serves the dashboard and its local-only JSON endpoint.
type archiveRequest struct {
	RunIDs      []int64 `json:"runIds"`
	ProjectRoot string  `json:"projectRoot"`
	Archived    bool    `json:"archived"`
}

func Handler(logPath, dbPath string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dashboard", dashboardAPI(logPath, dbPath))
	mux.HandleFunc("/api/runs/archive", archiveAPI(dbPath))
	mux.HandleFunc("/api/policy", policyAPI())
	webMount(mux)
	return withLocalAPIGuard(mux)
}

// dashboardAPI serves GET /api/dashboard: the aggregated project/call/run view.
func dashboardAPI(logPath, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		d, err := NewDashboard(logPath, dbPath)
		if err != nil {
			http.Error(w, "dashboard data unavailable: "+err.Error(), http.StatusInternalServerError)
			return
		}
		d.GeneratedAt = nowUTC()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d)
	}
}

// archiveAPI serves POST /api/runs/archive: the reversible archive/restore
// batch operation over the run history. The request path is deliberately
// paranoid: strict content type, a size cap, unknown-field rejection, a
// trailing-data check, and a store round trip that never leaves a handle open.
func archiveAPI(dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "same-origin request required", http.StatusForbidden)
			return
		}
		request, ok := decodeArchiveRequest(w, r)
		if !ok {
			return
		}
		if dbPath == "" {
			http.Error(w, "history store is disabled", http.StatusServiceUnavailable)
			return
		}
		applyArchive(w, dbPath, request)
	}
}

// decodeArchiveRequest parses and validates the archive request body, writing
// the error response itself when the body is not a well-formed request. The
// bool result is the go/no-go for the store operation.
func decodeArchiveRequest(w http.ResponseWriter, r *http.Request) (archiveRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return archiveRequest{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var request archiveRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !validArchiveRequest(request) {
		http.Error(w, "invalid archive request", http.StatusBadRequest)
		return archiveRequest{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "invalid archive request", http.StatusBadRequest)
		return archiveRequest{}, false
	}
	return request, true
}

// applyArchive opens the store, performs the batch state change and writes the
// response. Every failure path closes the store before answering.
func applyArchive(w http.ResponseWriter, dbPath string, request archiveRequest) {
	st, err := store.Open(dbPath)
	if err != nil {
		http.Error(w, "archive operation rejected", http.StatusConflict)
		return
	}
	if err := st.SetRunsArchived(request.RunIDs, request.ProjectRoot, request.Archived); err != nil {
		_ = st.Close()
		http.Error(w, "archive operation rejected", http.StatusConflict)
		return
	}
	if err := st.Close(); err != nil {
		http.Error(w, "archive operation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// webMount registers the embedded static assets on the mux. When the
// embedded filesystem cannot be opened the mux gets an error fallback for
// every remaining path.
func webMount(mux *http.ServeMux) {
	webFiles, err := fs.Sub(webFS, "web")
	if err != nil {
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "dashboard assets unavailable", http.StatusInternalServerError)
		}))
		return
	}
	webHandler := http.FileServer(http.FS(webFiles))
	mux.Handle("/web/", http.StripPrefix("/web/", webHandler))
	mux.Handle("/", webHandler)
}

// withLocalAPIGuard rejects non-loopback hosts on API routes while leaving
// static assets reachable, then delegates to the mux.
func withLocalAPIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !allowLocalHost(r) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowLocalHost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func validArchiveRequest(request archiveRequest) bool {
	if len(request.RunIDs) == 0 || len(request.RunIDs) > 100 {
		return false
	}
	seen := make(map[int64]struct{}, len(request.RunIDs))
	for _, id := range request.RunIDs {
		if id <= 0 {
			return false
		}
		if _, ok := seen[id]; ok {
			return false
		}
		seen[id] = struct{}{}
	}
	return request.ProjectRoot == "" || filepath.IsAbs(request.ProjectRoot)
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }
