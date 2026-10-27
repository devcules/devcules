package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSecretFile stores content in a temp file and returns its path.
func writeSecretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSecretFiles(t *testing.T) {
	keyPath := ""
	_ = keyPath

	cases := []struct {
		name    string
		setup   func(t *testing.T) map[string]string
		check   func(t *testing.T, got Config)
		wantErr string
	}{
		{
			name: "vault key from file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{"DEVCULES_VAULT_KEY_FILE": writeSecretFile(t, validKey+"\n")}
			},
			check: func(t *testing.T, got Config) {
				if got.VaultKey != validKey {
					t.Fatalf("VaultKey = %q, want trimmed file content", got.VaultKey)
				}
			},
		},
		{
			name: "vault key file unreadable",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{"DEVCULES_VAULT_KEY_FILE": filepath.Join(t.TempDir(), "missing")}
			},
			wantErr: "prefix:invalid config:\n  - DEVCULES_VAULT_KEY_FILE unreadable: ",
		},
		{
			name: "vault key file bad content",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{"DEVCULES_VAULT_KEY_FILE": writeSecretFile(t, "!!!")}
			},
			wantErr: "invalid config:\n  - DEVCULES_VAULT_KEY invalid: not base64 (want base64 of 32 raw bytes)",
		},
		{
			name: "vault direct wins over file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY":      validKey,
					"DEVCULES_VAULT_KEY_FILE": filepath.Join(t.TempDir(), "missing"),
				}
			},
			check: func(t *testing.T, got Config) {
				if got.VaultKey != validKey {
					t.Fatalf("VaultKey = %q, want direct value", got.VaultKey)
				}
			},
		},
		{
			name: "vault direct invalid beats valid file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY":      "YQ==",
					"DEVCULES_VAULT_KEY_FILE": writeSecretFile(t, validKey),
				}
			},
			wantErr: "invalid config:\n  - DEVCULES_VAULT_KEY invalid: decodes to 1 bytes (want 32)",
		},
		{
			name: "runner token from file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY":         validKey,
					"DEVCULES_RUNNER_TOKEN_FILE": writeSecretFile(t, "tok-file"),
				}
			},
			check: func(t *testing.T, got Config) {
				if got.RunnerToken != "tok-file" {
					t.Fatalf("RunnerToken = %q, want file content", got.RunnerToken)
				}
			},
		},
		{
			name: "runner token file unreadable",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY":         validKey,
					"DEVCULES_RUNNER_TOKEN_FILE": filepath.Join(t.TempDir(), "missing"),
				}
			},
			wantErr: "prefix:invalid config:\n  - DEVCULES_RUNNER_TOKEN_FILE unreadable: ",
		},
		{
			name: "runner token direct wins over file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY":         validKey,
					"DEVCULES_RUNNER_TOKEN":      "tok-direct",
					"DEVCULES_RUNNER_TOKEN_FILE": filepath.Join(t.TempDir(), "missing"),
				}
			},
			check: func(t *testing.T, got Config) {
				if got.RunnerToken != "tok-direct" {
					t.Fatalf("RunnerToken = %q, want direct value", got.RunnerToken)
				}
			},
		},
		{
			name: "meta api key from file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY": validKey,
					"META_API_KEY_FILE":  writeSecretFile(t, "mk-file"),
				}
			},
			check: func(t *testing.T, got Config) {
				if got.MetaAPIKey != "mk-file" {
					t.Fatalf("MetaAPIKey = %q, want file content", got.MetaAPIKey)
				}
			},
		},
		{
			name: "meta api key direct wins over file",
			setup: func(t *testing.T) map[string]string {
				return map[string]string{
					"DEVCULES_VAULT_KEY": validKey,
					"META_API_KEY":       "mk-direct",
					"META_API_KEY_FILE":  writeSecretFile(t, "mk-file"),
				}
			},
			check: func(t *testing.T, got Config) {
				if got.MetaAPIKey != "mk-direct" {
					t.Fatalf("MetaAPIKey = %q, want direct value", got.MetaAPIKey)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.setup(t))
			got, err := Load()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() succeeded, want error %q", tc.wantErr)
				}
				want := tc.wantErr
				if strings.HasPrefix(want, "prefix:") {
					want = strings.TrimPrefix(want, "prefix:")
					if !strings.HasPrefix(err.Error(), want) {
						t.Fatalf("Load() error = %q, want prefix %q", err.Error(), want)
					}
					return
				}
				if err.Error() != want {
					t.Fatalf("Load() error = %q, want %q", err.Error(), want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			tc.check(t, got)
		})
	}
}

