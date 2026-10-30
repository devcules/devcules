package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/terminals"
)

// strictDecode rejects unknown fields: a misspelled payload fails
// instead of silently zeroing.
func strictDecode(data json.RawMessage) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec
}

// roundTripTimeout bounds one term.ok / term.error / term.listed wait.
// The browser REST layer turns the timeout into 504; late replies find
// no waiter and are dropped.
const roundTripTimeout = 5 * time.Second

// Relay routes terminal traffic between browsers and the linked
// runner. Build one with New, run it with Run (once), and serve the
// browser API through its methods. It is safe for concurrent use.
type Relay struct {
	hub       *hub.Hub
	publicURL string

	mu      sync.Mutex
	pending map[string]chan link.Frame // request frame id → reply waiter
	streams map[string]*stream         // browser stream id → stream
}

// stream is one browser's view of one terminal.
type stream struct {
	runner string
	term   string
	id     string
	conn   *websocket.Conn
	toUI   chan []byte // bounded browser queue (see queue limits)
	bytes  int         // bytes currently queued
	closed chan struct{}
}

// Browser queue bounds: 256 frames or 4 MiB. A browser slower than
// that is closed with 1013 so it reattaches; tmux redraws on reattach,
// so nothing is lost.
const (
	maxQueueFrames = 256
	maxQueueBytes  = 4 << 20
)

// New returns a Relay over the hub. publicURL joins the request host
// as a trusted browser origin for the stream socket.
func New(h *hub.Hub, publicURL string) *Relay {
	return &Relay{
		hub:       h,
		publicURL: publicURL,
		pending:   map[string]chan link.Frame{},
		streams:   map[string]*stream{},
	}
}

// Run registers the runner→serve frame handlers and the membership
// watcher. Call it once at serve start: OnChange first, then Runners,
// so no link change slips between subscribe and the first read.
func (r *Relay) Run() {
	r.hub.On(terminals.TypeOK, r.onReply)
	r.hub.On(terminals.TypeError, r.onReply)
	r.hub.On(terminals.TypeListed, r.onReply)
	r.hub.On(terminals.TypeAgentList, r.onReply)
	r.hub.On(terminals.TypeBrowsed, r.onReply)
	r.hub.On(terminals.TypeOutput, r.onOutput)
	r.hub.On(terminals.TypeExit, r.onExit)
	r.hub.OnChange(r.onChange)
}

// onReply routes term.ok / term.error / term.listed / term.agentlist to the request's
// waiter by frame id. Unknown ids are late replies past their timeout
// and are dropped. It never blocks: the channel holds one reply.
func (r *Relay) onReply(_ context.Context, _ string, f link.Frame) {
	r.mu.Lock()
	waiter, ok := r.pending[f.ID]
	if ok {
		delete(r.pending, f.ID)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	select {
	case waiter <- f:
	default:
	}
}

// request sends one frame to the runner and awaits the correlated
// reply for up to 5 s. The caller maps timeouts to 504.
func (r *Relay) request(ctx context.Context, runner, typ string, data any) (link.Frame, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return link.Frame{}, err
	}
	f, err := link.Encode(link.Frame{V: link.Version, Type: typ, ID: link.NewID(), Data: payload})
	if err != nil {
		return link.Frame{}, err
	}
	req, err := link.Decode(f)
	if err != nil {
		return link.Frame{}, err
	}
	waiter := make(chan link.Frame, 1)
	r.mu.Lock()
	r.pending[req.ID] = waiter
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, req.ID)
		r.mu.Unlock()
	}()
	if err := r.hub.SendTo(runner, req); err != nil {
		return link.Frame{}, err
	}
	timeout := time.NewTimer(roundTripTimeout)
	defer timeout.Stop()
	select {
	case reply := <-waiter:
		return reply, nil
	case <-timeout.C:
		return link.Frame{}, fmt.Errorf("runner did not answer within 5 s")
	case <-ctx.Done():
		return link.Frame{}, ctx.Err()
	}
}

// ok sends a request and expects term.ok, mapping term.error reasons
// through as errors.
func (r *Relay) ok(ctx context.Context, runner, typ string, data any) error {
	reply, err := r.request(ctx, runner, typ, data)
	if err != nil {
		return err
	}
	if reply.Type == terminals.TypeError {
		var failure terminals.Failure
		if jerr := json.Unmarshal(reply.Data, &failure); jerr != nil || failure.Reason == "" {
			return fmt.Errorf("runner refused the request")
		}
		return fmt.Errorf("%s", failure.Reason)
	}
	if reply.Type != terminals.TypeOK {
		return fmt.Errorf("runner answered %s", reply.Type)
	}
	return nil
}

