package hub_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
)

const testToken = "test-runner-token-no-whitespace"

// testHub builds a hub expecting testToken with fast timeouts.
func testHub() *hub.Hub {
	return hub.New(hub.HashToken(testToken), hub.Options{
		HelloTimeout: 500 * time.Millisecond,
		PingInterval: 20 * time.Millisecond,
		PingTimeout:  200 * time.Millisecond,
	})
}

// calmHub builds a hub with production-spaced heartbeats. Tests that
// assert exact close codes use it: on a synchronous pipe, a ping in
// flight during a server-initiated close deadlocks both writers (each
// waits for the other to read), while kernel buffers absorb the same
// race in production. See TestOversizeFrameDropsLink.
func calmHub() *hub.Hub {
	return hub.New(hub.HashToken(testToken), hub.Options{
		HelloTimeout: 500 * time.Millisecond,
		PingInterval: 30 * time.Second,
		PingTimeout:  5 * time.Second,
	})
}

func authHeader() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+testToken)
	return h
}

// dialRaw upgrades with the test token and returns the client conn.
func dialRaw(t *testing.T, s *linktest.Server, header http.Header) *websocket.Conn {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	conn, _, err := websocket.Dial(ctx, "ws://pipe/link", &websocket.DialOptions{
		HTTPClient: s.Client(),
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("dial: %s", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func helloBytes(t *testing.T, role, name string) []byte {
	t.Helper()
	data, err := json.Marshal(link.Hello{Role: role, Name: name, Version: "test"})
	if err != nil {
		t.Fatalf("marshal hello: %s", err)
	}
	b, err := link.Encode(link.Frame{V: link.Version, Type: "hello", ID: link.NewID(), Data: data})
	if err != nil {
		t.Fatalf("encode hello: %s", err)
	}
	return b
}

// linkUp dials, hellos, and returns the conn plus the welcome frame.
func linkUp(t *testing.T, s *linktest.Server, name string) (*websocket.Conn, link.Frame) {
	t.Helper()
	conn := dialRaw(t, s, authHeader())
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "runner", name)); err != nil {
		t.Fatalf("write hello: %s", err)
	}
	typ, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read welcome: %s", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("welcome type = %v, want text", typ)
	}
	welcome, err := link.Decode(raw)
	if err != nil {
		t.Fatalf("decode welcome: %s", err)
	}
	if welcome.Type != "welcome" {
		t.Fatalf("welcome type = %q", welcome.Type)
	}
	return conn, welcome
}

func closeReason(t *testing.T, err error) (websocket.StatusCode, string) {
	t.Helper()
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %T %v, want CloseError", err, err)
	}
	return ce.Code, ce.Reason
}

// pollCount waits for the hub to report want links. Polling with a
// deadline (not fixed sleeps) keeps timing tests deterministic.
func pollCount(t *testing.T, h *hub.Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.Count() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hub count = %d, want %d", h.Count(), want)
}

