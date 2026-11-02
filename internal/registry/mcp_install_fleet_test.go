package registry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// mcp_install_fleet_test.go — the fleet merge over a fake HOME: claude's
// ~/.claude.json and codex's config.toml, deduped by name+detail.

func writef(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanMCPFleetMergesHarnesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writef(t, filepath.Join(home, ".claude.json"), `{
		"mcpServers": {
			"kaithos-antro": {"type": "http", "url": "http://127.0.0.1:7659/api/v1/mcp"},
			"claude-only": {"type": "stdio", "command": "uvx", "args": ["some-server"]}
		}
	}`)
	writef(t, filepath.Join(home, ".codex", "config.toml"), `
[mcp_servers.kaithos-antro]
url = "http://127.0.0.1:7659/api/v1/mcp"
bearer_token_env_var = "KAITHOS_MCP_TOKEN"

[mcp_servers.codex-only]
command = "npx"
args = ["-y", "codex-server"]

[mcp_servers.known]
`)
	_ = "known"

	fleet := ScanMCPFleet(context.Background(), "/nonexistent-codex-bin")
	byName := map[string]MCPFleetItem{}
	for _, it := range fleet {
		byName[it.Name] = it
	}
	if len(fleet) != 4 {
		t.Fatalf("fleet: %+v", fleet)
	}
	both := byName["kaithos-antro"]
	if len(both.Harnesses) != 2 {
		t.Fatalf("kaithos-antro should be in both harnesses: %+v", both)
	}
	if byName["claude-only"].Harnesses[0] != "claude-code" {
		t.Fatalf("claude-only: %+v", byName["claude-only"])
	}
	if byName["codex-only"].Detail != "npx" {
		t.Fatalf("codex-only detail: %+v", byName["codex-only"])
	}
	if byName["known"].Harnesses[0] != "codex" {
		t.Fatalf("known (empty detail) should still list codex: %+v", byName["known"])
	}
}

func TestTomlValue(t *testing.T) {
	for _, tc := range []struct {
		line, key, want string
	}{
		{`url = "http://x"`, "url", "http://x"},
		{`command = "npx"`, "command", "npx"},
		{`bearer_token_env_var = "TOK"`, "bearer_token_env_var", "TOK"},
		{`url="http://y"`, "url", "http://y"},
		{`args = ["a"]`, "url", ""}, // not url
	} {
		got, ok := tomlValue(tc.line, tc.key)
		if tc.want == "" && ok {
			t.Errorf("tomlValue(%q,%q) = %q, want no match", tc.line, tc.key, got)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("tomlValue(%q,%q) = %q want %q", tc.line, tc.key, got, tc.want)
		}
	}
}

func TestCodexInstallerMissingCLIIsNotError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// no config, no CLI
	in := &CodexMCPInstaller{CodexBin: "/nonexistent-codex"}
	items, err := in.Installed(context.Background())
	if err != nil {
		t.Fatalf("Installed with missing CLI should be empty, not error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items: %+v", items)
	}
}
