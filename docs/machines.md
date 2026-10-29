# Machines

Devcules drives real computers through a machine helper linked over
the same WebSocket protocol as runners (`docs/link.md`). Each machine
enrols with a one-time code and proves itself with an ed25519 key.

## Drivers

The machine helper owns four small driver interfaces, one per sense.
Everything ships behind build tags, so the helper builds on every OS
even where a driver is a stub that answers `ErrUnavailable`:

| Package | Interface | macOS | Linux | Windows |
|---|---|---|---|---|
| `internal/machines/screen` | `Capture() (image.Image, error)` | stub | X11 (SHM where available) | stub |
| `internal/machines/input` | `Inject(Event) error` | stub | X11 XTest | stub |
| `internal/machines/elements` | `Tree(ctx, max) (Tree, error)` | AX tree | AT-SPI | UIA |
| `internal/machines/ocr` | `Read(ctx, frame) ([]Word, error)` | Vision | tesseract | WinRT OCR |

The stubs for screen and input on macOS/Windows are placeholders: M4.2
landed the Linux X11 path first, and the remaining platforms follow
the same interface. Elements and OCR ship all three platforms now
because their engines are scriptable per OS.

## Elements: the accessibility tree

`elements.Tree(max)` reads the frontmost application's tree — every
control with its role, pixel box, title and value — into a flat,
typed list. Element centres are valid `input` targets: boxes are in
the same native-pixel space the screen driver captures.

One small walker script per OS, embedded in the binary and run by the
helper so the helper's accessibility permission applies:

- **macOS** `scripts/darwin.swift` — the AX tree, compiled once per
  helper process with `swiftc` (cached) and run as this process.
- **Linux** `scripts/linux.py` — AT-SPI via python3-gi; prints a note
  when the stack is missing instead of failing.
- **Windows** `scripts/windows.ps1` — UI Automation via Windows
  PowerShell.

All three print the same flat TSV (tab-separated, `size`, `front`,
`window`, `el`, `note` lines), which one parser in Go decodes. The
`el` shape: `role, x, y, w, h, enabled, focused, title, value`.
Values of password fields never leave the walker script.

When the tree is empty the `Note` field says why: missing permission,
missing AT-SPI, truncation at the cap, or a locked screen. An empty
tree is never a mystery. Reads cap at 400 elements by default, 2000
max, and note when truncated.

**Text read from the screen is untrusted.** Whatever consumes a Tree
fences it as data before it reaches a model (PLAN §4); a hostile page
must not be able to speak as the system.

## OCR

`ocr.Read(frame)` answers the words on a captured frame with their
pixel boxes, in the frame's own coordinate space:

- **macOS** `scripts/reader.swift` — Vision (`VNRecognizeTextRequest`),
  accurate level, compiled once like the walker.
- **Linux** `tesseract` — the `tsv` output; words below 60% confidence
  drop, because a text-only model acts on these words and noise is
  worse than a gap. Detected at first use, not at start: a machine
  without tesseract gets "is tesseract-ocr installed?" on first read.
- **Windows** `scripts/reader.ps1` — the WinRT OCR engine via
  PowerShell.

A text-only model can still find and click the Pay button: OCR gives
it the words and their boxes, and word centres are valid input targets
against that same frame. Where a screenshot model is available,
elements and OCR are the cheaper, structured fallback.

## Grounding: SAM 3.1

When the tree and the words cannot see a control — canvas apps, remote
desktops, locked UIs — `internal/machines/ground` finds it by sight:
the captured frame plus a short noun phrase ("green Pay button") goes
to Meta's `sam-3.1` over the Responses API, through **serve's model
router** (`/llm/meta/v1/responses`) with a per-launch token — never a
provider key, never the public endpoint from the machine.

The model answers in a special-token grammar (one line, frame marker,
object records of a box and a base85-coded mask). Ground parses it in
pure Go from the documented wire contract:

- `ground.parse` decodes the line into typed matches; record
  boundaries never split on the mask payload's own commas.
- `ground` decodes both mask encodings: `one_bit` (requested — the
  smallest payload) and the lossless `~` default, through a shared
  carryless range coder with adaptive contexts, and takes the **largest
  region's centre** of the mask as the click point — the most
  defensible single target, away from stray fragments.
- A concept with zero matches is a valid outcome: `Ground` returns an
  empty list, never an error.

The decoder is pinned to the real wire format by three fixtures
generated with the official parser library: a canonical mask payload,
a per-pixel decision trace, and a per-shift coder-state trace — the Go
decoder is bit-identical to the reference, not merely self-consistent.

**The click lands where the mask says, not where a box guesses**: box
centres are wrong for L-shaped or fragmented controls; the mask
region's centre is the point the model actually segmented.

## Wiring

Serve never touches these drivers directly. The machine helper reads
screen, tree and words locally, and ships typed frames over the link;
the API and the MCP server expose them. Drivers are per-machine
capabilities advertised in the link hello, and a driver that answers
`ErrUnavailable` reports as "not on this machine" instead of failing
the helper.

## Task view and take control (M4.5)

Serve exposes the machine's senses to the owner through
session-guarded REST, relayed over the link by
`internal/machines/relay` (request/reply correlated by frame id, 5 s
round trip, mirroring the terminal relay):

| Route | What it does |
|---|---|
| `GET /api/machines` | the linked machine names and the mirrored budget |
| `GET /api/machines-screen?machine=` | one PNG capture (`image/png`) |
| `GET /api/machines-tree?machine=` | the accessibility tree as JSON |
| `POST /api/machines-input {kind,x,y,key,text}` | inject one input event — **take control** |
| `POST /api/machines-ground {phrase}` | SAM matches with click points |
| `POST /api/machines-verify` | the done-verifier's answer: base64 PNG, tree, note |
| `GET /api/machines-budget` | the mirrored remaining actions |

With several machines linked every route takes `?machine=<name>`;
with exactly one the parameter may be omitted. Zero machines is 503.

**The action budget is enforced at the machine, not serve.** The
helper starts each task at the plan's 40 screen actions, spends one
per injected input, and refuses at zero with `mach.error "budget
exhausted"` — no serve-side bug or restart can move a machine past
its budget. Serve keeps an in-memory mirror (start 40, decrement per
accepted inject, reset on `mach.budget`) only so the UI can show
what is left; a serve restart loses the mirror but never the
authority, and the machine's counter is the one that refuses.

**The done-verifier judges on the live screen.** `mach.verify` asks
the machine for one fresh capture plus one fresh tree read, answered
together with a note: a task may only report success against what
the machine sees *now*, not against a frame from before its last
action. An empty answer ("no screen: the verifier saw nothing") is
a failure, not a pass.

The web UI's Machines tab (M4.5) builds on these routes: the machine
list, a live screen view that re-fetches every 2 s, a Take-control
toggle that maps clicks on the rendered frame to screen pixels and
posts `machines-input`, a phrase box that grounds and draws the
match markers, and a Check-done button that shows the verifier's
note with the remaining budget beside it.
