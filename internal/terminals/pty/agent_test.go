package pty_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/terminals/pty"
)

// TestTmuxAgentLaunch runs a stand-in agent: its files land 0600 in
// the launch dir, its env comes from the env file (never argv), the
// list names the agent, and when it exits the launch dir is gone and
// the terminal is a shell.
func TestTmuxAgentLaunch(t *testing.T) {
	m, ctx := tmuxManager(t)
	id := "00000000000000aa"
	dir := m.LaunchDir(id)
	secret := "tok-" + "Z7QWERTY"
	l := pty.Launch{
		Cmd:   []string{"bash", "-c", `read -r line; echo "got:$line probe:$PROBE_VAR"; cat "$1"`, "agent", filepath.Join(dir, "conf", "a.txt")},
		Env:   map[string]string{"PROBE_VAR": "it's " + secret},
		Files: map[string][]byte{"conf/a.txt": []byte("file-body\n")},
	}
	if _, err := m.OpenAgent(ctx, id, "testagent", l); err != nil {
		t.Fatalf("OpenAgent: %s", err)
	}
	t.Cleanup(func() { _ = m.Close(ctx, id) })

	for _, f := range []string{"conf/a.txt", "env.sh"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v, want mode 0600", f, st, err)
		}
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("launch dir: %v %v, want 0700", st, err)
	}
	terms, err := m.List(ctx)
	if err != nil || len(terms) != 1 || terms[0].Agent != "testagent" {
		t.Fatalf("List = %+v %v, want the agent named", terms, err)
	}

	stream := "00000000000000bb"
	if _, err := m.Attach(ctx, id, stream, 80, 24); err != nil {
		t.Fatalf("Attach: %s", err)
	}
	t.Cleanup(func() { m.Detach(stream) })
	v := watch(m, stream)
	if err := m.Write(stream, []byte("go\n")); err != nil {
		t.Fatal(err)
	}
	if out := v.waitFor(t, "file-body"); !strings.Contains(out, "got:go probe:it's "+secret) {
		t.Fatalf("agent output %q lacks its env", out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("launch dir still there after the agent exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Still a terminal: the shell that follows answers.
	if err := m.Write(stream, []byte("echo after-$((6*7))-$PROBE_VAR.\n")); err != nil {
		t.Fatal(err)
	}
	got := v.waitFor(t, "after-42-.")
	if !strings.Contains(got, "testagent exited (0)") {
		t.Fatalf("no exit note in %q", got)
	}
}

func TestOpenAgentRefusesBadLaunches(t *testing.T) {
	m, ctx := tmuxManager(t)
	for name, tc := range map[string]struct {
		id, agent string
		l         pty.Launch
	}{
		"bad id":      {"zz", "a", pty.Launch{Cmd: []string{"true"}}},
		"bad agent":   {"00000000000000c1", "Bad Agent", pty.Launch{Cmd: []string{"true"}}},
		"no command":  {"00000000000000c2", "a", pty.Launch{}},
		"escape path": {"00000000000000c3", "a", pty.Launch{Cmd: []string{"true"}, Files: map[string][]byte{"../x": nil}}},
		"env file":    {"00000000000000c4", "a", pty.Launch{Cmd: []string{"true"}, Files: map[string][]byte{"env.sh": nil}}},
		"bad env":     {"00000000000000c5", "a", pty.Launch{Cmd: []string{"true"}, Env: map[string]string{"bad-name": "x"}}},
	} {
		if _, err := m.OpenAgent(ctx, tc.id, tc.agent, tc.l); err == nil {
			t.Errorf("%s: OpenAgent succeeded", name)
		}
		if tc.id != "zz" {
			if _, err := os.Stat(m.LaunchDir(tc.id)); !os.IsNotExist(err) {
				t.Errorf("%s: launch dir left behind", name)
			}
		}
	}
	if terms, _ := m.List(ctx); len(terms) != 0 {
		t.Fatalf("refused launches left terminals: %+v", terms)
	}
}
