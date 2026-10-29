package ground

// The lossless mask encoder: the mirror of decodeLossless, from the
// same documented context cascade. It exists for the round-trip test —
// production decodes one_bit masks — so it lives with the decoder and
// shares its tables. Keep the two in lockstep: every field here has a
// reader in decodeLossless.

type rangeEnc struct {
	out []byte
	low uint32
	rng uint32
}

func newRangeEnc() *rangeEnc { return &rangeEnc{rng: 0xffffffff} }

// encode advances past the symbol [cum, cum+freq) of total. The
// renormalisation mirrors the decoder exactly, clamp included: it
// runs only when the carry condition is false.
func (e *rangeEnc) encode(cum, freq, total uint32) {
	scaled := e.rng / total
	e.low += scaled * cum
	e.rng = scaled * freq
	const top, bottom = uint32(1) << 24, uint32(1) << 16
	for {
		if (e.low ^ (e.low + e.rng)) >= top {
			if e.rng >= bottom {
				break
			}
			e.rng = (-e.low) & (bottom - 1)
		}
		e.out = append(e.out, byte(e.low>>24))
		e.low = e.low << 8
		e.rng = e.rng << 8
	}
}

func (e *rangeEnc) finish() []byte {
	final := e.out
	for i := 0; i < 4; i++ {
		final = append(final, byte(e.low>>24))
		e.low = e.low << 8
	}
	return final
}

// encodeLossless encodes a binary raster (0/1) in the `~` format:
// zigzag residuals of a predictive filter, adaptively range-coded
// over the same zero/nonzero and symbol cascade the decoder reads.
func encodeLossless(raster []byte, w, h int) string {
	const (
		oZeroInit  = 2
		oOneInit   = 1
		oOneInc    = 56
		oOneLimit  = 8192
		oTwoStep   = 14 * 44
		oTwoLimit  = 14 * 3584
		oZeroStep  = 2 * 16
		oZeroLimit = 2 * 2048
	)
	// selector 4: the loze filter — any valid selector decodes.
	const selector = 4
	e := newRangeEnc()
	zero := [32]uint32{}
	for i := range zero {
		zero[i] = 1
	}
	oZero := make([]uint16, 256)
	for i := range oZero {
		oZero[i] = oZeroInit
	}
	oZeroTotal := 256 * oZeroInit
	oOne := make([]uint16, 256*256)
	oOneTot := make([]int32, 256)
	for i := range oOne {
		oOne[i] = oOneInit
	}
	for i := range oOneTot {
		oOneTot[i] = 256 * oOneInit
	}
	type o2key struct{ prev, above byte }
	oTwo := map[o2key][257]uint32{}

	// residuals: zigzag of (value - prediction)
	resid := make([]byte, len(raster))
	pred := make([]byte, len(raster))
	for off, v := range raster {
		x, y := off%w, off/w
		p := predict(pred, off, x, y, selector, w)
		pred[off] = v
		resid[off] = zig(int(v) - int(p))
	}

	prev := 0
	for off := 0; off < len(raster); off++ {
		x, y := off%w, off/w
		zo := zeroCtx(resid, off, x, y, w)
		zf, nzf := zero[zo<<1], zero[zo<<1|1]
		sym := int(resid[off])
		if sym == 0 {
			e.encode(0, zf, zf+nzf)
			zero[zo<<1] += 32
		} else {
			e.encode(zf, nzf, zf+nzf)
			zero[zo<<1|1] += 32
		}
		if zero[zo<<1]+zero[zo<<1|1] >= 16384 {
			zero[zo<<1] = maxOne(zero[zo<<1] >> 1)
			zero[zo<<1|1] = maxOne(zero[zo<<1|1] >> 1)
		}
		if sym != 0 {
			above := 0
			if off >= w {
				above = int(resid[off-w])
			}
			sec := oTwo[o2key{byte(prev), byte(above)}]
			fo := prev << 8
			exZero := sec[0] + uint32(oOne[fo]) + uint32(oZero[0])
			total := sec[256] + uint32(oOneTot[prev]) + uint32(oZeroTotal) - exZero
			// cumulative up to sym, frequency of sym
			cum := uint32(0)
			for v := 1; v < sym; v++ {
				cum += sec[v] + uint32(oOne[fo+v]) + uint32(oZero[v])
			}
			freq := sec[sym] + uint32(oOne[fo+sym]) + uint32(oZero[sym])
			e.encode(cum, freq, total)
			sec[sym] += oTwoStep
			sec[256] += oTwoStep
			if sec[256] >= oTwoLimit {
				var t uint32
				for v := 0; v < 256; v++ {
					sec[v] >>= 1
					t += sec[v]
				}
				sec[256] = t
			}
			oTwo[o2key{byte(prev), byte(above)}] = sec
			oOne[fo+sym] += oOneInc
			oOneTot[prev] += oOneInc
			if oOneTot[prev] >= oOneLimit {
				var t int32
				for v := 0; v < 256; v++ {
					s := oOne[fo+v] >> 1
					if s == 0 {
						s = 1
					}
					oOne[fo+v] = s
					t += int32(s)
				}
				oOneTot[prev] = t
			}
			oZero[sym] += oZeroStep
			oZeroTotal += oZeroStep
			if oZeroTotal >= oZeroLimit {
				var t uint32
				for v := 0; v < 256; v++ {
					oZero[v] >>= 1
					t += uint32(oZero[v])
				}
				oZeroTotal = int(t)
			}
		}
		prev = sym
	}
	return "~" + pack(append([]byte{selector}, e.finish()...))
}

// zig maps a residual to the zigzag byte the decoder's unzig mirrors.
// The residual wraps to the signed byte range first: unzig reads it
// back modulo 256, so zig must encode that same wrap.
func zig(v int) byte {
	v = ((v % 256) + 256) % 256 // to 0..255
	if v > 127 {
		v -= 256 // to the signed byte
	}
	if v < 0 {
		return byte(-2*v - 1)
	}
	return byte(2 * v)
}

// pack base85-encodes bytes with the 5-char length prefix, the
// encoder's mirror of unpack.
func pack(in []byte) string {
	var b []byte
	n := len(in)
	for i := 5; i >= 1; i-- {
		b = append(b, alphaTable[(n/intPow(85, i-1))%85])
	}
	if n/intPow(85, 5) != 0 {
		panic("ground: payload too large")
	}
	full := n - n%4
	for off := 0; off < full; off += 4 {
		v := uint32(in[off])<<24 | uint32(in[off+1])<<16 | uint32(in[off+2])<<8 | uint32(in[off+3])
		b = append(b, alphaTable[(v/(85*85*85*85))%85], alphaTable[(v/(85*85*85))%85], alphaTable[(v/(85*85))%85], alphaTable[(v/85)%85], alphaTable[v%85])
	}
	if rem := n % 4; rem > 0 {
		var v uint32
		for i := 0; i < 4; i++ {
			if i < rem {
				v |= uint32(in[full+i]) << (24 - i*8)
			}
		}
		d := []byte{alphaTable[(v/(85*85*85*85))%85], alphaTable[(v/(85*85*85))%85], alphaTable[(v/(85*85))%85], alphaTable[(v/85)%85], alphaTable[v%85]}
		b = append(b, d[:rem+1]...)
	}
	return string(b)
}

func intPow(base, exp int) int {
	out := 1
	for i := 0; i < exp; i++ {
		out *= base
	}
	return out
}
