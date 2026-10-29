package machrelay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/machframes"
)

const relayTestToken = "machrelay-test-token-0123456789"

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func testHub() *hub.Hub {
	return hub.New(hub.HashToken(relayTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
	})
}

// fakeMachine is a dial client answering canned machframes replies,
// standing in for the real helper: each handler answers under the
// request's id, exactly as helper.Serve does.
type fakeMachine struct {
	t      *testing.T
	latent time.Duration // reply delay for the timeout test

	mu       sync.Mutex
	dial     *dial.Client
	injected []machframes.Input
	budget   int
}

// runFakeMachine links a machine-role dialer on the pipe server and
// serves the canned machframes table. dial.New takes the role through
// Options (the hub keeps runner and machine memberships apart), so
// the fake shows up under hub.Machines() exactly like the helper.
func runFakeMachine(t *testing.T, s *linktest.Server, ctx context.Context, name string, h *hub.Hub) *fakeMachine {
	t.Helper()
	f := &fakeMachine{t: t, budget: machframes.BudgetMax}
	child, stop := context.WithCancel(ctx)
	c := dial.New("ws://pipe/link", name, "test", relayTestToken, dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   s.Client(),
		Logger:       discardLogger(),
		Role:         "machine",
	})
	f.dial = c
	f.serve(c)
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("dial Run did not return after context cancel")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for !c.Linked() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Linked() {
		t.Fatalf("machine %s never linked", name)
	}
	// The hub's view can lag the dialer's: a request racing it
	// answers "no machine linked" even though the link is up. Wait
	// for the hub to see this machine by name.
	for time.Now().Before(deadline) {
		for _, m := range h.Machines() {
			if m == name {
				return f
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the hub never saw machine %s", name)
	return f
}

// serve registers the canned machframes handlers.
func (f *fakeMachine) serve(c *dial.Client) {
	c.On(machframes.TypeScreen, func(ctx context.Context, req link.Frame) {
		var ask machframes.Screen
		_ = json.Unmarshal(req.Data, &ask)
		if ask.MaxBytes <= 0 {
			f.fail(ctx, req.ID, "bad screen request")
			return
		}
		// A 1x1 PNG: the smallest valid image.
		f.reply(ctx, req.ID, machframes.TypeFrame, machframes.FrameOut{PNG: []byte{0x89, 'P', 'N', 'G', 1}})
	})
	c.On(machframes.TypeInput, func(ctx context.Context, req link.Frame) {
		var ev machframes.Input
		if err := json.Unmarshal(req.Data, &ev); err != nil || ev.Kind == "" {
			f.fail(ctx, req.ID, "bad input event")
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.budget <= 0 {
			f.fail(ctx, req.ID, "budget exhausted")
			return
		}
		f.budget--
		f.injected = append(f.injected, ev)
		f.reply(ctx, req.ID, machframes.TypeOK, struct{}{})
	})
	c.On(machframes.TypeTree, func(ctx context.Context, req link.Frame) {
		f.reply(ctx, req.ID, machframes.TypeTreeOut, map[string]any{
			"screenW": 1920, "screenH": 1080, "front": "Safari",
			"elements": []map[string]any{{"role": "button", "x": 10, "y": 20, "w": 30, "h": 40, "title": "Pay"}},
		})
	})
	c.On(machframes.TypeWords, func(ctx context.Context, req link.Frame) {
		f.reply(ctx, req.ID, machframes.TypeWordsOut, []map[string]any{{"text": "Pay", "x": 1, "y": 2, "w": 3, "h": 4}})
	})
	c.On(machframes.TypeGround, func(ctx context.Context, req link.Frame) {
		var ask machframes.Ground
		if err := json.Unmarshal(req.Data, &ask); err != nil || ask.Phrase == "" {
			f.fail(ctx, req.ID, "bad ground request")
			return
		}
		f.reply(ctx, req.ID, machframes.TypeGroundOut, []map[string]any{{"id": 1, "x1": 5, "y1": 6, "x2": 7, "y2": 8, "maskW": 3, "maskH": 3, "clickX": 6, "clickY": 7}})
	})
	c.On(machframes.TypeBudget, func(ctx context.Context, req link.Frame) {
		var b machframes.Budget
		if err := json.Unmarshal(req.Data, &b); err != nil || b.Remaining < 0 || b.Remaining > machframes.BudgetMax {
			f.fail(ctx, req.ID, "bad budget")
			return
		}
		f.mu.Lock()
		f.budget = b.Remaining
		f.mu.Unlock()
		f.reply(ctx, req.ID, machframes.TypeOK, struct{}{})
	})
	c.On(machframes.TypeVerify, func(ctx context.Context, req link.Frame) {
		time.Sleep(f.latent)
		f.reply(ctx, req.ID, machframes.TypeVerifyOut, map[string]any{
			"png":  []byte{1, 2, 3},
			"tree": map[string]any{"front": "Safari", "elements": []map[string]any{{"role": "button", "x": 1, "y": 2, "w": 3, "h": 4}}},
			"note": "the verifier saw a screen",
		})
	})
}

// reply answers a request under its id, like helper.reply.
func (f *fakeMachine) reply(_ context.Context, id, typ string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		f.t.Errorf("fake machine marshal: %s", err)
		return
	}
	// Send errors mean the test ended first; that is fine.
	_ = f.dial.Send(link.Frame{V: link.Version, Type: typ, ID: id, Data: b})
}

func (f *fakeMachine) fail(ctx context.Context, id, reason string) {
	f.reply(ctx, id, machframes.TypeFailure, machframes.Failure{Reason: reason})
}

// injectedEvents returns the events the fake took.
func (f *fakeMachine) injectedEvents() []machframes.Input {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]machframes.Input(nil), f.injected...)
}

