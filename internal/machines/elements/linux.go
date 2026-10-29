//go:build linux

package elements

import (
	"context"
	_ "embed"
	"fmt"
	"os"
)

//go:embed scripts/linux.py
var linuxScript string

// linuxDriver walks the focused application's AT-SPI tree via an
// embedded Python script (GNOME and KDE ship the stack; the script
// prints a note when it is missing instead of failing).
type linuxDriver struct {
	dir string
	run command
}

func newLinux() (Driver, error) { return newLinuxWith(runCommand) }

// newLinuxWith wires the driver with a specific runner; tests use it
// to drive the real install/invoke path with a script.
func newLinuxWith(run command) (Driver, error) {
	dir, err := newDir()
	if err != nil {
		return nil, err
	}
	if _, err := install(dir, "walker.py", linuxScript); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &linuxDriver{dir: dir, run: run}, nil
}

func (d *linuxDriver) Tree(ctx context.Context, max int) (Tree, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out, err := d.run(ctx, "python3", d.dir+"/walker.py", fmt.Sprint(limit(max)))
	if err != nil {
		return Tree{}, fmt.Errorf("elements: AT-SPI walk: %w", err)
	}
	return tree(out, max)
}

func (d *linuxDriver) Close() error { return os.RemoveAll(d.dir) }
