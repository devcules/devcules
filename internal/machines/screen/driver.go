package screen

import "image"

// Driver captures frames from the machine's display.
type Driver interface {
	Capture() (image.Image, error)
	Close() error
}

// stub is built when no X11/macOS/Windows driver is compiled in.
type stub struct{}

func (stub) Capture() (image.Image, error) { return nil, ErrUnavailable }
func (stub) Close() error                  { return nil }

var ErrUnavailable = unavailableErr("screen driver unavailable on this build")

type unavailableErr string
func (e unavailableErr) Error() string { return string(e) }
