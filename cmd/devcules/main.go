package main

import (
	"fmt"
	"io"
	"os"

	"github.com/devcules/devcules/internal/config"
)

// Exit codes: 0 means done, 1 means the configuration or the run failed,
// 2 means the invocation itself was wrong.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usageLine = "usage: devcules [serve|runner|machine|hook|version|health|help]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one invocation and returns its exit code. Writers are
// parameters so tests can capture output without subprocesses.
func run(args []string, stdout, stderr io.Writer) int {
	name := ""
	rest := []string{}
	if len(args) > 0 {
		name, rest = args[0], args[1:]
	}
	if len(rest) > 0 {
		return usage(stderr, "too many arguments: %q takes none", name)
	}

	// version, help and usage errors never need configuration: they are most
	// useful exactly when the setup is broken.
	switch name {
	case "version":
		return runVersion(stdout)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usageLine)
		return exitOK
	case "", "serve", "runner", "machine", "hook", "health":
	default:
		return usage(stderr, "unknown subcommand %q", name)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}

	if name == "health" {
		return runHealth(cfg, stdout, stderr)
	}
	// No subcommand: DEVCULES_ROLE decides.
	role := name
	if role == "" {
		role = cfg.Role
	}
	cfg, err = cfg.ForRole(role)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	return runRole(role, cfg, stdout)
}

// usage explains the invocation on stderr. It always exits non-zero, so
// callers return its result directly.
func usage(stderr io.Writer, format string, args ...any) int {
	if format != "" {
		fmt.Fprintf(stderr, "devcules: "+format+"\n", args...)
	}
	fmt.Fprintln(stderr, usageLine)
	return exitUsage
}
