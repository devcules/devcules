package elements

import (
	"context"
	"testing"
)

// TestStubContract pins the unavailable stub: it builds on every OS,
// answers ErrUnavailable, and Close is a no-op. It also keeps the
// stub referenced on platforms whose New never returns it (Linux,
// macOS, Windows), where staticcheck would otherwise flag it unused.
func TestStubContract(t *testing.T) {
	var d Driver = stub{}
	tree, err := d.Tree(context.Background(), 10)
	if err != ErrUnavailable {
		t.Fatalf("err: %v", err)
	}
	if len(tree.Elements) != 0 {
		t.Fatalf("elements: %d", len(tree.Elements))
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestNewBuildsOnThisOS proves New answers a driver (real or stub) on
// every platform.
func TestNewBuildsOnThisOS(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d == nil {
		t.Fatal("New returned nil driver")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
