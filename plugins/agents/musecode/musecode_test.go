package musecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/plugins/agents"
	"github.com/devcules/devcules/plugins/agents/agenttest"
)

func TestContract(t *testing.T) {
	agenttest.Run(t, New(), agenttest.Fixture{
		ShellCall: []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","turn_id":"t1","cwd":"/workspace",` +
			`"transcript_path":null,"model":"muse-spark-1.3","permission_mode":"default",` +
			`"tool_name":"Bash","tool_input":{"command":"git push --force origin main","timeout":30},"tool_use_id":"u1"}`),
		WantTool:      "Bash",
		WantCommand:   "git push --force origin main",
		OtherCall:     []byte(`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"a.go","content":"x"}}`),
		WantOtherTool: "Write",
	})
}

func TestSettings(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK"})
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(spec.Files["config/muse/settings.json"], &s); err != nil {
		t.Fatal(err)
	}
	want := `{"endpoint_transport":{"auth":"bearer","base_url":"http://serve:7659/llm/meta/v1"},` +
		`"hooks":{"PreToolUse":[{"hooks":[{"command":"HOOK","statusMessage":"Checking with Devcules","timeout":960,"type":"command"}],"matcher":"*"}]},` +
		`"mcpServers":{"devcules":{"headers":{"Authorization":"Bearer tok"},"required":false,"type":"streamable-http","url":"http://serve:7659/mcp"}},` +
		`"model":"muse-spark-1.3","provider":"meta","schema_version":1}`
	if got, _ := json.Marshal(s); string(got) != want {
		t.Fatalf("settings =\n%s\nwant\n%s", got, want)
	}
	if spec.Env["XDG_CONFIG_HOME"] != "/run/l/config" || spec.Env["META_API_KEY"] != "tok" {
		t.Fatalf("env = %v", spec.Env)
	}
	if got := strings.Join(spec.Cmd, " "); got != "muse --approval-judge off --disable-approval" {
		t.Fatalf("cmd = %q", got)
	}
}

// A launch can ask for a different profile than Muse Code's own
// "meta" — the settings say that provider and its /llm base URL, so
// a second Meta-compatible endpoint (the Baseten deployment, for
// example) powers the launch without renaming the profile.
func TestLaunchOtherProvider(t *testing.T) {
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK", Provider: "meta2", Model: "meta-models/muse-spark-open-1026"})
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(spec.Files["config/muse/settings.json"], &s); err != nil {
		t.Fatal(err)
	}
	if s["provider"] != "meta2" {
		t.Fatalf("provider = %v, want meta2", s["provider"])
	}
	var wanted = map[string]any{"auth": "bearer", "base_url": "http://serve:7659/llm/meta2/v1"}
	got, _ := json.Marshal(s["endpoint_transport"])
	w, _ := json.Marshal(wanted)
	if string(got) != string(w) {
		t.Fatalf("endpoint_transport = %s, want %s", got, w)
	}
}

// A SelfAuth launch leaves the model call to Muse: no endpoint, no
// key, and MCP plus the hook still installed, because those ride the
// launch token.
func TestSelfAuthLaunch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK", SelfAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(spec.Files["config/muse/settings.json"], &s); err != nil {
		t.Fatal(err)
	}
	if _, set := s["endpoint_transport"]; set {
		t.Fatalf("settings carry an endpoint: %v", s["endpoint_transport"])
	}
	if got := spec.Env["META_API_KEY"]; got != "" {
		t.Fatalf("META_API_KEY = %q: it would take priority over the account login", got)
	}
	if spec.Env["XDG_CONFIG_HOME"] != "/run/l/config" {
		t.Fatalf("env = %v", spec.Env)
	}
	// The hook and MCP are what still route through Devcules.
	if s["hooks"] == nil || s["mcpServers"] == nil {
		t.Fatalf("hook or MCP dropped in self-auth mode: %v", s)
	}
	if _, ok := spec.Files["config/muse/auth.json"]; ok {
		t.Fatal("wrote an auth.json with no owner login to copy")
	}
}

// The owner's Muse credential record is copied into the private config
// dir, because Muse looks for it there and not in the owner's.
func TestSelfAuthCopiesOwnerCredential(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "muse"), 0o700); err != nil {
		t.Fatal(err)
	}
	owner := `{"schema_version":2,"providers":{"meta":{"storage":"keychain"}}}`
	if err := os.WriteFile(filepath.Join(cfg, "muse", "auth.json"), []byte(owner), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := New().LaunchSpec(agents.Launch{ServeURL: "http://serve:7659", Token: "tok", Dir: "/run/l", Hook: "HOOK", SelfAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(spec.Files["config/muse/auth.json"]); got != owner {
		t.Fatalf("auth.json = %q, want the owner's record", got)
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
		t.Fatalf("allow = %+v: Muse needs exit 0 and no JSON", r)
	}
	if r := a.HookAnswer(agents.Answer{}); r.Code != 2 || r.Stderr != "Devcules blocked this call: denied" {
		t.Fatalf("bare deny = %+v", r)
	}
}
