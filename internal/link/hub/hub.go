package hub

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
)

// Close codes and reasons on the wire. 1008 variants tell a runner
// operator exactly what to fix; 4000 marks a replaced duplicate so the
// old process exits instead of reconnect-looping against itself.
const (
	reasonUnsupportedVersion = "unsupported link version"
	reasonUnknownRole        = "unknown role"
	reasonNoHello            = "no hello"
	reasonReplaced           = "replaced"
	reasonShutdown           = "serve shutdown"
	reasonHeartbeat          = "heartbeat failed"
)

// replacedCode retires a duplicate link. 4000-4999 are free for
// application use; 4000 reads as "replaced" in the runner logs.
var replacedCode = websocket.StatusCode(4000)

// ErrUnknownRunner answers SendTo for a name with no live link. The
// runner may be reconnecting; callers treat this as "try again", never
// as a fatal error.
var ErrUnknownRunner = errors.New("hub: unknown runner")

// Options tunes the handshake and heartbeat. Zero values take the
// production defaults (10 s hello, 15 s ping, 5 s ping timeout); tests
// set millisecond values so liveness runs without sleeping.
type Options struct {
	HelloTimeout time.Duration
	PingInterval time.Duration
	PingTimeout  time.Duration
}

func (o Options) withDefaults() Options {
	if o.HelloTimeout <= 0 {
		o.HelloTimeout = 10 * time.Second
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 15 * time.Second
	}
	if o.PingTimeout <= 0 {
		o.PingTimeout = 5 * time.Second
	}
	return o
}

// HandlerFunc receives one frame from the named runner. Handlers run
// one at a time per link in read order; a slow handler back-pressures
// its own link, never other runners'.
type HandlerFunc func(ctx context.Context, runner string, f link.Frame)

// ChangeFunc receives link membership changes: linked reports a fresh
// registration, unlinked a real removal. Events are delivered one at a
// time, in the order the hub's link map changed, on a delivery
// goroutine of their own: never inside a read loop or a handshake, and
// never under the hub lock, so a watcher may call Runners or SendTo. A
// slow watcher delays only later events, never links or frames.
type ChangeFunc func(name string, linked bool)

// change is one queued membership event.
type change struct {
	name   string
	linked bool
}

// Hub accepts runner links and routes their frames. The zero Hub is
// not usable; build one with New. Hub is safe for concurrent use, and
// Close is idempotent so the SIGTERM drain can call it unconditionally.
type Hub struct {
	expected [32]byte
	opt      Options

	mu       sync.RWMutex
	links    map[string]*linkConn
	handlers map[string]HandlerFunc
	watchers []ChangeFunc
	// events queue membership changes in map order; delivering marks
	// a live delivery goroutine. Both are guarded by mu.
	events     []change
	delivering bool
	closed     bool
}

// New returns a Hub that accepts the token whose SHA-256 is expected.
// Only the hash is kept: a heap dump or a /debug endpoint added later
// can never leak the token.
func New(expected [32]byte, opt Options) *Hub {
	return &Hub{
		expected: expected,
		opt:      opt.withDefaults(),
		links:    map[string]*linkConn{},
		handlers: map[string]HandlerFunc{},
	}
}

// HashToken hashes a plaintext runner token for New. The plaintext
// lives in config and the token file only.
func HashToken(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

// On registers the handler for one frame type. M1.4 registers terminal
// frame types here without touching the hub. Unknown or malformed
// types are programmer errors, so On panics naming them.
func (h *Hub) On(msgType string, fn HandlerFunc) {
	if !link.ValidType(msgType) {
		panic("hub: invalid frame type " + msgType)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[msgType] = fn
}

// OnChange registers a membership watcher, called after every register
// and every real removal. A replaced duplicate fires linked again (the
// relay rebinds to the new link) but never unlinked for the retired
// elder, since the name never went down.
func (h *Hub) OnChange(fn ChangeFunc) {
	if fn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.watchers = append(h.watchers, fn)
}

// Runners returns the sorted names of the live runner links. The relay
// uses it to pick the only runner (or require ?runner= when several
// are linked); sorting keeps logs and errors deterministic.
func (h *Hub) Runners() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.links))
	for name := range h.links {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// notifyLocked queues a membership event; the caller holds mu and has
// just changed the link map, so the queue order is the map order. Two
// handshake goroutines notifying on their own could otherwise deliver
// "linked" before an older "unlinked" for the same name, and a watcher
// would believe a live runner is offline.
func (h *Hub) notifyLocked(name string, linked bool) {
	if len(h.watchers) == 0 {
		return
	}
	h.events = append(h.events, change{name, linked})
	if !h.delivering {
		h.delivering = true
		go h.deliver()
	}
}

// deliver drains the event queue in order, outside the hub lock, and
// exits once it is empty, so an idle hub holds no goroutine.
func (h *Hub) deliver() {
	for {
		h.mu.Lock()
		if len(h.events) == 0 {
			h.events = nil
			h.delivering = false
			h.mu.Unlock()
			return
		}
		ev := h.events[0]
		h.events = h.events[1:]
		fns := append([]ChangeFunc(nil), h.watchers...)
		h.mu.Unlock()
		for _, fn := range fns {
			fn(ev.name, ev.linked)
		}
	}
}

// Count returns the number of live runner links. /readyz reports it so
// the UI can show "runner offline" instead of failing readiness (the
// runner waits for serve to be healthy, so readiness must not wait for
// the runner).
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.links)
}

