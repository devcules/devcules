package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/plugins/agents"
	"github.com/devcules/devcules/plugins/agents/agenttest"
)

func TestContract(t *testing.T) {
	agenttest.Run(t, New(), agenttest.Fixture{
		ShellCall: []byte(`{"session_id":"s1","transcript_path":null,"cwd":"/workspace","hook_event_name":"PreToolUse",` +
			`"model":"muse-spark-1.3","permission_mode":"default","turn_id":"t1","tool_name":"Bash",` +
			`"tool_use_id":"c1","tool_input":{"command":["git","reset","--hard"]}}`),
		WantTool:      "Bash",
		WantCommand:   "git reset --hard",
		OtherCall:     []byte(`{"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{"input":"*** Begin Patch"}}`),
		WantOtherTool: "apply_patch",
	})
}

func TestLaunch(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "dvl_secret9", Dir: "/run/l", Hook: `env -i 'x' "y"`, Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(spec.Cmd, " "); got != "codex --sandbox workspace-write --model gpt-x" {
		t.Fatalf("cmd = %q", got)
	}
	if spec.Env["CODEX_HOME"] != "/run/l/codex" || spec.Env[keyEnv] != "dvl_secret9" || spec.Env[mcpEnv] != "dvl_secret9" {
		t.Fatalf("env = %v", spec.Env)
	}
	want := `model = "gpt-x"
model_provider = "devcules"

[model_providers.devcules]
name = "Devcules"
base_url = "http://serve:7659/llm/openai/v1"
env_key = "DEVCULES_LLM_KEY"
wire_api = "responses"

[mcp_servers.devcules]
url = "http://serve:7659/mcp"
bearer_token_env_var = "DEVCULES_MCP_TOKEN"

[features]
hooks = true
`
	if got := string(spec.Files["codex/config.toml"]); got != want {
		t.Fatalf("config.toml =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(string(spec.Files["codex/config.toml"]), "dvl_secret9") {
		t.Fatal("the token is in config.toml; it belongs in the env")
	}
	var h any
	_ = json.Unmarshal(spec.Files["codex/hooks.json"], &h)
	got, _ := json.Marshal(h)
	if string(got) != `{"hooks":{"PreToolUse":[{"hooks":[{"command":"env -i 'x' \"y\"","timeout":960,"type":"command"}]}]}}` {
		t.Fatalf("hooks.json = %s", got)
	}
}

func TestTOMLString(t *testing.T) {
	for in, want := range map[string]string{`a"b\c`: `"a\"b\\c"`, "x\ny\x7f": `"x\u000Ay\u007F"`, "é": `"é"`} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHookAnswer(t *testing.T) {
	if r := New().HookAnswer(agents.Answer{Allow: true}); r.Code != 0 || len(r.Stdout) != 0 {
		t.Fatalf("allow = %+v", r)
	}
	if r := New().HookAnswer(agents.Answer{Failed: true, Reason: "down"}); r.Code != 2 {
		t.Fatalf("failed = %+v", r)
	}
}

func TestLaunchWithoutModel(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://s", Token: "t", Dir: "/d", Hook: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(spec.Files["codex/config.toml"]), "model =") || strings.Contains(strings.Join(spec.Cmd, " "), "--model") {
		t.Fatalf("no model asked, yet cmd %v config %s", spec.Cmd, spec.Files["codex/config.toml"])
	}
}

// TestLaunchOnMeta points Codex at Meta's Responses API.
func TestLaunchOnMeta(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK", Model: "muse-spark-1.3", Provider: "meta"})
	if err != nil {
		t.Fatalf("LaunchSpec: %s", err)
	}
	if !strings.Contains(string(spec.Files["codex/config.toml"]), `base_url = "http://serve:7659/llm/meta/v1"`) {
		t.Fatalf("config.toml = %s", spec.Files["codex/config.toml"])
	}
	// Any OpenAI-format profile routes by id; serve checks the API.
	spec, err = New().LaunchSpec(agents.Launch{ServeURL: "http://s", Token: "t", Dir: "/d", Hook: "h", Provider: "nebius"})
	if err != nil || !strings.Contains(string(spec.Files["codex/config.toml"]), `base_url = "http://s/llm/nebius/v1"`) {
		t.Fatalf("profile launch = %v, %s", err, spec.Files["codex/config.toml"])
	}
	if New().Wire() != terminals.WireResponses || New().Only() != nil {
		t.Fatal("Codex must speak Responses to any profile")
	}
}
