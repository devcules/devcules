package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/config"
)

// cmdEnv lists every variable the command reads, so cases stay isolated.
var cmdEnv = []string{
	"DEVCULES_ROLE",
	"DEVCULES_LISTEN",
	"DEVCULES_DATA_DIR",
	"DEVCULES_DATABASE_URL",
	"DEVCULES_VAULT_KEY",
	"DEVCULES_VAULT_KEY_FILE",
	"DEVCULES_SERVE_URL",
	"DEVCULES_RUNNER_TOKEN",
	"DEVCULES_RUNNER_TOKEN_FILE",
	"DEVCULES_WORKSPACE",
	"DEVCULES_PUBLIC_URL",
	"META_API_KEY",
	"META_API_KEY_FILE",
	"HOME",
	"DEVCULES_LAUNCH_TOKEN",
	"DEVCULES_LAUNCH_TOKEN_FILE",
	"DEVCULES_PUSH_CONTACT",
}

func setCmdEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	saved := make(map[string]string, len(cmdEnv))
	for _, k := range cmdEnv {
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = v
		}
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range cmdEnv {
			os.Unsetenv(k)
		}
		for k, v := range saved {
			os.Setenv(k, v)
		}
	})
	os.Setenv("HOME", "/test/home")
	for k, v := range vars {
		os.Setenv(k, v)
	}
}

// invoke runs the command with vars set and returns exit code, stdout,
// stderr.
func invoke(t *testing.T, vars map[string]string, args ...string) (int, string, string) {
	t.Helper()
	setCmdEnv(t, vars)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// checkHealth is driven with faked answers: no sockets open, so the cases
// run anywhere, including sandboxes without loopback.
func TestCheckHealth(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		err      error
		wantExit int
		wantOut  string
	}{
		{"ok", 200, nil, exitOK, "ok: http://127.0.0.1:7659/healthz"},
		{"unhealthy status", 500, nil, exitError, "not healthy"},
		{"refused", 0, errors.New("connection refused"), exitError, "not running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			get := func(url string) (int, error) {
				if url != "http://127.0.0.1:7659/healthz" {
					t.Fatalf("probed %q, want the configured health URL", url)
				}
				return tc.code, tc.err
			}
			if code := checkHealth("http://127.0.0.1:7659/healthz", get, &stdout, &stderr); code != tc.wantExit {
				t.Fatalf("exit = %d, want %d (out %q err %q)", code, tc.wantExit, stdout.String(), stderr.String())
			}
			if out := stdout.String() + stderr.String(); !strings.Contains(out, tc.wantOut) {
				t.Fatalf("output = %q, want it to contain %q", out, tc.wantOut)
			}
		})
	}
}

func TestHealthURL(t *testing.T) {
	cfg := config.Config{Listen: "127.0.0.1:7659"}
	if got := healthURL(cfg); got != "http://127.0.0.1:7659/healthz" {
		t.Fatalf("healthURL = %q", got)
	}
}

