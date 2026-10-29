//go:build windows

package ocr

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"os"
	"path/filepath"
)

//go:embed scripts/reader.ps1
var readerPS1 string

// windowsDriver reads words with the WinRT OCR engine (the same one
// Windows' own apps use) via an embedded PowerShell script. The
// engine is present on every supported Windows 10+ install with a
// language pack.
type windowsDriver struct {
	dir string
	run command
}

func newWindows() (Driver, error) { return newWindowsWith(runCommand) }

// newWindowsWith wires the driver with a specific runner; tests use it
// to drive the real install/invoke path with a script.
func newWindowsWith(run command) (Driver, error) {
	dir, err := os.MkdirTemp("", "devcules-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reader.ps1"), []byte(readerPS1), 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("ocr: install reader: %w", err)
	}
	return &windowsDriver{dir: dir, run: run}, nil
}

func (d *windowsDriver) Read(ctx context.Context, frame image.Image) ([]Word, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	pngPath, err := writePNG(d.dir, "frame.png", frame)
	if err != nil {
		return nil, err
	}
	out, err := d.run(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(d.dir, "reader.ps1"), pngPath)
	if err != nil {
		return nil, fmt.Errorf("ocr: WinRT read: %w", err)
	}
	return parseWords(out), nil
}

func (d *windowsDriver) Close() error { return os.RemoveAll(d.dir) }
