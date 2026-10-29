package screen

import "testing"

// TestStubContract pins the unavailable stub: it builds on every OS,
// answers ErrUnavailable, and Close is a no-op. It also keeps the stub
// referenced on platforms whose New never returns it, where
// staticcheck would otherwise flag it unused.
func TestStubContract(t *testing.T) {
	var d Driver = stub{}
	if _, err := d.Capture(); err != ErrUnavailable {
		t.Fatalf("capture: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
