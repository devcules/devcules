package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/link"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/web"
)

// testWeb is a built app in miniature: an entry point plus one
// hashed asset.
func testWeb() http.Handler {
	return web.Handler(fstest.MapFS{
		"index.html":           {Data: []byte("<div id=\"root\"></div>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
	})
}

// fixture opens an isolated store with a bootstrapped admin and
// returns the mux plus the one-time password.
func fixture(t *testing.T, publicURL string) (http.Handler, string) {
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
	return Handler(Options{Store: s, Auth: svc, Web: testWeb(), PublicURL: publicURL}), password
}

// postForm posts a urlencoded form to target.
func postForm(mux http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func signInCredentials(password string) url.Values {
	return url.Values{"username": {auth.AdminUsername}, "password": {password}}
}

// sessionCookie signs in through the mux and returns the session
// cookie value.
func sessionCookie(t *testing.T, mux http.Handler, password string) string {
	t.Helper()
	w := postForm(mux, "/auth/sign-in", signInCredentials(password))
	if w.Code != http.StatusNoContent {
		t.Fatalf("sign-in status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c.Value
		}
	}
	t.Fatalf("sign-in set no %s cookie", auth.CookieName)
	return ""
}

func withSession(r *http.Request, value string) *http.Request {
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: value})
	return r
}

func TestHealthRoutes(t *testing.T) {
	mux, _ := fixture(t, "")
	cases := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantBody   string
	}{
		{"healthz get", http.MethodGet, "/healthz", http.StatusOK, "ok"},
		{"healthz wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, ""},
		{"readyz wrong method", http.MethodPost, "/readyz", http.StatusMethodNotAllowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantBody != "" && w.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", w.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestReadyzStates(t *testing.T) {
	open := func(t *testing.T) store.Store {
		t.Helper()
		s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatalf("Open: %s", err)
		}
		return s
	}
	up := open(t)
	defer up.Close()
	down := open(t)
	if err := down.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	muxUp := Handler(Options{Store: up, Auth: auth.New(up), Web: testWeb()})
	muxDown := Handler(Options{Store: down, Auth: auth.New(down), Web: testWeb()})
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	muxUp.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "{\"status\":\"ok\",\"runners_linked\":0}\n" {
		t.Fatalf("readyz up = %d %q, want 200 JSON with zero runners", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("readyz content type = %q, want application/json", ct)
	}
	w = httptest.NewRecorder()
	muxDown.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != "not ready: store unreachable" {
		t.Fatalf("readyz down = %d %q, want 503 fixed reason", w.Code, w.Body.String())
	}
}

// TestReadyzCountsRunners wires a real hub with one live link and
// requires the count in the JSON. The link itself runs over the
// in-process pipe harness (see internal/link/linktest).
func TestReadyzCountsRunners(t *testing.T) {
	up, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	h := hub.New(hub.HashToken("api-test-token"), hub.Options{HelloTimeout: 5 * time.Second})
	mux := Handler(Options{Store: up, Auth: auth.New(up), Web: testWeb(), Link: h})
	s := linktest.Serve(t, mux)

	conn, _, err := websocket.Dial(context.Background(), "ws://pipe/link", &websocket.DialOptions{
		HTTPClient: s.Client(),
		HTTPHeader: http.Header{"Authorization": {"Bearer api-test-token"}},
	})
	if err != nil {
		t.Fatalf("dial: %s", err)
	}
	defer conn.CloseNow()
	hello, err := link.Encode(link.Frame{V: 1, Type: "hello", ID: "1", Data: json.RawMessage(`{"role":"runner","name":"r","version":"t"}`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("write hello: %s", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read welcome: %s", err)
	}

	// The link registers just after its welcome goes out, so poll
	// with a deadline instead of reading once.
	want := "{\"status\":\"ok\",\"runners_linked\":1}\n"
	var w *httptest.ResponseRecorder
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if w.Code == http.StatusOK && w.Body.String() == want {
			break
		}
	}
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("readyz linked = %d %q, want runners_linked 1", w.Code, w.Body.String())
	}
}

// TestLinkRoutePublic proves GET /link reaches the hub without a
// session: a missing token answers 401 (not the catch-all's 401 for
// missing sessions — the status matches but the hub owns this path,
// proven by the 403 on Origin below), and an Origin header answers
// 403 instead of falling through to the app.
func TestLinkRoutePublic(t *testing.T) {
	up, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	h := hub.New(hub.HashToken("api-test-token"), hub.Options{})
	mux := Handler(Options{Store: up, Auth: auth.New(up), Web: testWeb(), Link: h})

	r := httptest.NewRequest(http.MethodGet, "/link", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/link without token = %d, want 401 from the hub", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/link", nil)
	r.Header.Set("Origin", "https://browser.example")
	r.Header.Set("Authorization", "Bearer api-test-token")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("/link with Origin = %d, want 403 from the hub", w.Code)
	}

	// Without a hub the route explains itself instead of 404ing into
	// the app shell.
	muxNoLink := Handler(Options{Store: up, Auth: auth.New(up), Web: testWeb()})
	w = httptest.NewRecorder()
	muxNoLink.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/link", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("/link without hub = %d, want 503", w.Code)
	}
}

func TestSignInSuccess(t *testing.T) {
	mux, password := fixture(t, "")
	w := postForm(mux, "/auth/sign-in", signInCredentials(password))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want none (JS is in charge now)", loc)
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("no %s cookie set", auth.CookieName)
	}
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode || session.Path != "/" {
		t.Fatalf("cookie attributes = HttpOnly:%v Secure:%v SameSite:%v Path:%q, want HttpOnly Secure Strict /",
			session.HttpOnly, session.Secure, session.SameSite, session.Path)
	}
	if session.MaxAge != int(auth.AbsoluteTimeout.Seconds()) {
		t.Fatalf("cookie MaxAge = %d, want the absolute timeout in seconds", session.MaxAge)
	}
}

func TestSignInFailures(t *testing.T) {
	mux, password := fixture(t, "")
	cases := []struct {
		name       string
		form       url.Values
		wantStatus int
		wantBody   string
	}{
		{"wrong password", signInCredentials(password + "x"), http.StatusUnauthorized, `{"error":"invalid username or password"}`},
		{"unknown user", url.Values{"username": {"nobody"}, "password": {"whatever"}}, http.StatusUnauthorized, `{"error":"invalid username or password"}`},
		{"empty form", url.Values{}, http.StatusUnauthorized, `{"error":"invalid username or password"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postForm(mux, "/auth/sign-in", tc.form)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if strings.TrimSpace(w.Body.String()) != tc.wantBody {
				t.Fatalf("body = %q, want %q", w.Body.String(), tc.wantBody)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content type = %q, want json", ct)
			}
		})
	}
}

func TestSignInRateLimited(t *testing.T) {
	mux, _ := fixture(t, "")
	bad := url.Values{"username": {auth.AdminUsername}, "password": {"wrong"}}
	// httptest requests share RemoteAddr, so one IP: five failures,
	// then the limit.
	for i := range 5 {
		if w := postForm(mux, "/auth/sign-in", bad); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401", i+1, w.Code)
		}
	}
	w := postForm(mux, "/auth/sign-in", bad)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "60" {
		t.Fatalf("Retry-After = %q, want 60", ra)
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"too many sign-in attempts, try again later"}` {
		t.Fatalf("limited body = %q", body)
	}
}

func TestSignInBrokenForm(t *testing.T) {
	mux, _ := fixture(t, "")
	// An unparseable body has no usable fields: it fails closed as
	// invalid credentials, never as a 500 or a bypass.
	r := httptest.NewRequest(http.MethodPost, "/auth/sign-in", strings.NewReader("x"))
	r.Header.Set("Content-Type", "multipart/form-data")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuthRoutesWrongMethod(t *testing.T) {
	mux, password := fixture(t, "")
	value := sessionCookie(t, mux, password)
	cases := []struct {
		name   string
		method string
		target string
		cookie bool
	}{
		{"sign-in get", http.MethodGet, "/auth/sign-in", false},
		{"sign-out get", http.MethodGet, "/auth/sign-out", true},
		{"me post", http.MethodPost, "/auth/me", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.cookie {
				r = withSession(r, value)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", w.Code)
			}
			if allow := w.Header().Get("Allow"); tc.target == "/auth/me" && allow != http.MethodGet ||
				tc.target != "/auth/me" && allow != http.MethodPost {
				t.Fatalf("Allow = %q", allow)
			}
		})
	}
}

func TestSignInStoreDown(t *testing.T) {
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatalf("EnsureAdmin: %s", err)
	}
	mux := Handler(Options{Store: s, Auth: svc})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %s", err)
	}
	w := postForm(mux, "/auth/sign-in", signInCredentials(password))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"internal error"}` {
		t.Fatalf("body = %q, want the fixed internal error (never driver text)", body)
	}
}

func TestMe(t *testing.T) {
	mux, password := fixture(t, "")
	r := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie status = %d, want 401", w.Code)
	}
	r = withSession(httptest.NewRequest(http.MethodGet, "/auth/me", nil), "bogus")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bogus cookie status = %d, want 401", w.Code)
	}
	value := sessionCookie(t, mux, password)
	r = withSession(httptest.NewRequest(http.MethodGet, "/auth/me", nil), value)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"username":"admin"}` {
		t.Fatalf("body = %q, want the admin username", body)
	}
}

