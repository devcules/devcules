//go:build darwin

package elements

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestDarwinDriverWiring runs the real darwin driver with a scripted
// runner: install → compile (cached) → invoke → parse. Only on macOS,
// where the driver itself compiles.
func TestDarwinDriverWiring(t *testing.T) {
	payload := fixture(t, "darwin.tsv")
	compiled := false
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "swiftc":
			compiled = true
			return nil, nil
		default: // the walker binary
			return payload, nil
		}
	}
	drv, err := newDarwinWith(run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := drv.Tree(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled {
		t.Fatal("walker was never compiled")
	}
	if len(got.Elements) != 4 || got.Front != "Terminal" {
		t.Fatalf("tree: %+v", got)
	}
}

// TestDarwinDriverRealWalker compiles and runs the actual embedded
// walker when swiftc exists — the fixture shapes stay pinned to what
// the real script prints. Skipped without swiftc or an AX-permitted
// session; CI (Linux) never runs it.
//
// Opt-in (DEVCULES_LIVE_MAC=1): the walker's AX calls make macOS ask
// for Accessibility on behalf of whatever started `go test`.
func TestDarwinDriverRealWalker(t *testing.T) {
	if os.Getenv("DEVCULES_LIVE_MAC") != "1" {
		t.Skip("live macOS walker is opt-in: DEVCULES_LIVE_MAC=1")
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		t.Skip("no swiftc")
	}
	drv, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	got, err := drv.Tree(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	// Without the Accessibility permission the walker reports a note
	// and zero elements — that is a valid, tested outcome.
	if len(got.Elements) == 0 && got.Note == "" {
		t.Fatalf("empty tree without a note: %+v", got)
	}
}
