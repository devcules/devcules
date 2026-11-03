package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/hermes"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/providers"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/terminals"
)

// fakeServiceTools puts stand-in systemctl and launchctl first on
// PATH and points HOME at a temp dir, so an install touches neither
// the real home nor the real service manager. Each call is logged;
// fail makes them exit 1.
func fakeServiceTools(t *testing.T, fail bool) (home, logFile string) {
	t.Helper()
	home = t.TempDir()
	bin := t.TempDir()
	logFile = filepath.Join(t.TempDir(), "calls.log")
	code := "0"
	if fail {
		code = "1"
	}
	script := "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> \"" + logFile + "\"\nexit " + code + "\n"
	for _, name := range []string{"systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home, logFile
}

func TestMachineServiceInstallUninstall(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no sign-in service on " + runtime.GOOS)
	}
	home, logFile := fakeServiceTools(t, false)
	cfg := config.Config{DataDir: t.TempDir()}
	var out, errs bytes.Buffer
	if code := installMachineService(cfg, &out, &errs); code != exitOK {
		t.Fatalf("install = %d: %s", code, errs.String())
	}
	file := filepath.Join(home, ".config", "systemd", "user", "devcules-machine.service")
	want := "systemctl --user enable devcules-machine.service"
	if runtime.GOOS == "darwin" {
		file = filepath.Join(home, "Library", "LaunchAgents", "dev.devcules.machine.plist")
		want = "launchctl load -w " + file
	}
	body, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(body), "machine") {
		t.Fatalf("service file %s: %v", file, err)
	}
	calls, _ := os.ReadFile(logFile)
	if !strings.Contains(string(calls), want) || !strings.Contains(out.String(), "starts at every sign-in") {
		t.Fatalf("calls %q, out %q", calls, out.String())
	}
	out.Reset()
	if code := uninstallMachineService(cfg, &out, &errs); code != exitOK || !strings.Contains(out.String(), "no longer starts") {
		t.Fatalf("uninstall = %d %q %q", code, out.String(), errs.String())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the service file stayed: %v", err)
	}
	// Uninstalling twice is fine.
	if code := uninstallMachineService(cfg, io.Discard, io.Discard); code != exitOK {
		t.Fatal("a second uninstall failed")
	}
}

func TestMachineServiceInstallFails(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no sign-in service on " + runtime.GOOS)
	}
	fakeServiceTools(t, true)
	var errs bytes.Buffer
	if code := installMachineService(config.Config{DataDir: t.TempDir()}, io.Discard, &errs); code != exitError || errs.Len() == 0 {
		t.Fatalf("a failing service manager = %d %q, want an error", code, errs.String())
	}
}

func TestServiceQuoting(t *testing.T) {
	if got := shellQuote("/opt/it's/devcules"); got != `'/opt/it'\''s/devcules'` {
		t.Fatalf("shellQuote = %s", got)
	}
	if got := escapeXML("/a&b/<c>"); got != "/a&amp;b/&lt;c&gt;" {
		t.Fatalf("escapeXML = %s", got)
	}
	t.Setenv("HOME", "/home/k")
	if mustHome() != "/home/k" {
		t.Fatal("mustHome ignores HOME")
	}
}

func TestRoleStubAndEmptyAssistants(t *testing.T) {
	var out bytes.Buffer
	if code := runRole("mcp", config.Config{Listen: ":1", DataDir: "/d"}, &out); code != exitOK || !strings.Contains(out.String(), "mcp: listening on :1, data in /d") {
		t.Fatalf("runRole = %d %q", code, out.String())
	}
	reg := emptyAssistants(slog.New(slog.DiscardHandler))
	if reg == nil || len(reg.List()) != 0 {
		t.Fatal("the empty registry is not empty")
	}
}

func TestAgentLauncher(t *testing.T) {
	l := newAgentLauncher("http://serve.test:7659")
	if list := l.Agents(context.Background()); len(list) != len(agentList()) {
		t.Fatalf("Agents = %d, want every adapter", len(list))
	}
	got, err := l.Launch(context.Background(), terminals.AgentLaunch{ID: "codex", Token: "dvl_tok"}, filepath.Join(t.TempDir(), "launch-0123456789abcdef"))
	if err != nil || len(got.Cmd) == 0 || got.Cmd[0] != "codex" || len(got.Files) == 0 {
		t.Fatalf("Launch = %+v %v", got, err)
	}
	if _, err := l.Launch(context.Background(), terminals.AgentLaunch{ID: "nope"}, t.TempDir()); err == nil {
		t.Fatal("an unknown agent launched")
	}
}

func TestStyleAdapter(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	profiles := providers.New(s.Providers(), s.Meta())
	log := slog.New(slog.DiscardHandler)
	ap, err := approvals.New(ctx, approvals.Options{Store: s})
	if err != nil {
		t.Fatal(err)
	}
	build := func(l *assistants.Loop, state string) (assistants.Assistant, error) { return hermes.New(l, state) }
	// No profile for it: not listed.
	if a := styleAdapter("hermes", hermes.DefaultModel, profiles, nil, nil, launch.New(s), ap, nil, nil, nil, build, log); a != nil {
		t.Fatal("an adapter without its profile was wired")
	}
	if _, err := profiles.Add(ctx, providers.Profile{ID: "hermes", Name: "Hermes", Format: providers.FormatOpenAI,
		BaseURL: "http://127.0.0.1:8642/v1", Model: "hermes-4-70b"}); err != nil {
		t.Fatal(err)
	}
	a := styleAdapter("hermes", hermes.DefaultModel, profiles, nil, nil, launch.New(s), ap, nil, nil, nil, build, log)
	if a == nil || a.ID() != "hermes" {
		t.Fatalf("styleAdapter = %v, want the hermes adapter on its profile", a)
	}
	// A build that refuses leaves it unlisted.
	refuse := func(*assistants.Loop, string) (assistants.Assistant, error) { return nil, os.ErrInvalid }
	if a := styleAdapter("hermes", hermes.DefaultModel, profiles, nil, nil, nil, ap, nil, nil, nil, refuse, log); a != nil {
		t.Fatal("a refused build was listed")
	}
}
