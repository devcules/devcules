package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/machframes"
	"github.com/devcules/devcules/internal/machines/elements"
	"github.com/devcules/devcules/internal/machines/ground"
	"github.com/devcules/devcules/internal/machines/input"
	"github.com/devcules/devcules/internal/machines/ocr"
	"github.com/devcules/devcules/internal/machines/screen"
)

// strictDecode unmarshals frame data, rejecting unknown fields: a
// misspelled key fails the request instead of half-acting on it.
func strictDecode(data json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Drivers is the machine's local senses: one interface per sense, all
// built by their packages' New(). A nil driver answers "unavailable
// on this machine" instead of failing the helper, so a machine
// without OCR still serves its screen.
type Drivers struct {
	Screen   screen.Driver
	Input    input.Driver
	Elements elements.Driver
	Words    ocr.Driver
	// Router is serve's base URL for SAM grounding; Ground uses the
	// machine's launch token, never a provider key.
	Router    string
	LaunchTok string
}

// Helper serves machframes over one dial. The zero Helper is not
// usable; build with New.
type Helper struct {
	d      Drivers
	dial   *dial.Client
	budget atomic.Int32
}

// New wraps the drivers. The budget starts at the plan's 40 screen
// actions; serve resets it per task with mach.budget.
func New(d Drivers) *Helper {
	h := &Helper{d: d}
	h.budget.Store(machframes.BudgetMax)
	return h
}

// Serve registers the machframes handlers on the dial and remembers
// it for replies. Like the runner's terminal Serve, handlers run
// inline in the link read loop, so every handler here returns fast:
// captures and grounding answer on their own goroutine.
func (h *Helper) Serve(d *dial.Client) {
	h.dial = d
	d.On(machframes.TypeScreen, h.async(h.screenReq))
	d.On(machframes.TypeInput, h.input)
	d.On(machframes.TypeTree, h.async(h.tree))
	d.On(machframes.TypeWords, h.async(h.words))
	d.On(machframes.TypeGround, h.async(h.groundReq))
	d.On(machframes.TypeBudget, h.budgetReq)
	d.On(machframes.TypeVerify, h.async(h.verify))
}

// async moves a slow handler (captures, grounding) off the read loop.
func (h *Helper) async(fn dial.HandlerFunc) dial.HandlerFunc {
	return func(ctx context.Context, req link.Frame) {
		go fn(ctx, req)
	}
}

// spend consumes one budget unit; ok is false at zero. The machine
// enforces its own ceiling, so a runaway serve cannot burn actions.
func (h *Helper) spend() bool {
	for {
		v := h.budget.Load()
		if v <= 0 {
			return false
		}
		if h.budget.CompareAndSwap(v, v-1) {
			return true
		}
	}
}

// screenReq captures one frame and answers mach.frame with PNG bytes
// capped by the request, shrunk to fit when the full size does not.
func (h *Helper) screenReq(ctx context.Context, req link.Frame) {
	if h.d.Screen == nil {
		h.fail(ctx, req.ID, "screen capture unavailable on this machine")
		return
	}
	var ask machframes.Screen
	if err := strictDecode(req.Data, &ask); err != nil || ask.MaxBytes <= 0 {
		h.fail(ctx, req.ID, "bad screen request")
		return
	}
	img, err := h.d.Screen.Capture()
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	frame, scale, err := encodeFit(img, ask.MaxBytes)
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	h.reply(ctx, req.ID, machframes.TypeFrame, machframes.FrameOut{PNG: frame, Scale: scale})
}

// input injects one event, spending one budget unit. The budget
// check comes before the inject: a task out of actions must not move
// the machine, whatever serve asks. A malformed event or a failed
// inject moves nothing and costs nothing.
func (h *Helper) input(ctx context.Context, req link.Frame) {
	if h.d.Input == nil {
		h.fail(ctx, req.ID, "input unavailable on this machine")
		return
	}
	var ev machframes.Input
	if err := strictDecode(req.Data, &ev); err != nil || ev.Kind == "" {
		h.fail(ctx, req.ID, "bad input event")
		return
	}
	if !h.spend() {
		h.fail(ctx, req.ID, "budget exhausted")
		return
	}
	if err := h.d.Input.Inject(input.Event{Kind: ev.Kind, X: ev.X, Y: ev.Y, Key: ev.Key, Text: ev.Text}); err != nil {
		// Nothing moved, so the action is not spent.
		h.budget.Add(1)
		h.fail(ctx, req.ID, err.Error())
		return
	}
	h.reply(ctx, req.ID, machframes.TypeOK, struct{}{})
}

// tree reads the accessibility tree.
func (h *Helper) tree(ctx context.Context, req link.Frame) {
	if h.d.Elements == nil {
		h.fail(ctx, req.ID, "accessibility tree unavailable on this machine")
		return
	}
	var ask machframes.Tree
	_ = strictDecode(req.Data, &ask)
	t, err := h.d.Elements.Tree(ctx, ask.Max)
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	h.reply(ctx, req.ID, machframes.TypeTreeOut, t)
}

// words OCRs the machine's live screen.
func (h *Helper) words(ctx context.Context, req link.Frame) {
	if h.d.Words == nil || h.d.Screen == nil {
		h.fail(ctx, req.ID, "OCR unavailable on this machine")
		return
	}
	img, err := h.d.Screen.Capture()
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	ws, err := h.d.Words.Read(ctx, img)
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	h.reply(ctx, req.ID, machframes.TypeWordsOut, ws)
}

// groundReq SAM-grounds a phrase against the live screen through the
// router with the machine's launch token.
func (h *Helper) groundReq(ctx context.Context, req link.Frame) {
	if h.d.Screen == nil {
		h.fail(ctx, req.ID, "screen capture unavailable on this machine")
		return
	}
	var ask machframes.Ground
	if err := strictDecode(req.Data, &ask); err != nil || ask.Phrase == "" {
		h.fail(ctx, req.ID, "bad ground request")
		return
	}
	img, err := h.d.Screen.Capture()
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	c := ground.New(h.d.Router, h.d.LaunchTok)
	matches, err := c.Ground(ctx, img, ask.Phrase)
	if err != nil {
		h.fail(ctx, req.ID, err.Error())
		return
	}
	h.reply(ctx, req.ID, machframes.TypeGroundOut, matches)
}

// budgetReq sets the remaining budget. Only serve sends this; a
// task's budget cannot be raised past the plan's ceiling.
func (h *Helper) budgetReq(ctx context.Context, req link.Frame) {
	var b machframes.Budget
	if err := strictDecode(req.Data, &b); err != nil || b.Remaining < 0 || b.Remaining > machframes.BudgetMax {
		h.fail(ctx, req.ID, "bad budget")
		return
	}
	h.budget.Store(int32(b.Remaining))
	h.reply(ctx, req.ID, machframes.TypeOK, struct{}{})
}

// verify is the done-verifier: a fresh capture plus a fresh tree
// read, answered together, so a task may only report success against
// what the machine sees NOW.
func (h *Helper) verify(ctx context.Context, req link.Frame) {
	var ask machframes.Verify
	_ = strictDecode(req.Data, &ask)
	out := verifyOut{}
	if h.d.Screen != nil {
		if img, err := h.d.Screen.Capture(); err == nil {
			if frame, scale, err := encodeFit(img, verifyMaxBytes); err == nil {
				out.PNG, out.Scale = frame, scale
			}
		}
	}
	if h.d.Elements != nil {
		if t, err := h.d.Elements.Tree(ctx, 0); err == nil {
			out.Tree = &t
		}
	}
	if out.PNG == nil {
		out.Note = "no screen: the verifier saw nothing"
	}
	h.reply(ctx, req.ID, machframes.TypeVerifyOut, out)
}

// verifyOut is the done-verifier's answer: what the machine sees
// now, so a task's success is judged on the live screen.
type verifyOut struct {
	PNG   []byte         `json:"png,omitempty"`
	Scale float64        `json:"scale,omitempty"`
	Tree  *elements.Tree `json:"tree,omitempty"`
	Note  string         `json:"note,omitempty"`
}

// verifyMaxBytes caps the verifier's capture. The tree rides in the
// same frame, so the capture keeps well under the link's frame limit.
const verifyMaxBytes = 2 << 20

// reply answers a request with a typed frame under the same id, so
// serve correlates answers machframes-local. Replies serialize on the
// dial's own Send lock.
func (h *Helper) reply(_ context.Context, id, typ string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	f := link.Frame{V: link.Version, Type: typ, ID: id, Data: b}
	if _, err := link.Encode(f); err != nil && typ != machframes.TypeFailure {
		// Over the frame limit: say so now instead of leaving serve
		// to time out on an answer that never left.
		reason, _ := json.Marshal(machframes.Failure{Reason: "answer is too large for the link"})
		f = link.Frame{V: link.Version, Type: machframes.TypeFailure, ID: id, Data: reason}
	}
	_ = h.dial.Send(f)
}

// fail refuses a request with the reason.
func (h *Helper) fail(ctx context.Context, id, reason string) {
	h.reply(ctx, id, machframes.TypeFailure, machframes.Failure{Reason: reason})
}
