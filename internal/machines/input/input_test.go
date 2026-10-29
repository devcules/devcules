package input

import (
	"os"
	"testing"
)

func TestInputRequiresDisplay(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no DISPLAY — run with Xvfb in CI for pointer path")
	}
	drv, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	if err := drv.Inject(Event{Kind: "move", X: 10, Y: 10}); err != nil {
		t.Fatal(err)
	}
}

func TestInputStubBuilds(t *testing.T) {
	d, _ := New()
	if d == nil {
		t.Fatal("New returned nil driver")
	}
	_ = d.Close()
}
