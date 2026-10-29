package machrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/machframes"
	"github.com/devcules/devcules/internal/machines/elements"
	"github.com/devcules/devcules/internal/machines/ground"
	"github.com/devcules/devcules/internal/machines/ocr"
)

// strictDecode rejects unknown fields: a misspelled payload fails
// instead of silently zeroing.
func strictDecode(data json.RawMessage) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec
}

// roundTripTimeout bounds one mach.* wait. The REST layer turns the
// timeout into 504; late replies find no waiter and are dropped.
const roundTripTimeout = 5 * time.Second

// slowTimeouts are the requests that do real work on the machine
// before they answer, each longer than the machine's own limit for
// that work: a tree walk allows 25 s (macOS compiles its walker on
// first use), an OCR pass 45 s, and grounding waits on the router,
// whose client allows 45 s. 5 s would time them out while they run.
var slowTimeouts = map[string]time.Duration{
	machframes.TypeScreen: 15 * time.Second,
	machframes.TypeTree:   30 * time.Second,
	machframes.TypeWords:  60 * time.Second,
	machframes.TypeVerify: 30 * time.Second,
	machframes.TypeGround: 60 * time.Second,
}

// waitFor is how long a request of typ waits for its answer.
func waitFor(typ string) time.Duration {
	if d, ok := slowTimeouts[typ]; ok {
		return d
	}
	return roundTripTimeout
}

// errNoAnswer is a request that outlived its wait.
type errNoAnswer struct{ after time.Duration }

func (e errNoAnswer) Error() string {
	return fmt.Sprintf("machine did not answer within %s", e.after)
}

// waiter is one request awaiting its answer, and the link address of
// the machine asked: only that machine may answer it.
type waiter struct {
	machine string
	reply   chan link.Frame
}

// screenMaxBytes is the PNG cap serve asks for: 2 MiB covers a
// retina frame without unbounded links, and the machine refuses
// over the cap rather than truncating.
const screenMaxBytes = 2 << 20

// Relay routes machine task-view traffic between browsers and the
// linked machine helpers. Build one with New, run Run once at serve
// start, and serve the browser API through its methods. It is safe
// for concurrent use.
type Relay struct {
	hub *hub.Hub

	mu      sync.Mutex
	pending map[string]waiter // request frame id → reply waiter

	budgetMu sync.Mutex
	budget   int // serve's mirror of the machine's action budget
}

// New returns a Relay over the hub. The budget mirror starts at the
// plan's 40; the machine's own counter stays the authority.
func New(h *hub.Hub) *Relay {
	return &Relay{
		hub:     h,
		pending: map[string]waiter{},
		budget:  machframes.BudgetMax,
	}
}

// Run registers the machine→serve frame handlers. Call it once at
// serve start.
func (r *Relay) Run() {
	r.hub.On(machframes.TypeOK, r.onReply)
	r.hub.On(machframes.TypeFailure, r.onReply)
	r.hub.On(machframes.TypeFrame, r.onReply)
	r.hub.On(machframes.TypeTreeOut, r.onReply)
	r.hub.On(machframes.TypeWordsOut, r.onReply)
	r.hub.On(machframes.TypeGroundOut, r.onReply)
	r.hub.On(machframes.TypeVerifyOut, r.onReply)
}

// onReply routes every machine answer to the request's waiter by
// frame id. Unknown ids are late replies past their timeout and are
// dropped, as is an answer from any machine but the one asked. It
// never blocks: the channel holds one reply.
func (r *Relay) onReply(_ context.Context, from string, f link.Frame) {
	r.mu.Lock()
	w, ok := r.pending[f.ID]
	if ok && w.machine == from {
		delete(r.pending, f.ID)
	}
	r.mu.Unlock()
	if !ok || w.machine != from {
		return
	}
	select {
	case w.reply <- f:
	default:
	}
}

// request sends one machframes request to the machine and awaits the
// correlated reply (5 s, longer for the slow requests). Callers map
// timeouts to 504.
func (r *Relay) request(ctx context.Context, machine, typ string, data any) (link.Frame, error) {
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
	w := waiter{machine: hub.MachineAddr(machine), reply: make(chan link.Frame, 1)}
	r.mu.Lock()
	r.pending[req.ID] = w
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, req.ID)
		r.mu.Unlock()
	}()
	if err := r.hub.SendTo(hub.MachineAddr(machine), req); err != nil {
		return link.Frame{}, err
	}
	wait := waitFor(typ)
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	select {
	case reply := <-w.reply:
		return reply, nil
	case <-timeout.C:
		return link.Frame{}, errNoAnswer{after: wait}
	case <-ctx.Done():
		return link.Frame{}, ctx.Err()
	}
}

