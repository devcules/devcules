//go:build darwin

package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"time"
)

// New returns the macOS driver: the system's screencapture tool, which
// needs no cgo and ships with every Mac. macOS shows the desktop
// without other apps' windows until the process running the helper
// (the terminal, or launchd's job) is allowed under Privacy &
// Security, Screen Recording.
func New() (Driver, error) {
	path, err := exec.LookPath("screencapture")
	if err != nil {
		path = "/usr/sbin/screencapture"
	}
	if _, err := os.Stat(path); err != nil {
		return nil, ErrUnavailable
	}
	return &darwin{tool: path, timeout: 10 * time.Second}, nil
}

type darwin struct {
	tool    string
	timeout time.Duration
}

// Capture writes one PNG of the main display to a private temp file
// and decodes it. -x keeps the shutter sound off; -C would add the
// cursor, which the model does not need.
func (d *darwin) Capture() (image.Image, error) {
	f, err := os.CreateTemp("", "devcules-screen-*.png")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	ctx, stop := context.WithTimeout(context.Background(), d.timeout)
	defer stop()
	out, err := exec.CommandContext(ctx, d.tool, "-x", "-t", "png", "-m", name).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = "timed out"
		}
		if strings.Contains(msg, "could not create image") {
			// What macOS says when the process may not record the screen.
			return nil, errors.New("screen recording not allowed on this Mac: allow the app running devcules machine in System Settings, Privacy & Security, Screen Recording, then restart it")
		}
		return nil, fmt.Errorf("screen: screencapture failed: %s", msg)
	}
	r, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	img, err := png.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("screen: screencapture wrote no image (allow Screen Recording for the process running devcules machine)")
	}
	return img, nil
}

func (d *darwin) Close() error { return nil }
