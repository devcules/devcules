package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/pty"
)

const relayTestToken = "relay-test-token-0123456789abcdef"

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// fixture wires a real hub to a pipe server plus a relay watching it.
// The /t endpoint is the test's stand-in for the future REST layer: it
// accepts the browser WebSocket and hands it to AttachStream. Query
// params carry runner, term, cols and rows.
func testHub() *hub.Hub {
	return hub.New(hub.HashToken(relayTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
	})
}

// testHubCalm skips fast heartbeats: a multi-megabyte flood must trip
// the browser queue, not the 100 ms pong deadline (production pings
// every 15 s, so this mirrors real timing better than testHub).
func testHubCalm() *hub.Hub {
	return hub.New(hub.HashToken(relayTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: 30 * time.Second,
		PingTimeout:  30 * time.Second,
	})
}

func fixture(t *testing.T, h *hub.Hub, r *Relay) *linktest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/link", h.Handler())
	mux.HandleFunc("/t", func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, r.AcceptOptions(req))
		if err != nil {
			return
		}
		q := req.URL.Query()
		cols, rows := 80, 24
		if n, err := strconv.Atoi(q.Get("cols")); err != nil {
			cols = -1
		} else {
			cols = n
		}
		if n, err := strconv.Atoi(q.Get("rows")); err != nil {
			rows = -1
		} else {
			rows = n
		}
		runner, rerr := r.Resolve(q.Get("runner"), q.Get("term"), cols, rows)
		if rerr != nil {
			_ = conn.Close(rerr.CloseCode(), rerr.Msg)
			return
		}
		r.AttachStream(req.Context(), runner, q.Get("term"), cols, rows, conn)
	})
	return linktest.Serve(t, mux)
}

func runDialer(t *testing.T, s *linktest.Server, ctx context.Context, name string) (*dial.Client, context.CancelFunc) {
	t.Helper()
	return runDialerCalm(t, s, ctx, name, false)
}

// runDialerCalm with calm set pings every 30 s. Floods need it: the
// linktest pipe is unbuffered, so when both ends ping at once each
// reader blocks writing its pong to the other, and the busy link dies
// of missed pongs. Real TCP buffers absorb those few bytes.
func runDialerCalm(t *testing.T, s *linktest.Server, ctx context.Context, name string, calm bool) (*dial.Client, context.CancelFunc) {
	t.Helper()
	child, stop := context.WithCancel(ctx)
	pingInterval, pingTimeout := time.Second, 5*time.Second
	if calm {
		pingInterval, pingTimeout = 30*time.Second, 30*time.Second
	}
	c := dial.New("ws://pipe/link", name, "test", relayTestToken, dial.Options{
		PingInterval: pingInterval,
		PingTimeout:  pingTimeout,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   s.Client(),
		Logger:       discardLogger(),
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

func startRelay(t *testing.T, h *hub.Hub) *Relay {
	t.Helper()
	r := New(h, "")
	r.Run()
	return r
}

// dialBrowser opens a browser stream through the /t stand-in. An empty
// runner selects the only linked runner, mirroring the REST layer.
func dialBrowser(t *testing.T, s *linktest.Server, ctx context.Context, runner, term string) *websocket.Conn {
	t.Helper()
	url := "ws://pipe/t?term=" + term + "&cols=80&rows=24"
	if runner != "" {
		url += "&runner=" + runner
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: s.Client()})
	if err != nil {
		t.Fatalf("browser dial: %s", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	// Real browsers have no message limit; output batches can pass
	// the library's 32 KiB default.
	conn.SetReadLimit(-1)
	return conn
}

func waitLinked(t *testing.T, h *hub.Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.Runners()) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("linked runners = %d, want %d", len(h.Runners()), want)
}

// readUntil collects browser messages until want appears or the deadline
// passes. Control frames are skipped; everything else accumulates.
func readUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, want string, timeout time.Duration) string {
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
			return sb.String()
		}
	}
	t.Fatalf("never saw %q (got %q)", want, sb.String())
	return ""
}

