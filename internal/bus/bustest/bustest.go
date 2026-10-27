package bustest

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/bus"
)

// Presence waits use a deadline; absence needs none (a correct
// implementation sends nothing, whenever you look). Failure branches live
// in these helpers once — the broken-bus probe run exercises them.
func mustf(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %s", msg, err)
	}
}

func check(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Fatalf("want %s", msg)
	}
}

func isClosed(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, bus.ErrClosed) {
		t.Fatalf("%s = %v, want an error wrapping ErrClosed", msg, err)
	}
}

func isBadTopic(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, bus.ErrBadTopic) {
		t.Fatalf("%s = %v, want an error wrapping ErrBadTopic", msg, err)
	}
}

// recvTimeout is how long recv waits for a message that should arrive. It
// is a variable so the broken-bus probe can fail fast instead of waiting it
// out in every subtest.
var recvTimeout = 5 * time.Second

// recv returns the next message, failing on close or deadline.
func recv(t *testing.T, ch <-chan []byte, what string) []byte {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatalf("%s: channel closed, want a message", what)
		}
		return msg
	case <-time.After(recvTimeout):
		t.Fatalf("%s: timed out waiting for a message", what)
		return nil
	}
}

// expectSilent fails on any message or close: with a correct
// implementation nothing can arrive, so no timing is involved.
func expectSilent(t *testing.T, ch <-chan []byte, what string) {
	t.Helper()
	select {
	case msg, ok := <-ch:
		t.Fatalf("%s: got %q (open=%v), want silence", what, msg, ok)
	case <-time.After(200 * time.Millisecond):
	}
}

