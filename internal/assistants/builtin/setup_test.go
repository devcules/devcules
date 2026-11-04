package builtin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistanttest"
	"github.com/devcules/devcules/internal/assistants/builtin"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// fakeMCP answers tools/list and tools/call the way serve's MCP server
// does, and records the bearer each call carried.
type fakeMCP struct{ bearer string }

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.bearer = r.Header.Get("Authorization")
	var req struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == "tools/list":
		_, _ = w.Write([]byte(`{"result":{"tools":[{"name":"status","description":"the desk"},{"name":"list_providers","inputSchema":{"type":"object"}}]}}`))
	case req.Params.Name == "boom":
		_, _ = w.Write([]byte(`{"error":{"message":"no such tool"}}`))
	case req.Params.Name == "empty":
		_, _ = w.Write([]byte(`{}`))
	case req.Params.Name == "garbled":
		_, _ = w.Write([]byte(`not json`))
	default:
		_, _ = w.Write([]byte(`{"result":{"content":[{"text":"ran "},{"text":"` + req.Params.Name + `"}],"isError":false}}`))
	}
}

func TestToolsRunner(t *testing.T) {
	ctx := context.Background()
	if (builtin.ToolsRunner{}).Tools() != nil {
		t.Fatal("tools listed without an MCP server")
	}
	if _, err := (builtin.ToolsRunner{}).Run(ctx, assistants.ToolCallAsk{Name: "status"}); err == nil {
		t.Fatal("a call ran without an MCP server")
	}
	m := &fakeMCP{}
	r := builtin.ToolsRunner{MCP: m, Token: "dvl_tok"}
	tools := r.Tools()
	if len(tools) != 2 || tools[0].Name != "list_providers" || tools[1].Name != "status" || m.bearer != "Bearer dvl_tok" {
		t.Fatalf("Tools = %+v (bearer %q), want sorted by name with the token", tools, m.bearer)
	}
	res, err := r.Run(ctx, assistants.ToolCallAsk{Name: "status", Args: json.RawMessage(`{}`)})
	if err != nil || res.Text != "ran status" || res.IsError {
		t.Fatalf("Run = %+v %v", res, err)
	}
	if res, err := r.Run(ctx, assistants.ToolCallAsk{Name: "boom", Args: json.RawMessage(`{}`)}); err != nil || !res.IsError || res.Text != "no such tool" {
		t.Fatalf("error reply = %+v %v", res, err)
	}
	for _, name := range []string{"empty", "garbled"} {
		if _, err := r.Run(ctx, assistants.ToolCallAsk{Name: name, Args: json.RawMessage(`{}`)}); err == nil {
			t.Fatalf("%s reply was accepted", name)
		}
	}
}

func TestBuiltinShadowMode(t *testing.T) {
	if _, err := builtin.Builtin(builtin.SetupOptions{Model: "m", BaseURL: "not-a-url"}); err == nil {
		t.Fatal("a bad base URL was accepted")
	}
	// No approvals service: it answers, but every tool call is denied
	// and it says why in its state.
	script := assistanttest.NewScript().
		Call("c1", "status", json.RawMessage(`{}`)).
		Say("I could not look, this desk has no approvals")
	ups := assistanttest.Upstream(t, script)
	a, err := builtin.Builtin(builtin.SetupOptions{Model: builtin.DefaultModel, BaseURL: ups.URL, Key: "test-key", MCP: &fakeMCP{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.State(), "read only") || a.ID() != "builtin" || a.Loop() == nil {
		t.Fatalf("State %q ID %q", a.State(), a.ID())
	}
	_, evs, err := a.Chat(context.Background(), "", "how is the desk?")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 || evs[0].Type != assistants.EventTool || evs[0].Tool.Decision != "deny" {
		t.Fatalf("events = %+v, want the tool call denied", evs)
	}
	// No key: the state says so first.
	b, err := builtin.Builtin(builtin.SetupOptions{Model: builtin.DefaultModel, BaseURL: ups.URL})
	if err != nil || !strings.Contains(b.State(), "no helper-model key") {
		t.Fatalf("no key: %v %q", err, b.State())
	}
}

func TestBuiltinWithApprovals(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	queue, err := approvals.New(ctx, approvals.Options{Store: s, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	script := assistanttest.NewScript().
		Call("c1", "status", json.RawMessage(`{}`)).
		Say("the desk is fine")
	ups := assistanttest.Upstream(t, script)
	m := &fakeMCP{}
	a, err := builtin.Builtin(builtin.SetupOptions{Model: builtin.DefaultModel, BaseURL: ups.URL, Key: "test-key",
		MCP: m, Launches: launch.New(s), Decider: queue})
	if err != nil {
		t.Fatal(err)
	}
	// Its own target starts at "ask on critical", and it answers.
	if queue.Modes()[approvals.Target("builtin")] != approvals.ModeCritical || a.State() != "" {
		t.Fatalf("mode %q, state %q", queue.Modes()[approvals.Target("builtin")], a.State())
	}
	_, evs, err := a.Chat(ctx, "", "how is the desk?")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 || evs[0].Tool.Decision != "allow" || evs[0].Tool.Result != "ran status" || !strings.HasPrefix(m.bearer, "Bearer "+launch.Prefix) {
		t.Fatalf("events = %+v (bearer %q), want the read run with the assistant's launch token", evs, m.bearer)
	}
	// A mode the owner already chose is left alone.
	if err := queue.SetMode(ctx, approvals.Target("builtin"), approvals.ModeAll, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := builtin.Builtin(builtin.SetupOptions{Model: builtin.DefaultModel, BaseURL: ups.URL, Key: "k", MCP: m, Decider: queue}); err != nil {
		t.Fatal(err)
	}
	if queue.Modes()[approvals.Target("builtin")] != approvals.ModeAll {
		t.Fatal("setup overwrote the owner's mode")
	}
}
