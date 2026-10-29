//go:build !linux

package input

// New returns a platform-appropriate input driver.
func New() (Driver, error) { return stub{}, nil }