// readCloseError waits for the server to close the browser stream and
// returns the status code and reason.
func readCloseError(t *testing.T, ctx context.Context, conn *websocket.Conn, timeout time.Duration) (websocket.StatusCode, string) {
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
			return ce.Code, ce.Reason
		}
		t.Fatalf("browser read error is %T (%s), want CloseError", err, err)
	}
	t.Fatal("browser stream never closed")
	return 0, ""
}

func TestPickRunner(t *testing.T) {
	t.Parallel()
	r := New(testHub(), "")
	if _, err := r.PickRunner(""); err == nil || err.Status != 503 {
		t.Fatalf("no runners: err = %+v, want 503", err)
	}
}

func TestPickRunnerVariants(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHub()
	r := New(h, "")
	s := fixture(t, h, r)
	defer s.Close()

	// Zero linked is 503.
	if _, err := r.PickRunner(""); err == nil || err.Status != 503 {
		t.Fatalf("zero runners: err = %+v, want 503", err)
	}

	c1, stop1 := runDialer(t, s, ctx, "r1")
	defer stop1()
	_ = c1
	waitLinked(t, h, 1)

	// One linked runner wins regardless of the query.
	if name, err := r.PickRunner(""); err != nil || name != "r1" {
		t.Fatalf("one runner: name = %q, err = %+v", name, err)
	}

	c2, stop2 := runDialer(t, s, ctx, "r2")
	defer stop2()
	_ = c2
	waitLinked(t, h, 2)

	// Several linked without a choice is 400.
	if _, err := r.PickRunner(""); err == nil || err.Status != 400 {
		t.Fatalf("ambiguous: err = %+v, want 400", err)
	}
	// An explicit choice resolves; an unknown one is 404.
	if name, err := r.PickRunner("r2"); err != nil || name != "r2" {
		t.Fatalf("choice: name = %q, err = %+v", name, err)
	}
	if _, err := r.PickRunner("nope"); err == nil || err.Status != 404 {
		t.Fatalf("unknown: err = %+v, want 404", err)
	}
}

func TestOpenTimeoutIs504(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()

	// A linked runner with no terminal handlers: the request is never
	// answered, so the relay must give up with 504, not hang.
	c, stopDial := runDialer(t, s, ctx, "silent")
	defer stopDial()
	_ = c
	waitLinked(t, h, 1)

	if _, err := r.Open(ctx, "silent"); err == nil || err.Status != 504 {
		t.Fatalf("Open with silent runner: err = %+v, want 504", err)
	}
}

func TestDeleteAndListWithoutRunner(t *testing.T) {
	t.Parallel()
	h := testHub()
	r := New(h, "")

	if _, err := r.List(context.Background(), ""); err == nil || err.Status != 503 {
		t.Fatalf("List with no runner: err = %+v, want 503", err)
	}
	if err := r.Delete(context.Background(), "", "0123456789abcdef"); err == nil || err.Status != 503 {
		t.Fatalf("Delete with no runner: err = %+v, want 503", err)
	}
	if err := r.Delete(context.Background(), "", "bogus"); err == nil || err.Status != 400 {
		t.Fatalf("Delete with bad id: err = %+v, want 400 even before picking a runner", err)
	}
}

func withTmux(t *testing.T, ctx context.Context, s *linktest.Server, name string) (*pty.Manager, context.CancelFunc) {
	t.Helper()
	return withTmuxCalm(t, ctx, s, name, false)
}

func withTmuxCalm(t *testing.T, ctx context.Context, s *linktest.Server, name string, calm bool) (*pty.Manager, context.CancelFunc) {
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
	c, stop := runDialerCalm(t, s, ctx, name, calm)
	pty.Serve(ctx, c, m, nil)
	return m, stop
}

func TestRelayFullRoundTrip(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()

	m, stopDial := withTmux(t, ctx, s, "r1")
	defer stopDial()
	_ = m
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}

	// The list comes from the runner, so a serve restart loses nothing.
	listed, rerr := r.List(ctx, "r1")
	if rerr != nil {
		t.Fatalf("List: %+v", rerr)
	}
	if len(listed) != 1 || listed[0].Term != termID {
		t.Fatalf("List = %+v, want the opened terminal", listed)
	}

	conn := dialBrowser(t, s, ctx, "", termID)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo hi-$((40+2))\n")); err != nil {
		t.Fatalf("browser write: %s", err)
	}
	readUntil(t, ctx, conn, "hi-42", 15*time.Second)

	// Reload resumes: a new stream redraws the earlier output from tmux.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	conn2 := dialBrowser(t, s, ctx, "", termID)
	readUntil(t, ctx, conn2, "hi-42", 15*time.Second)

	if err := r.Delete(ctx, "r1", termID); err != nil {
		t.Fatalf("Delete: %+v", err)
	}
	listed, rerr = r.List(ctx, "r1")
	if rerr != nil {
		t.Fatalf("List after delete: %+v", rerr)
	}
	if len(listed) != 0 {
		t.Fatalf("List after delete = %+v, want empty", listed)
	}
}

