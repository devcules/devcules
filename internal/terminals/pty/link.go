package pty

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/terminals"
)

// Batching bounds: flush output at 32 KiB or 10 ms after the first
// byte, whichever comes first, so a big `cat` streams in chunks while
// keystrokes stay interactive.
const (
	batchBytes = 32 << 10
	batchWait  = 10 * time.Millisecond
)

// maxTmuxCalls bounds the tmux commands running at once for link
// requests; more requests wait their turn off the read loop.
const maxTmuxCalls = 4

// Serve wires the manager to the link: it registers every term.*
// handler on the dialer before returning, so call it before the
// dialer's Run and no early frame finds a missing handler. A
// background watcher kills all attach clients whenever the link drops
// (serve forgets its streams on unlink, so stale pumps must not write
// into the next link) and when ctx ends. tmux sessions keep running
// for reattach.
//
// Dial handlers run inline in the link read loop, so nothing here may
// block it: requests that run tmux (open, attach, detach, close, list)
// move to their own goroutine, and input is queued per stream.
//
// agents builds agent launches; nil means this runner offers none.
func Serve(ctx context.Context, d *dial.Client, m *Manager, agents Launcher) {
	h := &linkRunner{d: d, m: m, agents: agents, calls: make(chan struct{}, maxTmuxCalls)}
	d.On(terminals.TypeOpen, h.async(h.open))
	d.On(terminals.TypeAttach, h.async(h.attachReq))
	d.On(terminals.TypeInput, h.input)
	d.On(terminals.TypeResize, h.resize)
	d.On(terminals.TypeDetach, h.async(h.detach))
	d.On(terminals.TypeClose, h.async(h.close))
	d.On(terminals.TypeList, h.async(h.list))
	d.On(terminals.TypeAgents, h.async(h.agentList))
	go func() {
		h.watchSession(ctx)
		m.dropStreams()
	}()
}

// linkRunner holds the dialer, the manager and one send mutex: output
// pumps on every stream share it, so concurrent term.output writes
// never interleave on the wire.
type linkRunner struct {
	d      *dial.Client
	m      *Manager
	agents Launcher
	send   sync.Mutex
	calls  chan struct{} // semaphore for concurrent tmux calls
}

// async runs a handler on its own goroutine, at most maxTmuxCalls at a
// time, so a slow or wedged tmux never stalls the link read loop (and
// with it pongs and every other stream's input). The handler keeps the
// link's ctx: an attach client dies with the link it served.
func (h *linkRunner) async(fn dial.HandlerFunc) dial.HandlerFunc {
	return func(ctx context.Context, req link.Frame) {
		go func() {
			select {
			case h.calls <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-h.calls }()
			fn(ctx, req)
		}()
	}
}

// sendFrame sends one frame, serialized with the other pumps. dial
// validates and times the write; a failed send means the link is
// dying, the pump ends, and the dialer's reconnect starts a clean
// generation.
func (h *linkRunner) sendFrame(f link.Frame) error {
	h.send.Lock()
	defer h.send.Unlock()
	return h.d.Send(f)
}

// reply answers a request frame with term.ok or term.error under the
// same id, so the relay correlates replies terminals-local.
func (h *linkRunner) reply(req link.Frame, ok bool, reason string) {
	var f link.Frame
	if ok {
		f = link.Frame{V: link.Version, Type: terminals.TypeOK, ID: req.ID, Data: json.RawMessage(`{}`)}
	} else {
		data, _ := json.Marshal(terminals.Failure{Reason: reason})
		f = link.Frame{V: link.Version, Type: terminals.TypeError, ID: req.ID, Data: data}
	}
	_ = h.sendFrame(f)
}

// decode unmarshals a request's data into v, answering term.error on
// malformed JSON the codec let through (right envelope, wrong shape).
// Unknown fields are rejected: a misspelled "col" fails instead of
// silently attaching at the wrong size.
func (h *linkRunner) decode(req link.Frame, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(req.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		h.reply(req, false, "invalid request")
		return false
	}
	return true
}

