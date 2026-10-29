//go:build darwin

package elements

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed scripts/darwin.swift
var darwinScript string

// darwinDriver walks the AX tree via a compiled Swift helper. The
// helper runs as this process (which holds the Accessibility
// permission), prints the shared TSV, and says so in a note when the
// permission is missing instead of failing.
type darwinDriver struct {
	dir      string // walker binary + script
	run      command
	mu       sync.Mutex
	compiled bool
}

func newDarwin() (Driver, error) { return newDarwinWith(runCommand) }

// newDarwinWith wires the driver with a specific runner; tests use it
// to drive the real install/compile/invoke path with a script.
func newDarwinWith(run command) (Driver, error) {
	dir, err := newDir()
	if err != nil {
		return nil, err
	}
	if _, err := install(dir, "walker.swift", darwinScript); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &darwinDriver{dir: dir, run: run}, nil
}

// Tree compiles the walker once (swiftc takes seconds; a binary is
// cached for the driver's life), then runs it with the element cap.
func (d *darwinDriver) Tree(ctx context.Context, max int) (Tree, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout+15*time.Second)
	defer cancel() // swiftc's first compile is slow; give it headroom
	d.mu.Lock()
	defer d.mu.Unlock()
	bin := filepath.Join(d.dir, "walker")
	if !d.compiled {
		script := filepath.Join(d.dir, "walker.swift")
		if _, err := d.run(ctx, "swiftc", "-O", script, "-o", bin); err != nil {
			return Tree{}, fmt.Errorf("elements: compile AX walker: %w", err)
		}
		d.compiled = true
	}
	out, err := d.run(ctx, bin, fmt.Sprint(limit(max)))
	if err != nil {
		return Tree{}, fmt.Errorf("elements: AX walk: %w", err)
	}
	return tree(out, max)
}

func (d *darwinDriver) Close() error { return os.RemoveAll(d.dir) }