func TestSlowBrowserGets1013(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHubCalm()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()

	_, stopDial := withTmuxCalm(t, ctx, s, "r1", true)
	defer stopDial()
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}
	t.Cleanup(func() { _ = r.Delete(ctx, "r1", termID) })

	// Never read: the queue must fill, the browser must be cut off with
	// 1013, and a fresh attach must work afterwards (tmux redraws).
	conn := dialBrowser(t, s, ctx, "", termID)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("yes | head -c 9000000 | tr '\\0' A\n")); err != nil {
		t.Fatalf("browser write: %s", err)
	}
	// Don't read until the relay gives up on this browser: the
	// unbuffered pipe stalls the relay's writer at once, so the queue
	// fills and the stream is dropped.
	deadline := time.Now().Add(20 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.streams)
		r.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay never dropped the slow browser")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The 1013 frame queues behind the write that is stuck on this
	// browser, so a browser that stopped reading entirely may only see
	// its socket drop. Either way it must not see a different code.
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(rctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if errors.As(err, &ce) && ce.Code != websocket.StatusCode(1013) {
			t.Fatalf("slow close = %d %q, want 1013 or a dropped socket", ce.Code, ce.Reason)
		}
		if rctx.Err() != nil {
			t.Fatal("slow browser was never cut off")
		}
		break
	}

	conn2 := dialBrowser(t, s, ctx, "", termID)
	if err := conn2.Write(ctx, websocket.MessageBinary, []byte("echo back\n")); err != nil {
		t.Fatalf("reattach write: %s", err)
	}
	readUntil(t, ctx, conn2, "back", 15*time.Second)
}

func TestRunnerOfflineCloses1012(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()

	_, stopDial := withTmux(t, ctx, s, "r1")
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}

	conn := dialBrowser(t, s, ctx, "", termID)

	// Killing the link must retire the stream with 1012, not hang it.
	stopDial()
	code, reason := readCloseError(t, ctx, conn, 15*time.Second)
	if code != websocket.StatusCode(1012) {
		t.Fatalf("offline close = %d %q, want 1012", code, reason)
	}

	if _, err := r.List(ctx, ""); err == nil || err.Status != 503 {
		t.Fatalf("List after unlink: err = %+v, want 503", err)
	}
}

func TestReplacedRunnerClosesOldStreams(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing in CI: the unit job must install it")
		}
		t.Skip("tmux not installed")
	}
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()

	_, stopOld := withTmux(t, ctx, s, "r1")
	defer stopOld()
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}
	conn := dialBrowser(t, s, ctx, "", termID)

	// A second runner with the same name fires linked with no unlinked;
	// the old attach clients belonged to the dead link, so the relay
	// retires their streams with 1012 too.
	_, stopNew := withTmux(t, ctx, s, "r1")
	defer stopNew()

	code, reason := readCloseError(t, ctx, conn, 15*time.Second)
	if code != websocket.StatusCode(1012) {
		t.Fatalf("replaced close = %d %q, want 1012", code, reason)
	}
}

