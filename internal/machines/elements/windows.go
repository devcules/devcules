//go:build windows

package elements

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed scripts/windows.ps1
var windowsScript string

// windowsDriver walks the focused window's UIA tree via an embedded
// PowerShell script (Windows PowerShell ships the UIAutomation
// .NET types; the script prints a note when access is blocked, e.g. by
// a UAC prompt or the lock screen).
type windowsDriver struct {
	dir string
	run command
}

func newWindows() (Driver, error) { return newWindowsWith(runCommand) }

// newWindowsWith wires the driver with a specific runner; tests use it
// to drive the real install/invoke path with a script.
func newWindowsWith(run command) (Driver, error) {
	dir, err := newDir()
	if err != nil {
		return nil, err
	}
	if _, err := install(dir, "walker.ps1", windowsScript); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &windowsDriver{dir: dir, run: run}, nil
}

func (d *windowsDriver) Tree(ctx context.Context, max int) (Tree, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	out, err := d.run(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(d.dir, "walker.ps1"), fmt.Sprint(limit(max)))
	if err != nil {
		return Tree{}, fmt.Errorf("elements: UIA walk: %w", err)
	}
	return tree(out, max)
}

func (d *windowsDriver) Close() error { return os.RemoveAll(d.dir) }
