// Package agenttest is the contract suite every agent adapter passes
// (PLAN.md §9): identity, a launch pointed at serve with the token
// kept off the command line, the approval hook installed, and a hook
// translation that fails closed. An off-router assistant (Wired ==
// false, plugins/agents/cli) skips the router and hook checks through
// its own "off-router" subtest instead.
package agenttest

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/plugins/agents"
)

// Fixture is one adapter's sample hook traffic.
type Fixture struct {
	// ShellCall is the agent's hook stdin for a shell tool running
	// WantCommand.
	ShellCall   []byte
	WantTool    string
	WantCommand string
	// OtherCall is the stdin for a non-shell tool (a file edit); its
	// Call.Command must name WantOtherTool.
	OtherCall     []byte
	WantOtherTool string
}

// Run checks a against the contract.
func Run(t *testing.T, a agents.Agent, f Fixture) {
	t.Helper()
	const serveURL = "http://serve.test:7659"
	token := "dvl_" + "contracttoken0123456789"
	dir := filepath.Join(t.TempDir(), "launch-0123456789abcdef")

	t.Run("listing", func(t *testing.T) {
		// The API's agent list marks the wiring: a coding agent is not
		// Direct; an off-router assistant is. That false/true pair is
		// what sendDownstream's POST keys on to skip provider and key.
		r := agents.New(serveURL, "/usr/local/bin/devcules", a)
		list := r.Agents(context.Background())
		if len(list) != 1 {
			t.Fatalf("Agents() = %d entries, want just this one", len(list))
		}
		if list[0].Direct == agents.Wired(a) {
			t.Errorf("Direct %v, want %v", list[0].Direct, !agents.Wired(a))
		}
	})

	t.Run("identity", func(t *testing.T) {
		if !terminals.ValidAgentID(a.ID()) {
			t.Fatalf("ID %q is not a lowercase agent id", a.ID())
		}
		if a.Name() == "" || a.InstallHint() == "" {
			t.Fatalf("Name %q, InstallHint %q: both are required", a.Name(), a.InstallHint())
		}
		if !agents.Wired(a) {
			if a.Provider() != "" || a.Wire() != "" || len(a.Only()) > 0 {
				t.Fatalf("an off-router assistant keeps no wire: Provider %q, Wire %q, Only %q", a.Provider(), a.Wire(), a.Only())
			}
			return
		}
		if a.Provider() == "" {
			t.Fatal("a wired agent names its provider")
		}
	})

	if !agents.Wired(a) {
		t.Run("off-router", func(t *testing.T) {
			r := agents.New(serveURL, "/usr/local/bin/devcules", a)
			spec, err := r.Spec(terminals.AgentLaunch{ID: a.ID(), Token: token, Model: "contract-model-1"}, dir)
			if err != nil {
				t.Fatalf("Spec: %s", err)
			}
			if len(spec.Cmd) == 0 || spec.Cmd[0] == "" {
				t.Fatal("no command")
			}
			text := specText(spec)
			for what := range map[string]string{
				"the serve URL": serveURL,
				"the model":     "contract-model-1",
				"the hook":      "hook --agent",
				"the token":     token,
			} {
				if strings.Contains(text, what) {
					t.Errorf("an off-router launch mentions %s (%q)", text, what)
				}
			}
			if len(spec.Files) > 0 {
				t.Errorf("an off-router launch writes files: %v", spec.Files)
			}
			for p := range spec.Files {
				if !filepath.IsLocal(p) {
					t.Fatalf("file %q escapes the launch dir", p)
				}
			}
			if _, err := a.LaunchSpec(agents.Launch{}); err == nil {
				t.Error("an empty launch was accepted")
			}
			if _, err := a.HookCall([]byte(`{}`)); err == nil {
				t.Error("an off-router assistant answered a hook call")
			}
			if r := a.HookAnswer(agents.Answer{Allow: true, Reason: "full access"}); r.Code == 0 {
				t.Errorf("an off-router assistant allowed a hook call: %+v", r)
			}
		})
		return
	}

	t.Run("providers", func(t *testing.T) {
		// It names the API it speaks; a malformed provider id is
		// refused; any other profile is routed to by id unless the
		// agent is limited to its Only list, which then refuses it.
		wire, only := a.Wire(), a.Only()
		bad := wire != terminals.WireMessages && wire != terminals.WireResponses
		for _, p := range only {
			bad = bad || !terminals.ValidAgentID(p)
		}
		bad = bad || (len(only) > 0 && only[0] != a.Provider())
		_, malformed := a.LaunchSpec(agents.Launch{ServeURL: serveURL, Token: token, Dir: dir, Hook: "HOOK", Provider: "Not An Id"})
		spec, other := a.LaunchSpec(agents.Launch{ServeURL: serveURL, Token: token, Dir: dir, Hook: "HOOK", Provider: "otherprofile"})
		routed := other == nil && strings.Contains(specText(spec), serveURL+"/llm/otherprofile")
		if bad || malformed == nil || (len(only) == 0) != routed {
			t.Fatalf("Wire %q, Only %q: want a known wire, Only led by %q, a malformed id refused (got %v), and another profile routed unless Only forbids it (got %v)", wire, only, a.Provider(), malformed, other)
		}
	})

	t.Run("detect is bounded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		start := time.Now()
		_, _ = a.Detect(ctx)
		if time.Since(start) > 9*time.Second {
			t.Fatal("Detect ignored its deadline")
		}
	})

	t.Run("launch", func(t *testing.T) {
		r := agents.New(serveURL, "/usr/local/bin/devcules", a)
		spec, err := r.Spec(terminals.AgentLaunch{ID: a.ID(), Token: token, Model: "contract-model-1"}, dir)
		if err != nil {
			t.Fatalf("Spec: %s", err)
		}
		if len(spec.Cmd) == 0 || spec.Cmd[0] == "" {
			t.Fatal("no command")
		}
		for _, arg := range spec.Cmd {
			if strings.Contains(arg, token) {
				t.Fatal("the launch token is on the command line, where other processes can read it")
			}
		}
		for p := range spec.Files {
			if !filepath.IsLocal(p) {
				t.Fatalf("file %q escapes the launch dir", p)
			}
		}
		text := specText(spec)
		tokenFile := filepath.Join(dir, agents.TokenFile)
		for what, want := range map[string]string{
			"the model router": serveURL + "/llm/" + a.Provider(),
			"the MCP server":   serveURL + "/mcp",
			"the model":        "contract-model-1",
			"the token":        token,
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the launch never mentions %s (%q)", what, want)
			}
		}
		if !strings.Contains(text, "'/usr/local/bin/devcules' hook --agent "+a.ID()) ||
			!strings.Contains(text, "DEVCULES_LAUNCH_TOKEN_FILE="+agents.ShQuote(tokenFile)) &&
				!strings.Contains(text, jsonish("DEVCULES_LAUNCH_TOKEN_FILE="+agents.ShQuote(tokenFile))) {
			t.Error("the approval hook is not installed")
		}
		if got := string(spec.Files[agents.TokenFile]); got != token {
			t.Errorf("token file = %q, want the launch token", got)
		}
		if _, err := a.LaunchSpec(agents.Launch{}); err == nil {
			t.Error("an empty launch was accepted")
		}
	})

	t.Run("hook call", func(t *testing.T) {
		c, err := a.HookCall(f.ShellCall)
		if err != nil {
			t.Fatalf("shell call: %s", err)
		}
		if c.Tool != f.WantTool || c.Command != f.WantCommand {
			t.Fatalf("shell call = %+v, want tool %q command %q", c, f.WantTool, f.WantCommand)
		}
		c, err = a.HookCall(f.OtherCall)
		if err != nil || c.Tool != f.WantOtherTool || !strings.Contains(c.Command, f.WantOtherTool) {
			t.Fatalf("other call = %+v %v, want tool %q named in the command", c, err, f.WantOtherTool)
		}
		for _, bad := range [][]byte{nil, []byte("not json"), []byte(`{}`), []byte(`[]`)} {
			if _, err := a.HookCall(bad); err == nil {
				t.Errorf("HookCall(%q) accepted", bad)
			}
		}
	})

	t.Run("hook answer fails closed", func(t *testing.T) {
		allow := a.HookAnswer(agents.Answer{Allow: true, Reason: "full access"})
		if allow.Code != 0 {
			t.Fatalf("allow exits %d", allow.Code)
		}
		deny := a.HookAnswer(agents.Answer{Reason: "denied by admin"})
		failed := a.HookAnswer(agents.Answer{Failed: true, Reason: "cannot reach serve"})
		for name, r := range map[string]agents.Reply{"deny": deny, "failed": failed} {
			if r.Code == 0 && !bytes.Contains(r.Stdout, []byte("deny")) && !bytes.Contains(r.Stdout, []byte("block")) {
				t.Errorf("%s does not block: %+v", name, r)
			}
		}
		if !strings.Contains(deny.Stderr+string(deny.Stdout), "denied by admin") {
			t.Errorf("deny drops its reason: %+v", deny)
		}
		if !strings.Contains(failed.Stderr+string(failed.Stdout), "cannot reach serve") {
			t.Errorf("failure drops its reason: %+v", failed)
		}
	})
}

// jsonish is s as it appears inside a JSON string.
func jsonish(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

// specText is everything a launch says: its command, environment and
// files.
func specText(spec agents.Spec) string {
	var all strings.Builder
	all.WriteString(strings.Join(spec.Cmd, " "))
	for k, v := range spec.Env {
		all.WriteString("\n" + k + "=" + v)
	}
	for _, data := range spec.Files {
		all.WriteString("\n" + string(data))
	}
	return all.String()
}
