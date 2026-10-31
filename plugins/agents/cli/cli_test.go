package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/devcules/devcules/plugins/agents"
	"github.com/devcules/devcules/plugins/agents/agenttest"
)

// fixture returns a marker assistant with a fixed binary, no PATH.
func fixture(t *testing.T) Agent {
	t.Helper()
	a, err := New("fixture",
		WithName("Fixture"),
		WithBinary("/opt/fixture/bin/fixture"),
		WithInstallHint("fixture install"),
		WithDetect(func(context.Context) (string, bool) { return "1.0.0", true }))
	if err != nil {
		t.Fatalf("New: %s", err)
	}
	return a
}

// TestContract holds an off-router assistant to the launch shape the
// coding agents share, minus their hook.
func TestContract(t *testing.T) {
	agenttest.Run(t, fixture(t), agenttest.Fixture{})
}

func TestLaunchIsBare(t *testing.T) {
	spec, err := fixture(t).LaunchSpec(agents.Launch{
		ServeURL: "http://serve.test:7659",
		Token:    "dvl_notmine",
		Dir:      "/launch",
		Provider: "meta",
		Model:    "m1",
		Hook:     "HOOK",
	})
	if err != nil {
		t.Fatalf("LaunchSpec: %s", err)
	}
	if len(spec.Cmd) != 1 || spec.Cmd[0] != "/opt/fixture/bin/fixture" {
		t.Fatalf("Cmd = %v, want just the binary", spec.Cmd)
	}
	if len(spec.Env) > 0 || len(spec.Files) > 0 {
		t.Fatalf("Env %v, Files %v: an off-router launch adds nothing", spec.Env, spec.Files)
	}
}

func TestBuiltins(t *testing.T) {
	for name, a := range map[string]Agent{"hermes": NewHermes(), "openclaw": NewOpenClaw()} {
		if a.ID() != name {
			t.Errorf("ID = %q, want %q", a.ID(), name)
		}
		if agents.Wired(a) {
			t.Errorf("%s claims the router", name)
		}
		if a.InstallHint() == "" {
			t.Errorf("%s has no install hint", name)
		}
		if _, err := a.LaunchSpec(agents.Launch{Dir: "/x"}); err != nil {
			t.Errorf("%s refused a dir: %s", name, err)
		}
	}
}

func TestNewRefuses(t *testing.T) {
	if _, err := New("Not An Id"); err == nil {
		t.Error("a bad id was accepted")
	}
	if _, err := New("ok", WithName("  ")); err == nil {
		t.Error("an empty name was accepted")
	}
	if _, err := New("ok", WithBinary("  ")); err == nil {
		t.Error("an empty binary was accepted")
	}
}

func TestHookClosed(t *testing.T) {
	a := fixture(t)
	if _, err := a.HookCall([]byte(`{"tool_name":"Bash"}`)); err == nil {
		t.Error("a hook call was accepted")
	}
	for _, allow := range []bool{true, false} {
		if r := a.HookAnswer(agents.Answer{Allow: allow}); r.Code == 0 {
			t.Errorf("allow %v: the hook succeeded", allow)
		} else if !strings.Contains(r.Stderr+string(r.Stdout), "no approval hook") {
			t.Errorf("allow %v: the reason is missing: %+v", allow, r)
		}
	}
}
