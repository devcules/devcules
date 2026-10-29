package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/trace"
)

type oneKey struct{}

func (oneKey) Get(context.Context, string) ([]byte, error) { return []byte("upstream"), nil }

type urlProvider struct{ base string }

func (p urlProvider) ID() string               { return "meta" }
func (p urlProvider) BaseURL() string          { return p.base }
func (p urlProvider) Auth(http.Header, string) {}

// auditFixture wires everything M2.4 touches: auth, redaction, launch
// tokens, the router against an echoing provider, traces and audit.
func auditFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string }
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "echo " + body.Messages[0].Content}}},
			"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 2}})
	}))
	t.Cleanup(up.Close)
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	tokens := launch.New(s)
	traces := trace.New(s, nil)
	rt, err := router.New(router.Options{Providers: []router.Provider{urlProvider{up.URL}}, Keys: oneKey{}, Tokens: tokens,
		Redactor: r, Settings: settings, Tracer: traces})
	if err != nil {
		t.Fatal(err)
	}
	return Handler(Options{Store: s, Auth: svc, Web: testWeb(), Router: rt, Launches: tokens, Redact: r, RedactSettings: settings,
		AuditLog: audit.New(s, nil), TraceStore: traces}), password
}

func auditActions(t *testing.T, mux http.Handler, session string) []audit.Entry {
	t.Helper()
	w := doJSON(mux, http.MethodGet, "/api/audit?limit=50", "", session)
	if w.Code != 200 {
		t.Fatalf("audit = %d %s", w.Code, w.Body)
	}
	var res struct{ Entries []audit.Entry }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	return res.Entries
}

func TestAuditAndTracesAPI(t *testing.T) {
	mux, password := auditFixture(t)
	if w := postForm(mux, "/auth/sign-in", url.Values{"username": {"admin"}, "password": {"wrong-" + password}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad sign-in = %d", w.Code)
	}
	session := sessionCookie(t, mux, password)

	pol := redact.DefaultPolicy()
	pol[redact.Email] = redact.Mask
	b, _ := json.Marshal(pol)
	if w := doJSON(mux, http.MethodPut, "/api/redact/policy", string(b), session); w.Code != 204 {
		t.Fatalf("policy = %d", w.Code)
	}
	// Saving the same policy again changes nothing and records nothing.
	doJSON(mux, http.MethodPut, "/api/redact/policy", string(b), session)

	w := doJSON(mux, http.MethodPost, "/api/launch-tokens", `{"label":"trace me","provider":"meta"}`, session)
	var minted struct {
		Token  string
		Launch launch.Launch
	}
	_ = json.Unmarshal(w.Body.Bytes(), &minted)
	secret := "AKIA" + "Z7QWERTY4UIOP3XZ"
	if w := doJSONBearer(mux, "/llm/meta/v1/chat/completions", `{"model":"muse-spark-1.3","messages":[{"role":"user","content":"use `+secret+` and ada@example.org"}]}`, minted.Token); w.Code != 200 || !strings.Contains(w.Body.String(), secret) {
		t.Fatalf("router = %d %s", w.Code, w.Body)
	}

	w = doJSON(mux, http.MethodGet, "/api/traces", "", session)
	var list struct {
		Sessions []struct {
			LaunchID, Label, Provider string
			Calls, Redacted           int
			InputTokens               int64
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Sessions) != 1 || list.Sessions[0].Label != "trace me" || list.Sessions[0].Calls != 1 ||
		list.Sessions[0].Redacted != 2 || list.Sessions[0].InputTokens != 11 {
		t.Fatalf("traces = %d %s", w.Code, w.Body)
	}
	w = doJSON(mux, http.MethodGet, "/api/traces/"+minted.Launch.ID, "", session)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "ada@example.org") ||
		!strings.Contains(w.Body.String(), "REDACTED_SECRET_") || !strings.Contains(w.Body.String(), `"label":"trace me"`) {
		t.Fatalf("trace = %d %s", w.Code, w.Body)
	}
	if w := doJSON(mux, http.MethodGet, "/api/traces/nope", "", session); w.Code != 404 {
		t.Fatalf("unknown trace = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodGet, "/api/traces/a/b", "", session); w.Code != 404 {
		t.Fatalf("nested trace path = %d", w.Code)
	}
	doJSON(mux, http.MethodDelete, "/api/launch-tokens/"+minted.Launch.ID, "", session)

	w = doJSON(mux, http.MethodGet, "/api/audit/export", "", session)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-ndjson" || !strings.Contains(w.Header().Get("Content-Disposition"), "devcules-audit-") {
		t.Fatalf("export = %d %v", w.Code, w.Header())
	}
	if n, err := audit.VerifyLines(strings.NewReader(w.Body.String())); err != nil || n != 7 {
		t.Fatalf("export verifies %d, %v:\n%s", n, err, w.Body)
	}
	for _, leak := range []string{password, minted.Token, secret} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("audit holds %q", leak)
		}
	}
	w = doJSON(mux, http.MethodGet, "/api/audit/export?verify=1", "", session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true,"checked":7`) {
		t.Fatalf("verify = %d %s", w.Code, w.Body)
	}
	if w := postForm(mux, "/auth/sign-out", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("sign-out without a session = %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/auth/sign-out", nil)
	r = withSession(r, session)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)

	// A fresh session reads the whole story, newest first.
	session = sessionCookie(t, mux, password)
	entries := auditActions(t, mux, session)
	var got []string
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		got = append(got, e.Action+":"+e.Actor)
	}
	want := []string{
		"auth.sign_in_failed:anonymous", "auth.sign_in:admin", "redact.policy:admin", "launch.mint:admin",
		"trace.read:admin", "launch.revoke:admin", "audit.export:admin", "auth.sign_out:admin", "auth.sign_in:admin",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("audit:\n got %v\nwant %v", got, want)
	}
	if entries[len(entries)-3].Detail["EMAIL"] != "off → mask" || entries[len(entries)-4].Target != minted.Launch.ID {
		t.Fatalf("details: %+v %+v", entries[len(entries)-3], entries[len(entries)-4])
	}
	if older := doJSON(mux, http.MethodGet, "/api/audit?before=3", "", session); !strings.Contains(older.Body.String(), `"seq":2`) || strings.Contains(older.Body.String(), `"seq":3`) {
		t.Fatalf("paging = %s", older.Body)
	}
	if w := doJSON(mux, http.MethodPost, "/api/audit", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST audit = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodPost, "/api/audit/export", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST export = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodPost, "/api/traces", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST traces = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodPost, "/api/traces/x", "", session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST trace = %d", w.Code)
	}
	if w := doJSON(mux, http.MethodGet, "/api/audit", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("audit without a session = %d", w.Code)
	}
}

func TestAuditNotConfigured(t *testing.T) {
	mux, password := fixture(t, "")
	session := sessionCookie(t, mux, password)
	for _, p := range []string{"/api/audit", "/api/audit/export", "/api/traces", "/api/traces/x"} {
		if w := doJSON(mux, http.MethodGet, p, "", session); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d", p, w.Code)
		}
	}
}
