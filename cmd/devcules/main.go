package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devcules/devcules/internal/bus/memory"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/plugins/agents"
)

// Exit codes: 0 means done, 1 means the configuration or the run failed,
// 2 means the invocation itself was wrong.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usageLine = "usage: devcules [serve|runner|machine [enrol <code> [--name <name>]]|hook [--agent <id>]|version|health|vault rotate|audit export|audit verify|admin reset-password [--user <name>]|help]"

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
	if name == "reap" {
		// Hidden: an agent's launch script runs it when the agent
		// exits. It needs no config and no link.
		return runReap(rest, stderr)
	}
	if len(rest) > 0 && name != "vault" && name != "audit" && name != "hook" && name != "machine" && name != "admin" {
		return usage(stderr, "too many arguments: %q takes none", name)
	}
	var hookWith agents.Agent
	if name == "hook" && len(rest) > 0 {
		ok := false
		if len(rest) == 2 && rest[0] == "--agent" {
			hookWith, ok = hookAgent(rest[1])
		}
		if !ok {
			return usage(stderr, "hook takes no arguments, or --agent <id> (known: %s)", agentIDs())
		}
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
	case "", "serve", "runner", "machine", "hook", "health", "vault", "audit", "admin":
	default:
		return usage(stderr, "unknown subcommand %q", name)
	}

	cfg, err := config.Load()
	if err != nil {
		return configFailed(hookWith, err, stdout, stderr)
	}

	// Blocking roles wait on these; created once per invocation.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	if name == "health" {
		return runHealth(cfg, stdout, stderr)
	}
	if name == "admin" {
		// Like audit: it works on serve's store, so it runs where
		// serve's data is, and holds no keys.
		return runAdmin(cfg, rest, stderr, openSQLite)
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
		return configFailed(hookWith, err, stdout, stderr)
	}
	// serve binds the listener and runner links to serve, so both
	// block: tests drive runServe/runRunner directly with fakes.
	if role == "hook" {
		return runHook(cfg, hookWith, os.Stdin, stdout, stderr, hookClient{http: &http.Client{Timeout: 45 * time.Second}, sleep: sleepCtx})
	}
	if role == "serve" || role == "runner" {
		return runBlockingRole(cfg, role, stdout, stderr, sigs)
	}
	if role == "machine" {
		return runMachine(cfg, rest, stdout, stderr, sigs, &http.Client{Timeout: 30 * time.Second})
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

// configFailed reports a configuration error. An agent's hook must
// still answer in the agent's protocol, blocking the call: a plain
// exit 1 lets some agents (Muse Code) run it anyway.
func configFailed(hookWith agents.Agent, err error, stdout, stderr io.Writer) int {
	if hookWith != nil {
		return agentReply(hookWith, agents.Answer{Failed: true, Reason: "devcules hook is misconfigured: " + err.Error()}, stdout, stderr)
	}
	fmt.Fprintln(stderr, err.Error())
	return exitError
}
