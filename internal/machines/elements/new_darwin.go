//go:build darwin

package elements

// New returns the AX-tree driver on macOS.
func New() (Driver, error) { return newDarwin() }
