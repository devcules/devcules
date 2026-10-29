//go:build windows

package ocr

// New returns the WinRT driver on Windows.
func New() (Driver, error) { return newWindows() }
