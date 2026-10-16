package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The version this server announces is a claim made to the host, so it is
// either true or it is a lie. task-2 (iter-19) turned the constant into a var
// so a release build can inject the real value, but that fix has a failure
// mode Go never reports: -ldflags -X addresses the *link* symbol, and a
// `package main` binary links its symbols as main.<name> no matter which
// directory the source sits in. The package-qualified form
// (-X metricgate/cmd/metricgate-mcp.toolVersion=...) matches nothing, the
// build exits 0, and the server keeps announcing the fallback. A release
// pipeline that copied that recipe would ship a silently wrong version on the
// one surface agents read.
//
// So this is not a test of string formatting. It builds the real binary twice
// with two different -X spellings and asserts the injected value actually
// reaches serverInfo.version over stdio, which is the only observation that
// distinguishes "injected" from "still 0.1.0".

// announceVersion builds ./cmd/metricgate-mcp with the given -ldflags and
// returns the version the resulting server announces on its stdio transport.
func announceVersion(t *testing.T, ldflags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "metricgate-mcp-probe")
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "./cmd/metricgate-mcp")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build (%s): %v\n%s", ldflags, err, out)
	}
	return initializeVersion(t, bin)
}

// initializeVersion speaks a single JSON-RPC initialize to the server and
// reads back the serverInfo block.
func initializeVersion(t *testing.T, bin string) string {
	t.Helper()
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "version_test", "version": "1"},
		},
	}
	if err := json.NewEncoder(stdin).Encode(req); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var resp struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(out).Decode(&resp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize returned an error: %v", resp.Error)
	}
	if resp.Result.ServerInfo.Name != "metricgate" {
		t.Fatalf("unexpected serverInfo.name %q", resp.Result.ServerInfo.Name)
	}
	return resp.Result.ServerInfo.Version
}

// The two tests below compile the real binary, so they cost ~30s together.
// They are skipped under -short, which is how a quick `go test ./...` passes;
// run them without -short before cutting a release, since they are the only
// thing that checks the injection recipe a pipeline would copy.

// TestToolVersionIsInjectableByMainSymbol is the executable form of the
// comment on toolVersion: the link-symbol spelling injects, and the value the
// host sees is the value the release build asked for.
func TestToolVersionIsInjectableByMainSymbol(t *testing.T) {
	releaseBuildGuard(t)
	const want = "9.9.9-test"
	if got := announceVersion(t, "-X main.toolVersion="+want); got != want {
		t.Errorf("-X main.toolVersion=%s: serverInfo.version = %q, want %q", want, got, want)
	}
}

// TestPackageQualifiedVersionSymbolDoesNotInject pins the trap so nobody
// re-documents it as the recipe. It asserts the *absence* of an effect, so a
// future Go release that starts warning about unknown -X symbols would fail
// this test loudly instead of leaving a false comment in place.
func TestPackageQualifiedVersionSymbolDoesNotInject(t *testing.T) {
	releaseBuildGuard(t)
	const injected = "8.8.8-silently-ignored"
	ldflags := fmt.Sprintf("-X metricgate/cmd/metricgate-mcp.toolVersion=%s", injected)
	got := announceVersion(t, ldflags)
	if got == injected {
		t.Fatalf("package-qualified -X now injects (%q): the comment on toolVersion and "+
			"docs/iterations/iter-19 must be rewritten to use it", injected)
	}
	if !strings.Contains(got, "0.1.0") && got != toolVersion {
		t.Errorf("unexpected fallback version %q, want the in-source default %q", got, toolVersion)
	}
}

// releaseBuildGuard keeps these tests off the fast path: each one shells out
// to the go tool, so they are skipped under -short (a quick `go test ./...`)
// and run when the suite is invoked normally, e.g. before cutting a release.
func releaseBuildGuard(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping release-build version check in short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatalf("go tool not on PATH, cannot verify the injection recipe: %v", err)
	}
}

// repoRoot resolves the module root from this file's location instead of the
// working directory, so the test does not care where `go test` was invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
