package helper

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"testing"
)

// noise is an image PNG cannot compress, so its size tracks its area.
func noise(w, h int) *image.RGBA {
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.UintN(256))
	}
	return img
}

func TestEncodeFitKeepsFullSizeWhenItFits(t *testing.T) {
	out, scale, err := encodeFit(noise(40, 30), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if scale != 1 || img.Bounds().Dx() != 40 || img.Bounds().Dy() != 30 {
		t.Fatalf("scale %v, size %v; want 1 and 40x30", scale, img.Bounds())
	}
}

func TestEncodeFitShrinksToTheCap(t *testing.T) {
	full, _, err := encodeFit(noise(400, 300), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	limit := len(full) / 3 // too big at 3/4, fits at 1/2
	out, scale, err := encodeFit(noise(400, 300), limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > limit {
		t.Fatalf("frame is %d bytes, over the %d cap", len(out), limit)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if scale != 2 || img.Bounds().Dx() != 200 || img.Bounds().Dy() != 150 {
		t.Fatalf("scale %v, size %v; want 2 and 200x150", scale, img.Bounds())
	}
}

func TestEncodeFitGivesUpBelowAQuarter(t *testing.T) {
	if _, _, err := encodeFit(noise(400, 300), 100); err == nil {
		t.Fatal("a 100 byte cap was met")
	}
}

func TestShrinkAveragesTheBox(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	src.Set(1, 0, color.RGBA{255, 0, 0, 255})
	src.Set(0, 1, color.RGBA{0, 0, 255, 255})
	src.Set(1, 1, color.RGBA{0, 0, 255, 255})
	got := shrink(src, 0.5)
	if got.Bounds().Dx() != 1 || got.Bounds().Dy() != 1 {
		t.Fatalf("size %v, want 1x1", got.Bounds())
	}
	if c := got.RGBAAt(0, 0); c.R != 127 || c.G != 0 || c.B != 127 || c.A != 255 {
		t.Fatalf("pixel %v, want the mean of red and blue", c)
	}
}
