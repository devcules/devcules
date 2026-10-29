package ocr

import (
	"context"
	"image"
	"time"
)

// Word is one recognised word and its pixel box, top-left origin, in
// the frame's own coordinate space — so Centre is a valid input
// target against that same frame.
type Word struct {
	Text string `json:"text"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

// Centre is the word's midpoint, ready for the input package.
func (w Word) Centre() (x, y int) { return w.X + w.W/2, w.Y + w.H/2 }

// readTimeout bounds one engine run: a wedged engine must not hang
// the helper. Vision gets headroom for its first compile in its
// driver; the rest use it as is.
const readTimeout = 45 * time.Second

// Driver reads words from a frame. macOS runs Vision, Linux
// tesseract, Windows WinRT OCR; anything else gets the unavailable
// stub so the helper builds everywhere.
type Driver interface {
	Read(ctx context.Context, frame image.Image) ([]Word, error)
	Close() error
}

// stub answers "unavailable" on builds with no engine.
type stub struct{}

func (stub) Read(context.Context, image.Image) ([]Word, error) { return nil, ErrUnavailable }
func (stub) Close() error                                      { return nil }

// ErrUnavailable is returned by drivers compiled without an engine.
var ErrUnavailable = unavailableErr("no OCR engine on this OS")

type unavailableErr string

func (e unavailableErr) Error() string { return string(e) }
