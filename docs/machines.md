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

## Wiring

Serve never touches these drivers directly. The machine helper reads
screen, tree and words locally, and ships typed frames over the link;
the API and the MCP server expose them. Drivers are per-machine
capabilities advertised in the link hello, and a driver that answers
`ErrUnavailable` reports as "not on this machine" instead of failing
the helper.
