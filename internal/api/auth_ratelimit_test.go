package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// b6AuditFixture is a mux with passkeys on and an audit log.
func b6AuditFixture(t *testing.T) (http.Handler, *audit.Log) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s, auth.WithPasskeys(passkeyURL, nil))
	if _, _, err := svc.EnsureAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	log := audit.New(s, nil)
	return Handler(Options{Store: s, Auth: svc, Web: testWeb(), AuditLog: log, PublicURL: passkeyURL}), log
}

// b6Reasons counts sign-in-failed audit rows by method/reason.
func b6Reasons(t *testing.T, log *audit.Log) (total int, by map[string]int) {
	t.Helper()
	rows, err := log.Recent(context.Background(), 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	by = map[string]int{}
	for _, e := range rows {
		if e.Action == audit.SignInFailed {
			by[e.Detail["method"]+"/"+e.Detail["reason"]]++
		}
	}
	return len(rows), by
}

// A throttled client writes one "throttled" audit row per window, not
// one per request (GOSEC-04).
func TestThrottledSignInAuditBounded(t *testing.T) {
	mux, log := b6AuditFixture(t)
	const n = 300
	throttled := 0
	for range n {
		r := httptest.NewRequest(http.MethodPost, "/auth/sign-in", strings.NewReader(url.Values{"username": {"admin"}, "password": {"x"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "198.51.100.7:4444"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			throttled++
			if w.Header().Get("Retry-After") != "60" {
				t.Fatal("throttled answer lost Retry-After")
			}
		}
	}
	total, by := b6Reasons(t, log)
	t.Logf("%d requests, %d throttled (429), %d audit rows", n, throttled, total)
	if throttled != n-auth.MaxSignInFailures {
		t.Fatalf("%d throttled, want %d", throttled, n-auth.MaxSignInFailures)
	}
	if by["password/invalid"] != auth.MaxSignInFailures || by["password/throttled"] != 1 || total != auth.MaxSignInFailures+1 {
		t.Fatalf("audit rows = %d %v, want %d invalid + 1 throttled", total, by, auth.MaxSignInFailures)
	}
}

// Throttled passkey finishes follow the same one-row rule, sharing
// the password limiter.
func TestThrottledPasskeyFinishAuditBounded(t *testing.T) {
	mux, log := b6AuditFixture(t)
	bad := url.Values{"username": {"admin"}, "password": {"x"}}
	for range auth.MaxSignInFailures {
		if w := postForm(mux, "/auth/sign-in", bad); w.Code != http.StatusUnauthorized {
			t.Fatalf("sign-in = %d, want 401", w.Code)
		}
	}
	for range 50 {
		w := postJSON(mux, "/auth/passkeys/login/finish", []byte("{}"), "", "0123456789abcdef0123456789abcdef")
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("passkey finish = %d, want 429", w.Code)
		}
	}
	_, by := b6Reasons(t, log)
	if by["passkey/throttled"] != 1 {
		t.Fatalf("audit rows %v, want one passkey/throttled", by)
	}
}

// login/begin is rate limited per IP: 429 with Retry-After past the
// limit, and no audit rows.
func TestPasskeyLoginBeginRateLimited(t *testing.T) {
	mux, log := b6AuditFixture(t)
	limited := 0
	for range 30 {
		w := postJSON(mux, "/auth/passkeys/login/begin", nil, "", "")
		switch w.Code {
		case http.StatusOK:
		case http.StatusTooManyRequests:
			limited++
			if w.Header().Get("Retry-After") != "60" {
				t.Fatal("429 without Retry-After")
			}
		default:
			t.Fatalf("login begin = %d", w.Code)
		}
	}
	if limited == 0 || limited == 30 {
		t.Fatalf("%d of 30 begins limited, want some but not all", limited)
	}
	if total, _ := b6Reasons(t, log); total != 0 {
		t.Fatalf("login begins wrote %d audit rows", total)
	}
}

// IPv6 clients are limited per /64 through the HTTP path, while each
// audit row keeps the full address the request came from.
func TestSignInIPv6Per64Audit(t *testing.T) {
	mux, log := b6AuditFixture(t)
	codes := []int{}
	for i := range auth.MaxSignInFailures + 1 {
		r := httptest.NewRequest(http.MethodPost, "/auth/sign-in", strings.NewReader(url.Values{"username": {"admin"}, "password": {"x"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "[2001:db8:7:7::" + string(rune('a'+i)) + "]:4444"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		codes = append(codes, w.Code)
	}
	if codes[auth.MaxSignInFailures] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want the 6th address in the /64 throttled", codes)
	}
	rows, err := log.Recent(context.Background(), 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range rows {
		if !strings.HasPrefix(e.IP, "2001:db8:7:7::") || strings.Contains(e.IP, "/") {
			t.Fatalf("audit IP = %q, want the client's full address", e.IP)
		}
	}
}
