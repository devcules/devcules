//go:build linux

package input

func New() (Driver, error) { return newX11() }
