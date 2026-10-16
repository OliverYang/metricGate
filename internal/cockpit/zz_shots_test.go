package cockpit

import (
	"os"
	"os/exec"
	"testing"
)

// TestZZDumpShotsPayloads is a scratch helper: it writes the live payloads the
// screenshot harness renders, using the same seeded store the live-payload test
// uses. Delete after the visual pass; it is not part of the suite.
func TestZZDumpShotsPayloads(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	logPath, dbPath := seedLiveCockpit(t)
	payloads := capturePayloads(t, node, Handler(logPath, dbPath))
	if err := writePayloads("/tmp/shots-payloads.json", payloads); err != nil {
		t.Fatal(err)
	}
	_ = os.Stdout
	t.Logf("wrote %d payloads", len(payloads))
}
