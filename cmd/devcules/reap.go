package main

import (
	"fmt"
	"io"

	"github.com/devcules/devcules/internal/terminals/pty"
)

// runReap serves the hidden `devcules reap <launch dir>`: an agent's
// launch script runs it when the agent exits, and it stops every
// process still pointing into that launch dir (a daemon the agent
// detached), sparing itself and the script that runs it.
func runReap(args []string, stderr io.Writer) int {
	if len(args) != 1 || !pty.ValidLaunchDir(args[0]) {
		return usage(stderr, "reap takes one launch dir (/…/launch-<16 hex>)")
	}
	if _, err := pty.Reap(args[0]); err != nil {
		fmt.Fprintln(stderr, "reap: "+err.Error())
		return exitError
	}
	return exitOK
}