func TestResolveValidation(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	h := testHub()
	r := New(h, "")
	s := fixture(t, h, r)
	defer s.Close()
	_, stopDial := runDialer(t, s, ctx, "r1")
	defer stopDial()
	waitLinked(t, h, 1)

	const goodTerm = "0123456789abcdef"
	for _, tc := range []struct {
		name       string
		runner     string
		term       string
		cols, rows int
		wantStatus int
	}{
		{"ok", "", goodTerm, 80, 24, 0},
		{"ok explicit runner", "r1", goodTerm, 80, 24, 0},
		{"bad term id", "", "bogus", 80, 24, 404},
		{"bad cols", "", goodTerm, 0, 24, 400},
		{"bad rows", "", goodTerm, 80, 501, 400},
		{"unknown runner", "nope", goodTerm, 80, 24, 404},
	} {
		// Synchronous: parallel subtests would outlive the dialer
		// context cancelled by the parent's defer.
		t.Run(tc.name, func(t *testing.T) {
			name, err := r.Resolve(tc.runner, tc.term, tc.cols, tc.rows)
			if tc.wantStatus == 0 {
				if err != nil || name != "r1" {
					t.Fatalf("Resolve = %q, %+v; want r1, nil", name, err)
				}
				return
			}
			if err == nil || err.Status != tc.wantStatus {
				t.Fatalf("Resolve err = %+v, want status %d", err, tc.wantStatus)
			}
		})
	}

	// The stream endpoint turns Resolve failures into a 4404 close.
	conn, _, err := websocket.Dial(ctx, "ws://pipe/t?term=bogus&cols=80&rows=24",
		&websocket.DialOptions{HTTPClient: s.Client()})
	if err != nil {
		t.Fatalf("browser dial: %s", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	code, _ := readCloseError(t, ctx, conn, 10*time.Second)
	if code != websocket.StatusCode(4404) {
		t.Fatalf("bad-term close = %d, want 4404", code)
	}
}

// fakeRunner answers term.* frames without tmux: open/attach/resize/
// detach/close say ok, list is empty, and input echoes back as
// output. A "FLOOD" line triggers 300 full-size output frames for the
// slow-browser test. It reports what it saw for assertions.
type fakeRunner struct {
	c        *dial.Client
	attached chan string // attach stream ids, in order
	detached chan string // detach stream ids, in order
	inputs   chan string // decoded input bytes
}

func answerOK(c *dial.Client, req link.Frame) {
	c.Send(link.Frame{V: link.Version, Type: terminals.TypeOK, ID: req.ID, Data: json.RawMessage(`{}`)})
}

func runFakeRunner(t *testing.T, s *linktest.Server, ctx context.Context, name string) (*fakeRunner, context.CancelFunc) {
	t.Helper()
	return runFakeRunnerCalm(t, s, ctx, name, false)
}

func runFakeRunnerCalm(t *testing.T, s *linktest.Server, ctx context.Context, name string, calm bool) (*fakeRunner, context.CancelFunc) {
	t.Helper()
	child, stop := context.WithCancel(ctx)
	pingInterval := 30 * time.Millisecond
	pingTimeout := 100 * time.Millisecond
	if calm {
		pingInterval = 30 * time.Second
		pingTimeout = 30 * time.Second
	}
	c := dial.New("ws://pipe/link", name, "test", relayTestToken, dial.Options{
		PingInterval: pingInterval,
		PingTimeout:  pingTimeout,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   s.Client(),
		Logger:       discardLogger(),
	})
	f := &fakeRunner{c: c, attached: make(chan string, 16), detached: make(chan string, 16), inputs: make(chan string, 64)}
	c.On(terminals.TypeOpen, func(_ context.Context, req link.Frame) { answerOK(c, req) })
	c.On(terminals.TypeAttach, func(_ context.Context, req link.Frame) {
		var a terminals.Attach
		if err := json.Unmarshal(req.Data, &a); err == nil {
			f.attached <- a.Stream
		}
		answerOK(c, req)
	})
	c.On(terminals.TypeInput, func(_ context.Context, req link.Frame) {
		var in terminals.Input
		if err := json.Unmarshal(req.Data, &in); err != nil {
			return
		}
		raw, err := base64.StdEncoding.DecodeString(in.B64)
		if err != nil {
			return
		}
		f.inputs <- string(raw)
		if string(raw) == "FLOOD\n" {
			// A stuck client: 300 full batches blow both queue
			// ceilings, so the relay must cut it off with 1013.
			go func() {
				big, _ := json.Marshal(terminals.Output{Stream: in.Stream, B64: base64.StdEncoding.EncodeToString(make([]byte, 32<<10))})
				for i := 0; i < 300; i++ {
					c.Send(link.Frame{V: link.Version, Type: terminals.TypeOutput, ID: "flood", Data: big})
				}
			}()
			return
		}
		// Input and Output share the wire shape; convert, don't rebuild.
		data, _ := json.Marshal(terminals.Output(in))
		c.Send(link.Frame{V: link.Version, Type: terminals.TypeOutput, ID: req.ID, Data: data})
	})
	c.On(terminals.TypeResize, func(_ context.Context, req link.Frame) { answerOK(c, req) })
	c.On(terminals.TypeDetach, func(_ context.Context, req link.Frame) {
		var d terminals.Detach
		if err := json.Unmarshal(req.Data, &d); err == nil {
			f.detached <- d.Stream
		}
		answerOK(c, req)
	})
	c.On(terminals.TypeClose, func(_ context.Context, req link.Frame) { answerOK(c, req) })
	c.On(terminals.TypeList, func(_ context.Context, req link.Frame) {
		data, _ := json.Marshal(terminals.Listed{})
		c.Send(link.Frame{V: link.Version, Type: terminals.TypeListed, ID: req.ID, Data: data})
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("fake runner Run did not return")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for !c.Linked() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Linked() {
		t.Fatalf("fake runner %s never linked", name)
	}
	return f, stop
}

func TestFakeEchoRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	f, stopFake := runFakeRunner(t, s, ctx, "r1")
	defer stopFake()
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}
	listed, rerr := r.List(ctx, "r1")
	if rerr != nil || len(listed) != 0 {
		t.Fatalf("List = %+v, %+v; want empty", listed, rerr)
	}

	conn := dialBrowser(t, s, ctx, "", termID)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello\n")); err != nil {
		t.Fatalf("browser write: %s", err)
	}
	readUntil(t, ctx, conn, "hello", 10*time.Second)

	// Leaving closes the browser side; the relay detaches the attach
	// client best-effort.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-f.detached:
	case <-time.After(10 * time.Second):
		t.Fatal("relay never detached the closed stream")
	}
}

