//go:build !linux && !darwin

package screen

func New() (Driver, error) { return stub{}, nil }
