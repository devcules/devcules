package link

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// Version is the only link protocol version accepted. A frame carrying
// anything else is rejected before the upgrade completes its handshake,
// so mixed-version fleets fail loudly instead of misbehaving.
const Version = 1

// MaxFrameBytes bounds one decoded frame. The hub enforces the same
// limit on the read side, so a peer cannot make serve buffer
// unboundedly. 1 MiB fits terminal output and PTY batches (M1.4);
// larger payloads need chunking in a later module.
const MaxFrameBytes = 1 << 20

// maxIDRunes bounds the id field: long enough for UUIDs, short enough
// to keep link bookkeeping cheap.
const maxIDRunes = 64

// typeRE keeps handler names routable: dotted lowercase paths such as
// "term.write". Uppercase, spaces and slashes never reach a handler.
var typeRE = regexp.MustCompile(`^[a-z][a-z0-9.]{0,31}$`)

// ValidType reports whether t is a routable handler name. The hub
// enforces it when registering handlers, so a typo fails at startup,
// not on the first frame.
func ValidType(t string) bool { return typeRE.MatchString(t) }

// Frame is one link message. Data stays json.RawMessage so each handler
// decodes its own shape; the codec only proves the envelope is sound.
type Frame struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

// DecodeError names the envelope field that failed and why. The hub
// maps Field "v" to the version close code and everything else to the
// handshake failure code; callers use errors.As, never string matching.
type DecodeError struct {
	Field   string
	Problem string
}

func (e *DecodeError) Error() string {
	return e.Field + ": " + e.Problem
}

// Decode parses and strictly validates one frame. Unknown top-level
// fields are rejected so a misspelled "typ" fails instead of silently
// becoming an unhandled type. Trailing bytes after the JSON value are
// rejected for the same reason: one frame is exactly one value.
func Decode(b []byte) (Frame, error) {
	if len(b) > MaxFrameBytes {
		return Frame{}, &DecodeError{Field: "frame", Problem: fmt.Sprintf("%d bytes exceeds the %d byte limit", len(b), MaxFrameBytes)}
	}
	var f Frame
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Frame{}, &DecodeError{Field: "frame", Problem: "not a JSON object: " + firstLine(err.Error())}
	}
	if dec.More() {
		return Frame{}, &DecodeError{Field: "frame", Problem: "trailing data after the JSON value"}
	}
	if f.V != Version {
		return Frame{}, &DecodeError{Field: "v", Problem: fmt.Sprintf("want %d (unsupported link version)", Version)}
	}
	if !ValidType(f.Type) {
		return Frame{}, &DecodeError{Field: "type", Problem: "want ^[a-z][a-z0-9.]{0,31}$"}
	}
	if n := utf8.RuneCountInString(f.ID); n == 0 || n > maxIDRunes {
		return Frame{}, &DecodeError{Field: "id", Problem: fmt.Sprintf("want 1-%d characters", maxIDRunes)}
	}
	if len(f.Data) == 0 || string(f.Data) == "null" {
		return Frame{}, &DecodeError{Field: "data", Problem: "missing (every frame carries a data object)"}
	}
	return f, nil
}

// Encode validates the envelope and marshals the frame. Handlers build
// Frames and send the bytes; Encode is the one place the envelope
// rules live, so hub and dial cannot drift apart.
func Encode(f Frame) ([]byte, error) {
	if f.V != Version {
		return nil, &DecodeError{Field: "v", Problem: fmt.Sprintf("want %d (unsupported link version)", Version)}
	}
	if !ValidType(f.Type) {
		return nil, &DecodeError{Field: "type", Problem: "want ^[a-z][a-z0-9.]{0,31}$"}
	}
	if n := utf8.RuneCountInString(f.ID); n == 0 || n > maxIDRunes {
		return nil, &DecodeError{Field: "id", Problem: fmt.Sprintf("want 1-%d characters", maxIDRunes)}
	}
	if len(f.Data) == 0 || string(f.Data) == "null" {
		return nil, &DecodeError{Field: "data", Problem: "missing (every frame carries a data object)"}
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, &DecodeError{Field: "frame", Problem: "cannot encode: " + firstLine(err.Error())}
	}
	if len(b) > MaxFrameBytes {
		return nil, &DecodeError{Field: "frame", Problem: fmt.Sprintf("%d bytes exceeds the %d byte limit", len(b), MaxFrameBytes)}
	}
	return b, nil
}

// firstLine keeps nested JSON errors short: the codec reports the
// field and the fix, not the decoder's full paragraph.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// NewID returns a random 16-hex-character frame id from crypto/rand.
// Random ids (not counters) keep correlating safe across reconnects:
// a resent frame never collides with an earlier generation.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Hello is the runner's first frame: its role, its link name (unique
// per runner; a duplicate name replaces the old link) and its build
// version. The hub answers with Welcome.
type Hello struct {
	Role    string `json:"role"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Welcome answers a hello. Session identifies this link generation;
// after a reconnect the session changes, so M1.4 terminal state keyed
// by session never mixes generations.
type Welcome struct {
	Session string `json:"session"`
}

// NewSession returns a random 32-hex-character link session id.
func NewSession() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
