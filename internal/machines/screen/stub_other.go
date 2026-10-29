//go:build !linux

package screen

func New() (Driver, error) { return stub{}, nil }
