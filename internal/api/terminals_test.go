package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

const terminalTestToken = "terminal-test-token-0123456789"

// terminalFixture builds the full mux (store, auth, hub, relay) on a
// pipe server: plain HTTP goes through s.Client, browser WebSockets
// dial ws://pipe. It returns the mux for bind-free calls, the pipe
// server, the hub, and a signed-in session cookie value.
func terminalFixture(t *testing.T) (http.Handler, *linktest.Server, *hub.Hub, string) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, created, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureAdmin: %s", err)
	}
	if !created {
		t.Fatal("EnsureAdmin created = false on a fresh store")
	}
	h := hub.New(hub.HashToken(terminalTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
	})
	r := relay.New(h, "")
	r.Run()
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Link: h, Terminals: r})
	pipe := linktest.Serve(t, mux)
	return mux, pipe, h, sessionCookie(t, mux, password)
}

func terminalDialer(t *testing.T, pipe *linktest.Server, ctx context.Context, name string) (*dial.Client, context.CancelFunc) {
	t.Helper()
	child, stop := context.WithCancel(ctx)
	c := dial.New("ws://pipe/link", name, "test", terminalTestToken, dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   pipe.Client(),
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("dial Run did not return after context cancel")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for !c.Linked() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Linked() {
		t.Fatalf("dialer %s never linked", name)
	}
	return c, stop
}

// terminalRequest issues one REST call with the session cookie.
func terminalRequest(t *testing.T, mux http.Handler, method, target, session string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestTerminalRoutesNeedSession(t *testing.T) {
	t.Parallel()
	mux, _, _, _ := terminalFixture(t)
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/api/terminals"},
		{http.MethodPost, "/api/terminals"},
		{http.MethodDelete, "/api/terminals/0123456789abcdef"},
		{http.MethodGet, "/api/terminals/0123456789abcdef/stream"},
	} {
		r := httptest.NewRequest(tc.method, tc.target, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.target, w.Code)
		}
	}
}

func TestTerminalMethodNotAllowed(t *testing.T) {
	t.Parallel()
	mux, _, _, session := terminalFixture(t)
	for _, tc := range []struct{ method, target string }{
		{http.MethodPut, "/api/terminals"},
		{http.MethodDelete, "/api/terminals"},
		{http.MethodGet, "/api/terminals/0123456789abcdef"},
		{http.MethodPost, "/api/terminals/0123456789abcdef"},
		{http.MethodDelete, "/api/terminals/0123456789abcdef/stream"},
	} {
		w := terminalRequest(t, mux, tc.method, tc.target, session)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tc.method, tc.target, w.Code)
		}
	}
}

func TestTerminalPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		path     string
		id       string
		isStream bool
	}{
		{"/api/terminals/0123456789abcdef", "0123456789abcdef", false},
		{"/api/terminals/0123456789abcdef/stream", "0123456789abcdef", true},
		{"/api/terminals/", "", false},
		{"/api/terminals/a/b", "", false},
		{"/api/terminals/a/stream/extra", "", false},
		{"/api/terminals", "", false},
	} {
		id, isStream := terminalPath(tc.path)
		if id != tc.id || isStream != tc.isStream {
			t.Errorf("terminalPath(%q) = (%q, %v), want (%q, %v)",
				tc.path, id, isStream, tc.id, tc.isStream)
		}
	}
}

func TestTerminalNoRunnerIs503(t *testing.T) {
	t.Parallel()
	mux, _, _, session := terminalFixture(t)
	if w := terminalRequest(t, mux, http.MethodGet, "/api/terminals", session); w.Code != http.StatusServiceUnavailable {
		t.Errorf("list with no runner = %d, want 503 (body %q)", w.Code, w.Body.String())
	}
	if w := terminalRequest(t, mux, http.MethodPost, "/api/terminals", session); w.Code != http.StatusServiceUnavailable {
		t.Errorf("open with no runner = %d, want 503 (body %q)", w.Code, w.Body.String())
	}
	if w := terminalRequest(t, mux, http.MethodDelete, "/api/terminals/0123456789abcdef", session); w.Code != http.StatusServiceUnavailable {
		t.Errorf("delete with no runner = %d, want 503 (body %q)", w.Code, w.Body.String())
	}
}

func TestTerminalOpenTimeoutIs504(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	mux, pipe, _, session := terminalFixture(t)
	// A linked runner with no terminal handlers never answers.
	_, stopDial := terminalDialer(t, pipe, ctx, "silent")
	defer stopDial()

	w := terminalRequest(t, mux, http.MethodPost, "/api/terminals", session)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("open with silent runner = %d, want 504 (body %q)", w.Code, w.Body.String())
	}
}

// dialTerminalStream opens a browser stream through the pipe server
// with the session cookie. Extra headers (e.g. Origin) ride along.
func dialTerminalStream(t *testing.T, pipe *linktest.Server, ctx context.Context, session, target string, header http.Header) *websocket.Conn {
	t.Helper()
	if header == nil {
		header = http.Header{}
	}
	header.Set("Cookie", auth.CookieName+"="+session)
	conn, _, err := websocket.Dial(ctx, "ws://pipe"+target, &websocket.DialOptions{
		HTTPClient: pipe.Client(),
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("stream dial: %s", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	// Real browsers have no message limit; output batches can pass
	// the library's 32 KiB default.
	conn.SetReadLimit(-1)
	return conn
}

func readBrowserUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var sb strings.Builder
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithDeadline(ctx, deadline)
		kind, msg, err := conn.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("browser read: %s (got %q)", err, sb.String())
		}
		if kind == websocket.MessageText {
			continue
		}
		sb.Write(msg)
		if strings.Contains(sb.String(), want) {
			return
		}
	}
	t.Fatalf("never saw %q (got %q)", want, sb.String())
}

