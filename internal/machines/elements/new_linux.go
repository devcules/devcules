//go:build linux

package elements

// New returns the AT-SPI driver on Linux.
func New() (Driver, error) { return newLinux() }
