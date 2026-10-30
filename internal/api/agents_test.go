package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/pty"
	"github.com/devcules/devcules/internal/terminals/relay"
)

// fakeAgents offers four agents: one that starts, one not installed,
// one for a provider the router lacks, and one whose launch fails.
type fakeAgents struct{}

func (fakeAgents) Agents(context.Context) []terminals.AgentInfo {
	return []terminals.AgentInfo{
		{ID: "fake", Name: "Fake", Provider: "meta", Providers: []string{"meta", "openai"}, Installed: true, Version: "1.0.0"},
		{ID: "absent", Name: "Absent", Provider: "meta", Install: "curl -fsSL https://example.com/install.sh | sh"},
		{ID: "other", Name: "Other", Provider: "nope", Installed: true},
		{ID: "broken", Name: "Broken", Provider: "meta", Installed: true},
	}
}

func (fakeAgents) Launch(_ context.Context, a terminals.AgentLaunch, _ string) (pty.Launch, error) {
	if a.ID == "broken" {
		return pty.Launch{}, errors.New("no such binary")
	}
	return pty.Launch{
		Cmd: []string{"sh", "-c", `echo "started-$((6*7)) model-$FAKE_MODEL token-${FAKE_TOKEN:+set}"`},
		Env: map[string]string{"FAKE_TOKEN": a.Token, "FAKE_MODEL": a.Model},
	}, nil
}

// metaKey holds a key for the meta provider only: openai is served
// but has no key yet.
type metaKey struct{}

func (metaKey) Get(_ context.Context, name string) ([]byte, error) {
	if name == router.KeySecret("meta") {
		return []byte("meta-test-key"), nil
	}
	return nil, context.Canceled
}

type agentFix struct {
	mux     http.Handler
	pipe    *linktest.Server
	h       *hub.Hub
	session string
	tokens  *launch.Tokens
	queue   *approvals.Service
}

func agentFixture(t *testing.T) agentFix {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	h := hub.New(hub.HashToken(terminalTestToken), hub.Options{HelloTimeout: 5 * time.Second, PingInterval: time.Second, PingTimeout: 5 * time.Second})
	rl := relay.New(h, "")
	rl.Run()
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	tokens := launch.New(s)
	rt, err := router.New(router.Options{Providers: []router.Provider{namedProvider("meta"), namedProvider("openai")}, Keys: metaKey{}, Tokens: tokens, Redactor: r, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	queue, _ := approvals.New(ctx, approvals.Options{Store: s, Timeout: time.Minute})
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Link: h, Terminals: rl, Router: rt, Launches: tokens,
		ApprovalQueue: queue, AuditLog: audit.New(s, nil)})
	pipe := linktest.Serve(t, mux)
	return agentFix{mux, pipe, h, sessionCookie(t, mux, password), tokens, queue}
}

