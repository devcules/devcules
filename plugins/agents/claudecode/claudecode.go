package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/devcules/devcules/plugins/agents"
)

const (
	DefaultModel = "claude-sonnet-4-5"
	installHint  = "npm i -g @anthropic-ai/claude-code"
	hookTimeout  = 960
	maxCommand   = 60 << 10
	routerPath   = "/llm/anthropic/v1"
)

// Agent is the Claude Code adapter.
type Agent struct{}

// New returns the adapter.
func New() Agent { return Agent{} }

func (Agent) ID() string          { return "claudecode" }
func (Agent) Name() string        { return "Claude Code" }
func (Agent) Provider() string    { return "anthropic" }
func (Agent) InstallHint() string { return installHint }

func (Agent) Detect(ctx context.Context) (string, bool) {
	return agents.Version(ctx, nil, "claude", "--version")
}

// LaunchSpec builds a launch that routes through the Devcules router.
// The token lives only in a 0600 file and env, never on the command line.
func (Agent) LaunchSpec(l agents.Launch) (agents.Spec, error) {
	if l.ServeURL == "" || l.Token == "" || l.Dir == "" {
		return agents.Spec{}, fmt.Errorf("claudecode: missing ServeURL/Token/Dir")
	}
	model := l.Model
	if model == "" {
		model = DefaultModel
	}
	base := strings.TrimRight(l.ServeURL, "/") + routerPath
	settings := map[string]any{
		"env": map[string]string{
			"ANTHROPIC_BASE_URL": base,
			"OPENAI_BASE_URL":    base,
			"ANTHROPIC_API_KEY":  l.Token,
			"OPENAI_API_KEY":     l.Token,
		},
		"mcpServers": map[string]any{"devcules": map[string]any{"url": strings.TrimRight(l.ServeURL, "/") + "/mcp", "headers": map[string]string{"Authorization": "Bearer " + l.Token}, "type": "streamable-http"}},
		"hooks": map[string][]map[string]any{
			"PreToolUse": {{
				"type": "command", "command": l.Hook, "timeout": hookTimeout,
			}},
		},
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return agents.Spec{}, err
	}
	return agents.Spec{
		Cmd: []string{"claude", "--model", model},
		Env: map[string]string{"HOME": filepath.Join(l.Dir, "home")},
		Files: map[string][]byte{
			"settings.json":              append(raw, '\n'),
			agents.TokenFile:             []byte(l.Token),
			"home/.claude/settings.json": append(raw, '\n'),
		},
	}, nil
}

type hookInput struct {
	Event     string          `json:"hook_event_name"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

func (Agent) HookCall(stdin []byte) (agents.Call, error) {
	var in hookInput
	if err := json.Unmarshal(stdin, &in); err != nil || in.ToolName == "" {
		return agents.Call{}, fmt.Errorf("claudecode: stdin is not a tool call")
	}
	if in.Event != "" && in.Event != "PreToolUse" && in.Event != "pre_exec_command" {
		return agents.Call{}, fmt.Errorf("claudecode: hook event %q is not PreToolUse", in.Event)
	}
	command := ""
	var args map[string]json.RawMessage
	if json.Unmarshal(in.ToolInput, &args) == nil {
		var cmd string
		if raw, ok := args["command"]; ok && json.Unmarshal(raw, &cmd) == nil {
			command = cmd
		}
	}
	if command == "" {
		var s string
		var compact bytes.Buffer
		switch {
		case json.Unmarshal(in.ToolInput, &s) == nil:
			command = in.ToolName + " " + s
		case len(in.ToolInput) > 0 && json.Compact(&compact, in.ToolInput) == nil:
			command = in.ToolName + " " + compact.String()
		default:
			command = in.ToolName
		}
	}
	if len(command) > maxCommand {
		command = strings.ToValidUTF8(command[:maxCommand], "")
	}
	return agents.Call{Tool: in.ToolName, Command: command}, nil
}

func (Agent) HookAnswer(a agents.Answer) agents.Reply {
	switch {
	case a.Failed:
		return agents.Reply{Code: 2, Stderr: "Devcules could not check this call, so it is blocked: " + a.Reason}
	case a.Allow:
		return agents.Reply{Code: 0}
	default:
		reason := a.Reason
		if reason == "" {
			reason = "denied"
		}
		return agents.Reply{Code: 2, Stderr: "Devcules blocked this call: " + reason}
	}
}
