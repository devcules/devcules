package ground

import (
	"fmt"
)

// The mask payload is base85 with an alphabet of 87 characters from
// the documented wire contract: printable 0x21..0x7e minus the six
// characters the token grammar reserves (", ; < > | and the escape).
// Groups of 4 bytes encode to 5 characters big-endian; the payload
// opens with a 5-character byte-length prefix in the same alphabet.
var (
	alphaTable [87]byte
	valueTable [128]int8
)

// The alphabet is exactly the documented wire contract: printable
// 0x21..0x7e minus the six characters the token grammar reserves.
// Built in one pass at init; a wrong count is a panic, not a silent
// decoder.
func init() {
	n := 0
	for c := byte(0x21); c <= 0x7e; c++ {
		switch c {
		case '"', '\\', ',', ';', '<', '>', '|':
			continue
		}
		alphaTable[n] = c
		valueTable[c] = int8(n)
		n++
	}
	if n != 87 {
		panic("ground: alphabet construction is wrong")
	}
}

// digit decodes one alphabet character.
func digit(c byte) (int, bool) {
	if c >= 128 || valueTable[c] < 0 {
		return 0, false
	}
	return int(valueTable[c]), true
}

// unpack base85-decodes a payload with its 5-char length prefix.
func unpack(payload string) ([]byte, error) {
	if len(payload) < 5 {
		return nil, fmt.Errorf("mask payload is missing its length prefix")
	}
	length := 0
	for i := 0; i < 5; i++ {
		d, ok := digit(payload[i])
		if !ok {
			return nil, fmt.Errorf("mask payload has an invalid character %q at %d", payload[i], i)
		}
		length = length*85 + d
	}
	rem := length % 4
	want := 5 + (length/4)*5
	if rem != 0 {
		want += rem + 1
	}
	if len(payload) != want {
		return nil, fmt.Errorf("mask payload length %d does not match its prefix %d", len(payload), want)
	}
	out := make([]byte, length)
	src, dst := 5, 0
	full := length - rem
	for dst < full {
		var v uint32
		for i := 0; i < 5; i++ {
			d, ok := digit(payload[src+i])
			if !ok {
				return nil, fmt.Errorf("mask payload has an invalid character %q", payload[src+i])
			}
			v = v*85 + uint32(d)
		}

		out[dst] = byte(v >> 24)
		out[dst+1] = byte(v >> 16)
		out[dst+2] = byte(v >> 8)
		out[dst+3] = byte(v)
		src += 5
		dst += 4
	}
	if rem > 0 {
		var v uint32
		for i := 0; i < 5; i++ {
			d := uint32(84)
			if i < rem+1 {
				dd, ok := digit(payload[src+i])
				if !ok {
					return nil, fmt.Errorf("mask payload has an invalid character %q", payload[src+i])
				}
				d = uint32(dd)
			}
			v = v*85 + d
		}

		for i := 0; i < rem; i++ {
			out[dst+i] = byte(v >> (24 - i*8))
		}
	}
	return out, nil
}

// rangeDec is the arithmetic decoder both mask encodings share: a
// carryless 32-bit range coder whose state advances with the byte
// stream, exactly as the documented wire format defines.
type rangeDec struct {
	in             []byte
	pos            int
	low, rng, code uint32
	scaled         uint32
}

func newRangeDec(in []byte) (*rangeDec, error) {
	if len(in) < 4 {
		return nil, fmt.Errorf("mask payload is missing its finalization")
	}
	d := &rangeDec{in: in, rng: 0xffffffff}
	for i := 0; i < 4; i++ {
		d.code = (d.code << 8) | uint32(d.read())
	}
	return d, nil
}

func (d *rangeDec) read() byte {
	if d.pos >= len(d.in) {
		return 0 // canonical payloads never read past the end
	}
	b := d.in[d.pos]
	d.pos++
	return b
}

// freq answers the current symbol index without consuming.
func (d *rangeDec) freq(total uint32) uint32 {
	d.scaled = d.rng / total
	v := (d.code - d.low) / d.scaled
	if v >= total {
		v = total - 1
	}
	return v
}

// decode advances past the symbol [cum, cum+freq) of the total the
// caller passed to freq. Renormalisation is the carryless range
// coder's documented form: emit while the interval spans a carry
// boundary, and clamp a too-small range to keep 16 bits of precision.
func (d *rangeDec) decode(cum, freq uint32) {
	const top, bottom = uint32(1) << 24, uint32(1) << 16
	d.low += d.scaled * cum
	d.rng = d.scaled * freq
	for {
		// The carry condition alone decides the shift; the clamp runs
		// only when the carry test is false and the range is small —
		// the exact short-circuit the wire format's coder specifies.
		if (d.low ^ (d.low + d.rng)) >= top {
			if d.rng >= bottom {
				break
			}
			d.rng = (-d.low) & (bottom - 1)
		}
		d.code = (d.code << 8) | uint32(d.read())
		d.low = d.low << 8
		d.rng = d.rng << 8
	}
}

const (
	maskPad      = 2
	maskCtxCount = 1 << 12
	maskInc      = 14
	maskLimit    = 4096
)