func TestAgentsAPI(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	f := agentFixture(t)

	// No runner yet.
	if w := doJSON(f.mux, http.MethodGet, "/api/agents", "", f.session); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no runner = %d %s", w.Code, w.Body)
	}

	m, err := pty.NewForTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	c, stopDial := terminalDialer(t, f.pipe, f.h, ctx, "laptop")
	defer stopDial()
	pty.Serve(ctx, c, m, fakeAgents{})
	for deadline := time.Now().Add(10 * time.Second); f.h.Count() == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	_ = f.queue.SetMode(ctx, "runner:laptop", approvals.ModeCritical, "admin", "")

	w := doJSON(f.mux, http.MethodGet, "/api/agents", "", f.session)
	var listed struct {
		Runner string
		Mode   string
		Agents []terminals.AgentInfo
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listed)
	if w.Code != 200 || listed.Runner != "laptop" || listed.Mode != "critical" || len(listed.Agents) != 4 || listed.Agents[0].Version != "1.0.0" {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}

	for body, want := range map[string]int{
		`{"agent":"absent"}`:                 http.StatusConflict,
		`{"agent":"other"}`:                  http.StatusConflict,
		`{"agent":"missing"}`:                http.StatusNotFound,
		`{"agent":"Bad Id"}`:                 http.StatusBadRequest,
		`{"agent":"fake","model":"a b"}`:     http.StatusBadRequest,
		`nope`:                               http.StatusBadRequest,
		`{"agent":"broken","model":"m-1.0"}`: http.StatusBadGateway,
		// Provider choice: one it cannot speak, one with no key yet.
		`{"agent":"fake","provider":"anthropic","model":"x"}`: http.StatusBadRequest,
		`{"agent":"fake","provider":"openai","model":"x"}`:    http.StatusConflict,
		`{"agent":"fake","name":"bad\tname"}`:                 http.StatusBadRequest,
	} {
		if w := doJSON(f.mux, http.MethodPost, "/api/agents", body, f.session); w.Code != want {
			t.Errorf("POST %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
	}
	w = doJSON(f.mux, http.MethodPost, "/api/agents", `{"agent":"absent"}`, f.session)
	if !strings.Contains(w.Body.String(), "install it there with curl") {
		t.Errorf("absent = %s, want the install hint", w.Body)
	}

	// The failed launch left its token revoked, not live.
	list, _ := f.tokens.List(ctx)
	if len(list) != 1 || list[0].Active || list[0].Label != "Broken on laptop" {
		t.Fatalf("tokens after a failed launch = %+v", list)
	}

	w = doJSON(f.mux, http.MethodPost, "/api/agents", `{"agent":"fake","provider":"openai","model":"x"}`, f.session)
	if !strings.Contains(w.Body.String(), "add its key in Settings") {
		t.Errorf("no key = %s, want the next step", w.Body)
	}
	w = doJSON(f.mux, http.MethodPost, "/api/agents", `{"agent":"fake","model":"m-1.0","provider":"meta","name":"review bot"}`, f.session)
	var started struct {
		ID     string
		Launch launch.Launch
	}
	_ = json.Unmarshal(w.Body.Bytes(), &started)
	if w.Code != http.StatusCreated || started.ID == "" || started.Launch.Runner != "laptop" || started.Launch.Provider != "meta" ||
		strings.Contains(w.Body.String(), launch.Prefix) {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}

	w = terminalRequest(t, f.mux, http.MethodGet, "/api/terminals", f.session)
	if !strings.Contains(w.Body.String(), `"agent":"fake"`) || !strings.Contains(w.Body.String(), `"name":"review bot"`) {
		t.Fatalf("terminals = %s, want the agent label", w.Body)
	}
	conn := dialTerminalStream(t, f.pipe, ctx, f.session, "/api/terminals/"+started.ID+"/stream?cols=80&rows=24", nil)
	readBrowserUntil(t, ctx, conn, "started-42 model-m-1.0 token-set", 15*time.Second)

	var actions []string
	for _, e := range auditActions(t, f.mux, f.session) {
		actions = append(actions, e.Action)
	}
	got := strings.Join(actions, " ")
	for _, want := range []string{audit.AgentLaunch, audit.LaunchMint, audit.LaunchRevoke} {
		if !strings.Contains(got, want) {
			t.Errorf("audit = %s, want %s", got, want)
		}
	}
}

func TestAgentsAPIWithoutRunnerOrTokens(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb()})
	session := sessionCookie(t, mux, password)
	if w := doJSON(mux, http.MethodGet, "/api/agents", "", session); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no relay = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodGet, "/api/agents", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", w.Code)
	}

	h := hub.New(hub.HashToken(terminalTestToken), hub.Options{})
	rl := relay.New(h, "")
	mux = Handler(Options{Store: s, Auth: svc, Web: testWeb(), Link: h, Terminals: rl})
	if w := doJSON(mux, http.MethodPost, "/api/agents", `{"agent":"fake"}`, session); w.Code != http.StatusServiceUnavailable ||
		!strings.Contains(w.Body.String(), "launch tokens") {
		t.Fatalf("no tokens = %d %s", w.Code, w.Body)
	}
	if w := doJSON(mux, http.MethodPut, "/api/agents", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT = %d", w.Code)
	}
}
