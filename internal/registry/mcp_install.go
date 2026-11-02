package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// MCPInstaller adds/removes MCP servers by shelling out to the real
// `claude` CLI (add-json / remove) rather than hand-editing ~/.claude.json
// -- that file carries a large amount of unrelated Claude Code state
// (onboarding flags, caches, oauth tokens, ...) that only the CLI itself
// should be trusted to rewrite safely.
type MCPInstaller struct {
	ClaudeBin string // defaults to "claude" (resolved via PATH)
	Scope     string // defaults to "user"
}

func (in *MCPInstaller) Kind() ArtifactKind { return KindMCP }

func (in *MCPInstaller) bin() string {
	if in.ClaudeBin != "" {
		return in.ClaudeBin
	}
	return "claude"
}

func (in *MCPInstaller) scope() string {
	if in.Scope != "" {
		return in.Scope
	}
	return "user"
}

func (in *MCPInstaller) Install(ctx context.Context, name string, entry CatalogEntry, params map[string]string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	spec, ok := entry.Install.(MCPInstallSpec)
	if !ok {
		return fmt.Errorf("entry has no MCP install spec")
	}

	var cfg map[string]any
	switch spec.Type {
	case "stdio":
		env := map[string]string{}
		for _, hint := range spec.EnvVars {
			if v := strings.TrimSpace(params[hint.Name]); v != "" {
				env[hint.Name] = v
			} else if hint.Default != "" {
				env[hint.Name] = hint.Default
			}
		}
		// allow values for env vars the registry didn't declare too
		for k, v := range params {
			if _, known := env[k]; !known && strings.TrimSpace(v) != "" {
				env[k] = v
			}
		}
		cfg = map[string]any{"type": "stdio", "command": spec.Command, "args": spec.Args, "env": env}
	case "http", "sse":
		cfg = map[string]any{"type": spec.Type, "url": spec.URL}
	default:
		return fmt.Errorf("unsupported install type %q", spec.Type)
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, in.bin(), "mcp", "add-json", name, string(b), "-s", in.scope())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp add-json: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (in *MCPInstaller) Remove(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	cmd := exec.CommandContext(ctx, in.bin(), "mcp", "remove", name, "-s", in.scope())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Installed reads ~/.claude.json's top-level mcpServers key directly
// (read-only, so no risk to the rest of that file) rather than parsing
// `claude mcp list`'s human-oriented text output.
func (in *MCPInstaller) Installed(ctx context.Context) ([]InstalledItem, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return []InstalledItem{}, nil
		}
		return nil, err
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make([]InstalledItem, 0, len(doc.MCPServers))
	for name, cfg := range doc.MCPServers {
		detail := ""
		if c, ok := cfg["command"].(string); ok {
			detail = c
		} else if u, ok := cfg["url"].(string); ok {
			detail = u
		}
		out = append(out, InstalledItem{Name: name, Kind: KindMCP, Detail: detail})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