func TestHandshakeSuccess(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	conn, welcome := linkUp(t, s, "r1")
	defer conn.CloseNow()
	var data struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(welcome.Data, &data); err != nil || data.Session == "" {
		t.Fatalf("welcome data = %s, want a session", welcome.Data)
	}
	pollCount(t, h, 1)

	// Frames route to the registered handler with the runner name.
	got := make(chan link.Frame, 1)
	h.On("term.write", func(_ context.Context, runner string, f link.Frame) {
		if runner != "r1" {
			t.Errorf("handler runner = %q, want r1", runner)
		}
		got <- f
	})
	payload, err := link.Encode(link.Frame{V: 1, Type: "term.write", ID: "a", Data: json.RawMessage(`{"n":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write frame: %s", err)
	}
	select {
	case f := <-got:
		if f.Type != "term.write" || string(f.Data) != `{"n":1}` {
			t.Fatalf("handler got %+v", f)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("handler never fired")
	}

	// A clean client close unregisters the link.
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("client close: %s", err)
	}
	pollCount(t, h, 0)
}

func TestAuthRejections(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())

	get := func(header http.Header, method string) int {
		req, _ := http.NewRequest(method, "http://pipe/link", nil)
		for k, vs := range header {
			req.Header[k] = vs
		}
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %s", method, err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}

	if code := get(nil, "GET"); code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", code)
	}
	bad := http.Header{}
	bad.Set("Authorization", "Bearer wrong-token")
	if code := get(bad, "GET"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", code)
	}
	basic := http.Header{}
	basic.Set("Authorization", "Basic abc")
	if code := get(basic, "GET"); code != http.StatusUnauthorized {
		t.Fatalf("basic auth = %d, want 401", code)
	}
	origin := authHeader()
	origin.Set("Origin", "https://evil.example")
	if code := get(origin, "GET"); code != http.StatusForbidden {
		t.Fatalf("origin header = %d, want 403", code)
	}
	if code := get(authHeader(), "POST"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", code)
	}
	// A plain GET with a valid token is not asserted here: without
	// WebSocket handshake headers the upgrade fails below the auth
	// checks, and every other test proves the upgrade succeeds.
}

func TestHandshakeFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		first      string // first frame bytes; "" means send nothing
		wantCode   websocket.StatusCode
		wantReason string
	}{
		{name: "unsupported version", first: `{"v":2,"type":"hello","id":"1","data":{"role":"runner","name":"r","version":"v"}}`, wantCode: websocket.StatusPolicyViolation, wantReason: "unsupported link version"},
		{name: "unknown role", first: `{"v":1,"type":"hello","id":"1","data":{"role":"machine","name":"m","version":"v"}}`, wantCode: websocket.StatusPolicyViolation, wantReason: "unknown role"},
		{name: "not hello", first: `{"v":1,"type":"welcome","id":"1","data":{"session":"s"}}`, wantCode: websocket.StatusPolicyViolation, wantReason: "no hello"},
		{name: "garbage", first: `{{{`, wantCode: websocket.StatusPolicyViolation, wantReason: "no hello"},
		{name: "no hello", first: ``, wantCode: websocket.StatusPolicyViolation, wantReason: "no hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := testHub()
			s := linktest.Serve(t, h.Handler())
			conn := dialRaw(t, s, authHeader())
			if tc.first != "" {
				ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := conn.Write(ctx, websocket.MessageText, []byte(tc.first)); err != nil {
					t.Fatalf("write first frame: %s", err)
				}
			}
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			_, _, err := conn.Read(ctx)
			if err == nil {
				t.Fatal("read succeeded, want a close")
			}
			code, reason := closeReason(t, err)
			if code != tc.wantCode || reason != tc.wantReason {
				t.Fatalf("close = %d %q, want %d %q", code, reason, tc.wantCode, tc.wantReason)
			}
			pollCount(t, h, 0)
		})
	}
}

// TestSessionCookieIgnored proves /link never authenticates with the
// session cookie: a bogus cookie plus the valid token still links.
// (The header value below is a fixture, not a secret.)
func TestSessionCookieIgnored(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	header := authHeader()
	header.Set("Cookie", "devcules_session=bogus")
	conn := dialRaw(t, s, header)
	defer conn.CloseNow()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, helloBytes(t, "runner", "cookie")); err != nil {
		t.Fatalf("write hello: %s", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read welcome: %s", err)
	}
	pollCount(t, h, 1)
}

func TestDuplicateNameReplaces(t *testing.T) {
	t.Parallel()
	h := calmHub()
	s := linktest.Serve(t, h.Handler())
	old, _ := linkUp(t, s, "dup")
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()

	// The second link with the same name connects and is welcomed…
	fresh, _ := linkUp(t, s, "dup")
	defer fresh.CloseNow()
	pollCount(t, h, 1)

	// …while the elder reads its replacement notice and is gone.
	_, _, err := old.Read(ctx)
	if err == nil {
		t.Fatal("old link read succeeded, want a close")
	}
	if code, reason := closeReason(t, err); code != websocket.StatusCode(4000) || reason != "replaced" {
		t.Fatalf("old link close = %d %q, want 4000 replaced", code, reason)
	}
}

func TestOversizeFrameDropsLink(t *testing.T) {
	t.Parallel()
	h := calmHub()
	s := linktest.Serve(t, h.Handler())
	conn, _ := linkUp(t, s, "big")
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	huge := make([]byte, link.MaxFrameBytes+100)
	for i := range huge {
		huge[i] = 'x'
	}
	// A well-formed envelope is not required: the read limit trips on
	// size alone, before the codec ever runs.
	if err := conn.Write(ctx, websocket.MessageText, huge); err != nil {
		t.Fatalf("write huge frame: %s", err)
	}
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("read succeeded after oversize frame, want a close")
	}
	if code, _ := closeReason(t, err); code != websocket.StatusMessageTooBig {
		t.Fatalf("close code = %d, want 1009", code)
	}
	pollCount(t, h, 0)
}

func TestHeartbeatDropsDeadLink(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	conn, _ := linkUp(t, s, "ghost")
	pollCount(t, h, 1)
	// Abandon the connection without a close handshake: no more pongs
	// can arrive, so the ping loop must retire the link on its own.
	conn.CloseNow()
	pollCount(t, h, 0)
}

func TestShutdownClosesLinks(t *testing.T) {
	t.Parallel()
	h := calmHub()
	s := linktest.Serve(t, h.Handler())
	conn, _ := linkUp(t, s, "drain")
	defer conn.CloseNow()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	// The client reads while the hub closes, like a live runner during
	// the SIGTERM drain: Hub.Close waits for the handshakes, so a
	// read started after it returns would deadlock the rendezvous.
	type readOut struct {
		err error
	}
	readCh := make(chan readOut, 1)
	go func() {
		_, _, err := conn.Read(ctx)
		readCh <- readOut{err}
	}()
	h.Close()
	out := <-readCh
	if out.err == nil {
		t.Fatal("read succeeded after hub close, want 1001")
	}
	if code, reason := closeReason(t, out.err); code != websocket.StatusGoingAway || reason != "serve shutdown" {
		t.Fatalf("close = %d %q, want 1001 serve shutdown", code, reason)
	}
	// Close is idempotent: the SIGTERM drain calls it unconditionally.
	h.Close()
}

func TestSendTo(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	conn, _ := linkUp(t, s, "target")
	defer conn.CloseNow()
	// The welcome goes out before the link registers (so nothing can
	// overtake it), so wait for the registration, not just the welcome.
	pollCount(t, h, 1)

	sent := link.Frame{V: 1, Type: "term.write", ID: "s1", Data: json.RawMessage(`{}`)}
	// The client reads concurrently: a pipe has no buffers, so the
	// server's write rendezvouses with this read.
	type readOut struct {
		raw []byte
		err error
	}
	readCh := make(chan readOut, 1)
	go func() {
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, raw, err := conn.Read(ctx)
		readCh <- readOut{raw, err}
	}()
	if err := h.SendTo("target", sent); err != nil {
		t.Fatalf("SendTo: %s", err)
	}
	out := <-readCh
	if out.err != nil {
		t.Fatalf("read sent frame: %s", out.err)
	}
	got, err := link.Decode(out.raw)
	if err != nil {
		t.Fatalf("decode sent frame: %s", err)
	}
	if got.ID != "s1" {
		t.Fatalf("sent frame id = %q, want s1", got.ID)
	}

	if err := h.SendTo("nobody", sent); !errors.Is(err, hub.ErrUnknownRunner) {
		t.Fatalf("SendTo unknown = %v, want ErrUnknownRunner", err)
	}
	if err := h.SendTo("target", link.Frame{}); err == nil {
		t.Fatal("SendTo invalid frame succeeded")
	}
}

func TestOnRejectsBadType(t *testing.T) {
	t.Parallel()
	h := testHub()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("On(bad-type) did not panic")
		}
	}()
	h.On("Bad Type!", func(_ context.Context, _ string, _ link.Frame) {})
}

func TestEnsureTokenFile(t *testing.T) {
	t.Parallel()
	t.Run("plaintext wins, no file written", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "runner.token")
		token, created, err := hub.EnsureTokenFile(path, "direct-token")
		if err != nil || created || token != "direct-token" {
			t.Fatalf("= %q %v %v, want direct-token false nil", token, created, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("token file was written despite plaintext")
		}
	})
	t.Run("missing file is generated 0400", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "sub", "runner.token")
		token, created, err := hub.EnsureTokenFile(path, "")
		if err != nil || !created {
			t.Fatalf("= %q %v %v, want generated true nil", token, created, err)
		}
		if len(token) != 64 {
			t.Fatalf("generated token length = %d, want 64 hex chars", len(token))
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated file: %s", err)
		}
		if strings.TrimSpace(string(raw)) != token {
			t.Fatal("generated file does not hold the token")
		}
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o400 {
			t.Fatalf("token file mode = %o, want 400", fi.Mode().Perm())
		}
		// A second call reads the same token back: rotation needs a
		// delete plus a restart, never a silent rewrite.
		again, created, err := hub.EnsureTokenFile(path, "")
		if err != nil || created || again != token {
			t.Fatalf("second call = %q %v %v, want the same token", again, created, err)
		}
	})
	t.Run("existing file is read", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "runner.token")
		if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		token, created, err := hub.EnsureTokenFile(path, "")
		if err != nil || created || token != "file-token" {
			t.Fatalf("= %q %v %v, want file-token false nil", token, created, err)
		}
	})
	t.Run("empty file is replaced", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "runner.token")
		if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
			t.Fatal(err)
		}
		token, created, err := hub.EnsureTokenFile(path, "")
		if err != nil || !created || token == "" {
			t.Fatalf("= %q %v %v, want a generated token", token, created, err)
		}
	})
	t.Run("invalid file content errors", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "runner.token")
		if err := os.WriteFile(path, []byte("has space"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := hub.EnsureTokenFile(path, ""); err == nil {
			t.Fatal("invalid file content succeeded")
		} else if !strings.Contains(err.Error(), path) {
			t.Fatalf("error = %q, want the path with next steps", err)
		}
	})
}

// TestRunnersListsSortedNames proves Runners reports exactly the live
// links in sorted order: the relay picks the only runner by it.
func TestRunnersListsSortedNames(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	if got := h.Runners(); len(got) != 0 {
		t.Fatalf("Runners() = %v, want empty", got)
	}
	c1, _ := linkUp(t, s, "bravo")
	defer c1.CloseNow()
	c2, _ := linkUp(t, s, "alpha")
	defer c2.CloseNow()
	pollCount(t, h, 2)
	got := h.Runners()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "bravo" {
		t.Fatalf("Runners() = %v, want [alpha bravo]", got)
	}
	if err := c1.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("client close: %s", err)
	}
	pollCount(t, h, 1)
	got = h.Runners()
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("Runners() = %v, want [alpha]", got)
	}
}

// TestOnChangeEvents proves watchers fire after register and after a
// real removal: link, clean close, and a duplicate that fires linked
// again but never unlinked for the retired elder.
func TestOnChangeEvents(t *testing.T) {
	t.Parallel()
	h := calmHub()
	s := linktest.Serve(t, h.Handler())
	type event struct {
		name   string
		linked bool
	}
	var mu sync.Mutex
	var events []event
	h.OnChange(func(name string, linked bool) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event{name, linked})
	})
	want := func(n int) []event {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			got := append([]event(nil), events...)
			mu.Unlock()
			if len(got) >= n {
				return got
			}
			time.Sleep(5 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("events = %v, want at least %d", events, n)
		return nil
	}

	c1, _ := linkUp(t, s, "r1")
	if got := want(1); got[0] != (event{"r1", true}) {
		t.Fatalf("events = %v, want [{r1 true}]", got)
	}
	if err := c1.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("client close: %s", err)
	}
	if got := want(2); got[1] != (event{"r1", false}) {
		t.Fatalf("events = %v, want second {r1 false}", got)
	}

	// Duplicate: the new link fires linked again, the retired elder
	// stays silent because the name never went down.
	old, _ := linkUp(t, s, "dup")
	if got := want(3); got[2] != (event{"dup", true}) {
		t.Fatalf("events = %v, want third {dup true}", got)
	}
	fresh, _ := linkUp(t, s, "dup")
	defer fresh.CloseNow()
	pollCount(t, h, 1)
	// The elder reads its replacement notice.
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_, _, _ = old.Read(ctx)
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 4 {
		t.Fatalf("events = %v, want exactly 4 (no unlink for the replaced elder)", events)
	}
	if events[3] != (event{"dup", true}) {
		t.Fatalf("events = %v, want fourth {dup true}", events)
	}
}

// TestOnChangeNeverBlocksReads proves a slow watcher never stalls
// another link's read loop: while the "slow" link's watcher still
// sleeps, frames on the "fast" link dispatch and the "fast" link
// registers. Watchers run on the hub's delivery goroutine only.
func TestOnChangeNeverBlocksReads(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	h.OnChange(func(name string, linked bool) {
		if name == "slow" && linked {
			time.Sleep(8 * time.Second)
		}
	})
	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		c, _ := linkUp(t, s, "slow")
		c.CloseNow()
	}()
	c2, _ := linkUp(t, s, "fast")
	defer c2.CloseNow()

	got := make(chan link.Frame, 1)
	h.On("term.write", func(_ context.Context, _ string, f link.Frame) { got <- f })
	payload, err := link.Encode(link.Frame{V: 1, Type: "term.write", ID: "a", Data: json.RawMessage(`{"n":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	// The fast link's frames dispatch far sooner than the slow
	// watcher's 8 s nap ends: reads never wait on watchers.
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := c2.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write frame: %s", err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never fired while a watcher slept")
	}
	<-slowDone
}

// TestOnChangeKeepsOrder proves events arrive in the order the link
// map changed. The watcher stalls on "unlinked", standing in for a
// goroutine descheduled mid-notify: a relink right behind the unlink
// must still be reported last, or the relay would treat a live runner
// as offline.
func TestOnChangeKeepsOrder(t *testing.T) {
	t.Parallel()
	h := calmHub()
	s := linktest.Serve(t, h.Handler())
	var mu sync.Mutex
	var events []bool
	h.OnChange(func(name string, linked bool) {
		if !linked {
			time.Sleep(300 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		events = append(events, linked)
	})
	c1, _ := linkUp(t, s, "r")
	pollCount(t, h, 1)
	if err := c1.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("client close: %s", err)
	}
	pollCount(t, h, 0)
	c2, _ := linkUp(t, s, "r")
	defer c2.CloseNow()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := append([]bool(nil), events...)
		mu.Unlock()
		if len(got) == 3 {
			if !got[0] || got[1] || !got[2] {
				t.Fatalf("events = %v, want [true false true]", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("events = %v, want 3", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
