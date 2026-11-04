package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/launch"
)

// TestSecretsDeleteInternal: the Secrets API manages user secrets only.
// serve's own vault entries (desk/sources, push/vapid_key, provider
// keys) are not listed and cannot be deleted through it, the same rule
// PUT applies, so the desk configuration survives a click on "remove".
func TestSecretsDeleteInternal(t *testing.T) {
	t.Parallel()
	f := newDeskFix(t)
	// Assemble the deliberately fake news API key so repository secret scans
	// never mistake this redaction fixture for a live credential.
	fakeNewsKey := "tvly-" + "abcdef" + "123456"
	if w := f.do(http.MethodPut, "/api/desk/sources", `{"tavily_key":"`+fakeNewsKey+`"}`); w.Code != http.StatusNoContent {
		t.Fatalf("sources put = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/api/secrets", `{"name":"MY_KEY","value":"v-1"}`); w.Code != http.StatusNoContent {
		t.Fatalf("user secret put = %d %s", w.Code, w.Body)
	}
	w := f.do(http.MethodGet, "/api/secrets", "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "desk/") || !strings.Contains(w.Body.String(), "MY_KEY") {
		t.Fatalf("list = %d %s, want MY_KEY and no internal names", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/api/secrets", `{"name":"desk/sources","value":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("PUT slash name = %d", w.Code)
	}
	for _, path := range []string{"/api/secrets/desk/sources", "/api/secrets/desk%2Fsources", "/api/secrets/push/vapid_key", "/api/secrets/provider/meta/api_key"} {
		if d := f.do(http.MethodDelete, path, ""); d.Code != http.StatusBadRequest {
			t.Errorf("DELETE %s = %d, want 400", path, d.Code)
		}
	}
	if src := f.do(http.MethodGet, "/api/desk/sources", ""); !strings.Contains(src.Body.String(), `"has_tavily":true`) {
		t.Fatalf("desk sources after the refused deletes = %s, want the token kept", src.Body)
	}
}

// TestSecretsDeleteMissing: deleting a name that is not stored answers
// 404 and writes no audit row; a real delete still audits once.
func TestSecretsDeleteMissing(t *testing.T) {
	t.Parallel()
	f := newDeskFix(t)
	if w := f.do(http.MethodDelete, "/api/secrets/NEVER_STORED", ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", w.Code)
	}
	if w := f.do(http.MethodPut, "/api/secrets", `{"name":"MY_KEY","value":"v-1"}`); w.Code != http.StatusNoContent {
		t.Fatalf("put = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/secrets/MY_KEY", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/secrets/MY_KEY", ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete again = %d, want 404", w.Code)
	}
	a := f.do(http.MethodGet, "/api/audit", "")
	if n := strings.Count(a.Body.String(), `"secret.delete"`); a.Code != http.StatusOK || n != 1 {
		t.Fatalf("audit = %d with %d secret.delete rows, want exactly 1: %s", a.Code, n, a.Body)
	}
}

// TestEnrolLimiterBounded: distinct addresses inside one window cannot
// grow the limiter past enrolMaxIPs, and a full map evicts in constant
// time instead of scanning it for every new address.
func TestEnrolLimiterBounded(t *testing.T) {
	l := newEnrolLimiter()
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }
	start := time.Now()
	for i := range 3 * enrolMaxIPs {
		l.allow(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	took := time.Since(start)
	if got := len(l.tries); got > enrolMaxIPs || l.order.Len() != got {
		t.Fatalf("limiter holds %d addresses (%d in order), bound is %d", got, l.order.Len(), enrolMaxIPs)
	}
	// abf457c took ~2 s here (one full sweep per new address past the
	// cap); a single sweep plus O(1) evictions is milliseconds. The
	// bound is loose for a shared CI box.
	if took > time.Second {
		t.Fatalf("%d calls took %s, want well under a second", 3*enrolMaxIPs, took)
	}
	// The newest addresses survived, the oldest were evicted.
	last := 3*enrolMaxIPs - 1
	if _, ok := l.tries[fmt.Sprintf("10.%d.%d.%d", last>>16&255, last>>8&255, last&255)]; !ok {
		t.Fatal("newest address evicted")
	}
	if _, ok := l.tries["10.0.0.0"]; ok {
		t.Fatal("oldest address kept past the cap")
	}
}

// TestEnrolLimiterIPv6Per64: one IPv6 /64 is one client, so cycling the
// low bits neither grows the map nor escapes the ten-try limit.
func TestEnrolLimiterIPv6Per64(t *testing.T) {
	l := newEnrolLimiter()
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }
	allowed := 0
	for i := range 100 {
		if l.allow(fmt.Sprintf("2001:db8:1:2::%x", i+1)) {
			allowed++
		}
	}
	if allowed != enrolTries || len(l.tries) != 1 {
		t.Fatalf("allowed %d tries over %d keys, want %d over 1", allowed, len(l.tries), enrolTries)
	}
	if !l.allow("2001:db8:1:3::1") {
		t.Fatal("another /64 refused")
	}
	if !l.allow("::ffff:192.0.2.7") || enrolKey("::ffff:192.0.2.7") != "192.0.2.7" {
		t.Fatal("IPv4-mapped address not keyed as IPv4")
	}
}

// TestMachineShServerMustBeOwn: ?server= can only restate this box's
// own address (its public URL when set, else the request's host), so a
// link on the real host cannot point the installer's downloads, and the
// checksum manifest they are verified against, at another origin.
func TestMachineShServerMustBeOwn(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "SHA256SUMS"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVCULES_MACHINE_DIST", d)
	getTLS := func(publicURL, host, server string, overTLS bool) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		registerMachineDist(mux, publicURL)
		target := "/install/machine.sh"
		if server != "" {
			target += "?server=" + url.QueryEscape(server)
		}
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Host = host
		if overTLS {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	get := func(publicURL, host, server string) *httptest.ResponseRecorder {
		return getTLS(publicURL, host, server, false)
	}
	for _, c := range []struct{ public, host, server string }{
		{"", "127.0.0.1:7664", "http://evil.example"},
		{"", "127.0.0.1:7664", "http://127.0.0.1:7665"},
		{"", "127.0.0.1:7664", "http://127.0.0.1:7664/other"},
		{"https://box.tail1234.ts.net", "box.tail1234.ts.net", "http://evil.example"},
		{"https://box.tail1234.ts.net", "box.tail1234.ts.net", "http://box.tail1234.ts.net"},
		{"https://box.tail1234.ts.net", "127.0.0.1:7664", "http://127.0.0.1:7664"},
	} {
		w := get(c.public, c.host, c.server)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "SERVER=") {
			t.Errorf("public=%q host=%q server=%q answered %d, want 400 and no script", c.public, c.host, c.server, w.Code)
		}
	}
	for _, c := range []struct{ public, host, server, want string }{
		// The default local flow: no ?server=, the request's own origin.
		{"", "127.0.0.1:7664", "", "http://127.0.0.1:7664"},
		// Behind a proxy with PUBLIC_URL: the public URL, asked or not.
		{"https://box.tail1234.ts.net", "127.0.0.1:7664", "", "https://box.tail1234.ts.net"},
		{"https://box.tail1234.ts.net/", "127.0.0.1:7664", "https://BOX.tail1234.ts.net/", "https://BOX.tail1234.ts.net"},
		// No PUBLIC_URL: the request's host, either scheme (a TLS proxy).
		{"", "box.lan:8080", "https://box.lan:8080", "https://box.lan:8080"},
		{"", "[::1]:8080", "http://[::1]:8080/", "http://[::1]:8080"},
	} {
		w := get(c.public, c.host, c.server)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SERVER="+c.want+"\n") {
			t.Errorf("public=%q host=%q server=%q answered %d, want SERVER=%s", c.public, c.host, c.server, w.Code, c.want)
		}
	}
	// A request that arrived over TLS cannot be downgraded to an http
	// script (its helper and SHA256SUMS would be fetched in plaintext).
	if w := getTLS("", "box.lan:8443", "http://box.lan:8443", true); w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "SERVER=") {
		t.Errorf("TLS request with ?server=http://box.lan:8443 answered %d %q, want 400 and no script", w.Code, w.Body.String())
	}
	for _, server := range []string{"", "https://box.lan:8443", "https://box.lan:8443/"} {
		w := getTLS("", "box.lan:8443", server, true)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SERVER=https://box.lan:8443\n") {
			t.Errorf("TLS request with server=%q answered %d, want SERVER=https://box.lan:8443", server, w.Code)
		}
	}
}

// TestSelfAuthTokenScope: a token minted for a self-login launch
// (NoProvider) is refused by the router for every provider, while an
// unscoped "" token, which the builtin assistant relies on, still passes
// the scope check.
func TestSelfAuthTokenScope(t *testing.T) {
	f := agentFixture(t)
	ctx := context.Background()
	own, _, err := f.tokens.MintOn(ctx, "", "Selfauth on laptop", launch.NoProvider, 0)
	if err != nil {
		t.Fatal(err)
	}
	all, _, err := f.tokens.MintOn(ctx, "", "builtin", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"meta", "openai"} {
		w := doJSONBearer(f.mux, "/llm/"+p+"/v1/chat/completions", `{}`, own)
		if w.Code != http.StatusForbidden {
			t.Errorf("self-auth token on /llm/%s = %d %s, want 403", p, w.Code, w.Body)
		}
	}
	// meta has a key in this fixture: the "" token gets past the scope
	// check to the (unreachable) upstream.
	if w := doJSONBearer(f.mux, "/llm/meta/v1/chat/completions", `{}`, all); w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		t.Fatalf("unscoped token on /llm/meta = %d %s, want past the scope check", w.Code, w.Body)
	}
}
