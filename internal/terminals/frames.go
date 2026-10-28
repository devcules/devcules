package terminals

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
)

// Frame types on the link. serve → runner requests end in a verb;
// runner → serve answers correlate by the request's frame id.
// Payloads below share the envelope from internal/link.
const (
	TypeOpen   = "term.open"
	TypeAttach = "term.attach"
	TypeInput  = "term.input"
	TypeResize = "term.resize"
	TypeDetach = "term.detach"
	TypeClose  = "term.close"
	TypeList   = "term.list"

	TypeOK     = "term.ok"
	TypeError  = "term.error"
	TypeOutput = "term.output"
	TypeExit   = "term.exit"
	TypeListed = "term.listed"
)

// Limits. MaxTerminals bounds tmux sessions per runner; dimensions
// keep one attach from requesting an absurd window; MaxInputBytes
// bounds one decoded input write (the envelope adds its own 1 MiB).
const (
	MaxTerminals  = 16
	MinCols       = 1
	MaxCols       = 500
	MinRows       = 1
	MaxRows       = 500
	MaxInputBytes = 64 << 10
)

// idRE accepts the 16 hex chars serve mints. Both ends validate:
// a crafted id must never reach a shell command line.
var idRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidID reports whether id is a serve-minted terminal or stream id.
func ValidID(id string) bool { return idRE.MatchString(id) }

// NewID mints a random 16-hex-character id from crypto/rand.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SessionName maps a terminal id to its tmux session. The prefix keeps
// devcules sessions greppable (`tmux ls | grep ^dvc-`) and collision
// -free against human sessions on a shared box.
func SessionName(id string) string { return "dvc-" + id }

// ValidSize reports whether cols/rows are a sane window: positive and
// bounded, so a browser cannot request a 0x0 or gigapixel canvas.
func ValidSize(cols, rows int) bool {
	return cols >= MinCols && cols <= MaxCols && rows >= MinRows && rows <= MaxRows
}

// Open asks the runner to create the terminal's tmux session.
type Open struct {
	Term string `json:"term"`
}

// Attach asks the runner to start an attach client for one browser:
// each browser gets its own stream, so phone and desktop share one
// terminal through separate attaches.
type Attach struct {
	Term   string `json:"term"`
	Stream string `json:"stream"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// Input carries browser keystrokes to the attach client, base64
// because link frames are JSON.
type Input struct {
	Stream string `json:"stream"`
	B64    string `json:"b64"`
}

// Resize forwards a browser window change to the attach client.
type Resize struct {
	Stream string `json:"stream"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// Detach stops one attach client; the tmux session keeps running for
// the next attach.
type Detach struct {
	Stream string `json:"stream"`
}

// Close kills the tmux session and every attach on it.
type Close struct {
	Term string `json:"term"`
}

// Failure answers a request the runner refused. Reason is a fixed
// string chosen at the call site, never command output: stderr can
// hold paths.
type Failure struct {
	Reason string `json:"reason"`
}

// Output streams attach-client bytes to the browser, base64 like
// input. Batched on the runner (32 KiB or 10 ms) so a `cat` of a big
// file does not become a frame per read.
type Output struct {
	Stream string `json:"stream"`
	B64    string `json:"b64"`
}

// Exit marks an attach client that ended (the session was killed, the
// browser detached): the stream is over, the terminal may not be.
type Exit struct {
	Stream string `json:"stream"`
}

// TermInfo describes one tmux session for the list.
type TermInfo struct {
	Term    string `json:"term"`
	Created int64  `json:"created"`
}

// Listed answers term.list from the runner's live tmux: serve keeps
// no terminal state of its own, so a serve restart loses nothing.
type Listed struct {
	Terms []TermInfo `json:"terms"`
}

// CheckOpen validates term.open.
func CheckOpen(o Open) error {
	if !ValidID(o.Term) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	return nil
}

// CheckAttach validates term.attach.
func CheckAttach(a Attach) error {
	if !ValidID(a.Term) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	if !ValidID(a.Stream) {
		return fmt.Errorf("stream: want 16 hex chars")
	}
	if !ValidSize(a.Cols, a.Rows) {
		return fmt.Errorf("size: want cols %d-%d, rows %d-%d", MinCols, MaxCols, MinRows, MaxRows)
	}
	return nil
}

// DecodeInput validates term.input and returns the raw bytes: id
// shape plus a 64 KiB decoded ceiling, so a browser cannot wedge the
// runner's writer with one giant frame.
func DecodeInput(in Input) ([]byte, error) {
	if !ValidID(in.Stream) {
		return nil, fmt.Errorf("stream: want 16 hex chars")
	}
	b, err := base64.StdEncoding.DecodeString(in.B64)
	if err != nil {
		return nil, fmt.Errorf("b64: invalid base64")
	}
	if len(b) > MaxInputBytes {
		return nil, fmt.Errorf("b64: decoded %d bytes exceeds %d", len(b), MaxInputBytes)
	}
	return b, nil
}

// CheckResize validates term.resize.
func CheckResize(r Resize) error {
	if !ValidID(r.Stream) {
		return fmt.Errorf("stream: want 16 hex chars")
	}
	if !ValidSize(r.Cols, r.Rows) {
		return fmt.Errorf("size: want cols %d-%d, rows %d-%d", MinCols, MaxCols, MinRows, MaxRows)
	}
	return nil
}

// CheckStream validates stream-only requests (detach).
func CheckStream(stream string) error {
	if !ValidID(stream) {
		return fmt.Errorf("stream: want 16 hex chars")
	}
	return nil
}

// CheckClose validates term.close.
func CheckClose(c Close) error {
	if !ValidID(c.Term) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	return nil
}

// DecodeOutput validates term.output and returns the raw bytes for
// the browser queue. No size ceiling here: the runner batches at
// 32 KiB and the envelope caps the frame at 1 MiB.
func DecodeOutput(o Output) ([]byte, error) {
	if !ValidID(o.Stream) {
		return nil, fmt.Errorf("stream: want 16 hex chars")
	}
	b, err := base64.StdEncoding.DecodeString(o.B64)
	if err != nil {
		return nil, fmt.Errorf("b64: invalid base64")
	}
	return b, nil
}
