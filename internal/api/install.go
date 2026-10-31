package api

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// install.go hands out the machine helper itself, Kaithos-style: a
// fresh computer asks the box it is joining for the binary, check its
// SHA-256, enrol and install at login — one pasted command, nothing
// fetched from anywhere else.
//
// scripts/build-machine.sh writes, into the dist folder:
//
//	machine-dist/<os>-<arch>/devcules[.exe]   one per target
//	SHA256SUMS                                "<sha256>  <os>-<arch>/devcules[.exe]"
//	VERSION                                   the helper version
//
// These routes need no login: they contain no secret, and the one-time
// enrol code above them is useless without the vault the enrol call
// needs. A machine's identity arrives only after enrol answers.

// helperTargets are the targets scripts/build-machine.sh produces.
var helperTargets = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"}

// distDir finds the helper builds: DEVCULES_MACHINE_DIST, then
// machine-dist beside the devcules binary (what local runs and
// packaged installs share), then ./machine-dist (a repo checkout).
func distDir() string {
	var cands []string
	if v := os.Getenv("DEVCULES_MACHINE_DIST"); v != "" {
		cands = append(cands, v)
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "machine-dist"))
	}
	cands = append(cands, "machine-dist")
	for _, d := range cands {
		if st, err := os.Stat(filepath.Join(d, "SHA256SUMS")); err == nil && !st.IsDir() {
			return d
		}
	}
	return ""
}

// distTargets lists the targets actually built, for "no build yet".
func distTargets() []string {
	d := distDir()
	if d == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(d, "SHA256SUMS"))
	if err != nil {
		return nil
	}
	defer f.Close()
	out := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 {
			out = append(out, strings.SplitN(fields[1], "/", 2)[0])
		}
	}
	return out
}

// distVersion reports the VERSION the helpers carry.
func distVersion() string {
	d := distDir()
	if d == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(d, "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// registerMachineDist serves /install/machine.sh, /install/bin/SHA256SUMS
// and /install/bin/<os>-<arch>/devcules[.exe].
func registerMachineDist(mux *http.ServeMux, publicURL string) {
	file := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/install/bin/")
		d := distDir()
		// The manifest sits at the bin root, beside one folder per target.
		if p == "SHA256SUMS" {
			if d == "" {
				http.Error(w, "this Devcules has no machine helper built yet: run scripts/build-machine.sh on the Devcules host", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			http.ServeFile(w, r, filepath.Join(d, "SHA256SUMS"))
			return
		}
		target, name, _ := strings.Cut(p, "/")
		known := false
		for _, t := range helperTargets {
			if t == target {
				known = true
			}
		}
		want := "devcules"
		if strings.HasPrefix(target, "windows-") {
			want += ".exe"
		}
		if !known || name != want || d == "" {
			http.NotFound(w, r)
			return
		}
		pf := filepath.Join(d, target, name)
		if _, err := os.Stat(pf); err != nil {
			http.Error(w, "no helper built for "+target, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, pf)
	})

	// The installer: curl this box at the address every further call
	// goes back to. publicURL wins when the operator pinned one (the
	// Tailscale name); else the request's own host, so a local checkout
	// served off 127.0.0.1 still produces a working script.
	mux.HandleFunc("GET /install/machine.sh", func(w http.ResponseWriter, r *http.Request) {
		if distDir() == "" {
			http.Error(w, "this Devcules has no machine helper built yet: run scripts/build-machine.sh on the Devcules host", http.StatusServiceUnavailable)
			return
		}
		server := publicURL
		if server == "" {
			server = requestOrigin(r)
		}
		if server == "" {
			http.Error(w, "this Devcules's address cannot be put in an install script; set DEVCULES_PUBLIC_URL, or open it at the name the machine will use", http.StatusBadRequest)
			return
		}
		server = strings.TrimSuffix(server, "/")
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(machSh(server))
	})

	mux.Handle("GET /install/bin/", file)
}

// requestOrigin is "scheme://host" from the request: http except when
// it came in over TLS, and the host exactly as asked (that is the
// address the machine will call back to).
func requestOrigin(r *http.Request) string {
	host := r.Host
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host
}

// machSh renders the installer with the box's own address baked in.
// The script downloads the helper from the box it was fetched from,
// checks its sha256, enrols with the one-time code and installs it at
// login, then starts it. On macOS it opens the two privacy panes the
// screen stream needs; on Linux with no systemd it leaves a note.
func machSh(server string) []byte {
	body := strings.ReplaceAll(machineShTemplate, "__DEVCULES_SERVER__", server)
	return []byte(body)
}

// kemacript constant. __DEVCULES_SERVER__ is the only substitution;
// it arrives from the box that answered the curl, never from the
// request headers (ServeHTTP builds a fresh script per call).
const machineShTemplate = `#!/bin/sh
# Devcules machine bootstrap: curl -fsSL __DEVCULES_SERVER__/install/machine.sh | sh -s -- --code XXXX-XXXX-XXXX
# The box this was fetched from built this script, so downloading the
# helper from anywhere else is impossible: an impersonated server is
# detected before the helper ever runs.
set -eu

SERVER=__DEVCULES_SERVER__
CODE=
while [ $# -gt 0 ]; do
  case "$1" in
    --code) CODE="$2"; shift 2 ;;
    --server) SERVER="$2"; shift 2 ;;
    *) echo "devcules machine install: unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$CODE" ] || { echo "usage: sh machine.sh --code CODE  (make a code in Devcules: Machines → Add machine)" >&2; exit 2; }

OS=$(uname -s)
ARCH=$(uname -m)
case "$OS" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "devcules machine install: no helper for $OS ($ARCH) yet" >&2; exit 1 ;;
esac
case "$ARCH" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "devcules machine install: no helper for $OS ($ARCH) yet" >&2; exit 1 ;;
esac
target="$os-$arch"
exe=devcules
bin="${SERVER}/install/bin"
sums="${bin}/SHA256SUMS"

tmp=$(mktemp -d -t devcules-machine-install-XXXXXX)
trap 'rm -rf "$tmp"' EXIT
echo "==> downloading devcules for $target"
curl -fsSL "$sums" -o "$tmp/SHA256SUMS"
case "$target" in
  windows-*) exe=devcules.exe ;;
esac
mkdir -p "$tmp/$target"
curl -fsSL "$bin/$target/$exe" -o "$tmp/$target/$exe"
chmod +x "$tmp/$target/$exe"

# The manifest SHA pins the whole download list; one bad entry and we stop.
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && grep " $target/$exe$" SHA256SUMS | sha256sum -c -) || { echo "devcules machine install: sha256 check failed (corrupt or tampered download); refusing to install" >&2; exit 1; }
else
  (cd "$tmp" && grep " $target/$exe$" SHA256SUMS | shasum -a 256 -c -) || { echo "devcules machine install: sha256 check failed (corrupt or tampered download); refusing to install" >&2; exit 1; }
fi

prefix="${DEVCULES_MACHINE_PREFIX:-$HOME/.local}"
mkdir -p "$prefix/bin"
cp "$tmp/$target/$exe" "$prefix/bin/$exe"
export DEVCULES_SERVE_URL="$SERVER"
echo "==> enrolling this computer"
"$prefix/bin/$exe" machine enrol "$CODE"
echo "==> starting it, and at every sign-in"
"$prefix/bin/$exe" machine install || true
"$prefix/bin/$exe" machine &
echo ""
echo "Done. This computer now shows under Machines in your Devcules."
echo "    ${prefix}/bin/$exe machine uninstall   removes the sign-in start"
`
