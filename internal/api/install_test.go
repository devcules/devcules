package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMachineShRefusesShellInjection checks that ?server= (and the Host
// it falls back to) can never smuggle shell into the installer.
func TestMachineShRefusesShellInjection(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "SHA256SUMS"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVCULES_MACHINE_DIST", d)
	mux := http.NewServeMux()
	registerMachineDist(mux, "")

	// Each request comes in on the host it names, so a ?server= that
	// passes the shell check also passes the own-address check
	// (TestMachineShServerMustBeOwn covers that one).
	get := func(server string) *httptest.ResponseRecorder {
		target := "/install/machine.sh"
		if server != "" {
			target += "?server=" + url.QueryEscape(server)
		}
		r := httptest.NewRequest(http.MethodGet, target, nil)
		if u, err := url.Parse(server); err == nil && u.Host != "" {
			r.Host = u.Host
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}
	for _, bad := range []string{
		"http://x;touch /tmp/pwned", "http://x$(id)", "http://x`id`", "http://x 'y'",
		"ftp://box", "box:8080", "http://u:p@box", "http://box/?a=b",
	} {
		if rr := get(bad); rr.Code != http.StatusBadRequest {
			t.Errorf("server=%q answered %d, want 400", bad, rr.Code)
		}
	}
	for _, good := range []string{"https://box.tail1234.ts.net", "http://192.168.1.4:8080/", "http://[::1]:8080", ""} {
		rr := get(good)
		if rr.Code != http.StatusOK {
			t.Fatalf("server=%q answered %d: %s", good, rr.Code, rr.Body)
		}
		if good != "" && !strings.Contains(rr.Body.String(), "SERVER="+strings.TrimSuffix(good, "/")+"\n") {
			t.Fatalf("server=%q not baked in", good)
		}
	}
}
