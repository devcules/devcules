package helper

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// fitSteps are the sizes a capture shrinks through, as a fraction of
// the screen, until its PNG fits the cap. A quarter is the floor:
// smaller, a frame stops being worth looking at.
var fitSteps = []float64{1, 0.75, 0.5, 0.375, 0.25}

// encodeFit encodes img as PNG no larger than maxBytes, shrinking it
// when it does not fit. scale is screen pixels per frame pixel (1
// when the frame is full size), so a viewer maps a point on the frame
// back to the screen by multiplying.
func encodeFit(img image.Image, maxBytes int) (out []byte, scale float64, err error) {
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	for _, f := range fitSteps {
		frame := img
		if f != 1 {
			frame = shrink(img, f)
		}
		var buf bytes.Buffer
		if err := enc.Encode(&buf, frame); err != nil {
			return nil, 0, fmt.Errorf("cannot encode the frame")
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), float64(img.Bounds().Dx()) / float64(frame.Bounds().Dx()), nil
		}
	}
	return nil, 0, fmt.Errorf("frame is over the %d byte cap even at a quarter size", maxBytes)
}

// shrink box-averages img down to f of its size: every output pixel
// is the mean of the source pixels it covers, which keeps text
// readable where nearest-neighbour would drop strokes.
func shrink(img image.Image, f float64) *image.RGBA {
	b := img.Bounds()
	w := max(int(float64(b.Dx())*f), 1)
	h := max(int(float64(b.Dy())*f), 1)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0 := b.Min.Y + y*b.Dy()/h
		y1 := max(b.Min.Y+(y+1)*b.Dy()/h, y0+1)
		for x := range w {
			x0 := b.Min.X + x*b.Dx()/w
			x1 := max(b.Min.X+(x+1)*b.Dx()/w, x0+1)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
			}
			i := out.PixOffset(x, y)
			out.Pix[i+0] = uint8(r / n >> 8)
			out.Pix[i+1] = uint8(g / n >> 8)
			out.Pix[i+2] = uint8(bl / n >> 8)
			out.Pix[i+3] = uint8(a / n >> 8)
		}
	}
	return out
}
