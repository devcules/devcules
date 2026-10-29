//go:build darwin

package screen

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const defaultTestTimeout = 5 * time.Second

// TestDarwinCapture takes one real capture when the tool is there. A
// headless or unpermitted session may refuse; that must be an error,
// never a nil image without one.
func TestDarwinCapture(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Skipf("screencapture unavailable: %s", err)
	}
	defer d.Close()
	img, err := d.Capture()
	if err != nil {
		t.Skipf("capture refused here: %s", err)
	}
	if img == nil || img.Bounds().Dx() == 0 {
		t.Fatal("capture returned an empty image")
	}
}

// TestDarwinCaptureToolFails maps a failing tool to an error.
func TestDarwinCaptureToolFails(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "screencapture")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\necho no display >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	d := &darwin{tool: bad, timeout: defaultTestTimeout}
	if _, err := d.Capture(); err == nil {
		t.Fatal("a failing screencapture returned no error")
	}
	// A tool that exits 0 without writing a PNG is an error too.
	empty := filepath.Join(t.TempDir(), "screencapture")
	if err := os.WriteFile(empty, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	d = &darwin{tool: empty, timeout: defaultTestTimeout}
	if _, err := d.Capture(); err == nil {
		t.Fatal("an empty capture returned no error")
	}
}
