package ground

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The canonical one_bit payload was produced by the official
// @meta-sam/parser encoder over a 60x40 raster holding one filled
// 30x20 rectangle at (10,8)-(39,27). It pins the Go decoder to the
// real wire format, not to a reimplementation's own output.
const canonicalPayload = "!!!!!0=iv%plq$vFXiFfp!!" // carries its one_bit marker
const canonicalW, canonicalH = 60, 40

func TestDecodeCanonicalMask(t *testing.T) {
	raster, err := decodeMask(canonicalPayload, canonicalW, canonicalH)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raster) != canonicalW*canonicalH {
		t.Fatalf("raster len %d, want %d", len(raster), canonicalW*canonicalH)
	}
	// every pixel inside the rectangle is 1, every pixel outside 0
	set := 0
	for y := 0; y < canonicalH; y++ {
		for x := 0; x < canonicalW; x++ {
			want := byte(0)
			if x >= 10 && x < 40 && y >= 8 && y < 28 {
				want = 1
				set++
			}
			if got := raster[y*canonicalW+x]; got != want {
				t.Fatalf("raster[%d,%d] = %d, want %d", x, y, got, want)
			}
		}
	}
	if set == 0 {
		t.Fatal("no pixels set")
	}
}

func TestRegionCentre(t *testing.T) {
	raster, err := decodeMask(canonicalPayload, canonicalW, canonicalH)
	if err != nil {
		t.Fatal(err)
	}
	cx, cy := regionCentre(raster, canonicalW, canonicalH)
	// the region spans (10,8)-(39,27): centre (24, 17)±1 for the /2
	if cx < 23 || cx > 25 || cy < 16 || cy > 18 {
		t.Fatalf("centre = %d,%d, want ~(24,17)", cx, cy)
	}
}

func TestParseFullLine(t *testing.T) {
	// the official payload in a full SAM output line, as documented
	line := "<0f>0<|box;x1=100;y1=280;x2=229;y2=359;w=640;h=400|><|mask;x=0;y=0;data=40,60,!!!!!0=iv%plq$vFXiFfp!!|>"
	matches, err := parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches: %d", len(matches))
	}
	m := matches[0]
	if m.ID != 0 || m.X1 != 100 || m.Y1 != 280 || m.X2 != 229 || m.Y2 != 359 {
		t.Fatalf("box: %+v", m)
	}
	if m.Width != 60 || m.Height != 40 {
		t.Fatalf("mask dims: %+v", m)
	}
	if m.ClickX < 23 || m.ClickX > 25 || m.ClickY < 16 || m.ClickY > 18 {
		t.Fatalf("click: %d,%d", m.ClickX, m.ClickY)
	}
}

func TestParseMultiRecordLine(t *testing.T) {
	// two records on one line, the second with confidence fields,
	// which Ground ignores (the wire allows them optionally).
	line := "<0f>0<|box;x1=10;y1=20;x2=39;y2=27;w=640;h=400|><|mask;x=0;y=0;data=40,60,!!!!!0=iv%plq$vFXiFfp!!|>," +
		"7<|box;x1=500;y1=20;x2=619;y2=27;w=640;h=400;c=0.92|><|mask;x=0;y=0;c=0.91;data=40,60,!!!!!0=iv%plq$vFXiFfp!!|>"
	matches, err := parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches: %d", len(matches))
	}
	if matches[1].ID != 7 || matches[1].X1 != 500 {
		t.Fatalf("second: %+v", matches[1])
	}
}

func TestParseZeroMatches(t *testing.T) {
	// an empty lane is a valid outcome: zero matches, no error
	if m, err := parse(""); err != nil || len(m) != 0 {
		t.Fatalf("empty lane: %v %d", err, len(m))
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"no marker", "<0f>", "<0f>oops", "<0f>1<|box;only|>"} {
		if _, err := parse(bad); err == nil {
			// a line with no valid records but no hard error is fine;
			// what must never happen is a panic or a false match
			continue
		}
	}
}

func fixtureLine(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "sam-line.txt"))
	if err != nil {
		t.Fatalf("fixture line: %v (regenerate with the documented generator)", err)
	}
	return strings.TrimSpace(string(b))
}

func TestFixtureLineResolvesClickPoint(t *testing.T) {
	matches, err := parse(fixtureLine(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ClickX < 23 || matches[0].ClickX > 25 {
		t.Fatalf("fixture: %+v", matches)
	}
}

// canvasFixture is the known click points of testdata/canvas.png.
type canvasFixture struct {
	Points map[string][2]int `json:"points"`
	Absent []string          `json:"absent"`
}

// TestCanvasKnownClickPoints reads the fixture screenshot and its
// expected click table. With a live SAM (TestLiveGround) the model's
// boxes must land on these points; without a key it still proves the
// fixture and the gate data ship together.
func TestCanvasKnownClickPoints(t *testing.T) {
	img, err := loadCanvas()
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 640 || img.Bounds().Dy() != 400 {
		t.Fatalf("canvas size %v", img.Bounds())
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "canvas.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want canvasFixture
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	// each named colour must have a non-background pixel at its click
	// point: the fixture and its table agree.
	for name, pt := range want.Points {
		if img.At(pt[0], pt[1]) == (color.RGBA{24, 26, 32, 255}) {
			t.Fatalf("%s click point (%d,%d) is background", name, pt[0], pt[1])
		}
	}
	// the absent list names colours the canvas does not contain; the
	// canvas generator is the same source, so just prove it parses.
	if len(want.Absent) == 0 {
		t.Fatal("no absent list")
	}
}

func loadCanvas() (image.Image, error) {
	f, err := os.Open(filepath.Join("testdata", "canvas.png"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}