func (f *fakeMachine) remaining() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.budget
}

// machineRelayFixture builds a hub, a relay and one linked fake
// machine on a pipe server.
func machineRelayFixture(t *testing.T) (*Relay, *fakeMachine) {
	t.Helper()
	h := testHub()
	r := New(h)
	r.Run()
	mux := http.NewServeMux()
	mux.Handle("/link", h.Handler())
	pipe := linktest.Serve(t, mux)
	fake := runFakeMachine(t, pipe, context.Background(), "kitchen-mac", h)
	// The dial registers with the hub asynchronously; a request racing
	// it answers "no machine linked" even though the fixture is about
	// to have one. Wait for the hub's view, exactly like the terminal
	// tests wait for their runner.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.Machines()) == 1 {
			return r, fake
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the fake machine never linked")
	return r, fake
}

func TestScreenRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := machineRelayFixture(t)
	png, aerr := r.Screen(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Screen: %d %s", aerr.Status, aerr.Msg)
	}
	if len(png) == 0 || png[0] != 0x89 {
		t.Fatalf("Screen PNG = %v, want the canned bytes", png)
	}
}

func TestTreeRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := machineRelayFixture(t)
	tree, aerr := r.Tree(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Tree: %d %s", aerr.Status, aerr.Msg)
	}
	if tree.Front != "Safari" || len(tree.Elements) != 1 || tree.Elements[0].Title != "Pay" {
		t.Fatalf("Tree = %+v", tree)
	}
}

func TestWordsRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := machineRelayFixture(t)
	words, aerr := r.Words(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Words: %d %s", aerr.Status, aerr.Msg)
	}
	if len(words) != 1 || words[0].Text != "Pay" {
		t.Fatalf("Words = %+v", words)
	}
}

func TestGroundRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := machineRelayFixture(t)
	matches, aerr := r.Ground(context.Background(), "", "green Pay button")
	if aerr != nil {
		t.Fatalf("Ground: %d %s", aerr.Status, aerr.Msg)
	}
	if len(matches) != 1 || matches[0].ClickX != 6 || matches[0].ClickY != 7 {
		t.Fatalf("Ground = %+v", matches)
	}
}

func TestInjectRoundTripAndBudgetMirror(t *testing.T) {
	t.Parallel()
	r, fake := machineRelayFixture(t)
	if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap", X: 6, Y: 7}); aerr != nil {
		t.Fatalf("Inject: %d %s", aerr.Status, aerr.Msg)
	}
	events := fake.injectedEvents()
	if len(events) != 1 || events[0].Kind != "tap" || events[0].X != 6 {
		t.Fatalf("injected = %+v", events)
	}
	if got := r.BudgetRemaining(); got != machframes.BudgetMax-1 {
		t.Fatalf("BudgetRemaining = %d, want %d", got, machframes.BudgetMax-1)
	}
	if got := fake.remaining(); got != machframes.BudgetMax-1 {
		t.Fatalf("machine budget = %d, want %d", got, machframes.BudgetMax-1)
	}
	// SetBudget resets both the machine and the mirror.
	if aerr := r.SetBudget(context.Background(), "", 5); aerr != nil {
		t.Fatalf("SetBudget: %d %s", aerr.Status, aerr.Msg)
	}
	if got := r.BudgetRemaining(); got != 5 {
		t.Fatalf("BudgetRemaining after set = %d, want 5", got)
	}
	if got := fake.remaining(); got != 5 {
		t.Fatalf("machine budget after set = %d, want 5", got)
	}
}