func TestFakeSlowBrowserGets1013(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHubCalm()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	f, stopFake := runFakeRunnerCalm(t, s, ctx, "r1", true)
	defer stopFake()
	_ = f
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}

	// Never read: 300 full batches must trip the queue ceiling and
	// cut the browser off with 1013. Reading promptly would let the
	// writer keep up (a fast reader is not a slow browser), so the
	// test waits for the cutoff first: overflow forgets the stream,
	// which the test observes before its first read.
	conn := dialBrowser(t, s, ctx, "", termID)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("FLOOD\n")); err != nil {
		t.Fatalf("browser write: %s", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		r.mu.Lock()
		left := len(r.streams)
		r.mu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow browser never cut off")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code, _ := readCloseError(t, ctx, conn, 15*time.Second); code != websocket.StatusCode(1013) {
		t.Fatalf("slow close = %d, want 1013", code)
	}

	// A fresh attach works straight after the cutoff.
	conn2 := dialBrowser(t, s, ctx, "", termID)
	if err := conn2.Write(ctx, websocket.MessageBinary, []byte("back\n")); err != nil {
		t.Fatalf("reattach write: %s", err)
	}
	readUntil(t, ctx, conn2, "back", 10*time.Second)
}

func TestFakeExitClosesStream(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHubCalm()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	f, stopFake := runFakeRunnerCalm(t, s, ctx, "r1", true)
	defer stopFake()
	waitLinked(t, h, 1)

	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}
	conn := dialBrowser(t, s, ctx, "", termID)

	// The attach client ending (session killed) ends the stream with
	// a normal close, so the UI reports a closed terminal, not an
	// error.
	var streamID string
	select {
	case streamID = <-f.attached:
	case <-time.After(10 * time.Second):
		t.Fatal("fake never saw the attach")
	}
	data, _ := json.Marshal(terminals.Exit{Stream: streamID})
	f.c.Send(link.Frame{V: link.Version, Type: terminals.TypeExit, ID: "exit1", Data: data})
	if code, _ := readCloseError(t, ctx, conn, 10*time.Second); code != websocket.StatusNormalClosure {
		t.Fatalf("exit close = %d, want normal closure", code)
	}
	_ = termID
}
