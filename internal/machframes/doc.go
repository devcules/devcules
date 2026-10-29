// Package machframes defines the machine-side link protocol (M4.5):
// the frame types and payloads a machine helper and serve exchange to
// drive a real computer — screen captures, input injection, the
// accessibility tree, OCR words and SAM grounding. It mirrors the
// terminals protocol's shape (serve requests end in a verb; the
// machine answers correlate by the request's frame id) and shares the
// link envelope from internal/link.
//
// It holds only types: no package may grow behaviour here, so the
// protocol stays one small honest surface both ends compile against.
package machframes

// Frame types on the link. serve → machine requests; machine → serve
// answers.
const (
	TypeScreen = "mach.screen" // serve → machine: capture one frame
	TypeTree   = "mach.tree"   // serve → machine: read the AX/AT-SPI/UIA tree
	TypeWords  = "mach.words"  // serve → machine: OCR words off the last frame
	TypeInput  = "mach.input"  // serve → machine: inject one input event
	TypeGround = "mach.ground" // serve → machine: SAM-ground a phrase (M4.4 client rides the machine's router session)
	TypeBudget = "mach.budget" // serve → machine: the task's remaining action budget
	TypeVerify = "mach.verify" // serve → machine: done-verifier: fresh screen + tree read

	TypeOK        = "mach.ok"        // machine → serve: request accepted (data per request)
	TypeFailure   = "mach.error"     // machine → serve: request refused
	TypeFrame     = "mach.frame"     // machine → serve: one captured frame (PNG bytes)
	TypeTreeOut   = "mach.treeout"   // machine → serve: the tree
	TypeWordsOut  = "mach.wordsout"  // machine → serve: OCR words
	TypeGroundOut = "mach.groundout" // machine → serve: grounded matches
	TypeVerifyOut = "mach.verifyout" // machine → serve: done-verifier result
)

// Screen asks for one capture. MaxBytes bounds the PNG the machine
// may answer with; smaller frames need no negotiation.
type Screen struct {
	MaxBytes int `json:"maxBytes"`
}

// Tree asks for the accessibility tree, capped as the elements
// package caps it (0 = its default).
type Tree struct {
	Max int `json:"max,omitempty"`
}

// Input is one input event to inject. The machine driver maps it to
// the OS; Kind is one of move, tap, key, text.
type Input struct {
	Kind string `json:"kind"`
	X    int    `json:"x,omitempty"`
	Y    int    `json:"y,omitempty"`
	Key  string `json:"key,omitempty"`
	Text string `json:"text,omitempty"`
}

// Ground asks the machine to SAM-ground a phrase against its live
// screen. The machine holds the router session (its launch token);
// serve only relays the request, so no key ever crosses the link.
type Ground struct {
	Phrase   string `json:"phrase"`
	MaxBytes int    `json:"maxBytes"`
}

// Budget tells the machine how many screen actions the task may
// still spend; a machine at zero refuses further input with
// TypeFailure "budget exhausted".
type Budget struct {
	Remaining int `json:"remaining"`
}

// Verify asks the done-verifier for a fresh look: one capture plus
// one tree read, answered together.
type Verify struct {
	Phrase string `json:"phrase"` // what "done" should look like, for the note
}

// Failure answers a refused request with the reason.
type Failure struct {
	Reason string `json:"reason"`
}

// FrameOut is one captured screen: PNG bytes, capped by the request.
// A capture that does not fit is shrunk; Scale is then the screen
// pixels per frame pixel, so a point on the frame times Scale is the
// point on the screen. Zero or one means full size.
type FrameOut struct {
	PNG   []byte  `json:"png"`
	Scale float64 `json:"scale,omitempty"`
}

// BudgetMax is the plan's per-task action budget: 40 screen actions.
const BudgetMax = 40
