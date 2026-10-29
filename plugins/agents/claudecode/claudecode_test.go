package claudecode

import (
	"strings"
	"testing"

	"github.com/devcules/devcules/plugins/agents"
	"github.com/devcules/devcules/plugins/agents/agenttest"
)

func TestContract(t *testing.T) {
	agenttest.Run(t, New(), agenttest.Fixture{
		ShellCall: []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","turn_id":"t1","cwd":"/workspace",` +
			`"transcript_path":null,"model":"claude-sonnet-4-5","permission_mode":"default",` +
			`"tool_name":"Bash","tool_input":{"command":"git push --force origin main","timeout":30},"tool_use_id":"u1"}`),
		WantTool:      "Bash",
		WantCommand:   "git push --force origin main",
		OtherCall:     []byte(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"a.go","content":"x"}}`),
		WantOtherTool: "Write",
	})
}

func TestSettings(t *testing.T) {
	a := New()
	if a.Provider() != "anthropic" {
		t.Fatalf("provider = %q want anthropic", a.Provider())
	}
	if a.ID() != "claudecode" {
		t.Fatalf("id = %q", a.ID())
	}
	spec, err := a.LaunchSpec(agents.Launch{ServeURL: "http://serve.test:7659", Token: "tok", Dir: "/tmp/x", Hook: "echo ok"})
	if err != nil {
		t.Fatal(err)
	}
	var all string
	for _, v := range spec.Files {
		all += string(v)
	}
	for _, needle := range []string{"/llm/anthropic/v1", "/mcp", "devcules", "Bearer tok", "hook"} {
		if !strings.Contains(all, needle) && !strings.Contains(strings.Join(spec.Cmd, " ")+" "+all, needle) {
			t.Errorf("launch missing %q", needle)
		}
	}
}

func TestHookCallShapes(t *testing.T) {
	a := New()
	for in, want := range map[string]string{
		`{"tool_name":"shell","tool_input":{"command":"ls -la"}}`:                                   "ls -la",
		`{"tool_name":"Edit","tool_input":{"file_path":"x.go"}}`:                                    `Edit {"file_path":"x.go"}`,
		`{"tool_name":"Custom","tool_input":"raw text"}`:                                            "Custom raw text",
		`{"tool_name":"Empty"}`:                                                                     "Empty",
		`{"tool_name":"Big","tool_input":{"command":"` + strings.Repeat("a", maxCommand+10) + `"}}`: strings.Repeat("a", maxCommand),
	} {
		c, err := a.HookCall([]byte(in))
		if err != nil || c.Command != want {
			t.Errorf("HookCall(%.60s) = %.80q %v, want %.80q", in, c.Command, err, want)
		}
	}
	if _, err := a.HookCall([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`)); err == nil {
		t.Error("a PostToolUse payload was accepted")
	}
}

func TestHookAnswerShapes(t *testing.T) {
	a := New()
	if r := a.HookAnswer(agents.Answer{Allow: true}); r.Code != 0 || len(r.Stdout) != 0 || r.Stderr != "" {
		t.Fatalf("allow = %+v: Claude needs exit 0 and no JSON", r)
	}
	if r := a.HookAnswer(agents.Answer{}); r.Code != 2 || r.Stderr != "Devcules blocked this call: denied" {
		t.Fatalf("bare deny = %+v", r)
	}
}
