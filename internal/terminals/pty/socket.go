package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// maxSocketPath is the longest Unix domain socket path the OS binds.
// macOS allows 104 bytes (sysctl net.local.dgram.maxdgram); Linux 108.
// tmux appends /tmux-<uid>/devcules to TMUX_TMPDIR, and temp dirs on
// macOS (t.TempDir under $TMPDIR, ~45 chars before the test suffix)
// can push the full socket past the limit: every tmux call then dies
// with "File name too long". One shared budget keeps every platform
// inside its limit.
var maxSocketPath = map[string]int{
	"darwin":  104,
	"linux":   108,
	"windows": 260,
}[runtime.GOOS]

// shortenSocket moves the tmux socket dir to a short private path
// when the one given would overflow the OS socket limit. It returns
// the effective dir and an undo that removes the relocation (safe to
// call more than once). When the path already fits, the dir is
// returned as is and undo is a no-op: the common case on Linux.
func shortenSocket(dir string) (string, func(), error) {
	noop := func() {}
	socket := filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), "devcules")
	if len(socket)+16 <= maxSocketPath { // headroom for tmux's own names
		return dir, noop, nil
	}
	// A short deterministic base: /tmp on darwin/linux (14 chars of
	// "tmux-<uid>/devcules" leaves ~80), the user temp dir elsewhere.
	base := os.TempDir()
	short, err := os.MkdirTemp(base, "dvc-pty-")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create short socket dir under %s: %s", base, err)
	}
	if err := os.Chmod(short, 0o700); err != nil {
		os.RemoveAll(short)
		return "", nil, fmt.Errorf("cannot secure socket dir: %s", err)
	}
	var once bool
	return short, func() {
		if !once {
			once = true
			os.RemoveAll(short)
		}
	}, nil
}
