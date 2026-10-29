//go:build !darwin && !linux && !windows

package ocr

// New returns the unavailable stub on other OSes, so the helper
// builds everywhere.
func New() (Driver, error) { return stub{}, nil }
