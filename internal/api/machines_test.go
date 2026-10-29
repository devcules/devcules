package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/machines"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// machineFixture builds the mux with a real enrol issuer and a
// signed-in admin session.
func machineFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, created, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureAdmin: %s", err)
	}
	if !created {
		t.Fatal("EnsureAdmin created = false on a fresh store")
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Enrol: machines.NewIssuer(s)})
	return mux, sessionCookie(t, mux, password)
}

func TestMachinesEnrolFlow(t *testing.T) {
	t.Parallel()
	mux, cookie := machineFixture(t)

	// Mint a code with a label.
	w := httptest.NewRecorder()
	r := withSession(httptest.NewRequest(http.MethodPost, "/api/machines/enrol-code",
		strings.NewReader(`{"label":"kitchen-mac"}`)), cookie)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("mint = %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	var minted struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &minted); err != nil || minted.Code == "" {
		t.Fatalf("mint body: %q (%v)", w.Body.String(), err)
	}

	// Redeem it once with a throwaway pubkey.
	pub := base64.StdEncoding.EncodeToString(make([]byte, 32))
	redeem := func() int {
		w := httptest.NewRecorder()
		r := withSession(httptest.NewRequest(http.MethodPost, "/api/machines/redeem",
			strings.NewReader(`{"code":"`+minted.Code+`","name":"kitchen-mac","pubkey":"`+pub+`"}`)), cookie)
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if code := redeem(); code != http.StatusCreated {
		t.Fatalf("redeem = %d, want 201", code)
	}
	// The code is single-use: a second redeem must fail.
	if code := redeem(); code != http.StatusBadRequest {
		t.Fatalf("second redeem = %d, want 400", code)
	}
}

func TestMachinesEnrolNeedsSession(t *testing.T) {
	t.Parallel()
	mux, _ := machineFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/api/machines/enrol-code", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mint = %d, want 401", w.Code)
	}
}

func TestMachinesEnrolNilIssuerIs503(t *testing.T) {
	t.Parallel()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb()})
	cookie := sessionCookie(t, mux, password)
	r := withSession(httptest.NewRequest(http.MethodPost, "/api/machines/enrol-code",
		strings.NewReader(`{}`)), cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil issuer = %d, want 503", w.Code)
	}
}
