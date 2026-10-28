//go:build ignore

// Regenerate the PWA icons (web/public/icons/*.png) from the same
// geometry as icon.svg: a dark rounded square, an amber ring, a
// solid centre. Pure stdlib with 4x supersampling; no rasterizer
// needed. Run: go run web/icons/generate.go. Sizes follow the
// manifest (192, 512) plus Apple's 180 touch icon.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var (
	bg     = color.RGBA{0x10, 0x14, 0x18, 0xff}
	amber  = color.RGBA{0xe0, 0xa6, 0x3f, 0xff}
	center = 0.5
	ringR  = 0.30
	ringW  = 0.075
	dotR   = 0.10
	corner = 0.225
)

func clamp(v, lo, hi float64) float64 {
	return math.Min(hi, math.Max(lo, v))
}

// rounded reports the coverage of (x,y) in unit space by the rounded
// square: 1 inside, fading over one device pixel at the edge.
func rounded(x, y, px float64) float64 {
	qx := math.Abs(x-center) - (center - corner)
	qy := math.Abs(y-center) - (center - corner)
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - corner
	return clamp(0.5-d/px, 0, 1)
}

// ring reports coverage by the amber ring and dot.
func ring(x, y, px float64) float64 {
	d := math.Hypot(x-center, y-center)
	edge := math.Abs(d-ringR) - ringW/2
	dot := d - dotR
	a := clamp(0.5-edge/px, 0, 1)
	b := clamp(0.5-dot/px, 0, 1)
	return math.Max(a, b)
}

func icon(size int) image.Image {
	const ss = 4
	big := image.NewRGBA(image.Rect(0, 0, size*ss, size*ss))
	px := 1 / float64(size*ss)
	for j := 0; j < size*ss; j++ {
		for i := 0; i < size*ss; i++ {
			x, y := (float64(i)+0.5)*px, (float64(j)+0.5)*px
			big.Set(i, j, color.RGBA{bg.R, bg.G, bg.B, uint8(rounded(x, y, px) * 255)})
		}
	}
	// Amber on a second pass so the ring antialiases against the bg.
	for j := 0; j < size*ss; j++ {
		for i := 0; i < size*ss; i++ {
			x, y := (float64(i)+0.5)*px, (float64(j)+0.5)*px
			a := ring(x, y, px)
			if a > 0 {
				bgA := float64(big.RGBAAt(i, j).A) / 255
				big.Set(i, j, color.RGBA{amber.R, amber.G, amber.B, uint8(a * bgA * 255)})
			}
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for j := 0; j < size; j++ {
		for i := 0; i < size; i++ {
			var r, g, b, a uint64
			for dj := 0; dj < ss; dj++ {
				for di := 0; di < ss; di++ {
					c := big.RGBAAt(i*ss+di, j*ss+dj)
					r += uint64(c.R)
					g += uint64(c.G)
					b += uint64(c.B)
					a += uint64(c.A)
				}
			}
			n := uint64(ss * ss)
			out.Set(i, j, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)})
		}
	}
	return out
}

func main() {
	dir := filepath.Join("web", "public", "icons")
	for _, size := range []int{180, 192, 512} {
		name := map[int]string{180: "apple-touch-icon.png", 192: "icon-192.png", 512: "icon-512.png"}[size]
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, icon(size)); err != nil {
			panic(err)
		}
		f.Close()
		fmt.Println("wrote", name)
	}
}