func TestSignOut(t *testing.T) {
	mux, password := fixture(t, "")
	r := httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie status = %d, want 401", w.Code)
	}
	value := sessionCookie(t, mux, password)
	r = withSession(httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil), value)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("sign-out = %d %q, want 200 ok", w.Code, w.Body.String())
	}
	var cleared *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			cleared = c
		}
	}
	if cleared == nil || cleared.Value != "" {
		t.Fatalf("sign-out did not clear the cookie: %+v", cleared)
	}
	// The session is dead: /auth/me rejects the old value.
	r = withSession(httptest.NewRequest(http.MethodGet, "/auth/me", nil), value)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("post-sign-out status = %d, want 401", w.Code)
	}
}

func TestSignOutWithoutMiddleware(t *testing.T) {
	// signOut behind requireSession always sees a cookie; called
	// bare (a miswired mux) it must 401, never panic.
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "direct.db"))
	if err != nil {
		t.Fatalf("Open: %s", err)
	}
	defer s.Close()
	r := httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil)
	w := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() != nil {
				t.Fatal("bare signOut panicked on a missing cookie")
			}
		}()
		signOut(auth.New(s))(w, r)
	}()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bare sign-out status = %d, want 401", w.Code)
	}
}

func TestRootGuard(t *testing.T) {
	mux, password := fixture(t, "")
	// A page navigation gets the app, no session needed: the app
	// holds no data and checks the session itself.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != `<div id="root"></div>` {
		t.Fatalf("anonymous page / = %d %q, want the app", w.Code, w.Body.String())
	}
	// Unknown reads belong to the static app (public: they carry
	// no data), so they 404 with or without a session. Known
	// protected routes still 401.
	r = httptest.NewRequest(http.MethodGet, "/unknown.json", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("anonymous /unknown.json status = %d, want 404", w.Code)
	}
	value := sessionCookie(t, mux, password)
	r = withSession(httptest.NewRequest(http.MethodGet, "/unknown.json", nil), value)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("authenticated /unknown.json status = %d, want 404", w.Code)
	}
	// Unknown auth paths never fall back to the app.
	r = httptest.NewRequest(http.MethodGet, "/auth/nope", nil)
	r.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /auth/nope status = %d, want 401, never the app", w.Code)
	}
	// Unknown writes need a session too, even with a cookie missing.
	r = httptest.NewRequest(http.MethodPost, "/unknown", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST /unknown status = %d, want 401", w.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	mux, _ := fixture(t, "")
	for _, target := range []string{"/healthz", "/auth/me", "/"} {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		if target == "/" {
			r.Header.Set("Accept", "text/html")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if csp := w.Header().Get("Content-Security-Policy"); csp != "default-src 'self'; style-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'" {
			t.Fatalf("%s CSP = %q", target, csp)
		}
		if v := w.Header().Get("X-Content-Type-Options"); v != "nosniff" {
			t.Fatalf("%s nosniff = %q", target, v)
		}
		if v := w.Header().Get("Referrer-Policy"); v != "no-referrer" {
			t.Fatalf("%s referrer = %q", target, v)
		}
	}
}

func TestCSRF(t *testing.T) {
	mux, password := fixture(t, "")

	t.Run("cross-site POST rejected", func(t *testing.T) {
		value := sessionCookie(t, mux, password)
		r := withSession(httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil), value)
		r.Header.Set("Origin", "https://evil.test")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("cross-site status = %d, want 403", w.Code)
		}
		// Rejected means untouched: the session still works.
		r = withSession(httptest.NewRequest(http.MethodGet, "/auth/me", nil), value)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("session after rejected POST = %d, want 200", w.Code)
		}
	})

	t.Run("sec-fetch-site cross-site rejected", func(t *testing.T) {
		value := sessionCookie(t, mux, password)
		r := withSession(httptest.NewRequest(http.MethodPost, "/auth/sign-in", strings.NewReader(signInCredentials(password).Encode())), value)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("sec-fetch cross-site status = %d, want 403", w.Code)
		}
	})

	t.Run("same-origin POST passes", func(t *testing.T) {
		value := sessionCookie(t, mux, password)
		r := withSession(httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil), value)
		r.Host = "serve:7659"
		r.Header.Set("Origin", "http://serve:7659")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("same-origin status = %d, want 200", w.Code)
		}
	})

	t.Run("GET untouched", func(t *testing.T) {
		value := sessionCookie(t, mux, password)
		r := withSession(httptest.NewRequest(http.MethodGet, "/auth/me", nil), value)
		r.Header.Set("Origin", "https://evil.test")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("cross-site GET status = %d, want 200 (safe methods pass)", w.Code)
		}
	})

	t.Run("no origin passes", func(t *testing.T) {
		// curl sends no Origin: non-browser requests are allowed.
		value := sessionCookie(t, mux, password)
		r := withSession(httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil), value)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("no-origin status = %d, want 200", w.Code)
		}
	})
}

