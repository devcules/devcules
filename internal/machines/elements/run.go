package elements

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// command runs one backend program and returns its stdout. Tests swap
// it for a fake; only the drivers call it.
type command func(ctx context.Context, name string, args ...string) ([]byte, error)

// runCommand is the production command: stdout captured, stderr
// wrapped into the error. Walkers print only TSV to stdout, so any
// stderr means the run failed.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if errb.Len() > 0 {
			return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errb.String()))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out.Bytes(), nil
}

// install writes one embedded script into the driver's private dir.
// The dir is deleted on Close, so nothing lingers between reads.
func install(dir, name, body string) (string, error) {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("elements: install %s: %w", name, err)
	}
	return p, nil
}

// newDir makes a driver's private dir. Every driver cleans it in
// Close; on setup failure it is removed before the error returns.
func newDir() (string, error) {
	dir, err := os.MkdirTemp("", "devcules-elements-*")
	if err != nil {
		return "", fmt.Errorf("elements: %w", err)
	}
	return dir, nil
}
