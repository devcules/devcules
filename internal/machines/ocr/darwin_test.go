//go:build darwin

package ocr

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"
)

// TestDarwinDriverWiring drives the real Vision driver with a
// scripted runner: install → compile (cached) → invoke → parse.
func TestDarwinDriverWiring(t *testing.T) {
	payload := []byte("Pay\t12\t20\t88\t14\nnow\t12\t40\t200\t14\n")
	compiled := false
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "swiftc":
			compiled = true
			return nil, nil
		default: // the reader binary
			return payload, nil
		}
	}
	drv, err := newDarwinWith(run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })

	frame := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for i := range frame.Pix {
		frame.Pix[i] = 0xff
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	words, err := drv.Read(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled {
		t.Fatal("reader was never compiled")
	}
	if len(words) != 2 || words[0].Text != "Pay" {
		t.Fatalf("words: %+v", words)
	}
}

// TestDarwinDriverCompileFails proves a broken toolchain surfaces as
// an error, not a hang.
func TestDarwinDriverCompileFails(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "swiftc" {
			return nil, errFake
		}
		return nil, nil
	}
	drv, err := newDarwinWith(run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	_, err = drv.Read(t.Context(), solidFrame(50, 20))
	if err == nil {
		t.Fatal("expected compile error")
	}
}

var errFake = fakeErr("swiftc exploded")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

func solidFrame(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	return img
}
