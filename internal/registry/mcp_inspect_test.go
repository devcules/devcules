package registry

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// fakeMCPServerScript is a minimal MCP stdio server: handles initialize and
// tools/list, ignores the initialized notification (no id, no reply
// expected). Good enough to exercise the real handshake without needing an
// actual npm/pypi package downloaded during tests.
const fakeMCPServerScript = `
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    method = req.get("method")
    if method == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {
            "protocolVersion": "2024-11-05", "capabilities": {}, "serverInfo": {"name": "fake", "version": "0"}
        }}))
        sys.stdout.flush()
    elif method == "tools/list":
        print(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {
            "tools": [{"name": "echo", "description": "Echoes its input"},
                      {"name": "delete_everything", "description": "Deletes everything"}]
        }}))
        sys.stdout.flush()
`

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

func TestMCPInspectorDiscoversRealToolList(t *testing.T) {
	requirePython3(t)
	ins := &MCPInspector{Timeout: 5 * time.Second}
	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{
		Type: "stdio", Command: "python3", Args: []string{"-c", fakeMCPServerScript},
	}}
	insp, err := ins.Inspect(context.Background(), entry)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Warning != "" {
		t.Fatalf("expected no warning, got %q", insp.Warning)
	}
	if len(insp.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %+v", insp.Tools)
	}
	names := map[string]string{insp.Tools[0].Name: insp.Tools[0].Description, insp.Tools[1].Name: insp.Tools[1].Description}
	if names["echo"] != "Echoes its input" || names["delete_everything"] != "Deletes everything" {
		t.Fatalf("unexpected tools: %+v", insp.Tools)
	}
}

// Regression: the real @modelcontextprotocol/server-everything reference
// server sends an unsolicited "notifications/tools/list_changed" BEFORE
// replying to initialize -- found via live testing, where it silently
// produced "0 tools" instead of erroring, because a single blind readline
// per request consumed the notification as if it were the initialize
// response, then read the real initialize response when it should have
// read the tools/list response. A correct client must correlate replies
// by request id, not assume strict one-line-per-request ordering.
const fakeMCPServerWithInterleavedNotification = `
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    method = req.get("method")
    if method == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "method": "notifications/tools/list_changed"}))
        sys.stdout.flush()
        print(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"protocolVersion": "2024-11-05"}}))
        sys.stdout.flush()
    elif method == "tools/list":
        print(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"tools": [{"name": "real-tool", "description": "the real one"}]}}))
        sys.stdout.flush()
`

func TestMCPInspectorHandlesInterleavedNotifications(t *testing.T) {
	requirePython3(t)
	ins := &MCPInspector{Timeout: 5 * time.Second}
	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{
		Type: "stdio", Command: "python3", Args: []string{"-c", fakeMCPServerWithInterleavedNotification},
	}}
	insp, err := ins.Inspect(context.Background(), entry)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Warning != "" {
		t.Fatalf("expected no warning, got %q", insp.Warning)
	}
	if len(insp.Tools) != 1 || insp.Tools[0].Name != "real-tool" {
		t.Fatalf("expected the real tool list despite the interleaved notification, got %+v", insp.Tools)
	}
}

func TestMCPInspectorReportsWarningWhenCommandMissing(t *testing.T) {
	ins := &MCPInspector{Timeout: 2 * time.Second}
	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{
		Type: "stdio", Command: "this-binary-does-not-exist-kaithos-test",
	}}
	insp, err := ins.Inspect(context.Background(), entry)
	if err != nil {
		t.Fatalf("Inspect should not hard-error, got: %v", err)
	}
	if insp.Warning == "" {
		t.Fatal("expected a warning for a missing binary")
	}
}

func TestMCPInspectorTimesOutOnSilentServer(t *testing.T) {
	requirePython3(t)
	start := time.Now()
	ins := &MCPInspector{Timeout: 300 * time.Millisecond}
	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{
		Type: "stdio", Command: "python3", Args: []string{"-c", "import time; time.sleep(30)"},
	}}
	insp, err := ins.Inspect(context.Background(), entry)
	if err != nil {
		t.Fatalf("Inspect should not hard-error on timeout, got: %v", err)
	}
	if insp.Warning == "" {
		t.Fatal("expected a warning when the server never responds")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("expected the timeout to bound the call tightly, took %v", elapsed)
	}
}

func TestMCPInspectorSkipsRemoteServersWithoutRunningAnything(t *testing.T) {
	ins := &MCPInspector{}
	entry := CatalogEntry{Kind: KindMCP, Install: MCPInstallSpec{Type: "http", URL: "https://example.com/mcp"}}
	insp, err := ins.Inspect(context.Background(), entry)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if insp.Warning == "" {
		t.Fatal("expected a warning explaining remote servers aren't dry-run inspected")
	}
}
