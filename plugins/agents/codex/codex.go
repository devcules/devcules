package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/plugins/agents"
)

const (
	// installHint is the npm installer (checked with 0.165.0).
	installHint = "npm i -g @openai/codex"
	// hookTimeout is the hook's limit in seconds: longer than an
	// approval's 10 minutes plus the hook's retries.
	hookTimeout = 960
	// Env vars the config names: the provider key and the MCP bearer
	// token, both the launch token.
	keyEnv = "DEVCULES_LLM_KEY"
	mcpEnv = "DEVCULES_MCP_TOKEN"
)

// routerPath is the provider's base_url for router provider p. Codex's
// own OpenAI base ends in /v1 and it adds /responses, so the base
// carries the router's /v1.
func routerPath(p string) string { return "/llm/" + p + "/v1" }

// Agent is the Codex adapter.
type Agent struct{}

// New returns the adapter.
func New() Agent { return Agent{} }

// ID is "codex".
func (Agent) ID() string { return "codex" }

// Name is the product name.
func (Agent) Name() string { return "Codex" }

// Provider is "openai": Codex calls the router's OpenAI provider
// through the Responses API.
func (Agent) Provider() string { return "openai" }

// Wire: Codex only calls the Responses API (wire_api = "responses"),
// so it runs on OpenAI-format profiles not set to chat only.
func (Agent) Wire() string { return terminals.WireResponses }

// Only is empty: any profile serving Responses will do.
func (Agent) Only() []string { return nil }

// InstallHint is the documented installer.
func (Agent) InstallHint() string { return installHint }

// Detect runs `codex --version`.
func (Agent) Detect(ctx context.Context) (string, bool) {
	out, ok := agents.Version(ctx, nil, "codex", "--version")
	if !ok {
		return "", false
	}
	return agents.SemVer(out), true
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type hooks struct {
	Hooks map[string][]matcher `json:"hooks"`
}

// matcher has no matcher field: a group without one runs for every
// tool.
type matcher struct {
	Hooks []handler `json:"hooks"`
}

type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// LaunchSpec writes config.toml and hooks.json into the launch's
// CODEX_HOME and starts `codex` with its own approvals off.
func (Agent) LaunchSpec(l agents.Launch) (agents.Spec, error) {
	if l.ServeURL == "" || l.Token == "" || l.Dir == "" || l.Hook == "" {
		return agents.Spec{}, fmt.Errorf("codex: incomplete launch")
	}
	provider, err := agents.ProviderFor(Agent{}, l.Provider)
	if err != nil {
		return agents.Spec{}, err
	}
	var lines []string
	if l.Model != "" {
		lines = append(lines, "model = "+tomlString(l.Model))
	}
	config := strings.Join(append(lines,
		`model_provider = "devcules"`,
		"",
		"[model_providers.devcules]",
		`name = "Devcules"`,
		"base_url = "+tomlString(l.ServeURL+routerPath(provider)),
		"env_key = "+tomlString(keyEnv),
		`wire_api = "responses"`,
		"",
		"[mcp_servers.devcules]",
		"url = "+tomlString(l.ServeURL+"/mcp"),
		"bearer_token_env_var = "+tomlString(mcpEnv),
		"",
		"[features]",
		"hooks = true",
		"",
	), "\n")
	h, err := json.MarshalIndent(hooks{Hooks: map[string][]matcher{"PreToolUse": {{Hooks: []handler{{
		Type: "command", Command: l.Hook, Timeout: hookTimeout}}}}}}, "", "  ")
	if err != nil {
		return agents.Spec{}, err
	}
	// Codex keeps its own approvals: it runs a user hook only after
	// the owner trusted it (Codex asks at start), so until then its
	// own prompts and sandbox are the guard.
	cmd := []string{"codex", "--sandbox", "workspace-write"}
	if l.Model != "" {
		cmd = append(cmd, "--model", l.Model)
	}
	return agents.Spec{
		Cmd: cmd,
		Env: map[string]string{
			"CODEX_HOME": filepath.Join(l.Dir, "codex"),
			keyEnv:       l.Token,
			mcpEnv:       l.Token,
		},
		Files: map[string][]byte{
			"codex/config.toml": []byte(config),
			"codex/hooks.json":  append(h, '\n'),
		},
	}, nil
}

// HookCall reads Codex's PreToolUse payload; a shell command may come
// as an argv list.
func (Agent) HookCall(stdin []byte) (agents.Call, error) {
	return agents.PreToolUse("codex", stdin)
}

// HookAnswer: an allowed call exits 0 with nothing on stdout (an
// unknown output field would count as a failed hook); a denied or
// undecided one exits 2 with the reason on stderr.
func (Agent) HookAnswer(a agents.Answer) agents.Reply {
	if a.Allow {
		return agents.Reply{}
	}
	return agents.Blocked(a)
}
