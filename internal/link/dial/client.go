package dial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
)

// Reasons sent on the runner's way out. 1001 marks a clean shutdown;
// serve logs it as a retirement, not a failure.
const reasonShutdown = "runner shutdown"

// ErrNotLinked answers Send when no link is up. The link may be
// between attempts; callers treat this as "try again", never as fatal.
var ErrNotLinked = errors.New("dial: not linked")

// ErrReplaced ends Run when serve retires this link because another
// runner linked with the same name (close 4000). Redialling would
// evict that runner in turn, and the two would flap forever, so the
// process stops and says why.
var ErrReplaced = errors.New("dial: replaced by another runner with the same name: give each runner a unique hostname")

// replacedCode mirrors the hub's close code for a retired duplicate.
const replacedCode = websocket.StatusCode(4000)

// Options tunes heartbeat and backoff. Zero values take the production
// defaults (15 s ping, 5 s ping timeout, 0.5–30 s backoff); tests set
// millisecond values so reconnect runs without sleeping. Role is the
// hello's role: "runner" by default, "machine" for machine helpers
// (the hub keeps the two memberships apart).
type Options struct {
	PingInterval time.Duration
	PingTimeout  time.Duration
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	HTTPClient   *http.Client
	Logger       *slog.Logger
	Role         string
}

func (o Options) withDefaults() Options {
	if o.PingInterval <= 0 {
		o.PingInterval = 15 * time.Second
	}
	if o.PingTimeout <= 0 {
		o.PingTimeout = 5 * time.Second
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = 500 * time.Millisecond
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Role == "" {
		o.Role = "runner"
	}
	return o
}

// HandlerFunc receives one frame from serve. Handlers run one at a
// time in read order; a slow handler back-pressures the link.
type HandlerFunc func(ctx context.Context, f link.Frame)

// Client dials one hub URL as one runner name and stays linked across
// drops. The zero Client is not usable; build one with New. Client is
// safe for concurrent use.
type Client struct {
	url     string
	name    string
	version string
	token   string
	opt     Options

	mu       sync.Mutex
	handlers map[string]HandlerFunc
	conn     *websocket.Conn
	session  string

	attempts     int64 // atomic: total dial attempts, for tests and ops
	rejectLogged atomic.Bool
}

// New returns a Client that links name to the hub at wsURL with the
// plaintext runner token. The token lives only in this struct and the
// Authorization header; it is never logged.
func New(wsURL, name, version, token string, opt Options) *Client {
	return &Client{
		url:      wsURL,
		name:     name,
		version:  version,
		token:    token,
		opt:      opt.withDefaults(),
		handlers: map[string]HandlerFunc{},
	}
}

// On registers the handler for one frame type. Unknown or malformed
// types are programmer errors, so On panics naming them.
func (c *Client) On(msgType string, fn HandlerFunc) {
	if !link.ValidType(msgType) {
		panic("dial: invalid frame type " + msgType)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[msgType] = fn
}

// Linked reports whether a welcome handshake completed and the link is
// up. It flaps during reconnects by design; M1.4 terminal calls check
// it or handle ErrNotLinked from Send.
func (c *Client) Linked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Session returns the current link's welcome session, or "" when down.
// Sessions change on every welcome, so M1.4 state keyed by session
// never mixes link generations.
func (c *Client) Session() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// Attempts returns the total dial attempts so far. Tests use it to
// prove backoff paces retries instead of hammering.
func (c *Client) Attempts() int64 { return atomic.LoadInt64(&c.attempts) }

// Send delivers one frame to serve. The frame must encode cleanly; the
// write carries a timeout so a stuck link cannot wedge the sender.
// Send fails with ErrNotLinked when no link is up.
func (c *Client) Send(f link.Frame) error {
	b, err := link.Encode(f)
	if err != nil {
		return err
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotLinked
	}
	ctx, stop := context.WithTimeout(context.Background(), c.opt.PingTimeout)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("dial: send: %w", err)
	}
	return nil
}

// Run links and relinks until ctx ends, then closes cleanly and
// returns nil. Every drop — serve restart, network cut, missed
// heartbeats — leads back here for another attempt with backoff; only
// the caller's cancellation or a 4000 "replaced" close (ErrReplaced)
// ends the loop. A welcome resets the
// backoff, so a fresh link that later drops starts patient instead of
// inheriting the wait a long outage earned.
func (c *Client) Run(ctx context.Context) error {
	b := backoffer{min: c.opt.MinBackoff, max: c.opt.MaxBackoff}
	for {
		welcomed, code := c.connect(ctx)
		if welcomed {
			b.reset()
		}
		if ctx.Err() != nil {
			return nil
		}
		if code == replacedCode {
			c.opt.Logger.Error("link replaced by another runner with the same name: stopping")
			return ErrReplaced
		}
		sleep := b.next()
		c.opt.Logger.Debug("link down, backing off", "wait", sleep.String())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(sleep):
		}
	}
}

