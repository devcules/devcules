package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/terminals/pty"
	"github.com/devcules/devcules/internal/terminals/relay"
)

// projectsFixture is terminalFixture with machines verified and the
// project store wired.
func projectsFixture(t *testing.T) (http.Handler, *linktest.Server, *hub.Hub, string) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureAdmin: %s", err)
	}
	h := hub.New(hub.HashToken(terminalTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		Machines:     testMachineKeys,
	})
	r := relay.New(h, "")
	r.Run()
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Link: h, Terminals: r, Projects: s.Projects()})
	return mux, linktest.Serve(t, mux), h, sessionCookie(t, mux, password)
}

func projectRequest(t *testing.T, mux http.Handler, method, target, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestProjectsCRUD(t *testing.T) {
	t.Parallel()
	mux, _, _, session := projectsFixture(t)
	w := projectRequest(t, mux, http.MethodPost, "/api/projects", session, `{"name":"shop","kind":"runner","target":"laptop","dir":"/home/k/shop/"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %q", w.Code, w.Body.String())
	}
	var added struct {
		Project  projectJSON `json:"project"`
		Terminal string      `json:"terminal"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &added); err != nil || !strings.HasPrefix(added.Project.ID, "p_") || added.Project.Dir != "/home/k/shop" || added.Terminal != "" {
		t.Fatalf("add body = %q", w.Body.String())
	}
	if w := projectRequest(t, mux, http.MethodPost, "/api/projects", session, `{"name":"shop","kind":"machine","target":"mac","dir":"/x"}`); w.Code != http.StatusConflict {
		t.Fatalf("taken name = %d", w.Code)
	}
	for name, body := range map[string]string{
		"escaping dir": `{"kind":"runner","target":"laptop","dir":"../etc"}`,
		"file repo":    `{"kind":"runner","target":"laptop","repo":"file:///etc"}`,
		"bad kind":     `{"kind":"host","target":"laptop","dir":"/x"}`,
		"bad target":   `{"kind":"runner","target":"a/b","dir":"/x"}`,
		"unknown key":  `{"kind":"runner","target":"laptop","dir":"/x","cmd":"rm"}`,
		"no dir":       `{"kind":"runner","target":"laptop"}`,
	} {
		if w := projectRequest(t, mux, http.MethodPost, "/api/projects", session, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %q, want 400", name, w.Code, w.Body.String())
		}
	}
	// A clone needs the target online; nothing is recorded otherwise.
	if w := projectRequest(t, mux, http.MethodPost, "/api/projects", session, `{"kind":"runner","target":"laptop","repo":"https://github.com/acme/api.git"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("clone offline = %d %q", w.Code, w.Body.String())
	}
	w = projectRequest(t, mux, http.MethodGet, "/api/projects", session, "")
	var list struct {
		Projects []projectJSON `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Projects) != 1 || list.Projects[0].Name != "shop" {
		t.Fatalf("list = %q", w.Body.String())
	}
	if w := projectRequest(t, mux, http.MethodPost, "/api/projects/"+added.Project.ID+"/terminals", session, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("open on an offline runner = %d %q", w.Code, w.Body.String())
	}
	if w := projectRequest(t, mux, http.MethodDelete, "/api/projects/"+added.Project.ID, session, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodPost, "/api/projects/"+added.Project.ID+"/terminals", session, ""); w.Code != http.StatusNotFound {
		t.Fatalf("open a deleted project = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodGet, "/api/projects", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", w.Code)
	}
}

func TestRepoDir(t *testing.T) {
	for repo, want := range map[string]string{
		"https://github.com/acme/shop.git": "shop",
		"git@github.com:acme/api.git":      "api",
		"ssh://git@host/acme/tools":        "tools",
		"https://host/":                    "",
	} {
		if got := repoDir(repo); got != want {
			t.Errorf("repoDir(%q) = %q, want %q", repo, got, want)
		}
	}
}

// withTmuxMachine links a machine named name whose shell runs in a
// private tmux, the way `devcules machine` serves terminals.
func withTmuxMachine(t *testing.T, pipe *linktest.Server, h *hub.Hub, ctx context.Context, name, workspace string) {
	t.Helper()
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	m, err := pty.New(t.TempDir(), workspace, nil)
	if err != nil {
		t.Fatalf("pty manager: %s", err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	child, stop := context.WithCancel(ctx)
	c := dial.New("ws://pipe/link", name, "test", "", dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   pipe.Client(),
		Role:         "machine",
		Sign:         testMachineKeys.Add(name),
	})
	pty.Serve(child, c, m, nil)
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		<-done
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(h.Machines(), name) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("machine %s never linked", name)
}

// TestMachineProjectTerminal is the Machines-tab "terminal" and the
// Projects tab together: a project on a machine opens a shell in its
// directory, the machine lists it under the project, and the browser
// stream reaches it with ?machine=.
func TestMachineProjectTerminal(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	mux, pipe, h, session := projectsFixture(t)
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "shop", "marker.txt"), []byte("inside-shop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withTmuxMachine(t, pipe, h, ctx, "studio-mac", ws)

	// A plain machine terminal, the "ssh" path.
	w := projectRequest(t, mux, http.MethodPost, "/api/terminals?machine=studio-mac", session, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("machine terminal = %d %q", w.Code, w.Body.String())
	}
	// Without ?machine= the runner default does not fall back to it.
	if w := projectRequest(t, mux, http.MethodPost, "/api/terminals", session, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no runner = %d, want 503", w.Code)
	}

	w = projectRequest(t, mux, http.MethodPost, "/api/projects", session, `{"kind":"machine","target":"studio-mac","dir":"shop"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %q", w.Code, w.Body.String())
	}
	var added struct {
		Project projectJSON `json:"project"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &added)
	if added.Project.Name != "shop" {
		t.Fatalf("default name = %q, want the dir's base name", added.Project.Name)
	}
	w = projectRequest(t, mux, http.MethodPost, "/api/projects/"+added.Project.ID+"/terminals", session, `{"name":"build"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("project terminal = %d %q", w.Code, w.Body.String())
	}
	var opened struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &opened)

	w = projectRequest(t, mux, http.MethodGet, "/api/terminals?machine=studio-mac", session, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"term":"`+opened.ID+`"`) || !strings.Contains(w.Body.String(), `"project":"`+added.Project.ID+`"`) || !strings.Contains(w.Body.String(), `"name":"build"`) {
		t.Fatalf("machine list = %d %q", w.Code, w.Body.String())
	}

	conn := dialTerminalStream(t, pipe, ctx, session, "/api/terminals/"+opened.ID+"/stream?machine=studio-mac&cols=80&rows=24", nil)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("cat marker.txt\n")); err != nil {
		t.Fatalf("write: %s", err)
	}
	readBrowserUntil(t, ctx, conn, "inside-shop", 10*time.Second)

	if w := projectRequest(t, mux, http.MethodDelete, "/api/terminals/"+opened.ID+"?machine=studio-mac", session, ""); w.Code != http.StatusNoContent {
		t.Fatalf("close = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodGet, "/api/terminals?machine=nobody", session, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown machine = %d, want 503", w.Code)
	}
}

// TestBrowseRoute lists folders on a runner for the project picker. It
// needs no tmux: the listing is plain directory reads.
func TestBrowseRoute(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	mux, pipe, h, session := projectsFixture(t)
	m, err := pty.NewForTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	c, stopDial := terminalDialer(t, pipe, h, ctx, "box")
	defer stopDial()
	pty.Serve(ctx, c, m, nil)

	root := t.TempDir()
	for _, d := range []string{"shop", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// shop is a real repo on github, so remoteOf reads its origin URL.
	// notes stays a plain folder.
	initCmd := exec.Command("git", "init", "-q", filepath.Join(root, "shop"))
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", filepath.Join(root, "shop"), "remote", "add", "origin", "git@github.com:acme/shop.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, out)
	}
	w := projectRequest(t, mux, http.MethodGet, "/api/browse?path="+root, session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("browse = %d %q", w.Code, w.Body.String())
	}
	var got struct {
		Path    string `json:"path"`
		Parent  string `json:"parent"`
		Entries []struct {
			Name   string `json:"name"`
			Git    bool   `json:"git"`
			Remote string `json:"remote"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Entries) != 2 || got.Entries[1].Name != "shop" || !got.Entries[1].Git || got.Entries[0].Git {
		t.Fatalf("browse body = %q", w.Body.String())
	}
	if got.Entries[1].Remote != "github" || got.Entries[0].Remote != "" {
		t.Fatalf("remote codes = %q", w.Body.String())
	}
	if w := projectRequest(t, mux, http.MethodGet, "/api/browse?path="+filepath.Join(root, "missing"), session, ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodGet, "/api/browse?path=../etc", session, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("escaping = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodGet, "/api/browse?machine=nobody", session, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unlinked machine = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodGet, "/api/browse", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", w.Code)
	}
	if w := projectRequest(t, mux, http.MethodPost, "/api/terminals", session, `{"name":"bad\tname"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad name = %d", w.Code)
	}
}
