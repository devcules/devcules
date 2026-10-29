package musecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/devcules/devcules/plugins/agents"
)

const (
	// DefaultModel is the model a launch starts on.
	DefaultModel = "muse-spark-1.3"
	// installHint is the documented installer (dev.meta.ai/docs/muse-code).
	installHint = "curl -fsSL https://dev.meta.ai/install.sh | sh"
	// hookTimeout is Muse's per-hook limit in seconds: longer than an
	// approval's 10 minutes plus the hook's retries.
	hookTimeout = 960
	// maxCommand caps the command text sent to serve (it keeps 4 KiB).
	maxCommand = 60 << 10
	// routerPath is the base Muse's Meta provider calls. Muse appends
	// only "/responses" (checked against 1.4.14), so the base carries
	// the router's /v1.
	routerPath = "/llm/meta/v1"
)

// Agent is the Muse Code adapter.
type Agent struct{}

// New returns the adapter.
func New() Agent { return Agent{} }

// ID is "musecode".
func (Agent) ID() string { return "musecode" }

// Name is the product name.
func (Agent) Name() string { return "Muse Code" }

// Provider is "meta": Muse Code only speaks the Meta Model API.
func (Agent) Provider() string { return "meta" }

// InstallHint is the documented installer.
func (Agent) InstallHint() string { return installHint }

var versionRE = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)

// Detect runs `muse --version` ("Muse Code 1.4.14 (1.4.14-R7309.1)")
// without letting the launcher update itself.
func (Agent) Detect(ctx context.Context) (string, bool) {
	out, ok := agents.Version(ctx, []string{"MUSE_NO_AUTO_UPDATE=1"}, "muse", "--version")
	if !ok {
		return "", false
	}
	if m := versionRE.FindStringSubmatch(out); m != nil {
		return m[1], true
	}
	return out, true
}

// settings is the launch's ~/.config/muse/settings.json.
type settings struct {
	SchemaVersion     int                  `json:"schema_version"`
	Provider          string               `json:"provider"`
	Model             string               `json:"model"`
	EndpointTransport endpoint             `json:"endpoint_transport"`
	MCPServers        map[string]mcpServer `json:"mcpServers"`
	Hooks             map[string][]matcher `json:"hooks"`
}

type endpoint struct {
	BaseURL string `json:"base_url"`
	Auth    string `json:"auth"`
}

type mcpServer struct {
	Type     string            `json:"type"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Required bool              `json:"required"`
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

// LaunchSpec writes a private settings.json and starts `muse` with
// Muse's own approvals off. The base URL must be pinned in settings
// with auth "bearer": Muse withholds the key from a --base-url flag
// that is off its own endpoint. The MCP header is literal (Muse does
// not expand ${VAR} there), which is why settings.json stays 0600 in
// the launch dir.
func (Agent) LaunchSpec(l agents.Launch) (agents.Spec, error) {
	if l.ServeURL == "" || l.Token == "" || l.Dir == "" || l.Hook == "" {
		return agents.Spec{}, fmt.Errorf("musecode: incomplete launch")
	}
	model := l.Model
	if model == "" {
		model = DefaultModel
	}
	s := settings{
		SchemaVersion:     1,
		Provider:          "meta",
		Model:             model,
		EndpointTransport: endpoint{BaseURL: l.ServeURL + routerPath, Auth: "bearer"},
		MCPServers: map[string]mcpServer{"devcules": {Type: "streamable-http", URL: l.ServeURL + "/mcp",
			Headers: map[string]string{"Authorization": "Bearer " + l.Token}}},
		Hooks: map[string][]matcher{"PreToolUse": {{Matcher: "*", Hooks: []handler{{Type: "command",
			Command: l.Hook, Timeout: hookTimeout, StatusMessage: "Checking with Devcules"}}}}},
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return agents.Spec{}, err
	}
	return agents.Spec{
		Cmd: []string{"muse", "--model", model, "--approval-judge", "off", "--disable-approval"},
		Env: map[string]string{
			"XDG_CONFIG_HOME": filepath.Join(l.Dir, "config"),
			"META_API_KEY":    l.Token,
		},
		Files: map[string][]byte{"config/muse/settings.json": append(raw, '\n')},
	}, nil
}

// hookInput is the part of Muse's PreToolUse stdin the hook needs.
type hookInput struct {
	Event     string          `json:"hook_event_name"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// HookCall reads Muse's PreToolUse payload. A shell call's command is
// the command itself; any other tool is its name and arguments, so
// rules and the owner see what it touches.
func (Agent) HookCall(stdin []byte) (agents.Call, error) {
	var in hookInput
	if err := json.Unmarshal(stdin, &in); err != nil || in.ToolName == "" {
		return agents.Call{}, fmt.Errorf("musecode: stdin is not a Muse Code tool call")
	}
	if in.Event != "" && in.Event != "PreToolUse" {
		return agents.Call{}, fmt.Errorf("musecode: hook event %q is not PreToolUse", in.Event)
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
			// Custom tools may take a raw string.
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

// HookAnswer: an allowed call exits 0 with nothing on stdout (Muse
// then runs it); a denied or undecided one exits 2 with the reason on
// stderr, which Muse shows the model. Any other exit would count as a
// failed hook, and Muse would run the call anyway.
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
