package elements

import (
	"context"
	"time"
)

// Element is one control the tree exposes: a button, a field, a row.
// Coordinates are native screen pixels, top-left origin, matching the
// screen package's frames — so Centre is a valid input target.
type Element struct {
	Role    string `json:"role"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Enabled bool   `json:"enabled"`
	Focused bool   `json:"focused"`
	Title   string `json:"title"`
	Value   string `json:"value"`
}

// Centre is the element's midpoint, ready for the input package.
func (e Element) Centre() (x, y int) { return e.X + e.W/2, e.Y + e.H/2 }

// Tree is one read of the frontmost application's accessibility tree.
// Note says why Elements can be empty (permission, truncation, no
// desktop stack) in the OS's own words, so an empty read is never
// mistaken for a bare screen.
type Tree struct {
	ScreenW  int       `json:"screenW,omitempty"`
	ScreenH  int       `json:"screenH,omitempty"`
	Front    string    `json:"front,omitempty"`
	Window   string    `json:"window,omitempty"`
	Elements []Element `json:"elements"`
	Note     string    `json:"note,omitempty"`
}

// One read is capped whatever the caller asks: a giant tree costs more
// to ship than it is worth. Default is what an agent usually needs.
// readTimeout bounds one walker run: a wedged tree walk must not hang
// the helper. Compiling the macOS walker is slower, so it gets its own
// budget in its driver.
const (
	Max         = 2000
	Default     = 400
	readTimeout = 10 * time.Second
)

// Driver reads accessibility trees. macOS walks the AX tree, Linux
// AT-SPI, Windows UIA; anything else gets the unavailable stub so the
// helper builds everywhere.
type Driver interface {
	Tree(ctx context.Context, max int) (Tree, error)
	Close() error
}

// stub answers "unavailable" on builds with no walker, so callers have
// one code path on every OS.
type stub struct{}

func (stub) Tree(context.Context, int) (Tree, error) { return Tree{}, ErrUnavailable }
func (stub) Close() error                            { return nil }

// ErrUnavailable is returned by drivers compiled without a backend.
var ErrUnavailable = unavailableErr("no accessibility walker on this OS")

type unavailableErr string

func (e unavailableErr) Error() string { return string(e) }
