package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/auth/authtest"
)

const passkeyURL = "http://localhost:7659"

// passkeyFixture is a mux with passkeys on plus a software
// authenticator for its RP.
func passkeyFixture(t *testing.T) (http.Handler, string, *authtest.Authenticator) {
	t.Helper()
	mux, password := fixture(t, passkeyURL)
	a, err := authtest.New("localhost", passkeyURL)
	if err != nil {
		t.Fatalf("authtest.New: %s", err)
	}
	return mux, password, a
}

// postJSON posts a JSON body to target, carrying the session and
// ceremony cookies when given.
func postJSON(mux http.Handler, target string, body []byte, session, ceremony string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if session != "" {
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	}
	if ceremony != "" {
		r.AddCookie(&http.Cookie{Name: ceremonyCookieName, Value: ceremony})
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// ceremonyValue reads the ceremony cookie a begin set.
func ceremonyValue(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == ceremonyCookieName {
			return c.Value
		}
	}
	t.Fatalf("no %s cookie in %d response", ceremonyCookieName, w.Code)
	return ""
}

// beginRegister runs the register begin through the mux and returns
// the options plus the ceremony id.
func beginRegister(t *testing.T, mux http.Handler, session string) ([]byte, string) {
	t.Helper()
	w := postJSON(mux, "/auth/passkeys/register/begin", nil, session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("register begin status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	return w.Body.Bytes(), ceremonyValue(t, w)
}

func TestPasskeyStatusRoute(t *testing.T) {
	mux, _ := fixture(t, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/passkeys/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status status = %d, want 200", w.Code)
	}
	var off struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &off); err != nil {
		t.Fatalf("status body is not JSON: %s", err)
	}
	if off.Enabled || !strings.Contains(off.Reason, "DEVCULES_PUBLIC_URL") {
		t.Fatalf("status without a public URL = %+v, want disabled with next steps", off)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("status content type = %q, want application/json", ct)
	}

	on, _ := fixture(t, passkeyURL)
	w = httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/passkeys/status", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"enabled":true}` {
		t.Fatalf("status with a public URL = %d %q, want 200 {\"enabled\":true}", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/passkeys/status", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status POST = %d, want 405", w.Code)
	}
}

func TestPasskeyCeremonyCookieShape(t *testing.T) {
	mux, password, _ := passkeyFixture(t)
	session := sessionCookie(t, mux, password)
	w := postJSON(mux, "/auth/passkeys/register/begin", nil, session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("register begin status = %d, want 200", w.Code)
	}
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == ceremonyCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("no %s cookie set", ceremonyCookieName)
	}
	if !found.HttpOnly || found.SameSite != http.SameSiteStrictMode || found.Path != "/auth/passkeys" {
		t.Fatalf("ceremony cookie = %+v, want HttpOnly, SameSite=Strict, Path=/auth/passkeys", found)
	}
	if found.Secure {
		t.Fatal("ceremony cookie is Secure, want it without (the id alone completes nothing)")
	}
	if found.MaxAge != int(auth.CeremonyTTL.Seconds()) {
		t.Fatalf("ceremony cookie MaxAge = %d, want the %s ceremony TTL", found.MaxAge, auth.CeremonyTTL)
	}
	if len(found.Value) != 32 {
		t.Fatalf("ceremony id length = %d, want 32 hex chars", len(found.Value))
	}
}

