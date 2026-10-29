package ground

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDecoderStatesAgainstOfficial pins the Go range decoder's exact
// internal state (low, range, code) to the official one, pixel by
// pixel, on the canonical payload.
func TestDecoderStatesAgainstOfficial(t *testing.T) {
	raw, err := os.ReadFile("testdata/official-states.ndjson")
	if err != nil {
		t.Skipf("states fixture missing: %v", err)
	}
	type st struct {
		Low   uint32 `json:"low"`
		Range uint32 `json:"range"`
		Code  uint32 `json:"code"`
	}
	var want []st
	for _, line := range splitLines(string(raw)) {
		if line == "" {
			continue
		}
		var s st
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatal(err)
		}
		want = append(want, s)
	}
	packed, err := unpack(canonicalPayload[1:])
	if err != nil {
		t.Fatal(err)
	}
	d, err := newRangeDec(packed)
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]uint16, maskCtxCount*2)
	for i := range counts {
		counts[i] = 1
	}
	pw := canonicalW + 2*maskPad
	padded := make([]byte, pw*(canonicalH+maskPad))
	for i := 0; i < len(want); i++ {
		x, y := i%canonicalW, i/canonicalW
		row := (y+maskPad)*pw + maskPad
		p := row + x
		ctx := contextAt(padded, p, p-pw, p-2*pw)
		off := ctx << 1
		zero, one := uint32(counts[off]), uint32(counts[off+1])
		v := d.freq(zero + one)
		bit := 0
		if v >= zero {
			bit = 1
		}
		if i == 1069 || i == 1070 {
			t.Logf("step %d pre: rng mine-history check", i)
		}
		if d.low != want[i].Low || d.rng != want[i].Range || d.code != want[i].Code {
			if i > 1000 {
				t.Fatalf("step %d (px %d): state (%d,%d,%d), official (%d,%d,%d) — scaled=%d zero=%d total=%d v=%d cum=%d freq=%d",
					i, i, d.low, d.rng, d.code, want[i].Low, want[i].Range, want[i].Code, d.scaled, zero, zero+one, v, func() int {
						if v >= zero {
							return int(zero)
						}
						return 0
					}(), func() int {
						if v >= zero {
							return int(one)
						}
						return int(zero)
					}())
			}
			t.Fatalf("step %d: state (%d,%d,%d), official (%d,%d,%d)", i, d.low, d.rng, d.code, want[i].Low, want[i].Range, want[i].Code)
		}
		if bit == 0 {
			d.decode(0, zero)
		} else {
			d.decode(zero, one)
		}
		padded[p] = byte(bit)
		counts[off+bit] += maskInc
		if uint32(counts[off])+uint32(counts[off+1]) >= maskLimit {
			halve16(counts, off)
			halve16(counts, off+1)
		}
	}
}
