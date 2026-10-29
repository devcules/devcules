//go:build linux

package input

// x11 is the XTest-backed driver on Linux. Stubbed until xtest wiring lands.
type x11Driver struct{}
func newX11() (Driver, error) { return x11Driver{}, nil }
func (x11Driver) Inject(Event) error { return errUnavailable }
func (x11Driver) Close() error       { return nil }