func TestPasskeyRegisterRoundTrip(t *testing.T) {
	mux, password, a := passkeyFixture(t)
	session := sessionCookie(t, mux, password)

	options, ceremony := beginRegister(t, mux, session)
	body, err := a.Registration(options)
	if err != nil {
		t.Fatalf("Registration: %s", err)
	}
	w := postJSON(mux, "/auth/passkeys/register/finish", body, session, ceremony)
	if w.Code != http.StatusCreated {
		t.Fatalf("register finish status = %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	// The finish retired the ceremony cookie.
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == ceremonyCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("finish did not clear the ceremony cookie")
	}

	r := httptest.NewRequest(http.MethodGet, "/api/passkeys", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", w.Code)
	}
	var listed struct {
		Passkeys []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Created  int64  `json:"created"`
			LastUsed int64  `json:"lastUsed"`
			BackedUp bool   `json:"backedUp"`
		} `json:"passkeys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list body is not JSON: %s", err)
	}
	if len(listed.Passkeys) != 1 || listed.Passkeys[0].Name != "Passkey" {
		t.Fatalf("list = %+v, want the one registered key", listed)
	}
	rawID, err := base64.RawURLEncoding.DecodeString(listed.Passkeys[0].ID)
	if err != nil || !bytes.Equal(rawID, a.CredentialID) {
		t.Fatalf("listed id = %q, want the credential id base64url", listed.Passkeys[0].ID)
	}
	if listed.Passkeys[0].Created <= 0 || listed.Passkeys[0].LastUsed <= 0 {
		t.Fatalf("listed times = %+v, want unix seconds", listed.Passkeys[0])
	}
}

func TestPasskeyLoginRoundTrip(t *testing.T) {
	mux, password, a := passkeyFixture(t)
	session := sessionCookie(t, mux, password)
	options, ceremony := beginRegister(t, mux, session)
	regBody, err := a.Registration(options)
	if err != nil {
		t.Fatalf("Registration: %s", err)
	}
	if w := postJSON(mux, "/auth/passkeys/register/finish", regBody, session, ceremony); w.Code != http.StatusCreated {
		t.Fatalf("register finish status = %d, want 201", w.Code)
	}

	// Login is public: no session cookie on either call.
	w := postJSON(mux, "/auth/passkeys/login/begin", nil, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("login begin status = %d, want 200", w.Code)
	}
	loginBody, err := a.Assertion(w.Body.Bytes())
	if err != nil {
		t.Fatalf("Assertion: %s", err)
	}
	w = postJSON(mux, "/auth/passkeys/login/finish", loginBody, "", ceremonyValue(t, w))
	if w.Code != http.StatusNoContent {
		t.Fatalf("login finish status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("no %s cookie set", auth.CookieName)
	}
	if !found.HttpOnly || !found.Secure || found.SameSite != http.SameSiteStrictMode || found.Path != "/" {
		t.Fatalf("session cookie = %+v, want the password sign-in shape", found)
	}
	if found.MaxAge != int(auth.AbsoluteTimeout.Seconds()) {
		t.Fatalf("session cookie MaxAge = %d, want the absolute timeout", found.MaxAge)
	}
	r := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: found.Value})
	me := httptest.NewRecorder()
	mux.ServeHTTP(me, r)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"admin"`) {
		t.Fatalf("/auth/me = %d %q, want the admin", me.Code, me.Body.String())
	}
}

