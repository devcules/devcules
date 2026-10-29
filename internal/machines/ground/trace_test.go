package ground

import (
	"encoding/json"
	"os"
	"testing"
)

// TestTraceAgainstOfficial replays the official @meta-sam/parser
// decoder's decisions over the canonical payload and asserts the Go
// decoder makes the identical choice at every pixel — the strongest
// possible pin to the real wire format.
func TestTraceAgainstOfficial(t *testing.T) {
	type step struct {
		PX   int `json:"px"`
		CTX  int `json:"ctx"`
		Zero int `json:"zero"`
		One  int `json:"one"`
		V    int `json:"v"`
		Bit  int `json:"bit"`
		Pos  int `json:"pos"`
	}
	raw, err := os.ReadFile("testdata/official-trace.ndjson")
	if err != nil {
		t.Skipf("official trace not present: %v", err)
	}
	var want []step
	for _, line := range splitLines(string(raw)) {
		if line == "" {
			continue
		}
		var s step
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatalf("trace line %q: %v", line, err)
		}
		want = append(want, s)
	}
	if len(want) == 0 {
		t.Fatal("empty trace")
	}

	// replay
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
	for i, s := range want {
		off := s.CTX << 1
		if got := int(counts[off]); got != s.Zero {
			t.Fatalf("step %d (px %d): zero count %d, official %d — divergence starts here", i, s.PX, got, s.Zero)
		}
		if got := int(counts[off+1]); got != s.One {
			t.Fatalf("step %d (px %d): one count %d, official %d", i, s.PX, got, s.One)
		}
		zero, one := uint32(counts[off]), uint32(counts[off+1])
		v := d.freq(zero + one)
		if int(v) != s.V {
			t.Fatalf("step %d (px %d): freq %d, official %d (ctx %d, zero %d)", i, s.PX, v, s.V, s.CTX, s.Zero)
		}
		bit := 0
		if v >= zero {
			bit = 1
		}
		if bit != s.Bit {
			t.Fatalf("step %d (px %d): bit %d, official %d", i, s.PX, bit, s.Bit)
		}
		if bit == 0 {
			d.decode(0, zero)
		} else {
			d.decode(zero, one)
		}
		counts[off+bit] += maskInc
		if uint32(counts[off])+uint32(counts[off+1]) >= maskLimit {
			halve16(counts, off)
			halve16(counts, off+1)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
