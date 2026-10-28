package pty

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/dial"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/terminals"
)

// TestPumpBatching drives the output pump with an os pipe instead of a
// PTY: a small write flushes on the 10 ms timer, a 40 KiB write flushes
// at the 32 KiB boundary plus a timer remainder, and closing the pipe
// ends the stream with term.exit. No tmux needed; the batching
// contract is fully covered.
func TestPumpBatching(t *testing.T) {
	t.Parallel()
	h := hub.New(hub.HashToken("pump-token"), hub.Options{HelloTimeout: 5 * time.Second})
	s := linktest.Serve(t, h.Handler())
	c := dial.New("ws://pipe/link", "r1", "test", "pump-token", dial.Options{
		HTTPClient: s.Client(),
		MinBackoff: 10 * time.Millisecond,
		MaxBackoff: 50 * time.Millisecond,
	})
	outputs := make(chan terminals.Output, 32)
	exits := make(chan terminals.Exit, 4)
	h.On(terminals.TypeOutput, func(_ context.Context, _ string, f link.Frame) {
		var o terminals.Output
		if err := json.Unmarshal(f.Data, &o); err != nil {
			t.Errorf("bad output data: %s", err)
			return
		}
		if o.Stream != "2222222222222222" {
			t.Errorf("output stream = %q", o.Stream)
			return
		}
		outputs <- o
	})
	h.On(terminals.TypeExit, func(_ context.Context, _ string, f link.Frame) {
		var e terminals.Exit
		if err := json.Unmarshal(f.Data, &e); err != nil {
			t.Errorf("bad exit data: %s", err)
			return
		}
		exits <- e
	})
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
	deadline := time.Now().Add(10 * time.Second)
	for !c.Linked() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Linked() {
		t.Fatal("dial never linked")
	}

	mgr, err := NewForTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	lr := &linkRunner{d: c, m: mgr}
	a := newAttach("1111111111111111", "2222222222222222", r, nil)
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		lr.pump(a)
	}()

	// Small write: exactly one frame via the timer flush.
	small := []byte("hi-42\n")
	if _, err := w.Write(small); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-outputs:
		raw, err := base64.StdEncoding.DecodeString(o.B64)
		if err != nil {
			t.Fatalf("output b64: %s", err)
		}
		if string(raw) != string(small) {
			t.Fatalf("output = %q, want %q", raw, small)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timer flush never arrived")
	}

	// 40 KiB write: boundary flush plus timer remainder, same bytes.
	big := make([]byte, 40<<10)
	for i := range big {
		big[i] = byte(i)
	}
	if _, err := w.Write(big); err != nil {
		t.Fatal(err)
	}
	var got []byte
	boundary := time.Now().Add(5 * time.Second)
	for len(got) < len(big) && time.Now().Before(boundary) {
		select {
		case o := <-outputs:
			raw, err := base64.StdEncoding.DecodeString(o.B64)
			if err != nil {
				t.Fatalf("output b64: %s", err)
			}
			got = append(got, raw...)
		case <-time.After(5 * time.Second):
			t.Fatalf("stalled after %d of %d bytes", len(got), len(big))
		}
	}
	if string(got) != string(big) {
		t.Fatalf("batched %d bytes differ", len(got))
	}

	// Closing the pipe ends the stream with term.exit.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-exits:
		if e.Stream != "2222222222222222" {
			t.Fatalf("exit stream = %q", e.Stream)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("term.exit never arrived")
	}
	select {
	case <-pumpDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pump did not return after EOF")
	}
}