// TestLoadAggregatesEveryProblem feeds several bad variables at once and
// requires one error naming them all — and no secret value anywhere.
func TestLoadAggregatesEveryProblem(t *testing.T) {
	setEnv(t, map[string]string{
		"DEVCULES_ROLE":         "admin",
		"DEVCULES_LISTEN":       "notaport",
		"DEVCULES_VAULT_KEY":    "!!!not-base64!!!",
		"DEVCULES_RUNNER_TOKEN": "has space",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want aggregated error")
	}
	want := "invalid config:\n" +
		"  - DEVCULES_ROLE invalid: \"admin\" (want serve, runner or machine)\n" +
		"  - DEVCULES_LISTEN invalid: \"notaport\" (want host:port with port 1-65535)\n" +
		"  - DEVCULES_VAULT_KEY invalid: not base64 (want base64 of 32 raw bytes)\n" +
		"  - DEVCULES_RUNNER_TOKEN invalid: must be a single token without whitespace"
	if err.Error() != want {
		t.Fatalf("Load() error = %q, want %q", err.Error(), want)
	}
	for _, secret := range []string{"!!!not-base64!!!", "has space"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks secret value %q: %q", secret, err.Error())
		}
	}
}

// TestLoadErrorsNeverLeakURLPasswords checks every URL variable, not just
// the database one: any of them may carry credentials.
func TestLoadErrorsNeverLeakURLPasswords(t *testing.T) {
	for _, name := range []string{"DEVCULES_DATABASE_URL", "DEVCULES_SERVE_URL", "DEVCULES_PUBLIC_URL"} {
		for _, value := range []string{"ftp://u:S3cretPw@h/x", "postgres://u:S3cretPw@h/x%zz"} {
			setEnv(t, map[string]string{name: value})
			_, err := Load()
			if err == nil {
				t.Fatalf("%s=%q: Load() succeeded, want error", name, value)
			}
			if strings.Contains(err.Error(), "S3cretPw") {
				t.Fatalf("%s=%q: error leaks the password: %q", name, value, err.Error())
			}
		}
	}
}

// TestForRole pins the trust split: only serve may hold the vault key.
func TestForRole(t *testing.T) {
	withKey := Config{Role: "serve", VaultKey: validKey}
	if got, err := withKey.ForRole("serve"); err != nil || got.Role != "serve" || got.VaultKey != validKey {
		t.Fatalf("ForRole(serve) = %+v, %v; want the key kept", got, err)
	}
	for _, role := range []string{"runner", "machine", "hook"} {
		_, err := withKey.ForRole(role)
		want := "invalid config:\n  - DEVCULES_VAULT_KEY must not be set for " + role +
			": the key lives only in serve (unset DEVCULES_VAULT_KEY and DEVCULES_VAULT_KEY_FILE)"
		if err == nil || err.Error() != want {
			t.Fatalf("ForRole(%s) error = %v, want %q", role, err, want)
		}
		got, err := Config{Role: "serve"}.ForRole(role)
		if err != nil || got.Role != role {
			t.Fatalf("ForRole(%s) without a key = %+v, %v; want it accepted", role, got, err)
		}
	}
}

// TestDefaultDataDir covers both branches of the default without depending
// on the machine the test runs on.
func TestDefaultDataDir(t *testing.T) {
	if got := defaultDataDir("/test/home", true); got != "/data" {
		t.Fatalf("container default = %q, want /data", got)
	}
	want := filepath.Join("/test/home", ".local", "share", "devcules")
	if got := defaultDataDir("/test/home", false); got != want {
		t.Fatalf("host default = %q, want %q", got, want)
	}
}

// TestLoadDataDirDefault asserts the host branch end to end. It holds
// wherever tests run outside a container, which covers dev machines and CI.
func TestLoadDataDirDefault(t *testing.T) {
	if detectContainer() {
		t.Skip("running inside a container: host default does not apply")
	}
	setEnv(t, map[string]string{"DEVCULES_VAULT_KEY": validKey})
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := filepath.Join("/test/home", ".local", "share", "devcules")
	if got.DataDir != want {
		t.Fatalf("DataDir = %q, want %q", got.DataDir, want)
	}
}

// TestLoadNoHome covers a host with no HOME and no DATA_DIR: Load must say
// what to set instead of failing obscurely.
func TestLoadNoHome(t *testing.T) {
	if detectContainer() {
		t.Skip("running inside a container: HOME is irrelevant")
	}
	setEnv(t, map[string]string{"DEVCULES_VAULT_KEY": validKey})
	os.Unsetenv("HOME")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded, want missing DATA_DIR error")
	}
	if !strings.Contains(err.Error(), "DEVCULES_DATA_DIR missing") {
		t.Fatalf("Load() error = %q, want it to name DEVCULES_DATA_DIR", err.Error())
	}
}
