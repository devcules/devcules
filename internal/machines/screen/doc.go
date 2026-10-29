// Package screen provides the machine's screen capture path (M4.2).
// On Linux it uses X11 via github.com/jezek/xgb and the SHM extension where
// available; on macOS it runs the system screencapture tool; on Windows the driver is a stub behind build tags.
// Callers receive RGBA frames keyed by a monotonic frame id — no secrets.
package screen
