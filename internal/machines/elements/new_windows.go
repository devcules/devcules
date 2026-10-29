//go:build windows

package elements

// New returns the UIA driver on Windows.
func New() (Driver, error) { return newWindows() }