// Resolve validates a browser attach request before any socket work:
// it picks the runner (?runner=, or the only linked one) and checks
// the terminal id and size. The API layer maps *Error straight to the
// HTTP status; the stream endpoint maps it to a 4404 close.
func (r *Relay) Resolve(runner, term string, cols, rows int) (string, *Error) {
	name, err := r.PickRunner(runner)
	if err != nil {
		return "", err
	}
	if !terminals.ValidID(term) {
		return "", &Error{Status: 404, Msg: "unknown terminal: " + term}
	}
	if !terminals.ValidSize(cols, rows) {
		return "", &Error{Status: 400, Msg: "cols and rows must each be 1-500"}
	}
	return name, nil
}

// PickRunner resolves which linked runner a browser call targets: the
// only one, or the ?runner= choice among several. Zero linked is 503;
// several without a choice is 400 with next steps; an unknown choice
// is 404 naming the live ones.
func (r *Relay) PickRunner(query string) (string, *Error) {
	// A machine target arrives as its hub address (?machine= in the
	// API): machines host terminals too, but are only ever picked by
	// name, never as the default.
	if name, ok := strings.CutPrefix(query, hub.MachineAddr("")); ok {
		if slices.Contains(r.hub.Machines(), name) {
			return query, nil
		}
		return "", &Error{Status: 503, Msg: fmt.Sprintf("machine %q is not linked", name)}
	}
	names := r.hub.Runners()
	switch {
	case len(names) == 0:
		return "", &Error{Status: 503, Msg: "no runner linked"}
	case len(names) == 1 && (query == "" || query == names[0]):
		return names[0], nil
	case len(names) == 1:
		return "", &Error{Status: 404, Msg: fmt.Sprintf("unknown runner %q: linked runner is %q", query, names[0])}
	case query == "":
		return "", &Error{Status: 400, Msg: "more than one runner linked: specify ?runner=<name>"}
	default:
		for _, n := range names {
			if n == query {
				return n, nil
			}
		}
		return "", &Error{Status: 404, Msg: fmt.Sprintf("unknown runner %q", query)}
	}
}

// Error is an API-facing failure with an HTTP status. Messages are
// fixed strings: terminal ids and runner names are operational, never
// secret, and runner stderr never crosses the link.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// CloseCode is the browser stream close for a Resolve failure. No
// runner is a pause, not a missing terminal: 1012 makes the UI wait
// and reattach, as when a runner drops mid-stream. Anything else is
// 4404, and the UI stops retrying.
func (e *Error) CloseCode() websocket.StatusCode {
	if e.Status == 503 {
		return websocket.StatusCode(codeRunnerOffline)
	}
	return websocket.StatusCode(codeUnknownTerm)
}

// List asks the runner for its live terminals. Fresh every call, so a
// serve restart loses nothing.
func (r *Relay) List(ctx context.Context, runner string) ([]terminals.TermInfo, *Error) {
	name, aerr := r.PickRunner(runner)
	if aerr != nil {
		return nil, aerr
	}
	reply, err := r.request(ctx, name, terminals.TypeList, struct{}{})
	if err != nil {
		return nil, &Error{Status: 504, Msg: err.Error()}
	}
	if reply.Type != terminals.TypeListed {
		return nil, &Error{Status: 502, Msg: fmt.Sprintf("runner answered %s", reply.Type)}
	}
	var listed terminals.Listed
	dec := strictDecode(reply.Data)
	if err := dec.Decode(&listed); err != nil {
		return nil, &Error{Status: 502, Msg: "runner sent a bad list"}
	}
	if listed.Terms == nil {
		listed.Terms = []terminals.TermInfo{}
	}
	return listed.Terms, nil
}

// Place says where a terminal starts: a directory, the project it
// belongs to, and a git URL to clone into the directory first.
type Place struct {
	Dir     string
	Project string
	Clone   string
	Name    string
}

// Open mints a terminal id and asks the runner to create its session.
func (r *Relay) Open(ctx context.Context, runner string) (string, *Error) {
	return r.OpenAt(ctx, runner, Place{})
}

// OpenAt opens a shell at p.
func (r *Relay) OpenAt(ctx context.Context, runner string, p Place) (string, *Error) {
	name, aerr := r.PickRunner(runner)
	if aerr != nil {
		return "", aerr
	}
	o := terminals.Open{Term: terminals.NewID(), Dir: p.Dir, Project: p.Project, Clone: p.Clone, Name: p.Name}
	if err := terminals.CheckOpen(o); err != nil {
		return "", &Error{Status: 400, Msg: err.Error()}
	}
	if err := r.ok(ctx, name, terminals.TypeOpen, o); err != nil {
		return "", roundTripError(err)
	}
	return o.Term, nil
}

