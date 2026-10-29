package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// hostnameRE accepts what net.SplitHostPort hands back for ordinary
// listen addresses: IPs are checked with net.ParseIP, everything else
// must look like a DNS name so typos fail fast instead of at dial time.
var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]*[A-Za-z0-9])?$`)

// Config is the validated process configuration. It is a plain struct:
// every field is usable as-is, and a Config only exists when Load found
// no problems. Secret fields carry the value exactly as given (trimmed);
// they are never printed, not even inside error messages.
type Config struct {
	Role        string
	Listen      string
	DataDir     string
	DatabaseURL string
	VaultKey    string // optional: serve generates a key file on first start
	// VaultNewKey is read only by `devcules vault rotate`, and only when
	// VaultKey is set: it names the key to rotate to.
	VaultNewKey string
	ServeURL    string
	RunnerToken string
	// RunnerTokenFile names the token file for both roles. It defaults
	// to <DataDir>/runner.token so single-binary deploys share one
	// mechanism with compose (which sets /link/runner.token); serve
	// mints the file when it is missing, the runner only reads it.
	RunnerTokenFile string
	Workspace       string
	PublicURL       string
	MetaAPIKey      string
	// MetaBaseURL is the Meta Model API origin the router forwards to
	// (DEVCULES_META_BASE_URL, default https://api.meta.ai).
	MetaBaseURL string
	// TraceDays is how long model call traces are kept
	// (DEVCULES_TRACE_DAYS, default 30; 0 keeps them forever).
	TraceDays int
	// PushContact is the VAPID subject push services may use to reach
	// the operator (DEVCULES_PUSH_CONTACT, a mailto: or https: URL).
	// Empty means serve picks one (see PushSubject).
	PushContact string
	// LaunchToken is the agent's launch token, read by `devcules hook`
	// (DEVCULES_LAUNCH_TOKEN or _FILE).
	LaunchToken string
}

// PushSubject is the VAPID subject: PushContact, else the public URL
// when it is https, else the project's home page.
func (c Config) PushSubject() string {
	switch {
	case c.PushContact != "":
		return c.PushContact
	case strings.HasPrefix(c.PublicURL, "https://"):
		return strings.TrimRight(c.PublicURL, "/")
	}
	return "https://github.com/devcules/devcules"
}

// ForRole returns c for the named role. Vault keys and provider keys live
// only in serve (and the `vault` maintenance command, which runs beside
// it), so a runner, machine or hook process that was handed one is a
// misconfiguration: the untrusted side must never be able to read them.
func (c Config) ForRole(role string) (Config, error) {
	if role != "serve" && role != "vault" {
		for _, s := range []struct{ name, value string }{
			{"DEVCULES_VAULT_KEY", c.VaultKey},
			{"DEVCULES_VAULT_NEW_KEY", c.VaultNewKey},
			{"META_API_KEY", c.MetaAPIKey},
		} {
			if s.value != "" {
				return Config{}, fmt.Errorf("invalid config:\n  - %s must not be set for %s: it lives only in serve (unset %s and %s_FILE)", s.name, role, s.name, s.name)
			}
		}
	}
	c.Role = role
	return c, nil
}

// Load reads the configuration from the environment and validates it.
// Unset variables fall back to the PLAN.md §6 defaults; set variables
// must be valid. Every problem is collected and returned as one error
// listing them all, so one start shows everything to fix instead of the
// first failure.
func Load() (Config, error) {
	var cfg Config
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	cfg.Role = "serve"
	if v, set := os.LookupEnv("DEVCULES_ROLE"); set {
		switch v {
		case "serve", "runner", "machine":
			cfg.Role = v
		default:
			fail(`DEVCULES_ROLE invalid: %q (want serve, runner or machine)`, v)
		}
	}

	cfg.Listen = "127.0.0.1:7659"
	if v, set := os.LookupEnv("DEVCULES_LISTEN"); set {
		if checkListen(v) {
			cfg.Listen = v
		} else {
			fail(`DEVCULES_LISTEN invalid: %q (want host:port with port 1-65535)`, v)
		}
	}

	if v, set := os.LookupEnv("DEVCULES_DATA_DIR"); set {
		switch {
		case v == "":
			fail(`DEVCULES_DATA_DIR missing: set an absolute path`)
		case !filepath.IsAbs(v):
			fail(`DEVCULES_DATA_DIR invalid: %q (want an absolute path)`, v)
		default:
			cfg.DataDir = v
		}
	} else if detectContainer() {
		cfg.DataDir = defaultDataDir("", true)
	} else if home, err := os.UserHomeDir(); err != nil {
		fail(`DEVCULES_DATA_DIR missing: set it, or set HOME (no home directory: %s)`, err)
	} else {
		cfg.DataDir = defaultDataDir(home, false)
	}

	if v, set := os.LookupEnv("DEVCULES_DATABASE_URL"); set && v != "" {
		if u, err := url.Parse(v); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
			fail(`DEVCULES_DATABASE_URL invalid: %s (want empty or a postgres:// URL with a host)`, describeURL(v))
		} else {
			cfg.DatabaseURL = v
		}
	}

	if v, problem := readSecret("DEVCULES_VAULT_KEY", checkVaultKey); problem != "" {
		fail(`%s`, problem)
	} else {
		cfg.VaultKey = v
	}
	if v, problem := readSecret("DEVCULES_VAULT_NEW_KEY", checkVaultKey); problem != "" {
		fail(`%s`, problem)
	} else {
		cfg.VaultNewKey = v
	}

	cfg.ServeURL = "http://serve:7659"
	if v, set := os.LookupEnv("DEVCULES_SERVE_URL"); set {
		if checkWebURL(v) {
			cfg.ServeURL = v
		} else {
			fail(`DEVCULES_SERVE_URL invalid: %s (want an http(s) URL with a host)`, describeURL(v))
		}
	}

	// The runner token is the one secret whose file may not exist yet:
	// serve mints it on first start, so a missing file is not a config
	// error (readSecret would make it one, and serve could then never
	// start on a fresh compose volume). The file defaults into the data
	// dir so both roles name the same file without configuration; an
	// explicit _FILE must be absolute because serve writes it. A direct
	// value beats the file, as with every secret. The runner reports a
	// still-missing token with next steps when it starts.
	if v, set := os.LookupEnv("DEVCULES_RUNNER_TOKEN"); set && strings.TrimSpace(v) != "" {
		v = strings.TrimSpace(v)
		if problem := checkToken(v); problem != "" {
			fail(`DEVCULES_RUNNER_TOKEN invalid: %s`, problem)
		} else {
			cfg.RunnerToken = v
		}
	}
	cfg.RunnerTokenFile = filepath.Join(cfg.DataDir, "runner.token")
	tokenFileOK := true
	if v, set := os.LookupEnv("DEVCULES_RUNNER_TOKEN_FILE"); set {
		if !filepath.IsAbs(v) {
			fail(`DEVCULES_RUNNER_TOKEN_FILE invalid: %q (want an absolute path)`, v)
			tokenFileOK = false
		} else {
			cfg.RunnerTokenFile = v
		}
	}
	if tokenFileOK && cfg.RunnerToken == "" {
		raw, err := os.ReadFile(cfg.RunnerTokenFile)
		switch {
		case err == nil:
			if t := strings.TrimSpace(string(raw)); t != "" {
				if problem := checkToken(t); problem != "" {
					fail(`DEVCULES_RUNNER_TOKEN invalid: %s (fix or delete %s)`, problem, cfg.RunnerTokenFile)
				} else {
					cfg.RunnerToken = t
				}
			}
		case errors.Is(err, fs.ErrNotExist):
			// Not minted yet: serve creates it on start.
		default:
			fail(`DEVCULES_RUNNER_TOKEN_FILE unreadable: %s: %s`, cfg.RunnerTokenFile, err.Error())
		}
	}

	cfg.Workspace = "/workspace"
	if v, set := os.LookupEnv("DEVCULES_WORKSPACE"); set {
		switch {
		case v == "":
			fail(`DEVCULES_WORKSPACE missing: set an absolute path (default /workspace)`)
		case !filepath.IsAbs(v):
			fail(`DEVCULES_WORKSPACE invalid: %q (want an absolute path)`, v)
		default:
			cfg.Workspace = v
		}
	}

	if v, set := os.LookupEnv("DEVCULES_PUBLIC_URL"); set && v != "" {
		if checkWebURL(v) {
			cfg.PublicURL = v
		} else {
			fail(`DEVCULES_PUBLIC_URL invalid: %s (want empty or an http(s) URL with a host)`, describeURL(v))
		}
	}

	if v, problem := readSecret("META_API_KEY", checkToken); problem != "" {
		fail(`%s`, problem)
	} else {
		cfg.MetaAPIKey = v
	}

	cfg.MetaBaseURL = "https://api.meta.ai"
	if v, set := os.LookupEnv("DEVCULES_META_BASE_URL"); set && v != "" {
		if checkWebURL(v) {
			cfg.MetaBaseURL = strings.TrimRight(v, "/")
		} else {
			fail(`DEVCULES_META_BASE_URL invalid: %s (want empty or an http(s) URL with a host)`, describeURL(v))
		}
	}

	cfg.TraceDays = 30
	if v, set := os.LookupEnv("DEVCULES_TRACE_DAYS"); set && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3650 {
			cfg.TraceDays = n
		} else {
			fail(`DEVCULES_TRACE_DAYS invalid: %q (want a whole number of days, 0 to 3650; 0 keeps traces forever)`, v)
		}
	}

	if v, set := os.LookupEnv("DEVCULES_PUSH_CONTACT"); set && v != "" {
		if u, err := url.Parse(v); err == nil && ((u.Scheme == "mailto" && u.Opaque != "") || (u.Scheme == "https" && u.Host != "")) {
			cfg.PushContact = v
		} else {
			fail(`DEVCULES_PUSH_CONTACT invalid: %s (want a mailto: or https: URL)`, describeURL(v))
		}
	}

	if v, problem := readSecret("DEVCULES_LAUNCH_TOKEN", checkToken); problem != "" {
		fail(`%s`, problem)
	} else {
		cfg.LaunchToken = v
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

// readSecret loads one optional secret. When a secret is given both directly
// and via its *_FILE variant, the direct value wins and the file is ignored
// entirely, even if it is missing or unreadable: direct values are the
// explicit choice at the call site, files the fallback for process managers
// and secret stores. The rule is documented in docs/config.md and tested in
// config_secret_file_test.go. Values are trimmed: secret files conventionally
// end with a newline. validate checks a present value and describes the
// problem, if any, without ever repeating the value itself.
func readSecret(name string, validate func(string) string) (string, string) {
	if v, set := os.LookupEnv(name); set && strings.TrimSpace(v) != "" {
		v = strings.TrimSpace(v)
		if problem := validate(v); problem != "" {
			return "", name + ` invalid: ` + problem
		}
		return v, ""
	}
	fileVar := name + "_FILE"
	path, set := os.LookupEnv(fileVar)
	if !set {
		return "", ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fileVar + ` unreadable: ` + path + `: ` + err.Error()
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return "", ""
	}
	if problem := validate(v); problem != "" {
		return "", name + ` invalid: ` + problem
	}
	return v, ""
}

// checkVaultKey verifies the key decodes to 32 raw bytes. It describes the
// failure without repeating the key.
func checkVaultKey(v string) string {
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return `not base64 (want base64 of 32 raw bytes)`
	}
	if len(raw) != 32 {
		return `decodes to ` + strconv.Itoa(len(raw)) + ` bytes (want 32)`
	}
	return ""
}

