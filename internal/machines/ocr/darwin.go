//go:build darwin

package ocr

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
)

//go:embed scripts/reader.swift
var readerSwift string

// darwinDriver reads words via Vision (macOS's own OCR engine) from a
// compiled Swift helper. The helper runs as this process so it needs
// no extra permission beyond screen access the machine owner already
// granted.
type darwinDriver struct {
	dir      string // helper binary + script
	run      command
	mu       sync.Mutex
	compiled bool
}

func newDarwin() (Driver, error) { return newDarwinWith(runCommand) }

// newDarwinWith wires the driver with a specific runner; tests use it
// to drive the real install/compile/invoke path with a script.
func newDarwinWith(run command) (Driver, error) {
	dir, err := os.MkdirTemp("", "devcules-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reader.swift"), []byte(readerSwift), 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("ocr: install reader: %w", err)
	}
	return &darwinDriver{dir: dir, run: run}, nil
}

// Read encodes the frame once, compiles the reader on first use
// (swiftc takes seconds; the binary is cached for the driver's
// life), then runs it on the PNG.
func (d *darwinDriver) Read(ctx context.Context, frame image.Image) ([]Word, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	pngPath, err := writePNG(d.dir, frame)
	if err != nil {
		return nil, err
	}
	defer os.Remove(pngPath)
	d.mu.Lock()
	defer d.mu.Unlock()
	bin := filepath.Join(d.dir, "reader")
	if !d.compiled {
		if _, err := d.run(ctx, "swiftc", "-O", filepath.Join(d.dir, "reader.swift"), "-o", bin); err != nil {
			return nil, fmt.Errorf("ocr: compile Vision reader: %w", err)
		}
		d.compiled = true
	}
	out, err := d.run(ctx, bin, pngPath)
	if err != nil {
		return nil, fmt.Errorf("ocr: Vision read: %w", err)
	}
	return parseWords(out), nil
}

func (d *darwinDriver) Close() error { return os.RemoveAll(d.dir) }