// decodeMask decodes one mask raster (H rows x W columns, row-major
// 0/1) from its payload. The one_bit encoding (`!`) is what Ground
// requests; the lossless (`~`) raster decodes through the same range
// coder with the model's context model, so the lossless path shares
// the coder and follows the documented predictive filter.
func decodeMask(payload string, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("mask dimensions %dx%d are not positive", w, h)
	}
	if len(payload) == 0 || (payload[0] != '!' && payload[0] != '~') {
		return nil, fmt.Errorf("mask payload encoding marker %q is unknown", firstByte(payload))
	}
	if payload[0] == '!' {
		return decodeOneBit(payload, w, h)
	}
	return decodeLossless(payload, w, h)
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}

// decodeOneBit decodes the `!` encoding: a 12-bit context of the
// ten neighbours around each pixel drives adaptive bit frequencies.
func decodeOneBit(payload string, w, h int) ([]byte, error) {
	packed, err := unpack(payload[1:])
	if err != nil {
		return nil, err
	}
	d, err := newRangeDec(packed)
	if err != nil {
		return nil, err
	}
	counts := make([]uint16, maskCtxCount*2)
	for i := range counts {
		counts[i] = 1
	}
	pw := w + 2*maskPad
	padded := make([]byte, pw*(h+maskPad))
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		row := (y+maskPad)*pw + maskPad
		for x := 0; x < w; x++ {
			p := row + x
			ctx := contextAt(padded, p, p-pw, p-2*pw)
			off := ctx << 1
			zero, one := uint32(counts[off]), uint32(counts[off+1])
			bit := uint8(0)
			if d.freq(zero+one) >= zero {
				bit = 1
			}
			if bit == 0 {
				d.decode(0, zero)
			} else {
				d.decode(zero, one)
			}
			padded[p] = bit
			out[y*w+x] = bit
			counts[off+int(bit)] += maskInc
			if uint32(counts[off])+uint32(counts[off+1]) >= maskLimit {
				halve16(counts, off)
				halve16(counts, off+1)
			}
		}
	}
	return out, nil
}

// maxOne is the counts' halving rule: half, but never below one.
func maxOne(v uint32) uint32 {
	if v < 1 {
		return 1
	}
	return v
}

func halve16(a []uint16, i int) {
	h := a[i] >> 1
	if h == 0 {
		h = 1
	}
	a[i] = h
}

// contextAt builds the one_bit context from ten neighbouring pixels:
// two left, three above, two above that, and three above-right.
func contextAt(pad []byte, p, prior, earlier int) int {
	c := int(pad[p-1]) |
		int(pad[p-2])<<1 |
		int(pad[prior-2])<<2 |
		int(pad[prior-1])<<3 |
		int(pad[prior])<<4 |
		int(pad[prior+1])<<5 |
		int(pad[prior+2])<<6 |
		int(pad[earlier-2])<<7 |
		int(pad[earlier-1])<<8 |
		int(pad[earlier])<<9 |
		int(pad[earlier+1])<<10 |
		int(pad[earlier+2])<<11
	return c
}

// regionCentre finds the largest 4-connected region of set bits and
// returns its bounding-box centre: the most defensible single click
// point for a mask, away from stray fragments.
func regionCentre(raster []byte, w, h int) (int, int) {
	seen := make([]bool, len(raster))
	best, bestW, bestH, bestX, bestY := 0, 0, 0, 0, 0
	for i := range raster {
		if raster[i] == 0 || seen[i] {
			continue
		}
		// flood fill one region, tracking its bounds
		size, minX, minY, maxX, maxY := 0, w, h, 0, 0
		stack := []int{i}
		seen[i] = true
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			px, py := p%w, p/w
			if px < minX {
				minX = px
			}
			if px > maxX {
				maxX = px
			}
			if py < minY {
				minY = py
			}
			if py > maxY {
				maxY = py
			}
			if px > 0 && raster[p-1] == 1 && !seen[p-1] {
				seen[p-1] = true
				stack = append(stack, p-1)
			}
			if px < w-1 && raster[p+1] == 1 && !seen[p+1] {
				seen[p+1] = true
				stack = append(stack, p+1)
			}
			if py > 0 && raster[p-w] == 1 && !seen[p-w] {
				seen[p-w] = true
				stack = append(stack, p-w)
			}
			if py < h-1 && raster[p+w] == 1 && !seen[p+w] {
				seen[p+w] = true
				stack = append(stack, p+w)
			}
		}
		if size > best {
			best, bestW, bestH, bestX, bestY = size, maxX-minX+1, maxY-minY+1, minX, minY
		}
	}
	if best == 0 {
		return 0, 0
	}
	return bestX + bestW/2, bestY + bestH/2
}