// backoffer paces reconnects: exponential backoff with full jitter.
// The cap doubles per consecutive failure from min to max; a welcome
// resets the streak. Jitter spreads a fleet of restarting runners so
// serve never takes a thundering herd.
type backoffer struct {
	min, max time.Duration
	n        int
}

// cap returns the current sleep ceiling without drawing.
func (b *backoffer) cap() time.Duration {
	n := b.n
	if n > 20 {
		n = 20
	}
	cap := b.min
	for range n {
		cap *= 2
		if cap >= b.max {
			return b.max
		}
	}
	return cap
}

// next draws the next sleep: uniform in [0, cap], then grows the
// streak. A non-positive ceiling means no wait.
func (b *backoffer) next() time.Duration {
	cap := b.cap()
	b.n++
	if cap <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(cap)))
}

// reset restarts the streak after a welcome.
func (b *backoffer) reset() { b.n = 0 }

// connect attempts one link and pumps it to its end: dial, hello,
// welcome, then read and ping loops. It reports whether a welcome
// completed, so Run can reset the backoff on fresh links, and the
// close code the link ended with (0 when it never came up).
func (c *Client) connect(ctx context.Context) (bool, websocket.StatusCode) {
	atomic.AddInt64(&c.attempts, 1)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)
	conn, resp, err := websocket.Dial(ctx, c.url, &websocket.DialOptions{
		HTTPClient: c.opt.HTTPClient,
		HTTPHeader: header,
	})
	if err != nil {
		// The upgrade response comes back alongside the error, so a
		// rejection is read off its status, not parsed from text.
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		c.dialFailed(status, err)
		return false, 0
	}
	// The library default is 32 KiB; serve may send up to the frame
	// limit (terminal output in M1.4), and anything above 32 KiB would
	// otherwise kill the link with 1009.
	conn.SetReadLimit(link.MaxFrameBytes)
	hello, err := link.Encode(link.Frame{V: link.Version, Type: "hello", ID: link.NewID(), Data: helloData(c.opt.Role, c.name, c.version)})
	if err != nil {
		_ = conn.CloseNow()
		return false, 0
	}
	wctx, stop := context.WithTimeout(ctx, c.opt.PingTimeout)
	err = conn.Write(wctx, websocket.MessageText, hello)
	stop()
	if err != nil {
		_ = conn.CloseNow()
		c.opt.Logger.Debug("hello write failed", "err", shortErr(err))
		return false, 0
	}
	rctx, stop := context.WithTimeout(ctx, c.opt.PingTimeout)
	typ, raw, err := conn.Read(rctx)
	stop()
	if err != nil {
		_ = conn.CloseNow()
		c.opt.Logger.Debug("welcome read failed", "err", shortErr(err))
		return false, 0
	}
	if typ != websocket.MessageText {
		_ = conn.CloseNow()
		return false, 0
	}
	welcome, err := link.Decode(raw)
	if err != nil || welcome.Type != "welcome" {
		_ = conn.CloseNow()
		c.opt.Logger.Debug("bad welcome", "err", shortErr(err))
		return false, 0
	}
	var payload link.Welcome
	if err := json.Unmarshal(welcome.Data, &payload); err != nil || payload.Session == "" {
		_ = conn.CloseNow()
		c.opt.Logger.Debug("welcome without session")
		return false, 0
	}
	c.setConn(conn, payload.Session)
	c.opt.Logger.Info("runner linked")
	c.rejectLogged.Store(false)

	code, reason := c.pump(ctx, conn)

	c.setConn(nil, "")
	// A caller shutdown closes below; any other death is a drop worth
	// one line so an operator watching logs sees flaps, not silence.
	if ctx.Err() == nil {
		c.opt.Logger.Info("link dropped, reconnecting", "close", fmt.Sprintf("%d %s", code, reason))
	}
	return true, code
}

// dialFailed logs one failed attempt. A 401 names the fix at error
// level once and then quiets to debug: the token file will not fix
// itself, but the logs should not scream about it every backoff.
func (c *Client) dialFailed(status int, err error) {
	if status == http.StatusUnauthorized {
		if c.rejectLogged.CompareAndSwap(false, true) {
			c.opt.Logger.Error("runner token rejected: check DEVCULES_RUNNER_TOKEN_FILE")
		} else {
			c.opt.Logger.Debug("runner token rejected: check DEVCULES_RUNNER_TOKEN_FILE")
		}
		return
	}
	c.opt.Logger.Debug("dial failed", "status", status, "err", shortErr(err))
}