// Machines lists the linked machine names, sorted.
func (r *Relay) Machines() []string { return r.hub.Machines() }

// PickMachine resolves which linked machine a browser call targets:
// the only one, or the ?machine= choice among several. Zero linked
// is 503; several without a choice is 400 with next steps; an
// unknown choice is 404 naming the live ones.
func (r *Relay) PickMachine(query string) (string, *Error) {
	names := r.hub.Machines()
	switch {
	case len(names) == 0:
		return "", &Error{Status: 503, Msg: "no machine linked"}
	case len(names) == 1 && (query == "" || query == names[0]):
		return names[0], nil
	case len(names) == 1:
		return "", &Error{Status: 404, Msg: fmt.Sprintf("unknown machine %q: linked machine is %q", query, names[0])}
	case query == "":
		return "", &Error{Status: 400, Msg: "more than one machine linked: specify ?machine=<name>"}
	default:
		for _, n := range names {
			if n == query {
				return n, nil
			}
		}
		return "", &Error{Status: 404, Msg: fmt.Sprintf("unknown machine %q", query)}
	}
}

// Error is an API-facing failure with an HTTP status. Messages are
// fixed strings or the machine's own refusal reason: the machine
// names its senses (screen, tree), never secrets.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// roundTripError maps transport failures to API errors: a silent
// machine is 504, a refusal carries the machine's reason.
func roundTripError(err error) *Error {
	if errors.As(err, new(errNoAnswer)) {
		return &Error{Status: 504, Msg: err.Error()}
	}
	return &Error{Status: 502, Msg: err.Error()}
}

// refused decodes a mach.error reply's reason.
func refused(data json.RawMessage) string {
	var failure machframes.Failure
	if err := strictDecode(data).Decode(&failure); err != nil || failure.Reason == "" {
		return "machine refused the request"
	}
	return failure.Reason
}

// wantType awaits one reply of typ, mapping mach.error reasons and
// timeouts through as *Error.
func (r *Relay) wantType(ctx context.Context, machine, typ string, data any, want string) (link.Frame, *Error) {
	reply, err := r.request(ctx, machine, typ, data)
	if err != nil {
		return link.Frame{}, roundTripError(err)
	}
	switch {
	case reply.Type == machframes.TypeFailure:
		return link.Frame{}, &Error{Status: 502, Msg: refused(reply.Data)}
	case reply.Type != want:
		return link.Frame{}, &Error{Status: 502, Msg: fmt.Sprintf("machine answered %s", reply.Type)}
	}
	return reply, nil
}

// Screen captures one frame from the machine as PNG bytes. scale is
// the screen pixels per frame pixel (1 unless the machine shrank the
// capture to fit the cap).
func (r *Relay) Screen(ctx context.Context, machine string) (png []byte, scale float64, aerr *Error) {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return nil, 0, aerr
	}
	reply, aerr := r.wantType(ctx, name, machframes.TypeScreen, machframes.Screen{MaxBytes: screenMaxBytes}, machframes.TypeFrame)
	if aerr != nil {
		return nil, 0, aerr
	}
	var frame machframes.FrameOut
	if err := strictDecode(reply.Data).Decode(&frame); err != nil || len(frame.PNG) == 0 {
		return nil, 0, &Error{Status: 502, Msg: "machine sent a bad frame"}
	}
	return frame.PNG, fullSize(frame.Scale), nil
}

// fullSize reads a frame's scale: an absent or nonsense one means the
// frame is the screen's own size.
func fullSize(scale float64) float64 {
	if scale < 1 {
		return 1
	}
	return scale
}

// Tree reads the machine's accessibility tree.
func (r *Relay) Tree(ctx context.Context, machine string) (elements.Tree, *Error) {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return elements.Tree{}, aerr
	}
	reply, aerr := r.wantType(ctx, name, machframes.TypeTree, machframes.Tree{}, machframes.TypeTreeOut)
	if aerr != nil {
		return elements.Tree{}, aerr
	}
	var tree elements.Tree
	if err := strictDecode(reply.Data).Decode(&tree); err != nil {
		return elements.Tree{}, &Error{Status: 502, Msg: "machine sent a bad tree"}
	}
	if tree.Elements == nil {
		tree.Elements = []elements.Element{}
	}
	return tree, nil
}

