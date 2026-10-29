package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
)

// linkConn is one registered runner connection. coder/websocket allows
// every method but Read concurrently and frames each write whole, so
// writes need no lock here. A lock around Ping would be harmful: Ping
// waits for the pong, so a slow pong would stall every SendTo (M1.4
// terminal output) for up to the ping timeout.
type linkConn struct {
	conn *websocket.Conn
}

func (l *linkConn) write(ctx context.Context, typ websocket.MessageType, b []byte) error {
	return l.conn.Write(ctx, typ, b)
}

func (l *linkConn) ping(ctx context.Context) error {
	return l.conn.Ping(ctx)
}

// loop pumps one link to its end and reports how the read side died:
// a peer close echoes back, anything else maps to a close code for
// serve to send. The read loop dispatches frames to the currently
// registered handlers; the ping loop enforces liveness. Whichever
// loop fails first cancels the other and drops the link, so no
// goroutine outlives its connection.
func (l *linkConn) loop(ctx context.Context, h *Hub, name string) (websocket.StatusCode, string) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	go l.pingLoop(ctx, h, name)

	for {
		typ, raw, err := l.conn.Read(ctx)
		if err != nil {
			return readClose(err)
		}
		if typ != websocket.MessageText {
			continue
		}
		f, err := link.Decode(raw)
		if err != nil {
			// A corrupt frame ends the link: the peer is buggy or
			// hostile, and guessing corrupts routing. The runner
			// reconnects and starts clean.
			return websocket.StatusPolicyViolation, "bad frame"
		}
		if fn := h.handlerFor(f.Type); fn != nil {
			fn(ctx, name, f)
		}
	}
}

// handlerFor looks up the current handler for one frame type. The
// lookup (not the call) holds the read lock, so On can register new
// types while links are live without racing the dispatch.
func (h *Hub) handlerFor(msgType string) HandlerFunc {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.handlers[msgType]
}

// readClose maps a read failure to the close frame serve sends back. A
// peer close is echoed so the handshake completes; a read-limit breach
// (coder reports it as a 1009 close error) is confirmed; anything else
// — network errors, cancelled contexts — gets 1011 without details,
// because the underlying error can name local paths.
func readClose(err error) (websocket.StatusCode, string) {
	var ce websocket.CloseError
	if errors.As(err, &ce) && ce.Code != websocket.StatusNoStatusRcvd {
		return ce.Code, ""
	}
	return websocket.StatusInternalError, "read failed"
}

// settle sends a close frame without ever blocking the caller: the
// handshake runs in the background and CloseNow caps the wait, so a
// dead peer cannot wedge the hub or leak a goroutine. It returns a
// channel closed when the handshake finishes; Hub.Close waits on it so
// the SIGTERM drain flushes close frames before the process exits.
func settle(conn *websocket.Conn, code websocket.StatusCode, reason string, grace time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = conn.Close(code, reason)
	}()
	time.AfterFunc(grace, func() {
		select {
		case <-done:
		default:
			_ = conn.CloseNow()
		}
	})
	return done
}

// pingLoop pings on the interval and drops the link after two
// consecutive missed pongs. Each ping carries its own timeout so one
// wedged write cannot stall liveness forever; the miss counter resets
// on any successful pong, so an occasional slow pong never kills a
// healthy link.
func (l *linkConn) pingLoop(ctx context.Context, h *Hub, name string) {
	ticker := time.NewTicker(h.opt.PingInterval)
	defer ticker.Stop()
	missed := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, stop := context.WithTimeout(ctx, h.opt.PingTimeout)
			err := l.ping(pctx)
			stop()
			if err == nil {
				missed = 0
				continue
			}
			missed++
			if missed >= 2 {
				// The peer is dead by definition here, so the close
				// handshake cannot complete: settle caps the wait and
				// the read loop's failure drops the link.
				settle(l.conn, websocket.StatusInternalError, reasonHeartbeat, 5*time.Second)
				return
			}
		}
	}
}

// drop unregisters the link, but only if it is still the current one
// for the name: a replaced elder must not evict its successor. Only a
// real removal queues the unlink event; a replaced elder stays silent,
// since the name never went down.
func (h *Hub) drop(name string, l *linkConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.links[name] == l {
		delete(h.links, name)
		delete(h.roles, name)
		h.notifyLocked(name, false)
	}
}

// Close retires every link with a 1001 close frame so runners log a
// clean shutdown instead of a broken pipe, then marks the hub closed:
// late handshakes are refused instead of registering into a drain.
// Each close runs concurrently with a bounded wait, so one dead peer
// cannot stall the 20 s SIGTERM drain; Close is idempotent.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	links := make([]*linkConn, 0, len(h.links))
	for _, l := range h.links {
		links = append(links, l)
	}
	h.mu.Unlock()

	pending := make([]<-chan struct{}, 0, len(links))
	for _, l := range links {
		pending = append(pending, settle(l.conn, websocket.StatusGoingAway, reasonShutdown, 5*time.Second))
	}
	// The drain flushes close frames before the process exits, but one
	// dead peer cannot hold it past a bounded wait.
	deadline := time.After(5 * time.Second)
	for _, done := range pending {
		select {
		case <-done:
		case <-deadline:
			return
		}
	}
}

// EnsureTokenFile resolves the runner token for serve: the plaintext
// argument wins (DEVCULES_RUNNER_TOKEN), otherwise the file at path is
// read when it exists, otherwise 32 random bytes are generated,
// written 0400, and reported as created so serve logs the path — never
// the value. An existing but empty or whitespace-only file is treated
// as missing and replaced; existing content with inner whitespace is
// rejected with next steps instead of silently minting a token the
// operator cannot reproduce.
func EnsureTokenFile(path, plaintext string) (token string, created bool, err error) {
	if strings.TrimSpace(plaintext) != "" {
		return strings.TrimSpace(plaintext), false, nil
	}
	if raw, rerr := os.ReadFile(path); rerr == nil {
		if v := strings.TrimSpace(string(raw)); v != "" {
			if strings.ContainsAny(v, " \t\r\n") {
				return "", false, fmt.Errorf("runner token file %s holds an invalid token: delete it and restart serve to mint a fresh one", path)
			}
			return v, false, nil
		}
	} else if !os.IsNotExist(rerr) {
		return "", false, fmt.Errorf("cannot read runner token file %s: %s", path, rerr)
	}
	var b [32]byte
	if _, rerr := rand.Read(b[:]); rerr != nil {
		return "", false, fmt.Errorf("cannot generate runner token: %s", rerr)
	}
	token = hex.EncodeToString(b[:])
	if rerr := os.MkdirAll(filepath.Dir(path), 0o700); rerr != nil {
		return "", false, fmt.Errorf("cannot create token directory for %s: %s", path, rerr)
	}
	if rerr := os.WriteFile(path, []byte(token+"\n"), 0o400); rerr != nil {
		return "", false, fmt.Errorf("cannot write runner token file %s: %s", path, rerr)
	}
	return token, true, nil
}

// Kick closes the link at addr (a runner name or MachineAddr) with
// 1008 and reason, for a revoked machine. A missing link is a no-op.
// The drop and its unlink event follow from the read loop ending.
func (h *Hub) Kick(addr, reason string) {
	h.mu.Lock()
	l, ok := h.links[addr]
	h.mu.Unlock()
	if ok {
		settle(l.conn, websocket.StatusPolicyViolation, reason, 5*time.Second)
	}
}