// SendTo delivers one frame to the named runner. The frame must encode
// cleanly; the write carries a timeout so a stuck peer cannot wedge
// the sender.
func (h *Hub) SendTo(name string, f link.Frame) error {
	b, err := link.Encode(f)
	if err != nil {
		return err
	}
	h.mu.Lock()
	l, ok := h.links[name]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownRunner, name)
	}
	ctx, stop := context.WithTimeout(context.Background(), h.opt.PingTimeout)
	defer stop()
	return l.write(ctx, websocket.MessageText, b)
}

// Handler serves GET /link: method, Origin and token checks run before
// the upgrade, then the hello/welcome handshake registers the link.
// Only GET is a WebSocket upgrade; anything else is 405. Any Origin
// header is 403 — browsers always send one and the runner never does,
// so this blocks cross-site socket hijacking even with a leaked token.
// A missing or wrong token is 401 without any hint about the expected
// value.
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		token, ok := bearer(r.Header.Get("Authorization"))
		if !ok || subtle.ConstantTimeCompare(hash(token), h.expected[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.serve(context.Background(), conn)
	})
}

func hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// bearer splits "Bearer <token>". Anything else — missing header,
// wrong scheme, empty token — is not a bearer credential.
func bearer(header string) (string, bool) {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found || strings.TrimSpace(token) == "" {
		return "", false
	}
	return token, true
}

// helloPayload strictly decodes the data of a hello frame. Role must
// be "runner" ("machine" arrives in M4.1; anything else is rejected
// now rather than half-accepted). Name is 1-64 characters so it fits
// the link table and logs.
func helloPayload(data json.RawMessage) (link.Hello, error) {
	var hello link.Hello
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hello); err != nil {
		return link.Hello{}, err
	}
	if hello.Role != "runner" {
		return link.Hello{}, fmt.Errorf("role %q", hello.Role)
	}
	if hello.Name == "" || len(hello.Name) > 64 {
		return link.Hello{}, fmt.Errorf("name %q", hello.Name)
	}
	return hello, nil
}

// serve runs the handshake then pumps one connection to the end: read
// loop dispatching to handlers, ping loop enforcing liveness. Either
// loop ending drops the link; a duplicate name retires the elder.
func (h *Hub) serve(ctx context.Context, conn *websocket.Conn) {
	conn.SetReadLimit(link.MaxFrameBytes)
	hello, err := h.readHello(ctx, conn)
	if err != nil {
		if errors.Is(err, errHelloTimeout) {
			return
		}
		fail, code := errCode(err)
		// The transport is alive here (the read succeeded), so the
		// close carries its reason; settle keeps a dead peer from
		// stalling the handler.
		settle(conn, code, fail, 5*time.Second)
		return
	}
	l := &linkConn{conn: conn}
	h.mu.RLock()
	closed := h.closed
	h.mu.RUnlock()
	if closed {
		settle(conn, websocket.StatusGoingAway, reasonShutdown, 5*time.Second)
		return
	}

	// Welcome goes out before the link registers, so no SendTo can
	// reach the runner ahead of it: the runner treats any other first
	// frame as a failed handshake.
	session := link.NewSession()
	sessionJSON, _ := json.Marshal(map[string]string{"session": session})
	welcome, err := link.Encode(link.Frame{V: link.Version, Type: "welcome", ID: link.NewID(), Data: sessionJSON})
	if err != nil {
		settle(conn, websocket.StatusInternalError, "cannot encode welcome", 5*time.Second)
		return
	}
	wctx, stop := context.WithTimeout(ctx, h.opt.PingTimeout)
	defer stop()
	if err := l.write(wctx, websocket.MessageText, welcome); err != nil {
		_ = conn.CloseNow()
		return
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		settle(conn, websocket.StatusGoingAway, reasonShutdown, 5*time.Second)
		return
	}
	if old, dup := h.links[hello.Name]; dup {
		// The elder gets its own close frame as the map flips, so it
		// logs "replaced" and exits instead of fighting the new link
		// for the name. The close runs in the background: a dead
		// elder must not stall the new link's handshake.
		settle(old.conn, websocket.StatusCode(replacedCode), reasonReplaced, 5*time.Second)
	}
	h.links[hello.Name] = l
	h.notifyLocked(hello.Name, true)
	h.mu.Unlock()

	// Echo the read outcome back so the peer's close handshake always
	// completes: without this a client that closed cleanly would hang
	// waiting for serve's answer, and an oversize sender would never
	// learn its 1009.
	code, reason := l.loop(ctx, h, hello.Name)
	h.drop(hello.Name, l)
	settle(conn, code, reason, 5*time.Second)
}

