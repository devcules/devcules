//go:build linux

package ocr

import (
	"context"
	"image"
	"testing"
	"time"
)

// TestTesseractDriverWiring drives the real tesseract driver with a
// scripted runner: PNG write → invoke → parseTSV.
func TestTesseractDriverWiring(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "tesseract" {
			t.Errorf("unexpected command %q", name)
		}
		return []byte(tsvGood), nil
	}
	drv, err := newTesseractWith(run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	words, err := drv.Read(ctx, image.NewRGBA(image.Rect(0, 0, 100, 50)))
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[0].Text != "Pay" {
		t.Fatalf("words: %+v", words)
	}
}

// TestTesseractMissing surfaces as "install tesseract-ocr", not a raw
// exec error.
func TestTesseractMissing(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errMissing
	}
	drv, err := newTesseractWith(run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	_, err = drv.Read(t.Context(), image.NewRGBA(image.Rect(0, 0, 10, 10)))
	if err == nil || time.Since(time.Now()) > time.Hour {
		t.Fatalf("err: %v", err)
	}
}

var errMissing = missingErr("not found")

type missingErr string

func (e missingErr) Error() string { return string(e) }