// decodeLossless decodes the `~` encoding: a predictive filter over
// an adaptively range-coded symbol stream (documented as the lossless
// default). Values >=129 mean the pixel belongs to the object.
func decodeLossless(payload string, w, h int) ([]byte, error) {
	packed, err := unpack(payload[1:])
	if err != nil {
		return nil, err
	}
	if len(packed) < 5 {
		return nil, fmt.Errorf("lossless mask payload is truncated")
	}
	selector := packed[0]
	d, err := newRangeDec(packed[1:])
	if err != nil {
		return nil, err
	}
	length := w * h
	out := make([]byte, length)
	resid := make([]byte, length)
	zero := [32]uint32{}
	for i := range zero {
		zero[i] = 1
	}
	// order-0, order-1 and order-2 adaptive symbol models over 256
	// symbols, exactly the documented context cascade.
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
	oZero := make([]uint16, 256)
	oZeroTotal := 256 * oZeroInit
	for i := range oZero {
		oZero[i] = oZeroInit
	}
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
	prev := 0
	for off := 0; off < length; off++ {
		x, y := off%w, off/w
		zo := zeroCtx(resid, off, x, y, w)
		zf, nzf := zero[zo<<1], zero[zo<<1|1]
		bit := 0
		if d.freq(zf+nzf) >= zf {
			bit = 1
		}
		if bit == 0 {
			d.decode(0, zf)
		} else {
			d.decode(zf, nzf)
		}
		zero[uint32(zo)<<1|uint32(bit)] += 32
		if zero[uint32(zo)<<1]+zero[uint32(zo)<<1|1] >= 16384 {
			zero[uint32(zo)<<1] = maxOne(zero[uint32(zo)<<1] >> 1)
			zero[uint32(zo)<<1|1] = maxOne(zero[uint32(zo)<<1|1] >> 1)
		}
		sym := 0
		if bit == 1 {
			above := 0
			if off >= w {
				above = int(resid[off-w])
			}
			sec := oTwo[o2key{byte(prev), byte(above)}]
			fo := prev << 8
			exZero := sec[0] + uint32(oOne[fo]) + uint32(oZero[0])
			total := sec[256] + uint32(oOneTot[prev]) + uint32(oZeroTotal) - exZero
			target := d.freq(total)
			cum, freq := uint32(0), uint32(1)
			sym = 1
			freq = sec[1] + uint32(oOne[fo+1]) + uint32(oZero[1])
			for cum+freq <= target {
				cum += freq
				sym++
				if sym >= 256 {
					return nil, fmt.Errorf("lossless mask payload is malformed")
				}
				freq = sec[sym] + uint32(oOne[fo+sym]) + uint32(oZero[sym])
			}
			d.decode(cum, freq)
			sec2 := oTwo[o2key{byte(prev), byte(above)}]
			sec2[sym] += oTwoStep
			sec2[256] += oTwoStep
			if sec2[256] >= oTwoLimit {
				var t uint32
				for v := 0; v < 256; v++ {
					sec2[v] >>= 1
					t += sec2[v]
				}
				sec2[256] = t
			}
			oTwo[o2key{byte(prev), byte(above)}] = sec2
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
		resid[off] = byte(sym)
		out[off] = unzig(sym, predict(out, off, x, y, int(selector), w))
		prev = sym
	}
	for i, v := range out {
		if v >= 129 {
			out[i] = 1
		} else {
			out[i] = 0
		}
	}
	return out, nil
}

// zeroCtx is the lossless zero/nonzero context from four neighbours.
func zeroCtx(r []byte, off, x, y, w int) int {
	l, u, ul, ur := 1, 1, 1, 1
	if x > 0 && r[off-1] == 0 {
		l = 0
	}
	if y > 0 && r[off-w] == 0 {
		u = 0
	}
	if x > 0 && y > 0 && r[off-w-1] == 0 {
		ul = 0
	}
	if y > 0 && x < w-1 && r[off-w+1] == 0 {
		ur = 0
	}
	return l | u<<1 | ul<<2 | ur<<3
}

// predict is the lossless spatial predictor chosen by the payload's
// selector byte: 0 paeth, 1 up, 2 left, 3 average, 4 the loze filter.
func predict(out []byte, off, x, y, mode, w int) byte {
	left, up := 0, 0
	if x > 0 {
		left = int(out[off-1])
	}
	if y > 0 {
		up = int(out[off-w])
	}
	switch mode {
	case 1:
		return byte(up)
	case 2:
		return byte(left)
	case 3:
		return byte((left + up) >> 1)
	}
	ul := 0
	if x > 0 && y > 0 {
		ul = int(out[off-w-1])
	}
	if mode == 0 {
		e := left + up - ul
		dl, du, dul := abs(e-left), abs(e-up), abs(e-ul)
		if dl <= du && dl <= dul {
			return byte(left)
		}
		if du <= dul {
			return byte(up)
		}
		return byte(ul)
	}
	if ul >= max(left, up) {
		return byte(min(left, up))
	}
	if ul <= min(left, up) {
		return byte(max(left, up))
	}
	return byte((left + up - ul) & 0xff)
}

// unzig maps a zigzag residual back to a byte around its prediction.
func unzig(v int, pred byte) byte {
	var delta int
	if v&1 == 1 {
		delta = -((v + 1) >> 1)
	} else {
		delta = v >> 1
	}
	return byte((int(pred) + delta) & 0xff)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