func (h *linkRunner) open(ctx context.Context, req link.Frame) {
	var o terminals.Open
	if !h.decode(req, &o) {
		return
	}
	if err := terminals.CheckOpen(o); err != nil {
		h.reply(req, false, "invalid request")
		return
	}
	if o.Agent != nil {
		h.openAgent(ctx, req, o)
		return
	}
	if _, err := h.m.OpenIn(ctx, o.Term, Place{Dir: o.Dir, Project: o.Project, Clone: o.Clone}); err != nil {
		h.reply(req, false, reasonFor(err))
		return
	}
	h.reply(req, true, "")
}

// openAgent builds the agent's launch and starts it. Reasons stay
// fixed strings; the token never reaches a log line.
func (h *linkRunner) openAgent(ctx context.Context, req link.Frame, o terminals.Open) {
	if h.agents == nil {
		h.reply(req, false, "agents unavailable")
		return
	}
	l, err := h.agents.Launch(ctx, *o.Agent, h.m.LaunchDir(o.Term))
	if err != nil {
		h.m.log.Warn("agent launch refused", "agent", o.Agent.ID, "err", err.Error())
		h.reply(req, false, "agent unavailable")
		return
	}
	if _, err := h.m.OpenAgentIn(ctx, o.Term, o.Agent.ID, l, Place{Dir: o.Dir, Project: o.Project}); err != nil {
		h.reply(req, false, reasonFor(err))
		return
	}
	h.reply(req, true, "")
}

// agentList answers term.agents with the agents this runner knows and
// whether each CLI is installed here.
func (h *linkRunner) agentList(ctx context.Context, req link.Frame) {
	list := []terminals.AgentInfo{}
	if h.agents != nil {
		list = append(list, h.agents.Agents(ctx)...)
	}
	data, _ := json.Marshal(terminals.AgentList{Agents: list})
	_ = h.sendFrame(link.Frame{V: link.Version, Type: terminals.TypeAgentList, ID: req.ID, Data: data})
}

func (h *linkRunner) attachReq(ctx context.Context, req link.Frame) {
	var a terminals.Attach
	if !h.decode(req, &a) {
		return
	}
	if err := terminals.CheckAttach(a); err != nil {
		h.reply(req, false, "invalid request")
		return
	}
	att, err := h.m.Attach(ctx, a.Term, a.Stream, a.Cols, a.Rows)
	if err != nil {
		h.reply(req, false, reasonFor(err))
		return
	}
	h.reply(req, true, "")
	go h.pump(att)
}

func (h *linkRunner) input(ctx context.Context, req link.Frame) {
	var in terminals.Input
	if !h.decode(req, &in) {
		return
	}
	b, err := terminals.DecodeInput(in)
	if err != nil {
		h.reply(req, false, "invalid request")
		return
	}
	// Success is silent: input is the busiest frame, and the relay
	// sends it one-way. Only a failure answers.
	if err := h.m.Write(in.Stream, b); err != nil {
		h.reply(req, false, reasonFor(err))
	}
}

func (h *linkRunner) resize(ctx context.Context, req link.Frame) {
	var r terminals.Resize
	if !h.decode(req, &r) {
		return
	}
	if err := terminals.CheckResize(r); err != nil {
		h.reply(req, false, "invalid request")
		return
	}
	if err := h.m.Resize(ctx, r.Stream, r.Cols, r.Rows); err != nil {
		h.reply(req, false, reasonFor(err))
		return
	}
	h.reply(req, true, "")
}

func (h *linkRunner) detach(ctx context.Context, req link.Frame) {
	var dt terminals.Detach
	if !h.decode(req, &dt) {
		return
	}
	if err := terminals.CheckStream(dt.Stream); err != nil {
		h.reply(req, false, "invalid request")
		return
	}
	h.m.Detach(dt.Stream)
	h.reply(req, true, "")
}

