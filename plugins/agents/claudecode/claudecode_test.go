package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devcules/devcules/plugins/agents"
	"github.com/devcules/devcules/plugins/agents/agenttest"
)

func TestContract(t *testing.T) {
	agenttest.Run(t, New(), agenttest.Fixture{
		ShellCall: []byte(`{"session_id":"abc123","transcript_path":"/home/u/.claude/projects/x/t.jsonl","cwd":"/workspace",` +
			`"permission_mode":"default","hook_event_name":"PreToolUse","tool_name":"Bash",` +
			`"tool_input":{"command":"rm -rf build","description":"Clean","timeout":120000,"run_in_background":false},"tool_use_id":"toolu_01"}`),
		WantTool:      "Bash",
		WantCommand:   "rm -rf build",
		OtherCall:     []byte(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"a.go","old_string":"a","new_string":"b"}}`),
		WantOtherTool: "Edit",
	})
}

func TestLaunch(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK", Model: "m-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(spec.Cmd, " "); got != "claude --model m-1 --settings /run/l/settings.json --mcp-config /run/l/mcp.json" {
		t.Fatalf("cmd = %q", got)
	}
	for k, want := range map[string]string{
		"CLAUDE_CONFIG_DIR": "/run/l/claude", "ANTHROPIC_BASE_URL": "http://serve:7659/llm/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "tok", "ANTHROPIC_MODEL": "m-1", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "m-1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	} {
		if spec.Env[k] != want {
			t.Errorf("env %s = %q, want %q", k, spec.Env[k], want)
		}
	}
	if _, ok := spec.Env["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY set: it would prompt for approval and go in x-api-key")
	}
	var s, m any
	_ = json.Unmarshal(spec.Files["settings.json"], &s)
	_ = json.Unmarshal(spec.Files["mcp.json"], &m)
	gs, _ := json.Marshal(s)
	gm, _ := json.Marshal(m)
	if string(gs) != `{"hooks":{"PreToolUse":[{"hooks":[{"command":"HOOK","statusMessage":"Checking with Devcules","timeout":960,"type":"command"}],"matcher":"*"}]}}` {
		t.Errorf("settings = %s", gs)
	}
	if string(gm) != `{"mcpServers":{"devcules":{"headers":{"Authorization":"Bearer tok"},"type":"http","url":"http://serve:7659/mcp"}}}` {
		t.Errorf("mcp = %s", gm)
	}
	// No model: Claude Code picks its own default.
	spec, _ = New().LaunchSpec(agents.Launch{ServeURL: "http://s", Token: "t", Dir: "/d", Hook: "h"})
	if _, set := spec.Env["ANTHROPIC_MODEL"]; set || strings.Contains(strings.Join(spec.Cmd, " "), "--model") {
		t.Errorf("no model asked, yet cmd %v env %v", spec.Cmd, spec.Env)
	}
}

func TestHookAnswer(t *testing.T) {
	a := New()
	r := a.HookAnswer(agents.Answer{Allow: true, Reason: "critical mode"})
	var out struct {
		HookSpecificOutput map[string]string `json:"hookSpecificOutput"`
	}
	if r.Code != 0 || json.Unmarshal(r.Stdout, &out) != nil || out.HookSpecificOutput["permissionDecision"] != "allow" ||
		out.HookSpecificOutput["hookEventName"] != "PreToolUse" || out.HookSpecificOutput["permissionDecisionReason"] != "critical mode" {
		t.Fatalf("allow = %+v %s", r, r.Stdout)
	}
	if r := a.HookAnswer(agents.Answer{Allow: true}); !strings.Contains(string(r.Stdout), "allowed by Devcules") {
		t.Fatalf("bare allow = %s", r.Stdout)
	}
	if r := a.HookAnswer(agents.Answer{Reason: "no"}); r.Code != 2 || len(r.Stdout) != 0 {
		t.Fatalf("deny = %+v", r)
	}
}

// TestLaunchOnMeta points Claude Code at Meta's Anthropic-style API.
func TestLaunchOnMeta(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK", Model: "muse-spark-1.3", Provider: "meta"})
	if err != nil {
		t.Fatalf("LaunchSpec: %s", err)
	}
	if got := spec.Env["ANTHROPIC_BASE_URL"]; got != "http://serve:7659/llm/meta" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want the router's meta path", got)
	}
	if spec.Env["ANTHROPIC_MODEL"] != "muse-spark-1.3" {
		t.Fatalf("model = %q", spec.Env["ANTHROPIC_MODEL"])
	}
	if _, err := New().LaunchSpec(agents.Launch{ServeURL: "http://s", Token: "t", Dir: "/d", Hook: "h", Provider: "openai"}); err == nil {
		t.Fatal("Claude Code accepted the OpenAI provider")
	}
}
