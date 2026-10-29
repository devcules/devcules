//go:build linux

package ocr

import (
	"context"
	"fmt"
	"image"
	"os"
)

// tesseractDriver reads words with tesseract's TSV output: one word
// per line with its pixel box, exactly the shared shape. Tesseract is
// documented in docs/machines.md as the Linux OCR engine; it is
// detected at Read time, not New, so a machine without it reports
// "install tesseract-ocr" on first use instead of failing at start.
type tesseractDriver struct {
	dir string
	run command
}

func newTesseract() (Driver, error) { return newTesseractWith(runCommand) }

// newTesseractWith wires the driver with a specific runner; tests use
// it to drive the real invoke path with a script.
func newTesseractWith(run command) (Driver, error) {
	dir, err := os.MkdirTemp("", "devcules-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	return &tesseractDriver{dir: dir, run: run}, nil
}

// Read runs tesseract with `tsv` config: word, box, confidence. Words
// with weak confidence (below 60) are dropped: a text-only model acts
// on these words, so noise is worse than a gap.
func (d *tesseractDriver) Read(ctx context.Context, frame image.Image) ([]Word, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	pngPath, err := writePNG(d.dir, frame)
	if err != nil {
		return nil, err
	}
	defer os.Remove(pngPath)
	out, err := d.run(ctx, "tesseract", pngPath, "stdout", "-l", "eng", "tsv")
	if err != nil {
		return nil, fmt.Errorf("ocr: tesseract read (is tesseract-ocr installed?): %w", err)
	}
	return parseTSV(out), nil
}

func (d *tesseractDriver) Close() error { return os.RemoveAll(d.dir) }
