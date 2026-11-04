package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/pty"
)

// TestReattachRightAfterRelinkStaysOpen: after a runner relinks, an
// attach a browser opens on the NEW link must survive. The runner used
// to poll its welcome session and drop every attach up to 500 ms after
// the ""→new change, so those fresh attaches got term.exit, the relay
// closed the browser with 1000, and the UI marked a live terminal gone.
func TestReattachRightAfterRelinkStaysOpen(t *testing.T) {
	if !pty.HasTmux() {
		if os.Getenv("CI") == "true" {
			t.Fatal("tmux missing")
		}
		t.Skip("tmux not installed")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHubCalm()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	events := make(chan bool, 64)
	h.OnChange(func(_ string, linked bool) { events <- linked })
	_, stopDial := withTmuxCalm(t, ctx, s, "r1", true)
	defer stopDial()
	waitLinked(t, h, 1)
	<-events // initial link
	termID, rerr := r.Open(ctx, "r1")
	if rerr != nil {
		t.Fatalf("Open: %+v", rerr)
	}

	gone := 0
	const trials = 3
	for i := 0; i < trials; i++ {
		h.Kick("r1", "flap")
		for up := false; !up; {
			select {
			case l := <-events:
				up = l
			case <-time.After(10 * time.Second):
				t.Fatal("runner never relinked")
			}
		}
		conn, _, err := websocket.Dial(ctx, "ws://pipe/t?term="+termID+"&cols=80&rows=24", &websocket.DialOptions{HTTPClient: s.Client()})
		if err != nil {
			t.Fatalf("dial: %s", err)
		}
		conn.SetReadLimit(-1)
		// The old poll fired within 500 ms of the welcome; 1.2 s covers
		// it with margin.
		rctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		var code websocket.StatusCode = -1
		for {
			_, _, err := conn.Read(rctx)
			if err != nil {
				var ce websocket.CloseError
				if errors.As(err, &ce) {
					code = ce.Code
				}
				break
			}
		}
		cancel()
		if code == websocket.StatusNormalClosure {
			gone++
			t.Logf("trial %d: fresh attach closed with 1000", i)
		}
		_ = conn.CloseNow()
	}
	if gone > 0 {
		t.Fatalf("%d/%d fresh attaches right after relink were closed with 1000 (UI shows the live terminal as gone)", gone, trials)
	}
	// The terminal is still alive and attachable.
	ok := dialBrowser(t, s, ctx, "", termID)
	_ = ok.Write(ctx, websocket.MessageBinary, []byte("echo alive-$((1+1))\n"))
	readUntil(t, ctx, ok, "alive-2", 10*time.Second)
}

// TestBrowserTextHeartbeat: the heartbeat must be visible to browser
// JS. Protocol pings are answered natively and never reach onmessage,
// so the relay also sends a {"type":"ping"} text frame each interval.
func TestBrowserTextHeartbeat(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := New(h, "")
	r.pingEvery = 50 * time.Millisecond
	r.Run()
	s := fixture(t, h, r)
	defer s.Close()
	_, stopRunner := runFakeRunner(t, s, ctx, "r1")
	defer stopRunner()
	waitLinked(t, h, 1)

	conn := dialBrowser(t, s, ctx, "", "0123456789abcdef")
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		kind, msg, err := conn.Read(rctx)
		if err != nil {
			t.Fatalf("no text heartbeat within 2 s (40 intervals): %s", err)
		}
		if kind == websocket.MessageText {
			if string(msg) != `{"type":"ping"}` {
				t.Fatalf("text frame = %q, want the ping frame", msg)
			}
			return
		}
	}
}

// b5PonglessBrowser upgrades a raw connection to the /t stand-in and
// drains every byte without ever answering a ping: a browser whose
// pongs never arrive (a congested link, a suspended tab). The returned
// channel closes when the server drops the connection.
func b5PonglessBrowser(t *testing.T, s *linktest.Server, term string) <-chan struct{} {
	t.Helper()
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	req, err := http.NewRequest(http.MethodGet, "http://pipe/t?term="+term+"&cols=80&rows=24", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(key))
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("upgrade: %s", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", resp.StatusCode)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatal("101 body is not a ReadWriteCloser")
	}
	t.Cleanup(func() { _ = rwc.Close() })
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, rwc)
	}()
	return gone
}

// TestWriterFailureClosesStream: when the writer gives up (a pong
// slower than the timeout), the socket must close so the read loop
// ends, the attach client is detached and the stream is forgotten.
// coder's Ping does not close the conn on a pong timeout, so before
// the fix the pane froze: keystrokes still flowed, output never did.
func TestWriterFailureClosesStream(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := New(h, "")
	r.pingEvery = 50 * time.Millisecond
	r.pingTimeout = 100 * time.Millisecond
	r.Run()
	s := fixture(t, h, r)
	defer s.Close()
	fr, stopRunner := runFakeRunner(t, s, ctx, "r1")
	defer stopRunner()
	waitLinked(t, h, 1)

	gone := b5PonglessBrowser(t, s, "0123456789abcdef")
	var stream string
	select {
	case stream = <-fr.attached:
	case <-time.After(5 * time.Second):
		t.Fatal("never attached")
	}
	select {
	case got := <-fr.detached:
		if got != stream {
			t.Fatalf("detached %q, want %q", got, stream)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer failed but the stream was never detached (frozen pane)")
	}
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("writer failed but the browser socket stayed open")
	}
	r.mu.Lock()
	n := len(r.streams)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("streams = %d after the writer failed, want 0", n)
	}
}

// TestOversizeDimsClamp: an attach or resize beyond 1..500 is clamped,
// not refused, matching the web client's clamp.
func TestOversizeDimsClamp(t *testing.T) {
	t.Parallel()
	if c, r := terminals.ClampSize(900, 0); c != 500 || r != 1 {
		t.Fatalf("ClampSize(900, 0) = %d, %d; want 500, 1", c, r)
	}
	if c, r := terminals.ClampSize(80, 24); c != 80 || r != 24 {
		t.Fatalf("ClampSize(80, 24) = %d, %d; want unchanged", c, r)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	fr, stopRunner := runFakeRunner(t, s, ctx, "r1")
	defer stopRunner()
	waitLinked(t, h, 1)

	conn, _, err := websocket.Dial(ctx, "ws://pipe/t?term=0123456789abcdef&cols=80&rows=501",
		&websocket.DialOptions{HTTPClient: s.Client()})
	if err != nil {
		t.Fatalf("dial: %s", err)
	}
	defer func() { _ = conn.CloseNow() }()
	b5WantSize(t, fr, 80, 500)
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":900,"rows":0}`)); err != nil {
		t.Fatalf("resize write: %s", err)
	}
	b5WantSize(t, fr, 500, 1)
}

func b5WantSize(t *testing.T, fr *fakeRunner, cols, rows int) {
	t.Helper()
	select {
	case got := <-fr.sizes:
		if got != [2]int{cols, rows} {
			t.Fatalf("runner got %dx%d, want %dx%d", got[0], got[1], cols, rows)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("runner never got the %dx%d request", cols, rows)
	}
}