func readBrowserClose(t *testing.T, ctx context.Context, conn *websocket.Conn, timeout time.Duration) websocket.StatusCode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithDeadline(ctx, deadline)
		_, _, err := conn.Read(rctx)
		cancel()
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code
		}
		t.Fatalf("browser read error is %T (%s), want CloseError", err, err)
	}
	t.Fatal("browser stream never closed")
	return 0
}

func openTerminal(t *testing.T, mux http.Handler, session string) string {
	t.Helper()
	w := terminalRequest(t, mux, http.MethodPost, "/api/terminals", session)
	if w.Code != http.StatusCreated {
		t.Fatalf("open = %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.ID == "" {
		t.Fatalf("open body = %q, want {\"id\":...}", w.Body.String())
	}
	return body.ID
}

func withTmuxRunner(t *testing.T, pipe *linktest.Server, h *hub.Hub, ctx context.Context, name string) context.CancelFunc {
	t.Helper()
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	m, err := pty.NewForTest(t.TempDir())
	if err != nil {
		t.Fatalf("pty manager: %s", err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	c, stop := terminalDialer(t, pipe, ctx, name)
	pty.Serve(ctx, c, m, nil)
	// The hub registers a link just after its welcome: wait for the
	// hub's view so the first request finds the runner.
	deadline := time.Now().Add(10 * time.Second)
	for h.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return stop
}

func TestTerminalStreamOriginRejected(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	_, pipe, _, session := terminalFixture(t)

	// A cross-site WebSocket upgrade must fail closed with 403, the
	// same way /link rejects browser Origins.
	header := http.Header{"Origin": {"https://evil.example"}}
	header.Set("Cookie", auth.CookieName+"="+session)
	_, resp, err := websocket.Dial(ctx, "ws://pipe/api/terminals/0123456789abcdef/stream",
		&websocket.DialOptions{HTTPClient: pipe.Client(), HTTPHeader: header})
	if err == nil {
		t.Fatal("cross-site stream dial succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		t.Fatalf("cross-site status = %d, want 403", status)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func TestTerminalFullRoundTrip(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	mux, pipe, h, session := terminalFixture(t)
	defer withTmuxRunner(t, pipe, h, ctx, "r1")()

	id := openTerminal(t, mux, session)

	// GET lists the open terminal, then the stream carries a shell
	// round trip, then DELETE removes it.
	w := terminalRequest(t, mux, http.MethodGet, "/api/terminals", session)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var listed struct {
		Terminals []struct {
			Term string `json:"term"`
		} `json:"terminals"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list body = %q: %s", w.Body.String(), err)
	}
	if len(listed.Terminals) != 1 || listed.Terminals[0].Term != id {
		t.Fatalf("list = %+v, want the opened terminal", listed)
	}

	conn := dialTerminalStream(t, pipe, ctx, session,
		"/api/terminals/"+id+"/stream?cols=80&rows=24", nil)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo hi-$((40+2))\n")); err != nil {
		t.Fatalf("stream write: %s", err)
	}
	readBrowserUntil(t, ctx, conn, "hi-42", 15*time.Second)

	w = terminalRequest(t, mux, http.MethodDelete, "/api/terminals/"+id, session)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	w = terminalRequest(t, mux, http.MethodGet, "/api/terminals", session)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"terminals":[]}` {
		t.Fatalf("list after delete = %d %q, want 200 {\"terminals\":[]}", w.Code, w.Body.String())
	}
	// Deletes stay idempotent: closing a gone terminal still answers
	// 204, since the end state is what the browser asked for.
	if w := terminalRequest(t, mux, http.MethodDelete, "/api/terminals/"+id, session); w.Code != http.StatusNoContent {
		t.Fatalf("second delete = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
}

func TestTerminalStreamOfflineIs1012(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	mux, pipe, h, session := terminalFixture(t)
	stopRunner := withTmuxRunner(t, pipe, h, ctx, "r1")

	id := openTerminal(t, mux, session)
	conn := dialTerminalStream(t, pipe, ctx, session,
		"/api/terminals/"+id+"/stream?cols=80&rows=24", nil)

	// The runner going away retires the browser stream with 1012; the
	// UI shows "runner offline" and reattaches on return.
	stopRunner()
	if code := readBrowserClose(t, ctx, conn, 15*time.Second); code != websocket.StatusCode(1012) {
		t.Fatalf("offline close = %d, want 1012", code)
	}
	if w := terminalRequest(t, mux, http.MethodGet, "/api/terminals?runner=r1", session); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("list after unlink = %d, want 503 (body %q)", w.Code, w.Body.String())
	}
}

func TestTerminalUnknownRunnerChoice(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	mux, pipe, _, session := terminalFixture(t)
	_, stopDial := terminalDialer(t, pipe, ctx, "r1")
	defer stopDial()

	w := terminalRequest(t, mux, http.MethodGet, "/api/terminals?runner=nope", session)
	if w.Code != http.StatusNotFound {
		t.Fatalf("list with unknown runner = %d, want 404 (body %q)", w.Code, w.Body.String())
	}
}
