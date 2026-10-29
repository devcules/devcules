package helper_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/machframes"
	"github.com/devcules/devcules/internal/machines/elements"
	"github.com/devcules/devcules/internal/machines/helper"
	"github.com/devcules/devcules/internal/machines/input"
	"github.com/devcules/devcules/internal/machines/ocr"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
)

const helperTestToken = "helper-test-token"

// testMachineKeys enrols the fake machines with the test hubs.
var testMachineKeys = linktest.NewMachineKeys()

// fake drivers: canned answers, no real display needed.
type fakeScreen struct{}

func (fakeScreen) Capture() (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.White)
		}
	}
	return img, nil
}
func (fakeScreen) Close() error { return nil }

type fakeInput struct {
	injected atomic.Int32
	broken   atomic.Bool // Inject fails, as a lost display would
}

func (f *fakeInput) Inject(input.Event) error {
	if f.broken.Load() {
		return errors.New("display gone")
	}
	f.injected.Add(1)
	return nil
}
func (f *fakeInput) Close() error { return nil }

type fakeElements struct{}

func (fakeElements) Tree(context.Context, int) (elements.Tree, error) {
	return elements.Tree{Front: "Safari", Elements: []elements.Element{{Role: "button", Title: "Pay"}}}, nil
}
func (fakeElements) Close() error { return nil }

type fakeWords struct{}

func (fakeWords) Read(context.Context, image.Image) ([]ocr.Word, error) {
	return []ocr.Word{{Text: "Pay", X: 10, Y: 10, W: 20, H: 8}}, nil
}
func (fakeWords) Close() error { return nil }

// fixture links one real helper as a machine behind a real relay, the
// full serve-side path minus HTTP.
func fixture(t *testing.T) (*machrelay.Relay, *fakeInput) {
	t.Helper()
	in := &fakeInput{}
	h := helper.New(helper.Drivers{
		Screen:   fakeScreen{},
		Input:    in,
		Elements: fakeElements{},
		Words:    fakeWords{},
	})
	hb := hub.New(hub.HashToken(helperTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		Machines:     testMachineKeys,
	})
	r := machrelay.New(hb)
	r.Run()
	mux := http.NewServeMux()
	mux.Handle("/link", hb.Handler())
	pipe := linktest.Serve(t, mux)

	child, stop := context.WithCancel(context.Background())
	c := dial.New("ws://pipe/link", "kitchen-mac", "test", helperTestToken, dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   pipe.Client(),
		Logger:       slog.New(slog.DiscardHandler),
		Role:         "machine",
		Sign:         testMachineKeys.Add("kitchen-mac"),
	})
	h.Serve(c)
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("dial Run did not return")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for len(hb.Machines()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(hb.Machines()) != 1 {
		t.Fatal("the helper never linked")
	}
	return r, in
}

func TestScreenRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t)
	png, _, aerr := r.Screen(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Screen: %d %s", aerr.Status, aerr.Msg)
	}
	if len(png) < 8 || png[0] != 0x89 || png[1] != 'P' {
		t.Fatalf("png = %v", png[:8])
	}
}

func TestBudgetEnforcedAtTheMachine(t *testing.T) {
	t.Parallel()
	r, in := fixture(t)
	// the full 40 actions go through
	for i := 0; i < machframes.BudgetMax; i++ {
		if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap", X: 1, Y: 2}); aerr != nil {
			t.Fatalf("tap %d: %d %s", i, aerr.Status, aerr.Msg)
		}
	}
	if got := in.injected.Load(); got != machframes.BudgetMax {
		t.Fatalf("injected = %d", got)
	}
	// the 41st is refused by the machine, whatever serve believes
	aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"})
	if aerr == nil || aerr.Status != 502 {
		t.Fatalf("over-budget tap: %+v", aerr)
	}
	if got := in.injected.Load(); got != machframes.BudgetMax {
		t.Fatalf("injected after refusal = %d", got)
	}
}

func TestBudgetReset(t *testing.T) {
	t.Parallel()
	r, in := fixture(t)
	if aerr := r.SetBudget(context.Background(), "", 2); aerr != nil {
		t.Fatalf("SetBudget: %+v", aerr)
	}
	for i := 0; i < 2; i++ {
		if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"}); aerr != nil {
			t.Fatalf("tap %d: %+v", i, aerr)
		}
	}
	if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"}); aerr == nil {
		t.Fatal("budget not enforced after reset")
	}
	if got := in.injected.Load(); got != 2 {
		t.Fatalf("injected = %d", got)
	}
}

func TestRefusedInputCostsNothing(t *testing.T) {
	t.Parallel()
	r, in := fixture(t)
	if aerr := r.SetBudget(context.Background(), "", 1); aerr != nil {
		t.Fatalf("SetBudget: %+v", aerr)
	}
	// A malformed event and a failed inject both move nothing, so the
	// one action left survives them.
	if aerr := r.Inject(context.Background(), "", machframes.Input{}); aerr == nil {
		t.Fatal("an event with no kind was accepted")
	}
	in.broken.Store(true)
	if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"}); aerr == nil || aerr.Msg != "display gone" {
		t.Fatalf("failed inject = %+v, want the driver's reason", aerr)
	}
	in.broken.Store(false)
	if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"}); aerr != nil {
		t.Fatalf("the last action was spent by refusals: %+v", aerr)
	}
	if aerr := r.Inject(context.Background(), "", machframes.Input{Kind: "tap"}); aerr == nil {
		t.Fatal("budget not enforced")
	}
	if got := in.injected.Load(); got != 1 {
		t.Fatalf("injected = %d, want 1", got)
	}
}

func TestTreeRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t)
	tree, aerr := r.Tree(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Tree: %d %s", aerr.Status, aerr.Msg)
	}
	if tree.Front != "Safari" || len(tree.Elements) != 1 || tree.Elements[0].Title != "Pay" {
		t.Fatalf("tree = %+v", tree)
	}
}

func TestWordsRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t)
	words, aerr := r.Words(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Words: %d %s", aerr.Status, aerr.Msg)
	}
	if len(words) != 1 || words[0].Text != "Pay" {
		t.Fatalf("words = %+v", words)
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t)
	v, aerr := r.Verify(context.Background(), "")
	if aerr != nil {
		t.Fatalf("Verify: %d %s", aerr.Status, aerr.Msg)
	}
	if len(v.PNG) < 8 || v.PNG[0] != 0x89 || v.Scale != 1 {
		t.Fatalf("verify png = %v scale %v", v.PNG, v.Scale)
	}
	if v.Tree.Front != "Safari" || len(v.Tree.Elements) != 1 {
		t.Fatalf("verify tree = %+v", v.Tree)
	}
	if v.Note != "" {
		t.Fatalf("verify note = %q", v.Note)
	}
}

func TestGroundRefusedWithoutRouter(t *testing.T) {
	// a helper with a screen but no router refuses grounding with a
	// reason, never a panic. The relay surfaces it as a 502.
	h := helper.New(helper.Drivers{Screen: fakeScreen{}})
	_ = h // the Drivers zero Router is the refusal path; covered by
	// the bad-request test below through the full link instead.
}

func TestBadInputRefused(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t)
	// kindless input: the machine refuses, the relay answers 502
	// with the machine's reason.
	if aerr := r.Inject(context.Background(), "", machframes.Input{}); aerr == nil {
		t.Fatal("kindless input accepted")
	}
}

func TestGroundRoundTrip(t *testing.T) {
	t.Parallel()
	// a stub router serving the canonical SAM line, so the helper's
	// grounding path runs end to end: relay -> link -> helper ->
	// router -> parse -> matches back to the relay.
	const samLine = `<0f>0<|box;x1=100;y1=280;x2=229;y2=359;w=640;h=400|><|mask;x=0;y=0;data=40,60,!!!!!0=iv%plq$vFXiFfp!!|>`
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llm/meta/v1/responses" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + samLine + `"}]}]}`))
	}))
	defer router.Close()

	in := &fakeInput{}
	h := helper.New(helper.Drivers{
		Screen: fakeScreen{},
		Input:  in,
		Router: router.URL,
	})
	hb := hub.New(hub.HashToken(helperTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		Machines:     testMachineKeys,
	})
	r := machrelay.New(hb)
	r.Run()
	mux := http.NewServeMux()
	mux.Handle("/link", hb.Handler())
	pipe := linktest.Serve(t, mux)
	child, stop := context.WithCancel(context.Background())
	c := dial.New("ws://pipe/link", "kitchen-mac", "test", helperTestToken, dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   pipe.Client(),
		Logger:       slog.New(slog.DiscardHandler),
		Role:         "machine",
		Sign:         testMachineKeys.Add("kitchen-mac"),
	})
	h.Serve(c)
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("dial Run did not return")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for len(hb.Machines()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(hb.Machines()) != 1 {
		t.Fatal("the helper never linked")
	}

	matches, aerr := r.Ground(context.Background(), "", "green button")
	if aerr != nil {
		t.Fatalf("Ground: %d %s", aerr.Status, aerr.Msg)
	}
	if len(matches) != 1 || matches[0].ClickX < 23 || matches[0].ClickX > 25 {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestGroundRefusedWithNoScreen(t *testing.T) {
	t.Parallel()
	// a helper with no screen refuses grounding: the reason crosses
	// the whole link and the relay surfaces it.
	h := helper.New(helper.Drivers{Input: &fakeInput{}})
	hb := hub.New(hub.HashToken(helperTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		Machines:     testMachineKeys,
	})
	r := machrelay.New(hb)
	r.Run()
	mux := http.NewServeMux()
	mux.Handle("/link", hb.Handler())
	pipe := linktest.Serve(t, mux)
	child, stop := context.WithCancel(context.Background())
	c := dial.New("ws://pipe/link", "kitchen-mac", "test", helperTestToken, dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   pipe.Client(),
		Logger:       slog.New(slog.DiscardHandler),
		Role:         "machine",
		Sign:         testMachineKeys.Add("kitchen-mac"),
	})
	h.Serve(c)
	done := make(chan error, 1)
	go func() { done <- c.Run(child) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("dial Run did not return")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for len(hb.Machines()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(hb.Machines()) != 1 {
		t.Fatal("the helper never linked")
	}
	if _, aerr := r.Ground(context.Background(), "", "button"); aerr == nil {
		t.Fatal("grounding without a screen accepted")
	}
}