// OpenAgent mints a terminal id and asks the runner to start the
// agent in it. a carries the launch token; it crosses only the link.
func (r *Relay) OpenAgent(ctx context.Context, runner string, a terminals.AgentLaunch) (string, *Error) {
	return r.OpenAgentAt(ctx, runner, a, Place{})
}

// OpenAgentAt starts the agent in p's directory, tagged with its
// project. A clone is never part of an agent launch.
func (r *Relay) OpenAgentAt(ctx context.Context, runner string, a terminals.AgentLaunch, p Place) (string, *Error) {
	name, aerr := r.PickRunner(runner)
	if aerr != nil {
		return "", aerr
	}
	o := terminals.Open{Term: terminals.NewID(), Agent: &a, Dir: p.Dir, Project: p.Project, Name: p.Name}
	if err := terminals.CheckOpen(o); err != nil {
		return "", &Error{Status: 400, Msg: err.Error()}
	}
	if err := r.ok(ctx, name, terminals.TypeOpen, o); err != nil {
		return "", roundTripError(err)
	}
	return o.Term, nil
}

// Agents asks the runner which agent CLIs it offers and has
// installed. Detection runs commands on the runner, so it is fresh
// every call.
func (r *Relay) Agents(ctx context.Context, runner string) ([]terminals.AgentInfo, *Error) {
	name, aerr := r.PickRunner(runner)
	if aerr != nil {
		return nil, aerr
	}
	reply, err := r.request(ctx, name, terminals.TypeAgents, struct{}{})
	if err != nil {
		return nil, &Error{Status: 504, Msg: err.Error()}
	}
	if reply.Type != terminals.TypeAgentList {
		return nil, &Error{Status: 502, Msg: fmt.Sprintf("runner answered %s", reply.Type)}
	}
	var list terminals.AgentList
	if err := strictDecode(reply.Data).Decode(&list); err != nil {
		return nil, &Error{Status: 502, Msg: "runner sent a bad agent list"}
	}
	if list.Agents == nil {
		list.Agents = []terminals.AgentInfo{}
	}
	return list.Agents, nil
}

// Browse lists a directory's subdirectories on the runner or machine,
// for the project folder picker. Fresh every call.
func (r *Relay) Browse(ctx context.Context, runner, dir string) (terminals.Browsed, *Error) {
	if dir != "" && !terminals.ValidDir(dir) {
		return terminals.Browsed{}, &Error{Status: 400, Msg: "dir: want a clean absolute or workspace-relative path"}
	}
	name, aerr := r.PickRunner(runner)
	if aerr != nil {
		return terminals.Browsed{}, aerr
	}
	reply, err := r.request(ctx, name, terminals.TypeBrowse, terminals.Browse{Path: dir})
	if err != nil {
		return terminals.Browsed{}, &Error{Status: 504, Msg: err.Error()}
	}
	if reply.Type == terminals.TypeError {
		var e terminals.Failure
		_ = json.Unmarshal(reply.Data, &e)
		if e.Reason == "directory not found" {
			return terminals.Browsed{}, &Error{Status: 404, Msg: e.Reason}
		}
		return terminals.Browsed{}, &Error{Status: 502, Msg: e.Reason}
	}
	if reply.Type != terminals.TypeBrowsed {
		return terminals.Browsed{}, &Error{Status: 502, Msg: fmt.Sprintf("runner answered %s", reply.Type)}
	}
	var list terminals.Browsed
	if err := strictDecode(reply.Data).Decode(&list); err != nil {
		return terminals.Browsed{}, &Error{Status: 502, Msg: "runner sent a bad listing"}
	}
	if list.Entries == nil {
		list.Entries = []terminals.DirEntry{}
	}
	return list, nil
}

// Delete kills the terminal's session. The runner treats missing
// sessions as success, so deletes stay idempotent (204 either way).
func (r *Relay) Delete(ctx context.Context, runner, id string) *Error {
	if !terminals.ValidID(id) {
		return &Error{Status: 400, Msg: "terminal: want 16 hex chars"}
	}
	name, aerr := r.PickRunner(runner)
	if aerr != nil {
		return aerr
	}
	if err := r.ok(ctx, name, terminals.TypeClose, terminals.Close{Term: id}); err != nil {
		return roundTripError(err)
	}
	return nil
}

// roundTripError maps runner replies and timeouts to API errors: a
// silent runner is 504, a refusal carries its fixed reason.
func roundTripError(err error) *Error {
	if err.Error() == "runner did not answer within 5 s" {
		return &Error{Status: 504, Msg: err.Error()}
	}
	return &Error{Status: 502, Msg: err.Error()}
}

// Links lists the live runner and machine names, for pickers.
func (r *Relay) Links() (runners, machines []string) {
	return r.hub.Runners(), r.hub.Machines()
}
