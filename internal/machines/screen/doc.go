// Package screen provides the machine's screen capture path (M4.2).
// On Linux it uses X11 via github.com/jezek/xgb and the SHM extension where
// available; on macOS/Windows the drivers are stubs behind build tags.
// Callers receive RGBA frames keyed by a monotonic frame id — no secrets.
package screen