func TestCSRFTrustedPublicURL(t *testing.T) {
	// Each attempt burns its session, so the helper signs into the
	// given mux fresh and posts sign-out with the given Origin.
	attempt := func(t *testing.T, mux http.Handler, password, origin string) int {
		t.Helper()
		value := sessionCookie(t, mux, password)
		r := withSession(httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil), value)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	trusted, trustedPW := fixture(t, "https://ops.example.com")
	if got := attempt(t, trusted, trustedPW, "https://ops.example.com"); got != http.StatusOK {
		t.Fatalf("trusted origin status = %d, want 200", got)
	}
	plain, plainPW := fixture(t, "")
	if got := attempt(t, plain, plainPW, "https://ops.example.com"); got != http.StatusForbidden {
		t.Fatalf("untrusted origin status = %d, want 403", got)
	}
	withPath, withPathPW := fixture(t, "https://ops.example.com/console")
	if got := attempt(t, withPath, withPathPW, "https://ops.example.com"); got != http.StatusOK {
		t.Fatalf("origin with public-URL path status = %d, want 200 (only scheme plus host is trusted)", got)
	}
	broken, brokenPW := fixture(t, "://not a url")
	if got := attempt(t, broken, brokenPW, "https://ops.example.com"); got != http.StatusForbidden {
		t.Fatalf("unparseable public URL status = %d, want 403 (fail closed)", got)
	}
}
