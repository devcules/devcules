package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mcp_install_fleet.go — the fleet view for MCP servers: what each coding
// agent (claude-code, codex) has configured RIGHT NOW, read from each
// harness's own truth (claude: ~/.claude.json; codex: codex mcp list --json
// or config.toml on disk), never from a Devcules-side record. One server
// with the same detail/url in both harnesses shows as one row listing both
// agents, so the page answers "which agents have which MCP" in one glance.

// MCPFleetItem is one MCP server as installed across the harnesses.
type MCPFleetItem struct {
	Name   string `json:"name"`
	Detail string `json:"detail"` // url or command — how to recognize it
	// Harnesses lists the agents this server is configured in.
	Harnesses []string `json:"harnesses"`
	// EnabledCodex mirrors codex's enabled flag (claude has no equivalent).
	EnabledCodex *bool `json:"enabled_codex,omitempty"`
}

// MCPClaudeInstalled returns claude-code's MCP servers (name → detail).
// Read-only parse of ~/.claude.json, same shape MCPInstaller.Installed uses.
func MCPClaudeInstalled() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(doc.MCPServers))
	for name, cfg := range doc.MCPServers {
		detail := ""
		if c, ok := cfg["command"].(string); ok && c != "" {
			detail = c
		} else if u, ok := cfg["url"].(string); ok {
			detail = u
		}
		out[name] = detail
	}
	return out, nil
}

// MCPCodexInstalled reads codex's configured MCP servers from config.toml
// directly (read-only; `codex mcp list` needs the CLI alive and parses the
// same file). TOML tables [mcp_servers.NAME] with url (HTTP) or command
// (stdio) keys.
func MCPCodexInstalled() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	// line-oriented scan: [mcp_servers.NAME] then keys until the next table
	cur := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = ""
			table := strings.Trim(line, "[]")
			if strings.HasPrefix(table, "mcp_servers.") {
				cur = strings.TrimPrefix(table, "mcp_servers.")
				out[cur] = "" // present even if detail parse fails below
			}
			continue
		}
		if cur == "" {
			continue
		}
		for _, key := range []string{"url", "command"} {
			if v, ok := tomlValue(line, key); ok && out[cur] == "" {
				out[cur] = v
			}
		}
	}
	return out, nil
}

// tomlValue extracts `key = "value"` (spaces optional) from one TOML line.
func tomlValue(line, key string) (string, bool) {
	if !strings.HasPrefix(line, key) {
		return "", false
	}
	rest := strings.TrimLeft(line[len(key):], " ")
	if !strings.HasPrefix(rest, "=") {
		return "", false
	}
	rest = strings.TrimLeft(rest[1:], " ")
	rest = strings.Trim(rest, `"'`)
	return rest, true
}

// ScanMCPFleet reads both harnesses and merges by name+detail similarity.
// Servers with the same name in both are one row with both harnesses; the
// detail shown is the claude one (they're installed independently, so a
// name match with different urls is still two rows — recognized by detail).
func ScanMCPFleet(ctx context.Context, codexBin string) []MCPFleetItem {
	type row struct {
		name, detail string
		harnesses    map[string]bool
		enabled      *bool
	}
	rows := map[string]*row{}
	add := func(name, detail, harness string, enabled *bool) {
		key := name + "\x00" + detail
		r, ok := rows[key]
		if !ok {
			r = &row{name: name, detail: detail, harnesses: map[string]bool{}}
			rows[key] = r
		}
		r.harnesses[harness] = true
		if enabled != nil {
			r.enabled = enabled
		}
	}

	if m, err := MCPClaudeInstalled(); err == nil {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			add(n, m[n], "claude-code", nil)
		}
	}
	if m, err := MCPCodexInstalled(); err == nil {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			add(n, m[n], "codex", nil)
		}
	}

	out := make([]MCPFleetItem, 0, len(rows))
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r := rows[k]
		hs := make([]string, 0, len(r.harnesses))
		for h := range r.harnesses {
			hs = append(hs, h)
		}
		sort.Strings(hs)
		out = append(out, MCPFleetItem{Name: r.name, Detail: r.detail, Harnesses: hs})
	}
	return out
}

// InstallMCPTargeted installs an MCP spec into ONE harness (claude-code or
// codex). The Extensions UI picks the harness at install time; the API
// routes to the harness's installer.
func InstallMCPTargeted(ctx context.Context, harness string, name string, entry CatalogEntry, params map[string]string, claudeBin, codexBin string) error {
	switch harness {
	case "claude-code", "claude", "":
		return (&MCPInstaller{ClaudeBin: claudeBin}).Install(ctx, name, entry, params)
	case "codex":
		return (&CodexMCPInstaller{CodexBin: codexBin}).Install(ctx, name, entry, params)
	default:
		return fmt.Errorf("unknown harness %q (supported: claude-code, codex)", harness)
	}
}

// RemoveMCPTargeted removes a server from ONE harness.
func RemoveMCPTargeted(ctx context.Context, harness, name, claudeBin, codexBin string) error {
	switch harness {
	case "claude-code", "claude", "":
		return (&MCPInstaller{ClaudeBin: claudeBin}).Remove(ctx, name)
	case "codex":
		return (&CodexMCPInstaller{CodexBin: codexBin}).Remove(ctx, name)
	default:
		return fmt.Errorf("unknown harness %q (supported: claude-code, codex)", harness)
	}
}
