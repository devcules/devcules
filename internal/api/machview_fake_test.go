package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/machframes"
)

// machviewFake answers canned machframes replies under the request's
// id over one machine-role dial, mirroring helper.Serve's contract.
type machviewFake struct {
	t    *testing.T
	dial *dial.Client

	mu       sync.Mutex
	injected int
}

// linkFakeMachine links one machine-role dialer on the pipe server
// and serves the canned table; it stands in for the real helper.
func linkFakeMachine(t *testing.T, s *linktest.Server, name string) *machviewFake {
	t.Helper()
	f := &machviewFake{t: t}
	c := dial.New("ws://pipe/link", name, "test", machviewTestToken, dial.Options{
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		HTTPClient:   s.Client(),
		Logger:       slog.New(slog.DiscardHandler),
		Role:         "machine",
		Sign:         testMachineKeys.Add(name),
	})
	f.dial = c
	c.On(machframes.TypeScreen, func(ctx context.Context, req link.Frame) {
		f.reply(ctx, req.ID, machframes.TypeFrame, machframes.FrameOut{PNG: []byte{0x89, 'P', 'N', 'G', 1}})
	})
	c.On(machframes.TypeInput, func(ctx context.Context, req link.Frame) {
		f.mu.Lock()
		f.injected++
		f.mu.Unlock()
		f.reply(ctx, req.ID, machframes.TypeOK, struct{}{})
	})
	c.On(machframes.TypeTree, func(ctx context.Context, req link.Frame) {
		f.reply(ctx, req.ID, machframes.TypeTreeOut, map[string]any{
			"front": "Safari",
			"elements": []map[string]any{
				{"role": "button", "x": 10, "y": 20, "w": 30, "h": 40, "title": "Pay"},
			},
		})
	})
	c.On(machframes.TypeGround, func(ctx context.Context, req link.Frame) {
		f.reply(ctx, req.ID, machframes.TypeGroundOut, []map[string]any{
			{"id": 1, "x1": 5, "y1": 6, "x2": 7, "y2": 8, "maskW": 3, "maskH": 3, "clickX": 6, "clickY": 7},
		})
	})
	c.On(machframes.TypeVerify, func(ctx context.Context, req link.Frame) {
		f.reply(ctx, req.ID, machframes.TypeVerifyOut, map[string]any{
			"png":  []byte{1, 2, 3},
			"tree": map[string]any{"front": "Safari", "elements": []map[string]any{{"role": "button", "x": 1, "y": 2, "w": 3, "h": 4}}},
			"note": "the verifier saw a screen",
		})
	})
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
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
	return f
}

// reply answers a request under its id, like helper.reply.
func (f *machviewFake) reply(_ context.Context, id, typ string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		f.t.Errorf("fake machine marshal: %s", err)
		return
	}
	_ = f.dial.Send(link.Frame{V: link.Version, Type: typ, ID: id, Data: b})
}
