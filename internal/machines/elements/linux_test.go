//go:build linux

package elements

import (
	"context"
	"testing"
	"time"
)

// TestLinuxDriverWiring runs the real linux driver with a scripted
// runner: install → python3 invoke → parse. Only on Linux, where the
// driver compiles.
func TestLinuxDriverWiring(t *testing.T) {
	payload := fixture(t, "linux.tsv")
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "python3" {
			t.Errorf("unexpected command %q", name)
		}
		return payload, nil
	}
	drv, err := newLinuxWith(run)
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
	if len(got.Elements) != 3 || got.Front != "gnome-text-editor" {
		t.Fatalf("tree: %+v", got)
	}
}