// shortErr keeps transport errors to one line. The error never carries
// the token (it travels in a header, never the URL), but it can name
// local addresses, so debug level only.
func shortErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := len(s); i > 160 {
		return s[:160] + "…"
	}
	return s
}

func helloData(role, name, version string) []byte {
	b, _ := json.Marshal(link.Hello{Role: role, Name: name, Version: version})
	return b
}

func (c *Client) setConn(conn *websocket.Conn, session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
	c.session = session
}

// pump runs the read and ping loops to the link's end. On the caller's
// cancellation it closes with 1001 so serve retires the link cleanly;
// any other end just returns its close, and Run redials.
//
// The read runs on a detached context: the shutdown close must go out
// while the transport is alive, and a failed read reaps the transport
// first — after which no close frame can be sent. So a watcher sends
// the 1001 on owner cancellation and only then releases the read.
func (c *Client) pump(ownerCtx context.Context, conn *websocket.Conn) (websocket.StatusCode, string) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	shutdown := make(chan struct{})
	defer close(shutdown)
	go func() {
		select {
		case <-ownerCtx.Done():
			closeSync(c.opt.Logger, conn, websocket.StatusGoingAway, reasonShutdown, 5*time.Second)
			stop()
		case <-shutdown:
		}
	}()

	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		c.pingLoop(ctx, conn)
	}()

	var code websocket.StatusCode = websocket.StatusNoStatusRcvd
	reason := ""
loop:
	for {
		typ, raw, err := conn.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				code, reason = ce.Code, ce.Reason
			}
			break loop
		}
		if typ != websocket.MessageText {
			continue
		}
		f, err := link.Decode(raw)
		if err != nil {
			break loop
		}
		c.mu.Lock()
		fn := c.handlers[f.Type]
		c.mu.Unlock()
		if fn != nil {
			fn(ctx, f)
		}
	}
	stop()
	<-pingDone
	c.opt.Logger.Debug("pump ended", "code", int(code), "reason", reason, "owner", shortErr(ownerCtx.Err()))
	if ownerCtx.Err() != nil {
		// The watcher already sent the 1001 above; report the clean
		// shutdown regardless of which close the read saw last.
		return websocket.StatusGoingAway, reasonShutdown
	}
	if code == websocket.StatusNoStatusRcvd {
		// No peer close (corrupt frame, network cut): the read
		// succeeded, so the transport is alive and this close can
		// still be delivered. It runs in the background while Run
		// redials.
		settleClose(conn, websocket.StatusPolicyViolation, "bad frame")
		return code, reason
	}
	// Peer closed and the library already echoed: release.
	_ = conn.CloseNow()
	return code, reason
}

// pingLoop mirrors the hub's liveness: ping on the interval, give up
// after two consecutive missed pongs. A missed pong here means the
// write-then-wait failed; the link is dropped and Run redials.
func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.opt.PingInterval)
	defer ticker.Stop()
	missed := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, stop := context.WithTimeout(ctx, c.opt.PingTimeout)
			err := conn.Ping(pctx)
			stop()
			if err == nil {
				missed = 0
				continue
			}
			missed++
			if missed >= 2 {
				_ = conn.CloseNow()
				return
			}
		}
	}
}

// settleClose sends a close without hanging the caller: the handshake
// runs in the background while Run proceeds to redial, capped by
// CloseNow so a dead peer cannot leak the goroutine.
func settleClose(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = conn.Close(code, reason)
	}()
	time.AfterFunc(5*time.Second, func() {
		select {
		case <-done:
		default:
			_ = conn.CloseNow()
		}
	})
}

// closeSync sends a close and waits for the handshake up to timeout:
// the shutdown path must flush 1001 before the process exits, or
// serve logs an abnormal closure for a clean shutdown.
func closeSync(log *slog.Logger, conn *websocket.Conn, code websocket.StatusCode, reason string, timeout time.Duration) {
	type result struct{ err error }
	done := make(chan result, 1)
	go func() { done <- result{conn.Close(code, reason)} }()
	select {
	case r := <-done:
		log.Debug("shutdown close sent", "err", shortErr(r.err))
	case <-time.After(timeout):
		log.Debug("shutdown close timed out, forcing")
		_ = conn.CloseNow()
		<-done
	}
}