// errCode maps a hello failure to its close reason. A version mismatch
// names the version problem; every other malformed first frame —
// wrong type, bad payload, timeout with no hello at all — is "no
// hello", because from serve's side nothing usable arrived.
func errCode(err error) (string, websocket.StatusCode) {
	var derr *link.DecodeError
	if errors.As(err, &derr) && derr.Field == "v" {
		return reasonUnsupportedVersion, websocket.StatusPolicyViolation
	}
	var roleErr *roleError
	if errors.As(err, &roleErr) {
		return reasonUnknownRole, websocket.StatusPolicyViolation
	}
	return reasonNoHello, websocket.StatusPolicyViolation
}

// errHelloTimeout marks a hello that lost the race with the hello
// timer. The 1008 close already went out, so serve must not close
// again — it just drops the connection.
var errHelloTimeout = errors.New("no hello: timed out waiting for the first frame")

// readHello waits for the first frame inside the hello timeout, racing
// the read against a timer. The timer closes with 1008 while the
// transport is still alive: a read killed by its own deadline takes
// the connection down silently (the library reaps the transport on a
// failed read), so the "no hello" reason must go out first. A hello
// that arrives as the timer fires loses — the close already went out,
// so the link must not register.
func (h *Hub) readHello(ctx context.Context, conn *websocket.Conn) (link.Hello, error) {
	type outcome struct {
		hello link.Hello
		err   error
	}
	res := make(chan outcome, 1)
	go func() {
		hello, err := h.readFirst(ctx, conn)
		res <- outcome{hello, err}
	}()
	timer := time.NewTimer(h.opt.HelloTimeout)
	defer timer.Stop()
	select {
	case r := <-res:
		return r.hello, r.err
	case <-timer.C:
		settle(conn, websocket.StatusPolicyViolation, reasonNoHello, 5*time.Second)
		// The blocked read dies on the close above; waiting keeps the
		// reader goroutine from leaking, bounded by the settle grace.
		<-res
		return link.Hello{}, errHelloTimeout
	}
}

// readFirst reads and validates the first frame with no deadline: the
// hello timer owns the timeout, so this read never kills the transport
// itself.
func (h *Hub) readFirst(ctx context.Context, conn *websocket.Conn) (link.Hello, error) {
	typ, raw, err := conn.Read(ctx)
	if err != nil {
		return link.Hello{}, &helloError{err: err}
	}
	if typ != websocket.MessageText {
		return link.Hello{}, &helloError{err: errors.New("binary frame")}
	}
	f, err := link.Decode(raw)
	if err != nil {
		return link.Hello{}, err
	}
	if f.Type != "hello" {
		return link.Hello{}, &helloError{err: errors.New("first frame is " + f.Type)}
	}
	hello, err := helloPayload(f.Data)
	if err != nil {
		if strings.HasPrefix(err.Error(), "role ") {
			return link.Hello{}, &roleError{err: err}
		}
		return link.Hello{}, &helloError{err: err}
	}
	return hello, nil
}

// helloError marks a first frame that is not a usable hello.
type helloError struct{ err error }

func (e *helloError) Error() string { return "no hello: " + e.err.Error() }
func (e *helloError) Unwrap() error { return e.err }

// roleError marks a hello with an unknown role.
type roleError struct{ err error }

func (e *roleError) Error() string { return e.err.Error() }
func (e *roleError) Unwrap() error { return e.err }
