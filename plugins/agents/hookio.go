package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// MaxCommand caps the command text a hook sends to serve (serve keeps
// 4 KiB of it).
const MaxCommand = 60 << 10

// hookInput is the part of a PreToolUse payload Devcules needs. Muse
// Code, Claude Code and Codex all send these three fields.
type hookInput struct {
	Event     string          `json:"hook_event_name"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// PreToolUse reads a PreToolUse hook payload for the agent id. A
// shell call's command is the command itself (a string, or an argv
// list joined by spaces); any other tool is its name and arguments,
// so rules and the owner see what it touches.
func PreToolUse(id string, stdin []byte) (Call, error) {
	var in hookInput
	if err := json.Unmarshal(stdin, &in); err != nil || in.ToolName == "" {
		return Call{}, fmt.Errorf("%s: stdin is not a tool call", id)
	}
	if in.Event != "" && in.Event != "PreToolUse" {
		return Call{}, fmt.Errorf("%s: hook event %q is not PreToolUse", id, in.Event)
	}
	command := shellCommand(in.ToolInput)
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
	if len(command) > MaxCommand {
		command = strings.ToValidUTF8(command[:MaxCommand], "")
	}
	return Call{Tool: in.ToolName, Command: command}, nil
}

// shellCommand is tool_input.command as a string or an argv list.
func shellCommand(input json.RawMessage) string {
	var args map[string]json.RawMessage
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	raw, ok := args["command"]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		return strings.Join(argv, " ")
	}
	return ""
}

// Blocked is the reply that stops a call in every supported agent:
// exit 2 with the reason on stderr. Any other non-zero exit counts as
// a failed hook, and the agents then run the call anyway.
func Blocked(a Answer) Reply {
	if a.Failed {
		return Reply{Code: 2, Stderr: "Devcules could not check this call, so it is blocked: " + a.Reason}
	}
	reason := a.Reason
	if reason == "" {
		reason = "denied"
	}
	return Reply{Code: 2, Stderr: "Devcules blocked this call: " + reason}
}

var semverRE = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)

// SemVer picks the first x.y.z out of a --version line, or returns
// the line as is.
func SemVer(out string) string {
	if m := semverRE.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return out
}
