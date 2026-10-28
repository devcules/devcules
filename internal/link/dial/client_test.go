package dial_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
)

const testToken = "test-runner-token-no-whitespace"

func testOptions(log *slog.Logger, hc *http.Client) dial.Options {
	return dial.Options{
		PingInterval: 30 * time.Millisecond,
		PingTimeout:  100 * time.Millisecond,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		HTTPClient:   hc,
		Logger:       log,
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// syncBuffer is a goroutine-safe bytes.Buffer: dial logs from its
// connection goroutines while the test polls the output, so an
// unguarded Buffer races under -race.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// pollLinked waits for the client's link state. Polling with a
// deadline (not fixed sleeps) keeps timing tests deterministic.
func pollLinked(t *testing.T, c *dial.Client, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c.Linked() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Linked() = %v, want %v", c.Linked(), want)
}

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

func testHub() *hub.Hub {
	return hub.New(hub.HashToken(testToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: 30 * time.Millisecond,
		PingTimeout:  100 * time.Millisecond,
	})
}

func runClient(t *testing.T, c *dial.Client) context.CancelFunc {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v, want nil", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return stop
}

func TestLinkLifecycle(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	log := discardLogger()
	c := dial.New("ws://pipe/link", "r1", "test", testToken, testOptions(log, s.Client()))
	stop := runClient(t, c)
	defer stop()

	pollLinked(t, c, true)
	pollCount(t, h, 1)
	if c.Session() == "" {
		t.Fatal("Session() is empty on a live link")
	}

	// Frames flow serve -> runner through the registered handler.
	got := make(chan link.Frame, 1)
	c.On("term.write", func(_ context.Context, f link.Frame) { got <- f })
	sent := link.Frame{V: 1, Type: "term.write", ID: "w1", Data: json.RawMessage(`{}`)}
	if err := h.SendTo("r1", sent); err != nil {
		t.Fatalf("hub SendTo: %s", err)
	}
	select {
	case f := <-got:
		if f.ID != "w1" {
			t.Fatalf("handler got %+v", f)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner handler never fired")
	}

	// …and runner -> serve.
	back := make(chan string, 1)
	h.On("term.ack", func(_ context.Context, runner string, f link.Frame) {
		if runner != "r1" {
			t.Errorf("hub handler runner = %q, want r1", runner)
		}
		back <- f.ID
	})
	ack := link.Frame{V: 1, Type: "term.ack", ID: "a1", Data: json.RawMessage(`{}`)}
	if err := c.Send(ack); err != nil {
		t.Fatalf("client Send: %s", err)
	}
	select {
	case id := <-back:
		if id != "a1" {
			t.Fatalf("hub handler got id %q, want a1", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hub handler never fired")
	}

	// Cancelling ends Run cleanly and the hub retires the link.
	stop()
	pollLinked(t, c, false)
	pollCount(t, h, 0)
}

func TestBadTokenBacksOff(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	c := dial.New("ws://pipe/link", "r1", "test", "wrong-token", dial.Options{
		PingInterval: 30 * time.Millisecond,
		PingTimeout:  100 * time.Millisecond,
		MinBackoff:   20 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		HTTPClient:   s.Client(),
		Logger:       log,
	})
	stop := runClient(t, c)
	defer stop()

	// The 401 message names the fix…
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "runner token rejected: check DEVCULES_RUNNER_TOKEN_FILE") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "runner token rejected: check DEVCULES_RUNNER_TOKEN_FILE") {
		t.Fatalf("logs = %q, want the token rejection with next steps", logs.String())
	}
	// …and the token never appears in any log line.
	if strings.Contains(logs.String(), "wrong-token") {
		t.Fatalf("logs leak the token: %q", logs.String())
	}
	pollCount(t, h, 0)

	// Retries happen, but paced: far below one attempt per
	// millisecond, far above zero. Hammering would show thousands.
	time.Sleep(300 * time.Millisecond)
	n := c.Attempts()
	if n < 2 || n > 30 {
		t.Fatalf("Attempts() = %d over ~300 ms, want paced retries (2-30)", n)
	}
	if c.Linked() {
		t.Fatal("Linked() with a bad token")
	}
}

func TestServeRestartRelinks(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())
	c := dial.New("ws://pipe/link", "r1", "test", testToken, testOptions(discardLogger(), s.Client()))
	stop := runClient(t, c)
	defer stop()

	pollLinked(t, c, true)
	first := c.Session()

	// A new hub is a restarted serve: new links, new sessions.
	h2 := testHub()
	s.Restart(h2.Handler())
	pollCount(t, h2, 1)
	pollLinked(t, c, true)
	if c.Session() == "" || c.Session() == first {
		t.Fatalf("Session() = %q, want a fresh session after restart", c.Session())
	}
}

// TestBackoffResetsAfterWelcome proves Run restarts the backoff streak
// on every welcome: after failures grow the ceiling, one link resets
// the next sleep to [0, min]. max is huge so an unreset streak could
// almost never draw this small (50ms in 30s); the exact ceiling math
// lives in the white-box backoff_test.go.
func TestBackoffResetsAfterWelcome(t *testing.T) {
	t.Parallel()
	dead := linktest.Serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := dial.New("ws://pipe/link", "r1", "test", testToken, dial.Options{
		PingInterval: 30 * time.Millisecond,
		PingTimeout:  100 * time.Millisecond,
		MinBackoff:   50 * time.Millisecond,
		MaxBackoff:   30 * time.Second,
		HTTPClient:   dead.Client(),
		Logger:       log,
	})
	stop := runClient(t, c)
	defer stop()

	// Fail long enough to grow the streak past min several times over.
	time.Sleep(500 * time.Millisecond)
	if n := c.Attempts(); n < 3 {
		t.Fatalf("Attempts() = %d, want a grown backoff streak", n)
	}

	// One welcome resets the streak…
	h := testHub()
	dead.Restart(h.Handler())
	pollLinked(t, c, true)
	// …so the sleep after the next drop draws from min again. Drop by
	// restarting onto the dead handler, then read the logged wait.
	mark := len(logs.String())
	dead.Restart(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	pollLinked(t, c, false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		wait := firstBackoffWait(logs.String()[mark:])
		if wait >= 0 {
			if wait > 50*time.Millisecond {
				t.Fatalf("post-welcome backoff = %s, want within [0, 50ms]", wait)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no backoff logged after the drop")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// firstBackoffWait parses the first "link down, backing off" wait in
// the given logs, or -1 when none is logged yet. The first sleep after
// the drop is the reset draw; later lines belong to later streaks (a
// fast redial can already be backing off again before the poll below
// observes the drop), so only the first line proves the reset.
func firstBackoffWait(logs string) time.Duration {
	idx := strings.Index(logs, "link down, backing off")
	if idx < 0 {
		return -1
	}
	line := logs[idx:]
	start := strings.Index(line, "wait=")
	if start < 0 {
		return -1
	}
	rest := line[start+len("wait="):]
	end := strings.IndexAny(rest, " \n")
	if end > 0 {
		rest = rest[:end]
	}
	d, err := time.ParseDuration(strings.TrimSpace(rest))
	if err != nil {
		return -1
	}
	return d
}

func TestSendNotLinked(t *testing.T) {
	t.Parallel()
	c := dial.New("ws://pipe/link", "r1", "test", testToken, testOptions(discardLogger(), nil))
	f := link.Frame{V: 1, Type: "a", ID: "1", Data: json.RawMessage(`{}`)}
	if err := c.Send(f); !errors.Is(err, dial.ErrNotLinked) {
		t.Fatalf("Send down = %v, want ErrNotLinked", err)
	}
	if err := c.Send(link.Frame{}); err == nil {
		t.Fatal("Send invalid frame succeeded")
	}
}

func TestOnRejectsBadType(t *testing.T) {
	t.Parallel()
	c := dial.New("ws://pipe/link", "r1", "test", testToken, testOptions(discardLogger(), nil))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("On(bad-type) did not panic")
		}
	}()
	c.On("Bad Type!", func(_ context.Context, _ link.Frame) {})
}

// rawPipe is a rendezvous listener for the frame-level test server:
// Accept blocks until dialRaw creates the next pipe pair.
type rawPipe struct {
	pipes chan net.Conn
	done  chan struct{}
}

func (l *rawPipe) Accept() (net.Conn, error) {
	select {
	case c := <-l.pipes:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *rawPipe) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}
func (l *rawPipe) Addr() net.Addr { return rawAddr{} }

type rawAddr struct{}

func (rawAddr) Network() string { return "pipe" }
func (rawAddr) String() string  { return "pipe" }

// rawServer speaks just enough WebSocket to test the dialer against a
// peer the hub cannot simulate: it can ignore pings (a true missed
// pong) and it records the runner's close code. Frame sizes stay under
// 126 bytes by construction.
type rawServer struct {
	t     *testing.T
	ln    *rawPipe
	srv   *http.Server
	mu    sync.Mutex
	conns int
	codes []int
	pong  bool
}

func newRawServer(t *testing.T, pong bool) *rawServer {
	t.Helper()
	s := &rawServer{t: t, ln: &rawPipe{pipes: make(chan net.Conn), done: make(chan struct{})}, pong: pong}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.upgrade)}
	go s.srv.Serve(s.ln)
	t.Cleanup(func() { s.srv.Close(); s.ln.Close() })
	return s
}

// dialRawClient returns an http.Client whose transport dials s.
func (s *rawServer) dialRawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		select {
		case s.ln.pipes <- server:
			return client, nil
		case <-s.ln.done:
			client.Close()
			server.Close()
			return nil, net.ErrClosed
		}
	}}}
}

