package ocr

import (
	"image"
	"testing"
)

// parseTSV is pinned to tesseract's real TSV shape: 12 columns,
// header row, confidence in field 11, word in field 12.
const tsvGood = `level	page_num	block_num	par_num	line_num	word_num	left	top	width	height	conf	text
1	1	0	0	0	0	0	0	120	60	95	
5	1	1	1	0	1	12	20	88	14	96	Pay
5	1	1	1	0	2	104	20	40	14	41	no
5	1	1	1	0	3	12	40	200	14	92	now
`

func TestParseTSV(t *testing.T) {
	got := parseTSV([]byte(tsvGood))
	if len(got) != 2 { // "no" is below the confidence floor and drops
		t.Fatalf("words: %d", len(got))
	}
	if got[0].Text != "Pay" || got[0].X != 12 || got[0].Y != 20 || got[0].W != 88 || got[0].H != 14 {
		t.Fatalf("word: %+v", got[0])
	}
	if cx, cy := got[0].Centre(); cx != 56 || cy != 27 {
		t.Fatalf("centre: %d,%d", cx, cy)
	}
	if got[1].Text != "now" {
		t.Fatalf("second word: %+v", got[1])
	}
}

func TestParseTSVEmpty(t *testing.T) {
	if got := parseTSV([]byte("level\tpage\n")); got != nil {
		t.Fatalf("header only should be empty, got %d", len(got))
	}
}

func TestParseWords(t *testing.T) {
	raw := "Pay\t12\t20\t88\t14\nnow\t12\t40\t200\t14\nnote\tcannot load image\n\n"
	got0 := parseWords([]byte(raw))
	if len(got0) != 2 {
		t.Fatalf("words: %d", len(got0))
	}
	cx, cy := got0[0].Centre()
	if got0[0].Text != "Pay" || cx != 56 || cy != 27 {
		t.Fatalf("word: %+v", got0[0])
	}
}

func TestStubUnavailable(t *testing.T) {
	var d Driver = stub{}
	if _, err := d.Read(t.Context(), image.NewRGBA(image.Rect(0, 0, 4, 4))); err != ErrUnavailable {
		t.Fatalf("err: %v", err)
	}
}

func TestNewBuildsOnThisOS(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d == nil {
		t.Fatal("New returned nil driver")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestParseWordsSkipsMalformed(t *testing.T) {
	raw := "Pay\tnot\tan\tint\tbox\n\t12\t20\t88\t14\nnow\t12\t20\t88\t14\n"
	got := parseWords([]byte(raw))
	if len(got) != 1 || got[0].Text != "now" {
		t.Fatalf("words: %+v", got)
	}
}