func TestRun(t *testing.T) {
	key := map[string]string{"DEVCULES_VAULT_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}

	t.Run("version", func(t *testing.T) {
		code, stdout, stderr := invoke(t, key, "version")
		if code != exitOK {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(stdout, "devcules version dev") {
			t.Fatalf("stdout = %q, want dev version line", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("version needs no config", func(t *testing.T) {
		code, stdout, _ := invoke(t, map[string]string{}, "version")
		if code != exitOK || !strings.Contains(stdout, "devcules version dev") {
			t.Fatalf("exit = %d, stdout = %q, want version without configuration", code, stdout)
		}
	})

	// machine stays a stub and exits; serve binds and runner idles, so
	// both block and are driven directly in serve_test.go and
	// runner_test.go with fakes, and hook is driven in hook_test.go.
	for _, role := range []string{"machine"} {
		t.Run("role "+role, func(t *testing.T) {
			code, stdout, stderr := invoke(t, map[string]string{}, role)
			if code != exitOK {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !strings.Contains(stdout, role+": listening on 127.0.0.1:7659") {
				t.Fatalf("stdout = %q, want role stub line", stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}

	t.Run("subcommand wins over role", func(t *testing.T) {
		env := map[string]string{"DEVCULES_ROLE": "runner"}
		code, stdout, _ := invoke(t, env, "machine")
		if code != exitOK || !strings.HasPrefix(stdout, "machine:") {
			t.Fatalf("exit = %d, stdout = %q, want the machine stub", code, stdout)
		}
	})

	t.Run("no subcommand uses role", func(t *testing.T) {
		env := map[string]string{"DEVCULES_ROLE": "machine"}
		code, stdout, _ := invoke(t, env)
		if code != exitOK || !strings.HasPrefix(stdout, "machine:") {
			t.Fatalf("exit = %d, stdout = %q, want the machine stub", code, stdout)
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		code, _, stderr := invoke(t, key, "frobnicate")
		if code != exitUsage {
			t.Fatalf("exit = %d, want %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "unknown subcommand") || !strings.Contains(stderr, "usage:") {
			t.Fatalf("stderr = %q, want error plus usage", stderr)
		}
	})

	t.Run("extra arguments", func(t *testing.T) {
		code, _, stderr := invoke(t, key, "serve", "extra")
		if code != exitUsage {
			t.Fatalf("exit = %d, want %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "too many arguments") {
			t.Fatalf("stderr = %q, want argument error", stderr)
		}
	})

	t.Run("bad config", func(t *testing.T) {
		code, _, stderr := invoke(t, map[string]string{"DEVCULES_LISTEN": "notaport"}, "serve")
		if code != exitError {
			t.Fatalf("exit = %d, want %d", code, exitError)
		}
		if !strings.Contains(stderr, "DEVCULES_LISTEN invalid") {
			t.Fatalf("stderr = %q, want the listen error", stderr)
		}
	})

	t.Run("hook needs a launch token", func(t *testing.T) {
		code, _, stderr := invoke(t, map[string]string{}, "hook")
		if code != exitError || !strings.Contains(stderr, "DEVCULES_LAUNCH_TOKEN") {
			t.Fatalf("exit = %d, stderr = %q, want the missing token named", code, stderr)
		}
	})

	t.Run("agent hook blocks without a launch token", func(t *testing.T) {
		code, stdout, stderr := invoke(t, map[string]string{}, "hook", "--agent", "musecode")
		if code != 2 || stdout != "" || !strings.Contains(stderr, "so it is blocked: this terminal has no launch token") {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q, want a fail-closed block", code, stdout, stderr)
		}
	})

	for _, args := range [][]string{{"hook", "--agent", "nope"}, {"hook", "--agent"}, {"hook", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, _, stderr := invoke(t, map[string]string{}, args...)
			// The usage lists every registered adapter: musecode since
			// M3.3, claudecode and codex since M3.4.
			if code != exitUsage || !strings.Contains(stderr, "--agent <id> (known: musecode, claudecode, codex)") {
				t.Fatalf("exit = %d, stderr = %q, want usage with the known agents", code, stderr)
			}
		})
	}

	// The vault key is optional everywhere and forbidden outside serve.
	// serve needs a key and runner blocks, so only the stub qualifies here.
	for _, role := range []string{"machine"} {
		t.Run(role+" runs without a vault key", func(t *testing.T) {
			code, _, stderr := invoke(t, map[string]string{"DEVCULES_LISTEN": "127.0.0.1:1"}, role)
			if code != exitOK {
				t.Fatalf("exit = %d, stderr = %q, want 0", code, stderr)
			}
		})
	}

	for _, role := range []string{"runner", "machine", "hook"} {
		t.Run(role+" rejects a vault key", func(t *testing.T) {
			code, _, stderr := invoke(t, key, role)
			if code != exitError || !strings.Contains(stderr, "DEVCULES_VAULT_KEY must not be set for "+role) {
				t.Fatalf("exit = %d, stderr = %q, want the vault key rejected", code, stderr)
			}
		})
	}

	t.Run("role from env rejects a vault key", func(t *testing.T) {
		env := map[string]string{"DEVCULES_VAULT_KEY": key["DEVCULES_VAULT_KEY"], "DEVCULES_ROLE": "runner"}
		code, _, stderr := invoke(t, env)
		if code != exitError || !strings.Contains(stderr, "must not be set for runner") {
			t.Fatalf("exit = %d, stderr = %q, want the vault key rejected", code, stderr)
		}
	})

	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg+" needs no config", func(t *testing.T) {
			code, stdout, stderr := invoke(t, map[string]string{"DEVCULES_LISTEN": "notaport"}, arg)
			if code != exitOK || !strings.Contains(stdout, "usage:") || stderr != "" {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q, want usage on stdout", code, stdout, stderr)
			}
		})
	}

	t.Run("unknown subcommand needs no config", func(t *testing.T) {
		code, _, stderr := invoke(t, map[string]string{"DEVCULES_LISTEN": "notaport"}, "frobnicate")
		if code != exitUsage || !strings.Contains(stderr, "unknown subcommand") || strings.Contains(stderr, "invalid config") {
			t.Fatalf("exit = %d, stderr = %q, want usage error without a config error", code, stderr)
		}
	})

	t.Run("health dispatches to the probe", func(t *testing.T) {
		// The probe itself is covered by TestCheckHealth with faked
		// answers; here the dispatch only needs a refused address,
		// which fails before any socket opens.
		env := map[string]string{
			"DEVCULES_VAULT_KEY": key["DEVCULES_VAULT_KEY"],
			"DEVCULES_LISTEN":    "127.0.0.1:1",
		}
		code, _, stderr := invoke(t, env, "health")
		if code != exitError {
			t.Fatalf("exit = %d, want %d", code, exitError)
		}
		if !strings.Contains(stderr, "not running") {
			t.Fatalf("stderr = %q, want not-running error", stderr)
		}
	})
}

// An agent hook that cannot even load its config must still block
// the call in the agent's protocol: exit 1 would let Muse run it.
func TestAgentHookConfigErrorBlocks(t *testing.T) {
	for _, env := range []map[string]string{
		{"DEVCULES_LISTEN": "notaport"},
		{"META_API_KEY": "x", "DEVCULES_LAUNCH_TOKEN": "dvl_x"},
	} {
		code, stdout, stderr := invoke(t, env, "hook", "--agent", "musecode")
		if code != 2 || stdout != "" || !strings.Contains(stderr, "so it is blocked: devcules hook is misconfigured") {
			t.Errorf("%v: exit = %d, stderr = %q, want a fail-closed block", env, code, stderr)
		}
	}
}
