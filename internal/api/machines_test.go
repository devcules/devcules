package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/machines"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// machineFixture builds the mux with a real enrol issuer, machine
// store and relay, and a signed-in admin session.
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
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Enrol: machines.NewIssuer(s), MachineStore: machines.NewService(s), Machines: machrelay.New(hub.New(hub.HashToken("x"), hub.Options{}))})
	return mux, sessionCookie(t, mux, password)
}

// enrolBody is what `devcules machine enrol` posts: the code, the
// name, the public key and the key's signature over the code.
func enrolBody(t *testing.T, code, name string, priv ed25519.PrivateKey) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{
		"code":   code,
		"name":   name,
		"pubkey": base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)),
		"sig":    base64.StdEncoding.EncodeToString(ed25519.Sign(priv, machines.EnrolMessage(code))),
	})
	return string(b)
}

func mintCode(t *testing.T, mux http.Handler, cookie string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := withSession(httptest.NewRequest(http.MethodPost, "/api/machines/enrol-code",
		strings.NewReader(`{"label":"kitchen-mac"}`)), cookie)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("mint = %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	var minted struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expiresIn"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &minted); err != nil || minted.Code == "" || minted.ExpiresIn != 900 {
		t.Fatalf("mint body: %q (%v)", w.Body.String(), err)
	}
	return minted.Code
}

func TestMachinesEnrolFlow(t *testing.T) {
	t.Parallel()
	mux, cookie := machineFixture(t)
	code := mintCode(t, mux, cookie)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	enrol := func(body string, header ...string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/machines/enrol", strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		mux.ServeHTTP(w, r)
		return w
	}
	// A browser page can never enrol on a visitor's behalf.
	if w := enrol(enrolBody(t, code, "kitchen-mac", priv), "Origin", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("enrol with Origin = %d, want 403", w.Code)
	}
	// A signature by another key does not prove this key.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	bad := strings.Replace(enrolBody(t, code, "kitchen-mac", priv), `"sig":"`, `"sig":"x`, 1)
	if w := enrol(bad); w.Code != http.StatusBadRequest {
		t.Fatalf("garbled sig = %d, want 400", w.Code)
	}
	var mixed map[string]string
	_ = json.Unmarshal([]byte(enrolBody(t, code, "kitchen-mac", priv)), &mixed)
	var wrong map[string]string
	_ = json.Unmarshal([]byte(enrolBody(t, code, "kitchen-mac", other)), &wrong)
	mixed["sig"] = wrong["sig"]
	mb, _ := json.Marshal(mixed)
	if w := enrol(string(mb)); w.Code != http.StatusBadRequest {
		t.Fatalf("foreign sig = %d, want 400", w.Code)
	}
	// No session needed: the code and the signature are the credential.
	if w := enrol(enrolBody(t, code, "kitchen-mac", priv)); w.Code != http.StatusCreated {
		t.Fatalf("enrol = %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	if w := enrol(enrolBody(t, code, "kitchen-mac", priv)); w.Code != http.StatusBadRequest {
		t.Fatalf("second enrol = %d, want 400", w.Code)
	}
	// The same name with a fresh code is a conflict, not a takeover.
	code2 := mintCode(t, mux, cookie)
	if w := enrol(enrolBody(t, code2, "kitchen-mac", other)); w.Code != http.StatusConflict {
		t.Fatalf("taken name = %d, want 409", w.Code)
	}

	// The list shows the enrolled machine, offline.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, withSession(httptest.NewRequest(http.MethodGet, "/api/machines", nil), cookie))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"kitchen-mac","online":false`) {
		t.Fatalf("list = %d %q", w.Code, w.Body.String())
	}
	// Removing it frees the name.
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, withSession(httptest.NewRequest(http.MethodDelete, "/api/machines/kitchen-mac", nil), cookie))
	if w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d, want 204", w.Code)
	}
	code3 := mintCode(t, mux, cookie)
	if w := enrol(enrolBody(t, code3, "kitchen-mac", other)); w.Code != http.StatusCreated {
		t.Fatalf("re-enrol after remove = %d, want 201", w.Code)
	}
}

func TestMachinesEnrolRateLimited(t *testing.T) {
	t.Parallel()
	mux, _ := machineFixture(t)
	last := 0
	for range enrolTries + 1 {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/machines/enrol", strings.NewReader(`{"code":"ZZZZ-ZZZZ-ZZZZ","name":"x","pubkey":"","sig":""}`)))
		last = w.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("attempt %d = %d, want 429", enrolTries+1, last)
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
