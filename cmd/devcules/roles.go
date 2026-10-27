package main

import (
	"fmt"
	"io"

	"github.com/devcules/devcules/internal/config"
)

// runRole starts the named role. In M0.2 every role is a stub: it proves the
// configuration loads and reports what it would start, then exits.
func runRole(name string, cfg config.Config, stdout io.Writer) int {
	fmt.Fprintf(stdout, "%s: listening on %s, data in %s\n", name, cfg.Listen, cfg.DataDir)
	fmt.Fprintln(stdout, "(stub: the role itself lands in a later module)")
	return exitOK
}
