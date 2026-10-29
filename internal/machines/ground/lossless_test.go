package ground

import "testing"

func TestLosslessRoundTrip(t *testing.T) {
	// a 20x14 raster: two filled blocks, encoded with the package's
	// own lossless encoder (the mirror of the decoder, same cascade)
	// and decoded back. Production requests one_bit; the lossless
	// format is the wire default, so both paths must hold.
	// The lossless raster carries coverage values, not bits: the
	// decoder maps >=129 to in-mask. In-mask pixels are 255.
	w, h := 20, 14
	src := make([]byte, w*h)
	for y := 3; y < 9; y++ {
		for x := 4; x < 12; x++ {
			src[y*w+x] = 255
		}
	}
	for y := 10; y < 13; y++ {
		for x := 2; x < 5; x++ {
			src[y*w+x] = 255
		}
	}
	payload := encodeLossless(src, w, h)
	got, err := decodeMask(payload, w, h)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// decodeMask answers the binary raster: every 255-coverage pixel
	// is 1, every 0 stays 0.
	for i := range src {
		want := byte(0)
		if src[i] == 255 {
			want = 1
		}
		if got[i] != want {
			t.Fatalf("raster[%d,%d] = %d, want %d", i%w, i/w, got[i], want)
		}
	}
	cx, cy := regionCentre(got, w, h)
	if cx < 7 || cx > 8 || cy < 5 || cy > 6 {
		t.Fatalf("centre = %d,%d, want ~(7,5)", cx, cy)
	}
}

func TestPredictModes(t *testing.T) {
	out := []byte{10, 20, 30, 40, 5, 6, 7, 8, 9, 10, 11, 12}
	w := 4
	// every selector runs without panicking; two have exact answers.
	for mode := 0; mode < 5; mode++ {
		_ = predict(out, 5, 1, 1, mode, w)
	}
	if predict(out, 5, 1, 1, 1, w) != out[5-w] {
		t.Fatal("mode 1 must predict up")
	}
	if predict(out, 5, 1, 1, 2, w) != out[4] {
		t.Fatal("mode 2 must predict left")
	}
}

func TestUnzigZigPair(t *testing.T) {
	for _, v := range []int{-5, -1, 0, 1, 5, 127, -127} {
		if got := unzig(int(zig(v)), 0); got != byte(v) {
			t.Fatalf("zig(%d)/unzig = %d", v, got)
		}
	}
}
