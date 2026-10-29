package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// fakeAWSKey is a made-up value in the AWS access key shape, built
// from parts so secret scanners skip this file.
const fakeAWSKey = "AKIA" + "Z7QWERTY4UIOP3XZ"

func redactFixture(t *testing.T) (http.Handler, string, *redact.Redactor) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := redact.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := redact.LoadSettings(ctx, s.Meta())
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Redact: r, RedactSettings: settings})
	return mux, password, r
}

func doJSON(mux http.Handler, method, target, body, session string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if session != "" {
		r = withSession(r, session)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestRedactState(t *testing.T) {
	mux, password, _ := redactFixture(t)
	session := sessionCookie(t, mux, password)
	w := doJSON(mux, http.MethodGet, "/api/redact", "", session)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/redact = %d %q", w.Code, w.Body.String())
	}
	var st struct {
		Policy map[string]string `json:"policy"`
		Types  []string          `json:"types"`
		Modes  []string          `json:"modes"`
		Stats  map[string]struct {
			Detected int `json:"detected"`
		} `json:"stats"`
		Rules int `json:"rules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Policy["SECRET"] != "mask" || st.Policy["EMAIL"] != "off" || len(st.Types) != 5 || len(st.Modes) != 4 || st.Rules < 200 {
		t.Fatalf("state = %+v", st)
	}
	if _, ok := st.Stats["CARD"]; !ok {
		t.Fatalf("stats lack CARD: %+v", st.Stats)
	}
	if w := doJSON(mux, http.MethodGet, "/api/redact", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d, want 401", w.Code)
	}
	if w := doJSON(mux, http.MethodPost, "/api/redact", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", w.Code)
	}
}

func TestRedactPolicyAndTest(t *testing.T) {
	mux, password, r := redactFixture(t)
	session := sessionCookie(t, mux, password)
	text := `{"text":"key ` + fakeAWSKey + ` mail ada@example.org"}`

	w := doJSON(mux, http.MethodPost, "/api/redact/test", text, session)
	var res struct {
		Text     string           `json:"text"`
		Findings []redact.Finding `json:"findings"`
		Blocked  []string         `json:"blocked"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK {
		t.Fatalf("test = %d %q", w.Code, w.Body.String())
	}
	if strings.Contains(res.Text, fakeAWSKey) || !strings.Contains(res.Text, "ada@example.org") || len(res.Findings) != 1 || len(res.Blocked) != 0 {
		t.Fatalf("default policy result = %+v", res)
	}

	policy := `{"SECRET":"mask","EMAIL":"block","PHONE":"off","CARD":"off","IPV4":"log"}`
	if w := doJSON(mux, http.MethodPut, "/api/redact/policy", policy, session); w.Code != http.StatusNoContent {
		t.Fatalf("PUT policy = %d %q", w.Code, w.Body.String())
	}
	w = doJSON(mux, http.MethodPost, "/api/redact/test", text, session)
	res.Blocked = nil
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Text != "" || len(res.Blocked) != 1 || res.Blocked[0] != "EMAIL" {
		t.Fatalf("blocked result = %+v", res)
	}
	if strings.Contains(w.Body.String(), fakeAWSKey) || strings.Contains(w.Body.String(), "ada@example.org") {
		t.Fatalf("a value leaked into the blocked response: %s", w.Body.String())
	}
	if st := r.Stats()[redact.Secret]; st.Detected != 0 {
		t.Fatalf("the test box counted: %+v", st)
	}

	for _, bad := range []string{`{"SECRET":"loud"}`, `{"SECRET":"mask"}`, `[]`, `{`} {
		if w := doJSON(mux, http.MethodPut, "/api/redact/policy", bad, session); w.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d, want 400", bad, w.Code)
		}
	}
	if w := doJSON(mux, http.MethodPost, "/api/redact/test", `{"text":`, session); w.Code != http.StatusBadRequest {
		t.Fatalf("broken test body = %d, want 400", w.Code)
	}
	big := `{"text":"` + strings.Repeat("a", 70<<10) + `"}`
	if w := doJSON(mux, http.MethodPost, "/api/redact/test", big, session); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized test body = %d, want 400", w.Code)
	}
	if w := doJSON(mux, http.MethodGet, "/api/redact/test", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET test = %d, want 405", w.Code)
	}
	if w := doJSON(mux, http.MethodPost, "/api/redact/policy", policy, session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST policy = %d, want 405", w.Code)
	}
}

func TestRedactCrossSiteWriteRefused(t *testing.T) {
	mux, password, _ := redactFixture(t)
	session := sessionCookie(t, mux, password)
	r := httptest.NewRequest(http.MethodPut, "/api/redact/policy", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, withSession(r, session))
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site PUT = %d, want 403", w.Code)
	}
}

func TestRedactNotConfigured(t *testing.T) {
	mux, password := fixture(t, "")
	session := sessionCookie(t, mux, password)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/redact"},
		{http.MethodPut, "/api/redact/policy"},
		{http.MethodPost, "/api/redact/test"},
	} {
		if w := doJSON(mux, tc.method, tc.path, "{}", session); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d, want 503", tc.method, tc.path, w.Code)
		}
	}
}
