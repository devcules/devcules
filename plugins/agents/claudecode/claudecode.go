package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/devcules/devcules/plugins/agents"
)

const (
	// installHint is the documented installer (code.claude.com/docs/en/setup).
	installHint = "curl -fsSL https://claude.ai/install.sh | bash"
	// hookTimeout is the hook's limit in seconds: longer than an
	// approval's 10 minutes plus the hook's retries, because Claude
	// Code runs a call whose hook timed out.
	hookTimeout = 960
	// routerPath is ANTHROPIC_BASE_URL: Claude Code appends
	// /v1/messages itself.
	routerPath = "/llm/anthropic"
)

// Agent is the Claude Code adapter.
type Agent struct{}

// New returns the adapter.
func New() Agent { return Agent{} }

// ID is "claudecode".
func (Agent) ID() string { return "claudecode" }

// Name is the product name.
func (Agent) Name() string { return "Claude Code" }

// Provider is "anthropic": the router's Anthropic provider.
func (Agent) Provider() string { return "anthropic" }

// InstallHint is the documented installer.
func (Agent) InstallHint() string { return installHint }

// Detect runs `claude --version` ("2.1.211 (Claude Code)") with
// nonessential traffic (updates, telemetry) off.
func (Agent) Detect(ctx context.Context) (string, bool) {
	out, ok := agents.Version(ctx, []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}, "claude", "--version")
	if !ok {
		return "", false
	}
	return agents.SemVer(out), true
}

type settings struct {
	Hooks map[string][]matcher `json:"hooks"`
}

type matcher struct {
	Matcher string    `json:"matcher"`
	Hooks   []handler `json:"hooks"`
}

type handler struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout"`
	StatusMessage string `json:"statusMessage"`
}

type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// LaunchSpec writes the hook settings and the MCP config into the
// launch dir and starts `claude` on them. The MCP header is literal:
// Claude Code reads credential variables as empty in remote server
// headers, which is why mcp.json stays 0600 in the launch dir.
func (Agent) LaunchSpec(l agents.Launch) (agents.Spec, error) {
	if l.ServeURL == "" || l.Token == "" || l.Dir == "" || l.Hook == "" {
		return agents.Spec{}, fmt.Errorf("claudecode: incomplete launch")
	}
	s, err := json.MarshalIndent(settings{Hooks: map[string][]matcher{"PreToolUse": {{Matcher: "*", Hooks: []handler{{
		Type: "command", Command: l.Hook, Timeout: hookTimeout, StatusMessage: "Checking with Devcules"}}}}}}, "", "  ")
	if err != nil {
		return agents.Spec{}, err
	}
	m, err := json.MarshalIndent(mcpConfig{MCPServers: map[string]mcpServer{"devcules": {
		Type: "http", URL: l.ServeURL + "/mcp", Headers: map[string]string{"Authorization": "Bearer " + l.Token}}}}, "", "  ")
	if err != nil {
		return agents.Spec{}, err
	}
	cmd := []string{"claude"}
	env := map[string]string{
		"CLAUDE_CONFIG_DIR":                        filepath.Join(l.Dir, "claude"),
		"ANTHROPIC_BASE_URL":                       l.ServeURL + routerPath,
		"ANTHROPIC_AUTH_TOKEN":                     l.Token,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
	if l.Model != "" {
		// Every alias, and background work, stays on the chosen model.
		cmd = append(cmd, "--model", l.Model)
		for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"} {
			env[k] = l.Model
		}
	}
	return agents.Spec{
		Cmd: append(cmd, "--settings", filepath.Join(l.Dir, "settings.json"), "--mcp-config", filepath.Join(l.Dir, "mcp.json")),
		Env: env,
		Files: map[string][]byte{
			"settings.json": append(s, '\n'),
			"mcp.json":      append(m, '\n'),
		},
	}, nil
}

// HookCall reads Claude Code's PreToolUse payload.
func (Agent) HookCall(stdin []byte) (agents.Call, error) {
	return agents.PreToolUse("claudecode", stdin)
}

// HookAnswer: an allowed call exits 0 with a JSON "allow", so Claude
// Code runs it without asking again (silence would fall through to
// its own prompt); a denied or undecided one exits 2 with the reason
// on stderr. Any other exit counts as a non-blocking error, and Claude
// Code would run the call anyway.
func (Agent) HookAnswer(a agents.Answer) agents.Reply {
	if !a.Allow {
		return agents.Blocked(a)
	}
	reason := a.Reason
	if reason == "" {
		reason = "allowed by Devcules"
	}
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName": "PreToolUse", "permissionDecision": "allow", "permissionDecisionReason": reason}})
	return agents.Reply{Stdout: append(out, '\n')}
}