func TestPasskeyRouteFailures(t *testing.T) {
	mux, password, a := passkeyFixture(t)
	session := sessionCookie(t, mux, password)

	t.Run("register begin needs a session", func(t *testing.T) {
		if w := postJSON(mux, "/auth/passkeys/register/begin", nil, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("register finish needs a session", func(t *testing.T) {
		if w := postJSON(mux, "/auth/passkeys/register/finish", []byte("{}"), "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("register finish without a ceremony", func(t *testing.T) {
		w := postJSON(mux, "/auth/passkeys/register/finish", []byte("{}"), session, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})
	t.Run("register finish with garbage", func(t *testing.T) {
		_, ceremony := beginRegister(t, mux, session)
		w := postJSON(mux, "/auth/passkeys/register/finish", []byte("{broken"), session, ceremony)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, never 401 (the session is fine)", w.Code)
		}
	})
	t.Run("register duplicate", func(t *testing.T) {
		options, ceremony := beginRegister(t, mux, session)
		regBody, err := a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		if w := postJSON(mux, "/auth/passkeys/register/finish", regBody, session, ceremony); w.Code != http.StatusCreated {
			t.Fatalf("first finish = %d, want 201", w.Code)
		}
		options, ceremony = beginRegister(t, mux, session)
		regBody, err = a.Registration(options)
		if err != nil {
			t.Fatalf("Registration: %s", err)
		}
		w := postJSON(mux, "/auth/passkeys/register/finish", regBody, session, ceremony)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "already registered") {
			t.Fatalf("second finish = %d %q, want 400 already registered", w.Code, w.Body.String())
		}
	})
	t.Run("login finish without a ceremony", func(t *testing.T) {
		w := postJSON(mux, "/auth/passkeys/login/finish", []byte("{}"), "", "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})
	t.Run("login finish with garbage", func(t *testing.T) {
		w := postJSON(mux, "/auth/passkeys/login/begin", nil, "", "")
		if w.Code != http.StatusOK {
			t.Fatalf("login begin = %d, want 200", w.Code)
		}
		w = postJSON(mux, "/auth/passkeys/login/finish", []byte("{broken"), "", ceremonyValue(t, w))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("login rate limited", func(t *testing.T) {
		stranger, err := authtest.New("localhost", passkeyURL)
		if err != nil {
			t.Fatalf("authtest.New: %s", err)
		}
		regOptions, _ := beginRegister(t, mux, session)
		if _, err := stranger.Registration(regOptions); err != nil {
			t.Fatalf("Registration: %s", err)
		}
		var last *httptest.ResponseRecorder
		for range auth.MaxSignInFailures + 1 {
			w := postJSON(mux, "/auth/passkeys/login/begin", nil, "", "")
			if w.Code != http.StatusOK {
				t.Fatalf("login begin = %d, want 200", w.Code)
			}
			assertBody, err := stranger.Assertion(w.Body.Bytes())
			if err != nil {
				t.Fatalf("Assertion: %s", err)
			}
			last = postJSON(mux, "/auth/passkeys/login/finish", assertBody, "", ceremonyValue(t, w))
		}
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("overflow status = %d, want 429", last.Code)
		}
		if last.Header().Get("Retry-After") != "60" {
			t.Fatalf("Retry-After = %q, want 60", last.Header().Get("Retry-After"))
		}
	})
	t.Run("ceremony methods", func(t *testing.T) {
		for _, target := range []string{
			"/auth/passkeys/register/begin", "/auth/passkeys/register/finish",
			"/auth/passkeys/login/begin", "/auth/passkeys/login/finish",
		} {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("GET %s = %d, want 405", target, w.Code)
			}
		}
	})
}

func TestPasskeyDisabledRoutes(t *testing.T) {
	mux, password := fixture(t, "")
	session := sessionCookie(t, mux, password)
	for _, target := range []string{"/auth/passkeys/register/begin", "/auth/passkeys/login/begin"} {
		w := postJSON(mux, target, nil, session, "")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not enabled") {
			t.Fatalf("POST %s = %d %q, want 400 not enabled", target, w.Code, w.Body.String())
		}
	}
}

func TestPasskeyManagementRoutes(t *testing.T) {
	mux, password, a := passkeyFixture(t)
	session := sessionCookie(t, mux, password)
	options, ceremony := beginRegister(t, mux, session)
	regBody, err := a.Registration(options)
	if err != nil {
		t.Fatalf("Registration: %s", err)
	}
	if w := postJSON(mux, "/auth/passkeys/register/finish", regBody, session, ceremony); w.Code != http.StatusCreated {
		t.Fatalf("register finish = %d, want 201", w.Code)
	}
	id := base64.RawURLEncoding.EncodeToString(a.CredentialID)

	patch := func(id, name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/passkeys/"+id, strings.NewReader(`{"name":`+strconv.Quote(name)+`}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	delete := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/api/passkeys/"+id, nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	if w := patch(id, "Tablet"); w.Code != http.StatusNoContent {
		t.Fatalf("PATCH status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "/api/passkeys", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"name":"Tablet"`) {
		t.Fatalf("list after rename = %q, want Tablet", w.Body.String())
	}
	if w := patch(id, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH empty name = %d, want 400", w.Code)
	}
	if w := patch("missing", "Ghost"); w.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown id = %d, want 404", w.Code)
	}
	if w := patch("!!!", "Ghost"); w.Code != http.StatusNotFound {
		t.Fatalf("PATCH undecodable id = %d, want 404", w.Code)
	}
	if w := patch(id+"/extra", "Ghost"); w.Code != http.StatusNotFound {
		t.Fatalf("PATCH nested path = %d, want 404", w.Code)
	}
	if w := delete(id); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", w.Code)
	}
	if w := delete(id); w.Code != http.StatusNoContent {
		t.Fatalf("second DELETE = %d, want 204 (idempotent)", w.Code)
	}
	if w := delete("missing"); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE unknown id = %d, want 204 (idempotent)", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/passkeys", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if strings.TrimSpace(w.Body.String()) != `{"passkeys":[]}` {
		t.Fatalf("list after delete = %q, want empty", w.Body.String())
	}

	t.Run("needs a session", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/passkeys", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("list without session = %d, want 401", w.Code)
		}
		r = httptest.NewRequest(http.MethodDelete, "/api/passkeys/"+id, nil)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("delete without session = %d, want 401", w.Code)
		}
	})
	t.Run("methods", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/passkeys", nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST collection = %d, want 405", w.Code)
		}
		r = httptest.NewRequest(http.MethodGet, "/api/passkeys/"+id, nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET member = %d, want 405", w.Code)
		}
	})
}

func TestPasskeyCSRF(t *testing.T) {
	mux, password, _ := passkeyFixture(t)
	session := sessionCookie(t, mux, password)
	r := httptest.NewRequest(http.MethodPost, "/auth/passkeys/login/begin", nil)
	r.Header.Set("Origin", "https://evil.test")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site login begin = %d, want 403", w.Code)
	}
	r = httptest.NewRequest(http.MethodDelete, "/api/passkeys/abc", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	r.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site passkey delete = %d, want 403", w.Code)
	}
	// Safe reads pass cross-site.
	r = httptest.NewRequest(http.MethodGet, "/auth/passkeys/status", nil)
	r.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cross-site status read = %d, want 200", w.Code)
	}
}

func TestPasskeyPath(t *testing.T) {
	cases := []struct {
		path string
		id   string
		ok   bool
	}{
		{"/api/passkeys/abc", "abc", true},
		{"/api/passkeys/", "", false},
		{"/api/passkeys", "", false},
		{"/api/passkeys/a/b", "", false},
		{"/api/other/abc", "", false},
	}
	for _, tc := range cases {
		id, ok := passkeyPath(tc.path)
		if id != tc.id || ok != tc.ok {
			t.Fatalf("passkeyPath(%q) = (%q, %v), want (%q, %v)", tc.path, id, ok, tc.id, tc.ok)
		}
	}
}