// checkToken keeps opaque tokens to one whitespace-free string. Anything
// stricter needs a round trip against the issuer, which belongs to a later
// module, not to start-up validation.
func checkToken(v string) string {
	if strings.ContainsAny(v, " \t\n\v\f\r") {
		return `must be a single token without whitespace`
	}
	return ""
}

// checkListen accepts an empty host (all interfaces) or an IP or DNS name,
// with a numeric port in range.
func checkListen(v string) bool {
	host, portStr, err := net.SplitHostPort(v)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	if host == "" {
		return true
	}
	return net.ParseIP(host) != nil || hostnameRE.MatchString(host)
}

// describeURL quotes a URL for an error message without its password: a
// database or server URL may carry credentials, and errors must never
// repeat a secret. A URL that does not even parse is hidden entirely, since
// there is no telling which part is the password.
func describeURL(v string) string {
	u, err := url.Parse(v)
	if err != nil {
		return "(value hidden: it does not parse as a URL)"
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return strconv.Quote(u.Redacted())
	}
	return strconv.Quote(v)
}

// checkWebURL accepts http(s) URLs with a host.
func checkWebURL(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// detectContainer reports whether the process runs inside a container, in
// which case DATA_DIR defaults to /data. It trusts only well-known signals
// — the Docker and Podman marker files, Kubernetes' service-host variable
// and container runtimes in PID 1's cgroup — rather than guessing from
// anything else. The cgroup alone is not enough: under cgroup v2 it reads
// just "0::/".
func detectContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		for line := range strings.Lines(string(data)) {
			if strings.Contains(line, "docker") ||
				strings.Contains(line, "kubepods") ||
				strings.Contains(line, "containerd") ||
				strings.Contains(line, "lxc") {
				return true
			}
		}
	}
	return false
}

// defaultDataDir is the DATA_DIR default without environment lookups, kept
// separate so both branches are unit-testable.
func defaultDataDir(home string, inContainer bool) string {
	if inContainer {
		return "/data"
	}
	return filepath.Join(home, ".local", "share", "devcules")
}
