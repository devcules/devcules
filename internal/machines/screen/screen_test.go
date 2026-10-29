package screen

import (
	"os"
	"testing"
)

func TestScreenRequiresDisplay(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no DISPLAY — run with Xvfb in CI for the real capture path")
	}
	drv, err := New()
	if err != nil { t.Fatal(err) }
	defer drv.Close()
	img, err := drv.Capture()
	if err != nil { t.Fatal(err) }
	if img == nil { t.Fatal("capture returned nil frame") }
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 { t.Fatalf("zero-size frame %v", b) }
}

func TestScreenStubBuilds(t *testing.T) {
	d, _ := New()
	if d == nil { t.Fatal("New returned nil driver") }
	_ = d.Close()
}
