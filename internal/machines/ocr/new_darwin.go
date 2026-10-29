//go:build darwin

package ocr

// New returns the Vision driver on macOS.
func New() (Driver, error) { return newDarwin() }
