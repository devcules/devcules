package main

import (
	"fmt"
	"io"
)

// version, commit and date are set at release time with -ldflags (see
// .goreleaser.yaml). Local builds report the dev defaults, which is also
// what the tests assert.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// runVersion prints the build identity.
func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "devcules version %s (commit %s, built %s)\n", version, commit, date)
	return exitOK
}
