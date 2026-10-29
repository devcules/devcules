package pty_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/pty"
)

const linkToken = "test-runner-token-no-whitespace"

// livePair wires a real hub, a real dial client and the pty link layer
// with a tmux-less manager: every request answers term.error, but the
// full request/reply correlation runs for real. Replies arrive on the
// shared channel under their request id.
func livePair(t *testing.T) (*hub.Hub, context.CancelFunc, <-chan link.Frame) {
	t.Helper()
	h := hub.New(hub.HashToken(linkToken), hub.Options{HelloTimeout: 5 * time.Second})
	s := linktest.Serve(t, h.Handler())
	c := dial.New("ws://pipe/link", "r1", "test", linkToken, dial.Options{
		HTTPClient: s.Client(),
		MinBackoff: 10 * time.Millisecond,
		MaxBackoff: 50 * time.Millisecond,
	})
	replies := make(chan link.Frame, 32)
	h.On(terminals.TypeOK, func(_ context.Context, _ string, f link.Frame) { replies <- f })
	h.On(terminals.TypeError, func(_ context.Context, _ string, f link.Frame) { replies <- f })
	h.On(terminals.TypeListed, func(_ context.Context, _ string, f link.Frame) { replies <- f })
	mgr, err := pty.NewForTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("dial Run did not return")
		}
	})
	// Serve registers its handlers before it returns, so they are in
	// place before the first request below.
	pty.Serve(ctx, c, mgr, nil)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// The hub registers a link just after its welcome, so wait
		// for the hub's view, not the dialer's.
		if c.Linked() && h.Count() == 1 {
			return h, stop, replies
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("dial never linked")
	return nil, nil, nil
}

// request sends one term.* frame from the hub side and awaits the
// correlated term.ok / term.error / term.listed (same frame id).
func request(t *testing.T, h *hub.Hub, replies <-chan link.Frame, typ, id string, data any) link.Frame {
	t.Helper()
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	f, err := link.Encode(link.Frame{V: link.Version, Type: typ, ID: id, Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	back, err := link.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SendTo("r1", back); err != nil {
		t.Fatalf("SendTo: %s", err)
	}
	select {
	case f := <-replies:
		if f.ID != id {
			t.Fatalf("reply id = %q, want %q (replies correlate terminals-local)", f.ID, id)
		}
		return f
	case <-time.After(10 * time.Second):
		t.Fatal("no reply within the relay timeout")
		return link.Frame{}
	}
}

// TestLinkErrorPaths walks every request with bad ids and unknown
// targets: without tmux each answers term.error with a reason, and
// every answer carries the request's id.
func TestLinkErrorPaths(t *testing.T) {
	t.Parallel()
	h, _, replies := livePair(t)
	id := "0123456789abcdef"
	// unknown stays missing on every run: with tmux installed the
	// "open" case creates id above, so negatives use another id.
	unknown := "abcdef0123456789"
	// Tmux-dependent requests fail without tmux and succeed with it;
	// validation failures fail everywhere. The suite runs both ways
	// (skip-gated tmux locally, installed in CI).
	tmuxOK := pty.HasTmux()
	cases := []struct {
		name    string
		typ     string
		data    any
		wantErr bool
	}{
		{"open bad id", terminals.TypeOpen, terminals.Open{Term: "bad"}, true},
		{"open", terminals.TypeOpen, terminals.Open{Term: id}, !tmuxOK},
		{"attach bad term", terminals.TypeAttach, terminals.Attach{Term: "bad", Stream: id, Cols: 80, Rows: 24}, true},
		{"attach unknown term", terminals.TypeAttach, terminals.Attach{Term: unknown, Stream: id, Cols: 80, Rows: 24}, true},
		{"input bad stream", terminals.TypeInput, terminals.Input{Stream: "bad", B64: "aGk="}, true},
		{"input unknown stream", terminals.TypeInput, terminals.Input{Stream: id, B64: "aGk="}, true},
		{"input bad b64", terminals.TypeInput, terminals.Input{Stream: id, B64: "!!!"}, true},
		{"resize unknown", terminals.TypeResize, terminals.Resize{Stream: id, Cols: 80, Rows: 24}, true},
		{"detach unknown", terminals.TypeDetach, terminals.Detach{Stream: id}, false},
		{"close bad id", terminals.TypeClose, terminals.Close{Term: "bad"}, true},
		// Close stays idempotent: a missing session is success, so
		// this answers ok with and without tmux.
		{"close", terminals.TypeClose, terminals.Close{Term: id}, false},
		{"list", terminals.TypeList, struct{}{}, !tmuxOK},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Sequential within the test: one shared reply channel
			// needs request/response pairing in order.
			rid := fmt.Sprintf("req%013d", i)
			f := request(t, h, replies, tc.typ, rid, tc.data)
			if tc.wantErr && f.Type != terminals.TypeError {
				t.Fatalf("%s: reply = %s, want term.error", tc.typ, f.Type)
			}
			if !tc.wantErr && f.Type != terminals.TypeOK && f.Type != terminals.TypeListed {
				t.Fatalf("%s: reply = %s, want ok", tc.typ, f.Type)
			}
			if f.Type == terminals.TypeError {
				var failure terminals.Failure
				if err := json.Unmarshal(f.Data, &failure); err != nil || failure.Reason == "" {
					t.Fatalf("%s: error data = %s, want a reason", tc.typ, f.Data)
				}
			}
		})
	}
}