func TestBudgetExhaustedRefusal(t *testing.T) {
	t.Parallel()
	r, fake := machineRelayFixture(t)
	if aerr := r.SetBudget(context.Background(), "", 1); aerr != nil {
		t.Fatalf("SetBudget: %d %s", aerr.Status, aerr.Msg)
	}
	if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"}); aerr != nil {
		t.Fatalf("first Inject: %d %s", aerr.Status, aerr.Msg)
	}
	aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"})
	if aerr == nil {
		t.Fatal("Inject past zero was accepted")
	}
	if aerr.Msg != "budget exhausted" {
		t.Fatalf("refusal = %q, want the machine's reason", aerr.Msg)
	}
	if got := fake.remaining(); got != 0 {
		t.Fatalf("machine budget = %d, want 0", got)
	}
	if events := fake.injectedEvents(); len(events) != 1 {
		t.Fatalf("injected = %+v, want exactly one", events)
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := machineRelayFixture(t)
	png, tree, note, aerr := r.Verify(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Verify: %d %s", aerr.Status, aerr.Msg)
	}
	if len(png) != 3 || tree.Front != "Safari" || note != "the verifier saw a screen" {
		t.Fatalf("Verify = %q %+v %q", png, tree, note)
	}
}

func TestUnknownMachineIs503(t *testing.T) {
	t.Parallel()
	h := testHub()
	r := New(h)
	r.Run()
	if _, aerr := r.Screen(context.Background(), ""); aerr == nil || aerr.Status != 503 {
		t.Fatalf("Screen with no machine = %+v, want 503", aerr)
	}
	if _, _, _, aerr := r.Verify(context.Background(), "nope"); aerr == nil || aerr.Status != 503 {
		t.Fatalf("Verify with no machine = %+v, want 503", aerr)
	}
}

func TestPickMachine(t *testing.T) {
	t.Parallel()
	h := testHub()
	r := New(h)
	r.Run()
	mux := http.NewServeMux()
	mux.Handle("/link", h.Handler())
	pipe := linktest.Serve(t, mux)
	ctx := context.Background()
	runFakeMachine(t, pipe, ctx, "alpha", h)

	// Only one: empty picks it.
	if name, aerr := r.PickMachine(""); aerr != nil || name != "alpha" {
		t.Fatalf("PickMachine(\"\") = %q, %+v", name, aerr)
	}
	if _, aerr := r.PickMachine("beta"); aerr == nil || aerr.Status != 404 {
		t.Fatalf("PickMachine(beta) = %+v, want 404", aerr)
	}

	runFakeMachine(t, pipe, ctx, "beta", h)
	if _, aerr := r.PickMachine(""); aerr == nil || aerr.Status != 400 {
		t.Fatalf("PickMachine with two and no choice = %+v, want 400", aerr)
	}
	if name, aerr := r.PickMachine("beta"); aerr != nil || name != "beta" {
		t.Fatalf("PickMachine(beta) = %q, %+v", name, aerr)
	}
	if _, aerr := r.PickMachine("gamma"); aerr == nil || aerr.Status != 404 {
		t.Fatalf("PickMachine(gamma) = %+v, want 404", aerr)
	}
}

func TestMachineTimeoutIs504(t *testing.T) {
	t.Parallel()
	h := testHub()
	r := New(h)
	r.Run()
	mux := http.NewServeMux()
	mux.Handle("/link", h.Handler())
	pipe := linktest.Serve(t, mux)
	// The fake answers verify after the 5 s round-trip timeout.
	fake := runFakeMachine(t, pipe, context.Background(), "slow", h)
	fake.latent = 6 * time.Second
	_, _, _, aerr := r.Verify(context.Background(), "")
	if aerr == nil || aerr.Status != 504 {
		t.Fatalf("Verify with a slow machine = %+v, want 504", aerr)
	}
}
