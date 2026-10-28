package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/terminals"
)

// Unit coverage for the tmux-independent paths: the link-level
// routing, queueing and validation run without a runner, a browser
// socket, or tmux. The socket-coupled paths (AttachStream pumps,
// slow/offline closes) ride the integration tests, which need tmux
// and run in CI.

func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	var out terminals.Output
	if err := strictDecode(json.RawMessage(`{"stream":"0123456789abcdef","b64":"aGk="}`)).Decode(&out); err != nil {
		t.Fatalf("valid output rejected: %s", err)
	}
	if err := strictDecode(json.RawMessage(`{"stream":"0123456789abcdef","b64":"aGk=","zzz":1}`)).Decode(&out); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := strictDecode(json.RawMessage(`{`)).Decode(&out); err == nil {
		t.Fatal("broken JSON accepted")
	}
}

func TestErrorFormats(t *testing.T) {
	t.Parallel()
	err := &Error{Status: 503, Msg: "no runner linked"}
	// Error carries the message only: the status travels beside it,
	// so handlers map it to HTTP codes without parsing text.
	if got := err.Error(); got != "no runner linked" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestRoundTripErrorMaps(t *testing.T) {
	t.Parallel()
	timeout := fmt.Errorf("runner did not answer within 5 s")
	if got := roundTripError(timeout); got.Status != 504 {
		t.Fatalf("timeout status = %d, want 504", got.Status)
	}
	if got := roundTripError(fmt.Errorf("boom")); got.Status != 502 {
		t.Fatalf("failure status = %d, want 502", got.Status)
	}
}

func TestOnReplyDeliversAndDrops(t *testing.T) {
	t.Parallel()
	r := New(testHub(), "")
	waiter := make(chan link.Frame, 1)
	r.mu.Lock()
	r.pending["req1"] = waiter
	r.mu.Unlock()

	reply := link.Frame{V: 1, Type: terminals.TypeOK, ID: "req1", Data: json.RawMessage(`{}`)}
	r.onReply(context.Background(), "r1", reply)
	select {
	case got := <-waiter:
		if got.ID != "req1" {
			t.Fatalf("reply id = %q", got.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never got the reply")
	}

	// Late replies (no waiter) are dropped, never block.
	r.onReply(context.Background(), "r1", reply)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) != 0 {
		t.Fatal("late reply left a waiter behind")
	}
}

func TestForgetIsIdempotent(t *testing.T) {
	t.Parallel()
	r := New(testHub(), "")
	s := &stream{runner: "r1", term: "t", id: "s", toUI: make(chan []byte, 1), closed: make(chan struct{})}
	r.mu.Lock()
	r.streams["s"] = s
	r.mu.Unlock()
	r.forget("s")
	r.forget("s")
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.streams) != 0 {
		t.Fatal("forget left the stream behind")
	}
	select {
	case <-s.closed:
	default:
		t.Fatal("forget never signalled close")
	}
}

func TestSendNeedsRunner(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := New(h, "")
	s := fixture(t, h, r)
	defer s.Close()

	if err := r.send("nobody", terminals.TypeDetach, terminals.Detach{Stream: "0123456789abcdef"}); err == nil {
		t.Fatal("send to an unlinked runner succeeded")
	}
	_, stopDial := runDialer(t, s, ctx, "r1")
	defer stopDial()
	if err := r.send("r1", terminals.TypeDetach, terminals.Detach{Stream: "0123456789abcdef"}); err != nil {
		t.Fatalf("send to a linked runner: %s", err)
	}
}

func TestMustFrameRoundTrips(t *testing.T) {
	t.Parallel()
	f := mustFrame(terminals.TypeInput, []byte(`{}`))
	if f.Type != terminals.TypeInput {
		t.Fatalf("frame type = %q", f.Type)
	}
}

func TestOriginPatterns(t *testing.T) {
	t.Parallel()
	if got := originPatterns(&http.Request{}, ""); got != nil {
		t.Fatalf("empty public URL = %v, want nil", got)
	}
	if got := originPatterns(&http.Request{}, "https://term.example.com"); len(got) == 0 {
		t.Fatal("public URL gave no patterns")
	}
	if got := originPatterns(&http.Request{}, ":::bad"); got != nil {
		t.Fatalf("bad public URL = %v, want nil", got)
	}
}

func outputFrame(t *testing.T, stream, b64 string) link.Frame {
	t.Helper()
	data, err := json.Marshal(terminals.Output{Stream: stream, B64: b64})
	if err != nil {
		t.Fatal(err)
	}
	return link.Frame{V: 1, Type: terminals.TypeOutput, ID: "o1", Data: data}
}

func TestOnOutputRoutes(t *testing.T) {
	t.Parallel()
	r := New(testHub(), "")
	payload := base64.StdEncoding.EncodeToString([]byte("hi"))

	// Unknown streams and broken frames vanish silently.
	r.onOutput(context.Background(), "r1", outputFrame(t, "0123456789abcdef", payload))
	r.onOutput(context.Background(), "r1", link.Frame{V: 1, Type: terminals.TypeOutput, ID: "o2", Data: json.RawMessage(`{`)})
	r.onOutput(context.Background(), "r1", outputFrame(t, "bogus", payload))

	// Stream ids are 16 hex chars end to end (DecodeOutput rejects
	// anything else), so the map keys are real ids here.
	const live = "aaaaaaaaaaaaaaaa"
	const shut = "bbbbbbbbbbbbbbbb"
	s := &stream{runner: "r1", term: "t", id: live, toUI: make(chan []byte, maxQueueFrames), closed: make(chan struct{})}
	r.mu.Lock()
	r.streams[live] = s
	r.mu.Unlock()

	// Wrong runner: another runner's bytes never land here.
	r.onOutput(context.Background(), "r2", outputFrame(t, live, payload))
	// A closed stream reads nothing more.
	closed := &stream{runner: "r1", term: "t", id: shut, toUI: make(chan []byte, maxQueueFrames), closed: make(chan struct{})}
	close(closed.closed)
	r.mu.Lock()
	r.streams[shut] = closed
	r.mu.Unlock()
	r.onOutput(context.Background(), "r1", outputFrame(t, shut, payload))

	r.onOutput(context.Background(), "r1", outputFrame(t, live, payload))
	select {
	case got := <-s.toUI:
		if string(got) != "hi" {
			t.Fatalf("queued = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("output never queued")
	}
}

func TestOnExitIgnoresStrangers(t *testing.T) {
	t.Parallel()
	r := New(testHub(), "")
	exit := func(stream string) link.Frame {
		data, _ := json.Marshal(terminals.Exit{Stream: stream})
		return link.Frame{V: 1, Type: terminals.TypeExit, ID: "e1", Data: data}
	}
	// Broken frames, bad ids and unknown streams return before any
	// socket work (the happy path needs a real browser conn: CI).
	r.onExit(context.Background(), "r1", link.Frame{V: 1, Type: terminals.TypeExit, ID: "e2", Data: json.RawMessage(`{`)})
	r.onExit(context.Background(), "r1", exit("bogus"))
	r.onExit(context.Background(), "r1", exit("0123456789abcdef"))

	s := &stream{runner: "r2", term: "t", id: "s1", toUI: make(chan []byte, 1), closed: make(chan struct{})}
	r.mu.Lock()
	r.streams["s1"] = s
	r.mu.Unlock()
	r.onExit(context.Background(), "r1", exit("s1"))
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.streams["s1"]; !ok {
		t.Fatal("another runner's exit evicted the stream")
	}
}

func TestListTimeout504(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	_, stopDial := runDialer(t, s, ctx, "silent")
	defer stopDial()
	waitLinked(t, h, 1)

	if _, err := r.List(ctx, "silent"); err == nil || err.Status != 504 {
		t.Fatalf("List with silent runner: err = %+v, want 504", err)
	}
}

func TestDeleteTimeout504(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := testHub()
	r := startRelay(t, h)
	s := fixture(t, h, r)
	defer s.Close()
	_, stopDial := runDialer(t, s, ctx, "silent")
	defer stopDial()
	waitLinked(t, h, 1)

	// Delete shares the request() ceiling with Open and List: a
	// runner that never answers surfaces as 504, not a hang.
	if err := r.Delete(ctx, "silent", "0123456789abcdef"); err == nil || err.Status != 504 {
		t.Fatalf("Delete with silent runner: err = %+v, want 504", err)
	}
}
