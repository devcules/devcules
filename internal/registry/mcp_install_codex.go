package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// mcp_install_codex.go — installs/removes MCP servers in Codex through the
// real `codex mcp` CLI (add/remove/list), mirroring MCPInstaller's "drive
// the harness's own tool, never hand-edit its state" stance. Codex keeps
// MCP servers in ~/.codex/config.toml; the CLI owns that file safely.

// CodexMCPInstaller manages MCP servers via the `codex mcp` CLI.
type CodexMCPInstaller struct {
	CodexBin string // defaults to "codex"
}

func (in *CodexMCPInstaller) Kind() ArtifactKind { return KindMCP }

func (in *CodexMCPInstaller) bin() string {
	if in.CodexBin != "" {
		return in.CodexBin
	}
	return "codex"
}

// Install adds a server: `codex mcp add <name> --url <url>` for HTTP (with
// optional bearer-token env var), or `codex mcp add <name> -- <command...>`
// for stdio, with --env for each environment variable.
func (in *CodexMCPInstaller) Install(ctx context.Context, name string, entry CatalogEntry, params map[string]string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	spec, ok := entry.Install.(MCPInstallSpec)
	if !ok {
		return fmt.Errorf("entry has no MCP install spec")
	}
	args := []string{"mcp", "add", name}
	switch spec.Type {
	case "http", "sse":
		args = append(args, "--url", spec.URL)
		// codex wants the env var NAME that will hold the bearer token,
		// not a secret; params may carry "bearer_token_env_var".
		if v := strings.TrimSpace(params["bearer_token_env_var"]); v != "" {
			args = append(args, "--bearer-token-env-var", v)
		} else if v := strings.TrimSpace(params["BEARER_TOKEN_ENV_VAR"]); v != "" {
			// fallback for a caller passing env-var names in params
			args = append(args, "--bearer-token-env-var", v)
		}
	case "stdio":
		args = append(args, "--")
		args = append(args, spec.Command)
		args = append(args, spec.Args...)
		for _, hint := range spec.EnvVars {
			if v := strings.TrimSpace(params[hint.Name]); v != "" {
				args = append(args, "--env", hint.Name+"="+v)
			}
		}
	default:
		return fmt.Errorf("unsupported install type %q", spec.Type)
	}
	cmd := exec.CommandContext(ctx, in.bin(), args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("codex mcp add: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Remove drops a server: `codex mcp remove <name>`.
func (in *CodexMCPInstaller) Remove(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	cmd := exec.CommandContext(ctx, in.bin(), "mcp", "remove", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		// removing something already gone is success for the caller's purpose
		if strings.Contains(strings.ToLower(string(out)), "not found") {
			return nil
		}
		return fmt.Errorf("codex mcp remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// codexMCPEntry is one row of `codex mcp list --json`.
type codexMCPEntry struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Transport struct {
		Type              string   `json:"type"`
		URL               string   `json:"url"`
		BearerTokenEnvVar string   `json:"bearer_token_env_var"`
		Command           string   `json:"command"`
		Args              []string `json:"args"`
	} `json:"transport"`
}

// Installed reads `codex mcp list --json` — the CLI's own view of its config.
func (in *CodexMCPInstaller) Installed(ctx context.Context) ([]InstalledItem, error) {
	cmd := exec.CommandContext(ctx, in.bin(), "mcp", "list", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// codex not installed → no servers, not an error (the page shows
		// the agent as not detected rather than 500ing the whole fleet).
		if _, lookErr := exec.LookPath(in.bin()); lookErr != nil {
			return []InstalledItem{}, nil
		}
		return nil, fmt.Errorf("codex mcp list: %s", strings.TrimSpace(string(out)))
	}
	var entries []codexMCPEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("codex mcp list --json: %v", err)
	}
	items := make([]InstalledItem, 0, len(entries))
	for _, e := range entries {
		detail := e.Transport.URL
		if detail == "" {
			detail = strings.TrimSpace(strings.Join(append([]string{e.Transport.Command}, e.Transport.Args...), " "))
		}
		items = append(items, InstalledItem{
			Name: e.Name, Kind: KindMCP, Detail: detail, Agents: []string{"codex"},
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}
