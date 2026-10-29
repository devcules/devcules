//go:build linux

package screen

import "image"

type x11Driver struct{}
func newX11() (Driver, error)                      { return x11Driver{}, nil }
func (d x11Driver) Capture() (image.Image, error)  { return nil, ErrUnavailable }
func (d x11Driver) Close() error                   { return nil }
