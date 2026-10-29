package ocr

import (
	"strconv"
	"strings"
)

// parseWords decodes the shared OCR TSV (text\tx\ty\tw\th, one word
// per line — the shape the Swift and PowerShell readers print). Lines
// that are not five fields are skipped, not errored: an engine adding
// a note must never lose the words already read.
func parseWords(raw []byte) []Word {
	var words []Word
	for line := range strings.Lines(string(raw)) {
		f := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
		if len(f) != 5 {
			continue
		}
		var n [4]int
		bad := false
		for i, s := range f[1:] {
			v, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				bad = true
				break
			}
			n[i] = v
		}
		if bad || strings.TrimSpace(f[0]) == "" {
			continue
		}
		words = append(words, Word{Text: f[0], X: n[0], Y: n[1], W: n[2], H: n[3]})
	}
	return words
}

// parseTSV decodes tesseract's TSV: 12 columns, word text in field 12,
// box in 7-10, confidence in 11. The header row is skipped.
func parseTSV(raw []byte) []Word {
	var words []Word
	for line := range strings.Lines(string(raw)) {
		f := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
		if len(f) < 12 || f[0] == "level" {
			continue
		}
		conf, err := strconv.ParseFloat(f[10], 64)
		if err != nil || conf < 60 || strings.TrimSpace(f[11]) == "" {
			continue
		}
		x, ex := strconv.Atoi(f[6])
		y, ey := strconv.Atoi(f[7])
		w, ew := strconv.Atoi(f[8])
		h, eh := strconv.Atoi(f[9])
		if ex != nil || ey != nil || ew != nil || eh != nil {
			continue
		}
		words = append(words, Word{Text: f[11], X: x, Y: y, W: w, H: h})
	}
	return words
}
