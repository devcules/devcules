package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/terminals"
)

// Browser close codes. 1012 marks a dead runner (the UI shows offline
// and reattaches when it returns); 1013 marks a browser too slow for
// its queue (reattach redraws, so nothing is lost); 4404 marks an
// unknown terminal id.
const (
	codeRunnerOffline uint16 = 1012
	codeTooSlow       uint16 = 1013
	codeUnknownTerm   uint16 = 4404
)

// onOutput routes runner bytes to the browser's bounded queue. It
// never blocks the link read loop: a full queue closes that browser
// with 1013 (in the background) and tells the runner to detach the
// orphaned attach client.
func (r *Relay) onOutput(_ context.Context, runner string, f link.Frame) {
	var out terminals.Output
	if err := strictDecode(f.Data).Decode(&out); err != nil {
		return
	}
	raw, err := terminals.DecodeOutput(out)
	if err != nil {
		return
	}
	r.mu.Lock()
	s, ok := r.streams[out.Stream]
	if !ok || s.runner != runner {
		r.mu.Unlock()
		return
	}
	select {
	case <-s.closed:
		r.mu.Unlock()
		return
	default:
	}
	if len(s.toUI) >= maxQueueFrames || s.bytes+len(raw) > maxQueueBytes {
		r.mu.Unlock()
		r.dropSlow(s, out.Stream)
		return
	}
	s.bytes += len(raw)
	r.mu.Unlock()
	select {
	case s.toUI <- raw:
	case <-s.closed:
	}
}

// dropSlow closes an overfull browser with 1013 and detaches its
// attach client on the runner. The close handshake runs in the
// background: coder's Close waits for an echo, and the link read loop
// must never wait on a browser.
func (r *Relay) dropSlow(s *stream, streamID string) {
	r.forget(streamID)
	go func() {
		_ = s.conn.Close(websocket.StatusCode(codeTooSlow), "too slow, reattach")
	}()
	// Never write to the link from its own read loop: the runner may be
	// mid-write to us, and its pong waits behind that write, so a
	// blocking send here deadlocks both ends until the heartbeat kills
	// the link.
	go func() { _ = r.send(s.runner, terminals.TypeDetach, terminals.Detach{Stream: streamID}) }()
}

