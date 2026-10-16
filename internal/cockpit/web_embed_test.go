package cockpit

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// devHarness names the files that live in web/ for `go test` and must never be
// reachable from the browser or from the release artifact.
var devHarness = []string{"web/fixtures.json", "web/smoke.mjs", "web/render_all.mjs"}

// embeddedWebFiles lists what the binary actually carries.
func embeddedWebFiles(t *testing.T) map[string]bool {
	t.Helper()
	embedded := map[string]bool{}
	err := fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			embedded[p] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded assets: %v", err)
	}
	return embedded
}

// requestedWebFiles follows the real request graph instead of a restated list:
// the shell's href/src under /web/ plus app.js's relative module imports. A
// browser asks for exactly these; anything else in web/ is dev-only.
func requestedWebFiles(t *testing.T) map[string]bool {
	t.Helper()
	wanted := map[string]bool{}
	for _, name := range []struct {
		file    string
		pattern string
	}{
		{"web/index.html", `(?:href|src)="/web/([^"]+)"`},
		{"web/app.js", `from\s+"(\./[^"]+)"`},
	} {
		data, err := fs.ReadFile(webFS, name.file)
		if err != nil {
			t.Fatalf("%s is not embedded: %v", name.file, err)
		}
		for _, m := range regexp.MustCompile(name.pattern).FindAllStringSubmatch(string(data), -1) {
			wanted[path.Join("web", m[1])] = true
		}
	}
	return wanted
}

// The embed directives in dashboard.go are an explicit list rather than a glob,
// so the dev harness that lives in web/ does not ride along in the shipped
// binary. That choice only stays safe while the list is complete: forget one
// directive and the browser gets a 404 for a file that works perfectly on disk,
// which is the worst kind of miss because every Go-side test still passes.
func TestEmbeddedAssetsCoverTheRequestGraph(t *testing.T) {
	embedded := embeddedWebFiles(t)
	wanted := requestedWebFiles(t)

	for _, drop := range devHarness {
		if embedded[drop] {
			t.Errorf("%s is embedded: dev-only harness must not ship in the binary", drop)
		}
	}
	for want := range wanted {
		if !embedded[want] {
			t.Errorf("%s is requested by the UI but not embedded; add a //go:embed directive", want)
		}
	}
	// The reverse direction catches a renamed asset: an embedded file nothing
	// asks for means the shell still points at the old name.
	for have := range embedded {
		if have == "web/index.html" || wanted[have] {
			continue
		}
		if strings.HasPrefix(filepath.Base(have), ".") {
			continue
		}
		t.Errorf("%s is embedded but nothing requests it (unreferenced asset or a stale reference)", have)
	}
}

// The cockpit is a single binary that ships its own UI, so "the page loads" is
// an assertable fact: every asset and every endpoint answers over the same
// handler the server mounts. A missing embed directive or a route typo shows up
// here as a 404 that no browser would ever explain to anyone.
func TestEmbeddedUIServesEveryAssetAndEndpoint(t *testing.T) {
	handler := Handler("", "")
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	// Content types are asserted per asset, not just reached. app.js is loaded
	// as a module and spec.json is imported with `with { type: "json" }`: a
	// browser refuses either under the wrong MIME type, and the page then shows
	// nothing while every byte of it is present and every status is 200.
	assets := map[string][]string{
		"/":              {"text/html"},
		"/web/app.js":    {"text/javascript", "application/javascript"},
		"/web/style.css": {"text/css"},
		"/web/spec.json": {"application/json"},
	}
	for path, wants := range assets {
		rec := get(path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200: %s", path, rec.Code, rec.Body.String())
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s served an empty body", path)
		}
		ct := rec.Header().Get("Content-Type")
		matched := false
		for _, want := range wants {
			if strings.HasPrefix(ct, want) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s content-type = %q, want one of %v", path, ct, wants)
		}
		// Embedded assets have no modtime, so the file server emits no
		// validator. Without no-store a browser may reuse a heuristically
		// cached copy and the operator keeps seeing the previous build's UI
		// after a rebuild - the symptom is "I restarted it and nothing changed".
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s cache-control = %q, want no-store", path, got)
		}
	}
	// Endpoints answer JSON with and without a store: an empty environment is
	// the state a first-run operator is in.
	for _, path := range []string{"/api/spec", "/api/issues", "/api/gate", "/api/measures", "/api/policy"} {
		rec := get(path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("%s content-type = %q, want json", path, ct)
		}
	}
	// The shell must reference what it serves, and carry the hooks the views
	// write into. A renamed id is invisible in the browser until a view dies.
	index := get("/").Body.String()
	for _, want := range []string{
		"/web/style.css", "/web/app.js",
		`id="viewStage"`, `id="detailDialog"`, `id="detailTitle"`, `id="detailBody"`,
		`data-view="overview"`, `data-view="policy"`, `data-action="refresh"`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}
	// The loopback guard is what keeps a local tool local: a non-loopback Host
	// is refused on every API path.
	req := httptest.NewRequest(http.MethodGet, "/api/spec", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-loopback host = %d, want 403", rec.Code)
	}
}
