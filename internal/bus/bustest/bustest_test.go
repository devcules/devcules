package bustest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/bus"
)

// fakeBus is an independent in-memory Bus: same contract as memory, its
// own logic, so the suite has an anchor outside the implementation it
// usually guards.
type fakeBus struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	subs   map[string]map[chan []byte]struct{}
}

func newFakeBus() *fakeBus {
	return &fakeBus{done: make(chan struct{}), subs: make(map[string]map[chan []byte]struct{})}
}

func (f *fakeBus) Publish(ctx context.Context, topic string, msg []byte) error {
	if topic == "" {
		return fmt.Errorf("bus: publish: %w", bus.ErrBadTopic)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fmt.Errorf("bus: publish: %w", bus.ErrClosed)
	}
	for ch := range f.subs[topic] {
		select {
		case ch <- bytes.Clone(msg):
		default:
		}
	}
	return nil
}

func (f *fakeBus) Subscribe(ctx context.Context, topic string) (<-chan []byte, error) {
	if topic == "" {
		return nil, fmt.Errorf("bus: subscribe: %w", bus.ErrBadTopic)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, fmt.Errorf("bus: subscribe: %w", bus.ErrClosed)
	}
	ch := make(chan []byte, 64)
	if f.subs[topic] == nil {
		f.subs[topic] = make(map[chan []byte]struct{})
	}
	f.subs[topic][ch] = struct{}{}
	f.mu.Unlock()
	go f.watch(ctx, topic, ch)
	return ch, nil
}

func (f *fakeBus) watch(ctx context.Context, topic string, ch chan []byte) {
	select {
	case <-ctx.Done():
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.closed {
			return
		}
		delete(f.subs[topic], ch)
		close(ch)
	case <-f.done:
	}
}

func (f *fakeBus) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	for _, subs := range f.subs {
		for ch := range subs {
			close(ch)
		}
	}
	f.subs = make(map[string]map[chan []byte]struct{})
	close(f.done)
	return nil
}

// TestRunPassesOnFake proves the suite accepts a correct Bus.
func TestRunPassesOnFake(t *testing.T) {
	Run(t, func(t *testing.T) bus.Bus {
		t.Helper()
		return newFakeBus()
	})
}

// brokenBus delivers nothing: the suite must fail it.
type brokenBus struct {
	bus.Bus
}

func (b brokenBus) Publish(context.Context, string, []byte) error {
	return nil
}

// TestBusBrokenProbe runs the suite against the broken bus. It only runs
// inside the child process started by TestBusSuiteCatchesBroken: a failed
// subtest would taint the parent, so the failure must cross a process
// boundary to be assertable.
func TestBusBrokenProbe(t *testing.T) {
	if os.Getenv("DEVCULES_BUS_PROBE") != "1" {
		t.Skip("child-process probe; run via TestBusSuiteCatchesBroken")
	}
	// A broken bus delivers nothing: do not wait out the default in every subtest.
	recvTimeout = 100 * time.Millisecond
	Run(t, func(t *testing.T) bus.Bus {
		t.Helper()
		return brokenBus{Bus: newFakeBus()}
	})
}

// TestBusSuiteCatchesBroken proves the suite rejects a wrong Bus: the
// probe process must fail, and its output must show the suite itself
// failing (not a build or setup error, which would be a vacuous pass).
func TestBusSuiteCatchesBroken(t *testing.T) {
	cmd := exec.Command("go", "test", "-run", "TestBusBrokenProbe$", "-count=1", ".")
	cmd.Env = append(os.Environ(), "DEVCULES_BUS_PROBE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("contract probe passed on a broken bus, want failure:\n%s", out)
	}
	if !strings.Contains(string(out), "FAIL: TestBusBrokenProbe") {
		t.Fatalf("probe failed without the suite running:\n%s", out)
	}
}