func (s *rawServer) upgrade(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	accept := base64.StdEncoding.EncodeToString(sha1sum(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	_ = rw.Flush()
	s.mu.Lock()
	s.conns++
	n := s.conns
	s.mu.Unlock()
	s.t.Logf("raw server conn #%d", n)
	s.pump(conn)
}

func sha1sum(str string) []byte {
	h := sha1.New()
	h.Write([]byte(str))
	return h.Sum(nil)
}

// readFrame parses one masked client frame. Test frames are small, so
// extended lengths are rejected rather than handled.
func readFrame(r *bufio.Reader) (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = readFull(r, hdr); err != nil {
		return 0, nil, err
	}
	if hdr[1]&0x80 == 0 {
		return 0, nil, fmt.Errorf("client frame not masked")
	}
	n := int(hdr[1] & 0x7f)
	if n > 125 {
		return 0, nil, fmt.Errorf("frame too large for the test server")
	}
	mask := make([]byte, 4)
	if _, err = readFull(r, mask); err != nil {
		return 0, nil, err
	}
	payload = make([]byte, n)
	if _, err = readFull(r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return hdr[0] & 0x0f, payload, nil
}

func readFull(r *bufio.Reader, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := r.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func writeFrame(w *bufio.ReadWriter, opcode byte, payload []byte) error {
	if _, err := w.Write([]byte{0x80 | opcode, byte(len(payload))}); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func (s *rawServer) pump(conn net.Conn) {
	r := bufio.NewReader(conn)
	rw := bufio.NewReadWriter(r, bufio.NewWriter(conn))
	for {
		op, payload, err := readFrame(r)
		s.t.Logf("raw server frame op=%x n=%d err=%v", op, len(payload), err)
		if err != nil {
			return
		}
		switch op {
		case 0x1:
			var f link.Frame
			if err := json.Unmarshal(payload, &f); err != nil || f.Type != "hello" {
				return
			}
			welcome, err := link.Encode(link.Frame{V: 1, Type: "welcome", ID: link.NewID(), Data: sessionData()})
			if err != nil {
				return
			}
			if err := writeFrame(rw, 0x1, welcome); err != nil {
				return
			}
		case 0x9:
			// Pings are answered only when the test wants a live
			// peer; ignoring them simulates missed pongs.
			if s.pong {
				if err := writeFrame(rw, 0xa, nil); err != nil {
					return
				}
			}
		case 0x8:
			code := 0
			if len(payload) >= 2 {
				code = int(binary.BigEndian.Uint16(payload[:2]))
			}
			s.mu.Lock()
			s.codes = append(s.codes, code)
			s.mu.Unlock()
			_ = writeFrame(rw, 0x8, payload[:min(2, len(payload))])
			return
		}
	}
}

func sessionData() json.RawMessage {
	b, _ := json.Marshal(link.Welcome{Session: link.NewSession()})
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *rawServer) closeCodes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.codes...)
}

func (s *rawServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func TestRunnerShutdownSends1001(t *testing.T) {
	t.Parallel()
	s := newRawServer(t, true /* pong: live peer */)
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := dial.New("ws://pipe/link", "r1", "test", testToken, dial.Options{
		PingInterval: 30 * time.Second, // calm: no ping may race the shutdown close
		PingTimeout:  5 * time.Second,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		HTTPClient:   s.dialRawClient(),
		Logger:       log,
	})
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	pollLinked(t, c, true)
	t.Logf("linked, conns=%d attempts=%d", s.connections(), c.Attempts())
	stop()
	t.Logf("cancelled")
	select {
	case err := <-done:
		t.Logf("Run returned (conns=%d)", s.connections())
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if codes := s.closeCodes(); len(codes) > 0 {
			if codes[0] != 1001 {
				t.Fatalf("runner close code = %d, want 1001", codes[0])
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("dial logs:\n%s", logs.String())
	t.Fatal("server never saw the runner's close")
}

func TestMissedPongsDropAndRedial(t *testing.T) {
	t.Parallel()
	s := newRawServer(t, false /* never pong */)
	c := dial.New("ws://pipe/link", "r1", "test", testToken, dial.Options{
		PingInterval: 30 * time.Millisecond,
		PingTimeout:  50 * time.Millisecond,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		HTTPClient:   s.dialRawClient(),
		Logger:       discardLogger(),
	})
	stop := runClient(t, c)
	defer stop()

	// Two consecutive missed pongs drop the link…
	pollLinked(t, c, true)
	pollLinked(t, c, false)
	// …and the dialer comes back: the raw server takes the next
	// connection too, so attempts keep landing.
	pollLinked(t, c, true)
	if n := s.connections(); n < 2 {
		t.Fatalf("server connections = %d, want at least 2 (initial + redial)", n)
	}
	if c.Attempts() < 2 {
		t.Fatalf("Attempts() = %d, want at least 2", c.Attempts())
	}
}

// TestLargeFrameFromServe proves the client lifts the library's 32 KiB
// default read limit to the link's frame limit: terminal output in
// M1.4 arrives in frames well above 32 KiB.
func TestLargeFrameFromServe(t *testing.T) {
	t.Parallel()
	// SendTo's write timeout is the hub's ping timeout; the shared
	// millisecond test hub is too tight for 200 KiB through a pipe
	// under -race, so this hub keeps production-like timing.
	h := hub.New(hub.HashToken(testToken), hub.Options{PingInterval: time.Second, PingTimeout: 5 * time.Second})
	s := linktest.Serve(t, h.Handler())
	c := dial.New("ws://pipe/link", "big", "test", testToken, testOptions(discardLogger(), s.Client()))
	got := make(chan int, 1)
	c.On("test.big", func(_ context.Context, f link.Frame) { got <- len(f.Data) })
	runClient(t, c)
	pollCount(t, h, 1)

	payload, _ := json.Marshal(map[string]string{"blob": strings.Repeat("x", 200<<10)})
	if err := h.SendTo("big", link.Frame{V: link.Version, Type: "test.big", ID: link.NewID(), Data: payload}); err != nil {
		t.Fatalf("SendTo: %v", err)
	}
	select {
	case n := <-got:
		if n != len(payload) {
			t.Fatalf("handler saw %d data bytes, want %d", n, len(payload))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a 200 KiB frame never reached the handler")
	}
	if !c.Linked() {
		t.Fatal("link dropped after a large frame")
	}
}

// TestReplacedRunnerStops proves a runner retired by a same-name
// duplicate (close 4000) stops with ErrReplaced instead of redialling:
// two runners sharing a name would otherwise evict each other forever.
func TestReplacedRunnerStops(t *testing.T) {
	t.Parallel()
	h := testHub()
	s := linktest.Serve(t, h.Handler())

	elder := dial.New("ws://pipe/link", "same", "test", testToken, testOptions(discardLogger(), s.Client()))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- elder.Run(ctx) }()
	pollLinked(t, elder, true)

	younger := dial.New("ws://pipe/link", "same", "test", testToken, testOptions(discardLogger(), s.Client()))
	runClient(t, younger)

	select {
	case err := <-done:
		if !errors.Is(err, dial.ErrReplaced) {
			t.Fatalf("elder Run = %v, want ErrReplaced", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("elder kept running after being replaced")
	}
	pollLinked(t, younger, true)
	pollCount(t, h, 1)
	before := younger.Attempts()
	time.Sleep(200 * time.Millisecond) // several backoff periods
	if younger.Attempts() != before || !younger.Linked() {
		t.Fatalf("younger relinked (%d -> %d attempts): the pair is flapping", before, younger.Attempts())
	}
}