// onExit closes the browser whose attach client ended (the session was
// killed): the stream is over. A normal close code tells the UI the
// terminal closed rather than errored.
func (r *Relay) onExit(_ context.Context, runner string, f link.Frame) {
	var exit terminals.Exit
	if err := strictDecode(f.Data).Decode(&exit); err != nil {
		return
	}
	if err := terminals.CheckStream(exit.Stream); err != nil {
		return
	}
	r.mu.Lock()
	s, ok := r.streams[exit.Stream]
	if !ok || s.runner != runner {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.forget(exit.Stream)
	go func() {
		_ = s.conn.Close(websocket.StatusNormalClosure, "terminal closed")
	}()
}

// onChange retires every browser stream on the named runner: unlinked
// means the runner went away, and a fresh linked with open streams
// means a replaced runner whose attach clients belonged to the old
// link. Either way the browser gets 1012 and reattaches (tmux
// redraws). It only signals streams and returns: closes run in the
// background so the hub's delivery never stalls.
func (r *Relay) onChange(name string, _ bool) {
	r.mu.Lock()
	var dying []*stream
	for id, s := range r.streams {
		if s.runner == name {
			delete(r.streams, id)
			dying = append(dying, s)
		}
	}
	r.mu.Unlock()
	for _, s := range dying {
		select {
		case <-s.closed:
		default:
			close(s.closed)
		}
		go func() {
			_ = s.conn.Close(websocket.StatusCode(codeRunnerOffline), "runner offline")
		}()
	}
}

// forget removes a stream without closing: the caller owns the close.
func (r *Relay) forget(streamID string) {
	r.mu.Lock()
	s, ok := r.streams[streamID]
	if ok {
		delete(r.streams, streamID)
	}
	r.mu.Unlock()
	if ok {
		select {
		case <-s.closed:
		default:
			close(s.closed)
		}
	}
}

// send fires a one-way frame at the runner: input, resize and detach
// carry no waiter (term.error answers without one are dropped).
func (r *Relay) send(runner, typ string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	f, err := link.Encode(link.Frame{V: link.Version, Type: typ, ID: link.NewID(), Data: payload})
	if err != nil {
		return err
	}
	req, err := link.Decode(f)
	if err != nil {
		return err
	}
	return r.hub.SendTo(runner, req)
}

// AttachStream opens a browser stream: it picks the runner, attaches
// over the link (5 s ceiling), and pumps both directions until either
// end closes. The browser read loop forwards binary input (chunked to
// the 64 KiB input ceiling) and resize controls; the writer drains the
// bounded queue. Either pump ending detaches the attach client
// best-effort and forgets the stream.
func (r *Relay) AttachStream(ctx context.Context, runner, term string, cols, rows int, conn *websocket.Conn) {
	// Pastes arrive as one message; the loop below chunks them to the
	// 64 KiB input frame, so the socket takes up to one link frame.
	conn.SetReadLimit(link.MaxFrameBytes)
	streamID := terminals.NewID()
	// Register before attaching: tmux redraws the screen the moment
	// the client attaches, and those first term.output frames can
	// beat term.ok here. Without the stream in place they would be
	// dropped, and a resume would show a blank screen.
	s := &stream{runner: runner, term: term, id: streamID, conn: conn, toUI: make(chan []byte, maxQueueFrames), closed: make(chan struct{})}
	r.mu.Lock()
	r.streams[streamID] = s
	r.mu.Unlock()
	if err := r.ok(ctx, runner, terminals.TypeAttach, terminals.Attach{
		Term: term, Stream: streamID, Cols: cols, Rows: rows,
	}); err != nil {
		// A late attach must not leave an orphan client pumping
		// into a stream nobody reads.
		_ = r.send(runner, terminals.TypeDetach, terminals.Detach{Stream: streamID})
		select {
		case <-s.closed:
			// onChange already retired the stream with 1012.
			return
		default:
		}
		r.forget(streamID)
		// A killed session is gone for good: 4404 stops the UI's
		// reattach loop instead of retrying a terminal that is no more.
		if err.Error() == "unknown terminal" {
			_ = conn.Close(websocket.StatusCode(codeUnknownTerm), "unknown terminal")
			return
		}
		// Any other attach failure — the link died mid-request, or a
		// same-name runner replaced it while the request was in flight
		// and the attach went to the dying link — leaves the stream
		// unusable but the browser recoverable: 1012 tells the UI to
		// reattach, which either lands on the live replacement or
		// reports the runner offline. 1011 would tell it the relay is
		// broken, which it can't act on and can't be, from here,
		// distinguished reliably mid-swap.
		_ = conn.Close(websocket.StatusCode(codeRunnerOffline), "runner offline")
		return
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.closed:
				return
			case chunk, ok := <-s.toUI:
				if !ok {
					return
				}
				r.mu.Lock()
				s.bytes -= len(chunk)
				r.mu.Unlock()
				wctx, stop := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageBinary, chunk)
				stop()
				if err != nil {
					return
				}
			}
		}
	}()

	// The browser read loop owns cleanup: when it ends (browser gone,
	// protocol error, runner timeout), it detaches the attach client
	// and forgets the stream; the writer drains on closed.
	defer func() {
		_ = r.send(runner, terminals.TypeDetach, terminals.Detach{Stream: streamID})
		r.forget(streamID)
	}()
	for {
		typ, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			for len(raw) > 0 {
				n := len(raw)
				if n > terminals.MaxInputBytes {
					n = terminals.MaxInputBytes
				}
				data, _ := json.Marshal(terminals.Input{
					Stream: streamID,
					B64:    base64.StdEncoding.EncodeToString(raw[:n]),
				})
				_ = r.hub.SendTo(runner, mustFrame(terminals.TypeInput, data))
				raw = raw[n:]
			}
		case websocket.MessageText:
			var ctrl struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if jerr := json.Unmarshal(raw, &ctrl); jerr != nil || ctrl.Type != "resize" {
				continue
			}
			if !terminals.ValidSize(ctrl.Cols, ctrl.Rows) {
				continue
			}
			_ = r.send(runner, terminals.TypeResize, terminals.Resize{Stream: streamID, Cols: ctrl.Cols, Rows: ctrl.Rows})
		}
	}
}

// mustFrame builds an untracked frame: panics only on programmer
// error (the envelope is constructed, never parsed).
func mustFrame(typ string, data []byte) link.Frame {
	f, err := link.Encode(link.Frame{V: link.Version, Type: typ, ID: link.NewID(), Data: data})
	if err != nil {
		panic("relay: cannot encode " + typ + ": " + err.Error())
	}
	back, err := link.Decode(f)
	if err != nil {
		panic("relay: cannot decode " + typ + ": " + err.Error())
	}
	return back
}

// originPatterns trusts the request host (always allowed by the
// library) plus the configured public URL, so the browser UI can
// stream from outside the serve host while cross-site hijacks fail
// closed with 403.
func originPatterns(r *http.Request, publicURL string) []string {
	if publicURL == "" {
		return nil
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return nil
	}
	patterns := []string{u.Host}
	if u.Scheme != "" {
		patterns = append(patterns, u.Scheme+"://"+u.Host)
	}
	return patterns
}

// AcceptOptions builds the WebSocket upgrade options for a browser
// stream: browser origins only, no compression surprises.
func (r *Relay) AcceptOptions(req *http.Request) *websocket.AcceptOptions {
	return &websocket.AcceptOptions{OriginPatterns: originPatterns(req, r.publicURL)}
}
