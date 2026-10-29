package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/devcules/devcules/internal/bus/memory"
	"github.com/devcules/devcules/internal/config"
)

// Exit codes: 0 means done, 1 means the configuration or the run failed,
// 2 means the invocation itself was wrong.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usageLine = "usage: devcules [serve|runner|machine|hook|version|health|vault rotate|audit export|audit verify|help]"

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
	if len(rest) > 0 && name != "vault" && name != "audit" {
		return usage(stderr, "too many arguments: %q takes none", name)
	}

	// version, help and usage errors never need configuration: they are most
	// useful exactly when the setup is broken.
	switch name {
	case "mockllm":
		// Hidden test tooling for scripts/smoke.sh; needs no config.
		return runMockLLM(stderr)
	case "version":
		return runVersion(stdout)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usageLine)
		return exitOK
	case "", "serve", "runner", "machine", "hook", "health", "vault", "audit":
	default:
		return usage(stderr, "unknown subcommand %q", name)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}

	// Blocking roles wait on these; created once per invocation.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	if name == "health" {
		return runHealth(cfg, stdout, stderr)
	}
	if name == "audit" {
		// The audit commands read the store only; they hold no
		// secrets, so they run anywhere serve's data is.
		return runAudit(cfg, rest, os.Stdin, stdout, stderr, openSQLite)
	}
	if name == "vault" {
		if cfg, err = cfg.ForRole("vault"); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return exitError
		}
		return runVault(cfg, rest, stdout, stderr, openSQLite)
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
	// serve binds the listener and runner links to serve, so both
	// block: tests drive runServe/runRunner directly with fakes.
	if role == "serve" || role == "runner" {
		return runBlockingRole(cfg, role, stdout, stderr, sigs)
	}
	return runRole(role, cfg, stdout)
}

// runBlockingRole starts serve or runner for real.
func runBlockingRole(cfg config.Config, role string, stdout, stderr io.Writer, sigs chan os.Signal) int {
	if role == "runner" {
		return runRunner(cfg, stdout, stderr, sigs)
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "cannot listen on %s: %s\n", cfg.Listen, err)
		return exitError
	}
	return runServe(cfg, stdout, stderr, ln, sigs, openSQLite, memory.New())
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
