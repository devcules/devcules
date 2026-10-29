package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// validKey is base64 for 32 zero bytes.
const validKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// configEnv lists every variable Load reads, so tests can isolate one case
// from the ambient environment and from each other.
var configEnv = []string{
	"DEVCULES_ROLE",
	"DEVCULES_LISTEN",
	"DEVCULES_DATA_DIR",
	"DEVCULES_DATABASE_URL",
	"DEVCULES_VAULT_KEY",
	"DEVCULES_VAULT_KEY_FILE",
	"DEVCULES_VAULT_NEW_KEY",
	"DEVCULES_VAULT_NEW_KEY_FILE",
	"DEVCULES_SERVE_URL",
	"DEVCULES_RUNNER_TOKEN",
	"DEVCULES_RUNNER_TOKEN_FILE",
	"DEVCULES_WORKSPACE",
	"DEVCULES_PUBLIC_URL",
	"META_API_KEY",
	"META_API_KEY_FILE",
	"HOME",
}

// setEnv clears every known variable, sets HOME to a fixed fake home and
// applies vars, restoring the original environment afterwards. Tests must
// not run in parallel: they share the process environment.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	saved := make(map[string]string, len(configEnv))
	for _, k := range configEnv {
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = v
		}
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range configEnv {
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

// baseConfig is the expected Config when only the vault key is set.
// The token file defaults into the data dir, like Load does.
func baseConfig() Config {
	dataDir := filepath.Join("/test/home", ".local", "share", "devcules")
	return Config{
		Role:            "serve",
		Listen:          "127.0.0.1:7659",
		DataDir:         dataDir,
		ServeURL:        "http://serve:7659",
		VaultKey:        validKey,
		RunnerTokenFile: filepath.Join(dataDir, "runner.token"),
		Workspace:       "/workspace",
	}
}

func TestLoad(t *testing.T) {
	key := map[string]string{"DEVCULES_VAULT_KEY": validKey}

	cases := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name: "defaults",
			env:  key,
			want: baseConfig(),
		},
		{
			name: "role runner",
			env:  merge(key, map[string]string{"DEVCULES_ROLE": "runner"}),
			want: with(baseConfig(), func(c *Config) { c.Role = "runner" }),
		},
		{
			name: "role machine",
			env:  merge(key, map[string]string{"DEVCULES_ROLE": "machine"}),
			want: with(baseConfig(), func(c *Config) { c.Role = "machine" }),
		},
		{
			name:    "role invalid",
			env:     merge(key, map[string]string{"DEVCULES_ROLE": "admin"}),
			wantErr: "invalid config:\n  - DEVCULES_ROLE invalid: \"admin\" (want serve, runner or machine)",
		},
		{
			name: "listen hostname and empty host",
			env:  merge(key, map[string]string{"DEVCULES_LISTEN": "localhost:1"}),
			want: with(baseConfig(), func(c *Config) { c.Listen = "localhost:1" }),
		},
		{
			name: "listen port max",
			env:  merge(key, map[string]string{"DEVCULES_LISTEN": "127.0.0.1:65535"}),
			want: with(baseConfig(), func(c *Config) { c.Listen = "127.0.0.1:65535" }),
		},
		{
			name:    "listen missing port",
			env:     merge(key, map[string]string{"DEVCULES_LISTEN": "notaport"}),
			wantErr: "invalid config:\n  - DEVCULES_LISTEN invalid: \"notaport\" (want host:port with port 1-65535)",
		},
		{
			name:    "listen port out of range",
			env:     merge(key, map[string]string{"DEVCULES_LISTEN": "127.0.0.1:99999"}),
			wantErr: "invalid config:\n  - DEVCULES_LISTEN invalid: \"127.0.0.1:99999\" (want host:port with port 1-65535)",
		},
		{
			name:    "listen port zero",
			env:     merge(key, map[string]string{"DEVCULES_LISTEN": "127.0.0.1:0"}),
			wantErr: "invalid config:\n  - DEVCULES_LISTEN invalid: \"127.0.0.1:0\" (want host:port with port 1-65535)",
		},
		{
			name: "data dir explicit",
			env:  merge(key, map[string]string{"DEVCULES_DATA_DIR": "/data/x"}),
			want: with(baseConfig(), func(c *Config) {
				c.DataDir = "/data/x"
				c.RunnerTokenFile = "/data/x/runner.token"
			}),
		},
		{
			name:    "data dir empty",
			env:     merge(key, map[string]string{"DEVCULES_DATA_DIR": ""}),
			wantErr: "invalid config:\n  - DEVCULES_DATA_DIR missing: set an absolute path",
		},
		{
			name:    "data dir relative",
			env:     merge(key, map[string]string{"DEVCULES_DATA_DIR": "rel/path"}),
			wantErr: "invalid config:\n  - DEVCULES_DATA_DIR invalid: \"rel/path\" (want an absolute path)",
		},
		{
			name: "database url postgres",
			env:  merge(key, map[string]string{"DEVCULES_DATABASE_URL": "postgres://u@h/db"}),
			want: with(baseConfig(), func(c *Config) { c.DatabaseURL = "postgres://u@h/db" }),
		},
		{
			name: "database url postgresql scheme",
			env:  merge(key, map[string]string{"DEVCULES_DATABASE_URL": "postgresql://h/db"}),
			want: with(baseConfig(), func(c *Config) { c.DatabaseURL = "postgresql://h/db" }),
		},
		{
			name:    "database url wrong scheme",
			env:     merge(key, map[string]string{"DEVCULES_DATABASE_URL": "http://h/db"}),
			wantErr: "invalid config:\n  - DEVCULES_DATABASE_URL invalid: \"http://h/db\" (want empty or a postgres:// URL with a host)",
		},
		{
			name:    "database url no host",
			env:     merge(key, map[string]string{"DEVCULES_DATABASE_URL": "postgres://"}),
			wantErr: "invalid config:\n  - DEVCULES_DATABASE_URL invalid: \"postgres://\" (want empty or a postgres:// URL with a host)",
		},
		{
			name: "vault key unset is allowed",
			env:  map[string]string{},
			want: with(baseConfig(), func(c *Config) { c.VaultKey = "" }),
		},
		{
			name:    "database url password is masked",
			env:     merge(key, map[string]string{"DEVCULES_DATABASE_URL": "mysql://app:S3cretPw@db:3306/x"}),
			wantErr: "invalid config:\n  - DEVCULES_DATABASE_URL invalid: \"mysql://app:xxxxx@db:3306/x\" (want empty or a postgres:// URL with a host)",
		},
		{
			name:    "database url that does not parse is hidden",
			env:     merge(key, map[string]string{"DEVCULES_DATABASE_URL": "postgres://app:S3cretPw@db:5432/x%zz"}),
			wantErr: "invalid config:\n  - DEVCULES_DATABASE_URL invalid: (value hidden: it does not parse as a URL) (want empty or a postgres:// URL with a host)",
		},
		{
			name:    "vault key not base64",
			env:     map[string]string{"DEVCULES_VAULT_KEY": "!!!not-base64!!!"},
			wantErr: "invalid config:\n  - DEVCULES_VAULT_KEY invalid: not base64 (want base64 of 32 raw bytes)",
		},
		{
			name:    "vault key wrong length",
			env:     map[string]string{"DEVCULES_VAULT_KEY": "YQ=="},
			wantErr: "invalid config:\n  - DEVCULES_VAULT_KEY invalid: decodes to 1 bytes (want 32)",
		},
		{
			name: "new vault key for rotation",
			env:  merge(key, map[string]string{"DEVCULES_VAULT_NEW_KEY": validKey}),
			want: with(baseConfig(), func(c *Config) { c.VaultNewKey = validKey }),
		},
		{
			name:    "new vault key wrong length",
			env:     merge(key, map[string]string{"DEVCULES_VAULT_NEW_KEY": "YQ=="}),
			wantErr: "invalid config:\n  - DEVCULES_VAULT_NEW_KEY invalid: decodes to 1 bytes (want 32)",
		},
		{
			name: "serve url https",
			env:  merge(key, map[string]string{"DEVCULES_SERVE_URL": "https://serve.example:7659"}),
			want: with(baseConfig(), func(c *Config) { c.ServeURL = "https://serve.example:7659" }),
		},
		{
			name:    "serve url bad scheme",
			env:     merge(key, map[string]string{"DEVCULES_SERVE_URL": "ftp://h"}),
			wantErr: "invalid config:\n  - DEVCULES_SERVE_URL invalid: \"ftp://h\" (want an http(s) URL with a host)",
		},
		{
			name:    "serve url no host",
			env:     merge(key, map[string]string{"DEVCULES_SERVE_URL": "http://"}),
			wantErr: "invalid config:\n  - DEVCULES_SERVE_URL invalid: \"http://\" (want an http(s) URL with a host)",
		},
		{
			name: "runner token set",
			env:  merge(key, map[string]string{"DEVCULES_RUNNER_TOKEN": "tok123"}),
			want: with(baseConfig(), func(c *Config) { c.RunnerToken = "tok123" }),
		},
		{
			name:    "runner token whitespace",
			env:     merge(key, map[string]string{"DEVCULES_RUNNER_TOKEN": "has space"}),
			wantErr: "invalid config:\n  - DEVCULES_RUNNER_TOKEN invalid: must be a single token without whitespace",
		},
		{
			name: "workspace explicit",
			env:  merge(key, map[string]string{"DEVCULES_WORKSPACE": "/w"}),
			want: with(baseConfig(), func(c *Config) { c.Workspace = "/w" }),
		},
		{
			name:    "workspace empty",
			env:     merge(key, map[string]string{"DEVCULES_WORKSPACE": ""}),
			wantErr: "invalid config:\n  - DEVCULES_WORKSPACE missing: set an absolute path (default /workspace)",
		},
		{
			name:    "workspace relative",
			env:     merge(key, map[string]string{"DEVCULES_WORKSPACE": "w"}),
			wantErr: "invalid config:\n  - DEVCULES_WORKSPACE invalid: \"w\" (want an absolute path)",
		},
		{
			name: "public url set",
			env:  merge(key, map[string]string{"DEVCULES_PUBLIC_URL": "https://example.ts.net"}),
			want: with(baseConfig(), func(c *Config) { c.PublicURL = "https://example.ts.net" }),
		},
		{
			name:    "public url bad scheme",
			env:     merge(key, map[string]string{"DEVCULES_PUBLIC_URL": "gopher://h"}),
			wantErr: "invalid config:\n  - DEVCULES_PUBLIC_URL invalid: \"gopher://h\" (want empty or an http(s) URL with a host)",
		},
		{
			name: "meta api key set",
			env:  merge(key, map[string]string{"META_API_KEY": "mk-1"}),
			want: with(baseConfig(), func(c *Config) { c.MetaAPIKey = "mk-1" }),
		},
		{
			name:    "meta api key whitespace",
			env:     merge(key, map[string]string{"META_API_KEY": "a b"}),
			wantErr: "invalid config:\n  - META_API_KEY invalid: must be a single token without whitespace",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)
			got, err := Load()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() succeeded, want error %q", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("Load() error = %q, want %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Load() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// merge returns a copy of base with overrides applied.
func merge(base, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// with returns a copy of c with mutate applied.
func with(c Config, mutate func(*Config)) Config {
	mutate(&c)
	return c
}