func (h *linkRunner) close(ctx context.Context, req link.Frame) {
	var c terminals.Close
	if !h.decode(req, &c) {
		return
	}
	if err := terminals.CheckClose(c); err != nil {
		h.reply(req, false, "invalid request")
		return
	}
	if err := h.m.Close(ctx, c.Term); err != nil {
		h.reply(req, false, reasonFor(err))
		return
	}
	h.reply(req, true, "")
}

func (h *linkRunner) list(ctx context.Context, req link.Frame) {
	terms, err := h.m.List(ctx)
	if err != nil {
		h.reply(req, false, reasonFor(err))
		return
	}
	if terms == nil {
		terms = []terminals.TermInfo{}
	}
	data, _ := json.Marshal(terminals.Listed{Terms: terms})
	_ = h.sendFrame(link.Frame{V: link.Version, Type: terminals.TypeListed, ID: req.ID, Data: data})
}

// reasonFor maps manager errors to fixed wire reasons. tmux stderr
// can hold paths, so it never crosses the link; the runner log keeps
// one short line instead.
func reasonFor(err error) string {
	switch msg := err.Error(); {
	case msg == "terminal exists":
		return "terminal exists"
	case msg == "unknown terminal":
		return "unknown terminal"
	case msg == "unknown stream":
		return "unknown stream"
	case msg == "stream exists":
		return "stream exists"
	case msg == "terminal input queue full":
		return "input queue full"
	case startsWith(msg, "too many terminals"):
		return "too many terminals"
	case msg == "directory not found":
		return "directory not found"
	case msg == "bad directory" || msg == "bad project" || msg == "bad clone url":
		return "invalid request"
	case startsWith(msg, "agent:"):
		return "agent launch failed"
	case startsWith(msg, "term:") || startsWith(msg, "stream:") ||
		startsWith(msg, "size:") || startsWith(msg, "b64:"):
		return "invalid request"
	default:
		return "tmux failed"
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// pump streams one attach client's output as term.output frames,
// batched at 32 KiB or 10 ms. It ends on PTY EOF (or a dead link):
// the exit frame goes out best-effort, the attach is forgotten, and
// the tmux session keeps running for the next attach.
func (h *linkRunner) pump(a *attach) {
	defer reap(a)
	defer h.m.forget(a.stream)

	type result struct {
		b   []byte
		err error
	}
	readCh := make(chan result, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := a.file.Read(buf)
			out := make([]byte, n)
			copy(out, buf[:n])
			readCh <- result{out, err}
			if err != nil {
				return
			}
		}
	}()

	var pending []byte
	timer := time.NewTimer(batchWait)
	if !timer.Stop() {
		<-timer.C
	}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		data, _ := json.Marshal(terminals.Output{
			Stream: a.stream,
			B64:    base64.StdEncoding.EncodeToString(pending),
		})
		pending = nil
		_ = h.sendFrame(link.Frame{V: link.Version, Type: terminals.TypeOutput, ID: link.NewID(), Data: data})
	}
	for {
		select {
		case r := <-readCh:
			pending = append(pending, r.b...)
			if len(pending) >= batchBytes {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				flush()
			} else if r.err == nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(batchWait)
			}
			if r.err != nil {
				if !eofLike(r.err) {
					h.m.log.Debug("attach read failed", "stream", a.stream, "err", shortErr(r.err))
				}
				flush()
				h.exit(a.stream)
				return
			}
		case <-timer.C:
			flush()
		}
	}
}

// exit reports an ended attach client. Best-effort: the link may
// already be down, in which case nobody needs the news.
func (h *linkRunner) exit(stream string) {
	data, _ := json.Marshal(terminals.Exit{Stream: stream})
	_ = h.sendFrame(link.Frame{V: link.Version, Type: terminals.TypeExit, ID: link.NewID(), Data: data})
}

// watchSession kills every attach client when the welcome session
// changes: a drop means serve forgot its streams, so stale pumps must
// not write into the next link. tmux sessions survive for reattach.
func (h *linkRunner) watchSession(ctx context.Context) {
	last := h.d.Session()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if cur := h.d.Session(); cur != last {
				last = cur
				h.m.dropStreams()
			}
		}
	}
}