// waitFor polls cond until true or the deadline, for asserting the absence
// of leaked goroutines without sleeping through ordering.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// Run executes the shared bus.Bus contract suite. newBus returns an
// isolated bus per call. Implementations call it once from their own test.
func Run(t *testing.T, newBus func(t *testing.T) bus.Bus) {
	t.Helper()
	ctx := context.Background()

	t.Run("FanOut", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		const topic = "fanout"
		subs := make([]<-chan []byte, 3)
		for i := range subs {
			ch, err := b.Subscribe(ctx, topic)
			mustf(t, err, "Subscribe")
			subs[i] = ch
		}
		for i := range 5 {
			mustf(t, b.Publish(ctx, topic, []byte(fmt.Sprintf("m%d", i))), "Publish")
		}
		for si, ch := range subs {
			for i := range 5 {
				got := recv(t, ch, fmt.Sprintf("subscriber %d message %d", si, i))
				check(t, string(got) == fmt.Sprintf("m%d", i), fmt.Sprintf("subscriber %d got %q in order", si, got))
			}
		}
	})

	t.Run("NoCrossTopicLeak", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		ch, err := b.Subscribe(ctx, "a")
		mustf(t, err, "Subscribe")
		mustf(t, b.Publish(ctx, "b", []byte("x")), "Publish other topic")
		expectSilent(t, ch, "other topic")
	})

	t.Run("LateMissesHistory", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		const topic = "late"
		for i := range 3 {
			mustf(t, b.Publish(ctx, topic, []byte(fmt.Sprintf("old%d", i))), "Publish before subscribe")
		}
		ch, err := b.Subscribe(ctx, topic)
		mustf(t, err, "Subscribe")
		expectSilent(t, ch, "pre-subscribe messages")
		mustf(t, b.Publish(ctx, topic, []byte("new")), "Publish after subscribe")
		check(t, string(recv(t, ch, "new message")) == "new", "only post-subscribe message arrives")
	})

	t.Run("CopyIsolation", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		const topic = "copy"
		a, err := b.Subscribe(ctx, topic)
		mustf(t, err, "Subscribe A")
		c, err := b.Subscribe(ctx, topic)
		mustf(t, err, "Subscribe B")
		msg := []byte("hello")
		mustf(t, b.Publish(ctx, topic, msg), "Publish")
		msg[0] = 'X'
		gotA := recv(t, a, "A")
		check(t, string(gotA) == "hello", fmt.Sprintf("caller reuse leaked into A: %q", gotA))
		gotA[0] = 'Y'
		gotB := recv(t, c, "B")
		check(t, string(gotB) == "hello", fmt.Sprintf("A corrupted B: %q", gotB))
	})

	t.Run("SlowDrop", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		const topic = "slow"
		slow, err := b.Subscribe(ctx, topic)
		mustf(t, err, "Subscribe slow")
		fast, err := b.Subscribe(ctx, topic)
		mustf(t, err, "Subscribe fast")
		// No reader drains during the burst: Publish must still return,
		// or the test fails fast instead of hanging on the bug.
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := range 200 {
				_ = b.Publish(ctx, topic, []byte(fmt.Sprintf("m%03d", i)))
			}
		}()
		select {
		case <-done:
		case <-time.After(3 * recvTimeout):
			t.Fatal("Publish blocked on a full subscriber buffer")
		}
		// Both buffers hold exactly the first 64 in order; the rest
		// dropped identically for each subscriber.
		for name, ch := range map[string]<-chan []byte{"slow": slow, "fast": fast} {
			for i := range 64 {
				got := recv(t, ch, fmt.Sprintf("%s message %d", name, i))
				check(t, string(got) == fmt.Sprintf("m%03d", i), fmt.Sprintf("%s order broke at %d", name, i))
			}
			expectSilent(t, ch, name+" past the buffer")
		}
	})

	t.Run("UnsubscribeCloses", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		subCtx, stop := context.WithCancel(ctx)
		ch, err := b.Subscribe(subCtx, "unsub")
		mustf(t, err, "Subscribe")
		stop()
		select {
		case _, ok := <-ch:
			check(t, !ok, "unsubscribed channel stays open")
		case <-time.After(recvTimeout):
			t.Fatal("unsubscribed channel never closed")
		}
	})

	t.Run("CloseClosesAll", func(t *testing.T) {
		b := newBus(t)
		ch1, err := b.Subscribe(ctx, "t1")
		mustf(t, err, "Subscribe t1")
		ch2, err := b.Subscribe(ctx, "t2")
		mustf(t, err, "Subscribe t2")
		mustf(t, b.Close(), "Close")
		mustf(t, b.Close(), "second Close (must be a no-op)")
		for i, ch := range []<-chan []byte{ch1, ch2} {
			select {
			case _, ok := <-ch:
				check(t, !ok, fmt.Sprintf("channel %d stays open after Close", i))
			case <-time.After(recvTimeout):
				t.Fatalf("channel %d never closed by Close", i)
			}
		}
		_, err = b.Subscribe(ctx, "t1")
		isClosed(t, err, "Subscribe after Close")
		isClosed(t, b.Publish(ctx, "t1", []byte("x")), "Publish after Close")
	})

	t.Run("BadTopic", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		isBadTopic(t, b.Publish(ctx, "", []byte("x")), "Publish empty topic")
		_, err := b.Subscribe(ctx, "")
		isBadTopic(t, err, "Subscribe empty topic")
	})

	t.Run("CanceledCtx", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		canceled, stop := context.WithCancel(ctx)
		stop()
		if err := b.Publish(canceled, "t", []byte("x")); !errors.Is(err, context.Canceled) {
			t.Fatalf("Publish with canceled ctx = %v, want context.Canceled", err)
		}
		if _, err := b.Subscribe(canceled, "t"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe with canceled ctx = %v, want context.Canceled", err)
		}
	})

	t.Run("Concurrent", func(t *testing.T) {
		b := newBus(t)
		defer b.Close()
		const topic = "concurrent"
		// 40 messages total stays under every buffer even if a reader
		// stalls for the whole burst, so every subscriber gets all of
		// them deterministically.
		const pubs, per = 4, 10
		subs := make([]<-chan []byte, 4)
		for i := range subs {
			ch, err := b.Subscribe(ctx, topic)
			mustf(t, err, "Subscribe")
			subs[i] = ch
		}
		var wg sync.WaitGroup
		errs := make(chan error, pubs)
		for p := range pubs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range per {
					if err := b.Publish(ctx, topic, []byte(fmt.Sprintf("p%d/%d", p, i))); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			mustf(t, err, "concurrent Publish")
		}
		for si, ch := range subs {
			seen := make(map[string]bool, pubs*per)
			for range pubs * per {
				seen[string(recv(t, ch, fmt.Sprintf("subscriber %d", si)))] = true
			}
			check(t, len(seen) == pubs*per, fmt.Sprintf("subscriber %d got %d distinct of %d", si, len(seen), pubs*per))
		}
	})

	t.Run("NoLeak", func(t *testing.T) {
		base := runtime.NumGoroutine()
		b := newBus(t)
		stops := make([]context.CancelFunc, 10)
		for i := range stops {
			subCtx, stop := context.WithCancel(ctx)
			stops[i] = stop
			if _, err := b.Subscribe(subCtx, "leak"); err != nil {
				t.Fatalf("Subscribe: %s", err)
			}
		}
		for _, stop := range stops {
			stop()
		}
		waitFor(t, 5*time.Second, func() bool { return runtime.NumGoroutine() <= base },
			"goroutine count back to baseline after unsubscribe")
		b2 := newBus(t)
		for range 10 {
			if _, err := b2.Subscribe(ctx, "leak"); err != nil {
				t.Fatalf("Subscribe: %s", err)
			}
		}
		mustf(t, b2.Close(), "Close")
		waitFor(t, 5*time.Second, func() bool { return runtime.NumGoroutine() <= base },
			"goroutine count back to baseline after Close")
		mustf(t, b.Close(), "Close first bus")
	})
}