// Words OCRs the machine's live screen.
func (r *Relay) Words(ctx context.Context, machine string) ([]ocr.Word, *Error) {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return nil, aerr
	}
	reply, aerr := r.wantType(ctx, name, machframes.TypeWords, struct{}{}, machframes.TypeWordsOut)
	if aerr != nil {
		return nil, aerr
	}
	var words []ocr.Word
	if err := strictDecode(reply.Data).Decode(&words); err != nil {
		return nil, &Error{Status: 502, Msg: "machine sent bad words"}
	}
	if words == nil {
		words = []ocr.Word{}
	}
	return words, nil
}

// Inject sends one input event. The machine spends one budget unit
// there and refuses at zero; serve mirrors the spend so the UI's
// counter tracks the machine. A refusal is returned, not retried:
// the machine's budget is the authority.
func (r *Relay) Inject(ctx context.Context, machine string, ev machframes.Input) *Error {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return aerr
	}
	if _, aerr := r.wantType(ctx, name, machframes.TypeInput, ev, machframes.TypeOK); aerr != nil {
		return aerr
	}
	r.spendLocal()
	return nil
}

// Ground SAM-grounds a phrase against the machine's live screen.
// The machine holds the router session, so no key crosses the link.
func (r *Relay) Ground(ctx context.Context, machine, phrase string) ([]ground.Match, *Error) {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return nil, aerr
	}
	reply, aerr := r.wantType(ctx, name, machframes.TypeGround, machframes.Ground{Phrase: phrase}, machframes.TypeGroundOut)
	if aerr != nil {
		return nil, aerr
	}
	var matches []ground.Match
	if err := strictDecode(reply.Data).Decode(&matches); err != nil {
		return nil, &Error{Status: 502, Msg: "machine sent bad matches"}
	}
	if matches == nil {
		matches = []ground.Match{}
	}
	return matches, nil
}

// SetBudget tells the machine the task's remaining actions and
// resets serve's mirror to the same number.
func (r *Relay) SetBudget(ctx context.Context, machine string, remaining int) *Error {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return aerr
	}
	if remaining < 0 || remaining > machframes.BudgetMax {
		return &Error{Status: 400, Msg: "budget must be 0-40"}
	}
	reply, err := r.request(ctx, name, machframes.TypeBudget, machframes.Budget{Remaining: remaining})
	if err != nil {
		return roundTripError(err)
	}
	if reply.Type == machframes.TypeFailure {
		return &Error{Status: 502, Msg: refused(reply.Data)}
	}
	r.budgetMu.Lock()
	r.budget = remaining
	r.budgetMu.Unlock()
	return nil
}

// BudgetRemaining answers serve's mirror of the machine's action
// budget: what serve has sent since the last SetBudget, starting at
// the plan's 40. The machine's own counter stays the authority (it
// refuses input at zero whatever serve believes); this mirror is
// only the UI's display, so a serve restart losing it costs nothing.
func (r *Relay) BudgetRemaining() int {
	r.budgetMu.Lock()
	defer r.budgetMu.Unlock()
	return r.budget
}

// spendLocal decrements the mirror after an accepted inject.
func (r *Relay) spendLocal() {
	r.budgetMu.Lock()
	if r.budget > 0 {
		r.budget--
	}
	r.budgetMu.Unlock()
}

// Verified is the done-verifier's fresh look at the machine.
type Verified struct {
	PNG   []byte
	Scale float64 // screen pixels per PNG pixel
	Tree  elements.Tree
	Note  string
}

// Verify runs the done-verifier: a fresh capture plus a fresh tree
// read from the machine, answered together so a task may only
// report success against what the machine sees now.
func (r *Relay) Verify(ctx context.Context, machine string) (Verified, *Error) {
	name, aerr := r.PickMachine(machine)
	if aerr != nil {
		return Verified{}, aerr
	}
	reply, aerr := r.wantType(ctx, name, machframes.TypeVerify, machframes.Verify{}, machframes.TypeVerifyOut)
	if aerr != nil {
		return Verified{}, aerr
	}
	var out verifyOut
	if err := strictDecode(reply.Data).Decode(&out); err != nil {
		return Verified{}, &Error{Status: 502, Msg: "machine sent a bad verify"}
	}
	v := Verified{PNG: out.PNG, Scale: fullSize(out.Scale), Note: out.Note}
	if out.Tree != nil {
		v.Tree = *out.Tree
	}
	if v.Tree.Elements == nil {
		v.Tree.Elements = []elements.Element{}
	}
	return v, nil
}

// verifyOut mirrors the helper's done-verifier answer shape.
type verifyOut struct {
	PNG   []byte         `json:"png,omitempty"`
	Scale float64        `json:"scale,omitempty"`
	Tree  *elements.Tree `json:"tree,omitempty"`
	Note  string         `json:"note,omitempty"`
}
