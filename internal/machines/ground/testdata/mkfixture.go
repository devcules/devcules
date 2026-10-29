//go:build ignore

// mkfixture builds the M4.4 gate fixture: a canvas-style screenshot
// with buttons the accessibility tree cannot see, plus its expected
// click points. Run: go run testdata/mkfixture.go
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

// canvas is 640x400, dark. Three canvas-drawn buttons:
//
//	Pay        at (120,300)-(220,340)   — the SAM target
//	Settings   at (300,300)-(440,340)
//	Cancel     at (520,300)-(620,340)
//
// Nothing here has a native control: a canvas page the tree sees as
// one blank region, which is exactly when grounding kicks in.
func main() {
	w, h := 640, 400
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{24, 26, 32, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, bg)
		}
	}
	button := func(x1, y1, x2, y2 int, c color.RGBA) {
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				img.Set(x, y, c)
			}
		}
	}
	// A bright green block 24px tall over a dark stripe: SAM's phrase
	// prompt "green button" must find it; the expected click is its
	// mask centre, which the block font makes deterministic.
	stripe := color.RGBA{40, 44, 54, 255}
	for y := 280; y < 360; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, stripe)
		}
	}
	green := color.RGBA{46, 204, 113, 255}
	button(120, 300, 220, 340, green)
	gray := color.RGBA{120, 128, 140, 255}
	button(300, 300, 440, 340, gray)
	red := color.RGBA{231, 76, 60, 255}
	button(520, 300, 620, 340, red)

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	if err := os.WriteFile("testdata/canvas.png", out.Bytes(), 0o644); err != nil {
		panic(err)
	}
	// The known click points, in the same file the tests read.
	f, err := os.Create("testdata/canvas.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprint(f, `{
  "w": 640, "h": 400,
  "green button":  [170, 320],
  "gray button":   [370, 320],
  "red button":    [570, 320],
  "blue button":   null
}
`)
	fmt.Println("wrote testdata/canvas.png and testdata/canvas.json")
}
