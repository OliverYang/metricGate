package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"testing"
)

// mcpLine speaks one stdio JSON-RPC conversation with a metricgate-mcp process.
//
// It exists so parity_diff_test.go reads as "ask the server, take the answer"
// instead of as a transport tutorial, and so the failure message can show what
// the server actually said: a gate that silently returns a different shape on
// the wire is exactly the kind of divergence this suite is looking for, and
// "read response: EOF" alone would not identify it.
type mcpLine struct {
	stdin  io.Writer
	stdout io.Reader
	stderr *bytes.Buffer
	t      *testing.T
}

// startMCP launches the server with dir as its working directory. That is the
// knob that decides which .metricgate.yaml gets discovered and what a relative
// check_path argument resolves against, so it is part of the comparison, not
// incidental to it.
func startMCP(t *testing.T, bin, dir string) *mcpLine {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = dir
	line := &mcpLine{stderr: &bytes.Buffer{}, t: t}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	line.stdin, line.stdout = stdin, stdout
	cmd.Stderr = line.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line.initialize()
	return line
}

func (l *mcpLine) send(obj map[string]any) {
	l.t.Helper()
	if err := json.NewEncoder(l.stdin).Encode(obj); err != nil {
		l.t.Fatalf("send %v: %v", obj["method"], err)
	}
}

// recv reads one response, dumping the server's stderr when the stream does not
// produce one -- a server that died during policy load exits before answering,
// and its reason only ever appears on stderr.
func (l *mcpLine) recv() map[string]any {
	l.t.Helper()
	var resp map[string]any
	if err := json.NewDecoder(l.stdout).Decode(&resp); err != nil {
		rest, _ := io.ReadAll(l.stdout)
		l.t.Fatalf("read response: %v\nstderr: %s\nstdout tail: %q",
			err, firstLine(l.stderr.String()), firstLine(string(rest)))
	}
	return resp
}

func (l *mcpLine) initialize() {
	l.t.Helper()
	l.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "parity_test", "version": "1"},
	}})
	if resp := l.recv(); resp["error"] != nil {
		l.t.Fatalf("initialize failed: %v", resp["error"])
	}
	l.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// callTool invokes a tool and decodes its structured result into out, which must
// be a pointer. isError and a transport-level error are both fatal: the parity
// claim is about the answer two surfaces give, not about how a call fails.
func (l *mcpLine) callTool(name string, args map[string]any, out any) {
	l.t.Helper()
	l.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name":      name,
		"arguments": args,
	}})
	resp := l.recv()
	if resp["error"] != nil {
		l.t.Fatalf("%s failed: %v", name, resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if bad, _ := result["isError"].(bool); bad {
		l.t.Fatalf("%s returned an error result: %v", name, result["content"])
	}
	raw, err := json.Marshal(result["structuredContent"])
	if err != nil {
		l.t.Fatalf("re-encode %s structuredContent: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		l.t.Fatalf("decode %s output %s: %v", name, firstLine(string(raw)), err)
	}
}

// callCheckPath drives check_path with diff_only=true over the real transport.
func (l *mcpLine) callCheckPath(root string) checkPathOut {
	l.t.Helper()
	var out checkPathOut
	l.callTool("check_path", map[string]any{"path": root, "diff_only": true}, &out)
	return out
}
