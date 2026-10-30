package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/providers"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/terminals"
)

type profileFix struct {
	mux     http.Handler
	session string
	keys    *vaultKeys
	up      *httptest.Server
	auth    *atomic.Value
}

// newProfileFix serves the profile routes over a real store, seeded
// with Meta, and an upstream that lists two models.
func newProfileFix(t *testing.T) *profileFix {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seen := &atomic.Value{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization") + r.Header.Get("x-api-key"))
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-3"},{"id":"llama-4"}]}`))
	}))
	t.Cleanup(up.Close)
	profiles := providers.New(s.Providers(), s.Meta())
	if err := profiles.Seed(ctx, []providers.Profile{{ID: "meta", Name: "Meta", Format: providers.FormatOpenAI, AlsoAnthropic: true, BaseURL: up.URL, Default: true}}); err != nil {
		t.Fatal(err)
	}
	keys := &vaultKeys{m: map[string][]byte{router.KeySecret("meta"): []byte(fakeKey("meta"))}}
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	rt, err := router.New(router.Options{Catalog: profiles, Keys: keys, Tokens: launch.New(s), Redactor: r, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Router: rt, ProviderKeys: keys, Profiles: profiles, AuditLog: audit.New(s, nil)})
	return &profileFix{mux: mux, session: sessionCookie(t, mux, password), keys: keys, up: up, auth: seen}
}

func (f *profileFix) do(method, path, body string) *httptest.ResponseRecorder {
	return doJSON(f.mux, method, path, body, f.session)
}

type rowList struct {
	Providers []profileRow
}

func (f *profileFix) list(t *testing.T) map[string]profileRow {
	t.Helper()
	w := f.do(http.MethodGet, "/api/providers", "")
	var res rowList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	out := map[string]profileRow{}
	for _, p := range res.Providers {
		out[p.ID] = p
	}
	return out
}

func TestProfileRoutes(t *testing.T) {
	t.Parallel()
	f := newProfileFix(t)
	key := fakeKey("vllm")

	if got := f.list(t)["meta"]; got.Name != "Meta" || got.Format != "openai" || !got.AlsoAnthropic || !got.Default || !got.Ready {
		t.Fatalf("seeded meta = %+v", got)
	}
	for body, want := range map[string]int{
		`nope`: http.StatusBadRequest,
		`{"name":"x","format":"openai","baseUrl":"http://evil.example.com/v1"}`:      http.StatusBadRequest,
		`{"name":"x","format":"openai","baseUrl":"http://169.254.169.254/v1"}`:       http.StatusBadRequest,
		`{"name":"x","format":"openai","baseUrl":"https://u:p@api.example.com"}`:     http.StatusBadRequest,
		`{"name":"x","format":"gemini","baseUrl":"https://api.example.com"}`:         http.StatusBadRequest,
		`{"name":"x","format":"openai","baseUrl":"https://a.example","key":"short"}`: http.StatusBadRequest,
	} {
		if w := f.do(http.MethodPost, "/api/providers", body); w.Code != want {
			t.Errorf("POST %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
	}
	w := f.do(http.MethodPost, "/api/providers", `{"name":"Local vLLM","format":"openai","baseUrl":"`+f.up.URL+`/v1","api":"chat","model":"qwen-3","key":"`+key+`"}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"id":"localvllm"`) || strings.Contains(w.Body.String(), key) {
		t.Fatalf("add = %d %s", w.Code, w.Body)
	}
	if string(f.keys.m[router.KeySecret("localvllm")]) != key {
		t.Fatal("the key did not reach the vault")
	}
	got := f.list(t)["localvllm"]
	if got.API != "chat" || got.Model != "qwen-3" || got.Default || !got.Ready || got.BaseURL != f.up.URL+"/v1" {
		t.Fatalf("added = %+v", got)
	}
	// It serves at once: its model list comes through the router.
	if w := f.do(http.MethodGet, "/api/providers/localvllm/models", ""); w.Code != http.StatusOK || f.auth.Load() != "Bearer "+key {
		t.Fatalf("models = %d %s with %v", w.Code, w.Body, f.auth.Load())
	}

	// Edits: a blank key keeps the saved one on the same host; moving
	// to another host needs the key again.
	if w := f.do(http.MethodPut, "/api/providers/localvllm", `{"name":"vLLM","format":"openai","baseUrl":"`+f.up.URL+`","api":"responses"}`); w.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", w.Code, w.Body)
	}
	if got := f.list(t)["localvllm"]; got.Name != "vLLM" || got.API != "responses" || got.Model != "" || !got.Ready {
		t.Fatalf("edited = %+v", got)
	}
	moved := `{"name":"vLLM","format":"openai","baseUrl":"http://192.168.1.9:8000/v1"`
	if w := f.do(http.MethodPut, "/api/providers/localvllm", moved+`}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "paste the API key again") {
		t.Fatalf("moved without key = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/api/providers/localvllm", moved+`,"key":"`+fakeKey("new")+`","default":true}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"default":true`) {
		t.Fatalf("moved with key = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/api/providers/nope", `{"name":"x","format":"openai","baseUrl":"https://a.example"}`); w.Code != http.StatusNotFound {
		t.Fatalf("edit missing = %d", w.Code)
	}

	// The default moves; the default and the last are never removed.
	if w := f.do(http.MethodPut, "/api/providers/meta/default", ""); w.Code != http.StatusNoContent {
		t.Fatalf("default = %d", w.Code)
	}
	if w := f.do(http.MethodPut, "/api/providers/nope/default", ""); w.Code != http.StatusNotFound {
		t.Fatalf("default missing = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/providers/meta", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "make another one the default") {
		t.Fatalf("remove default = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodDelete, "/api/providers/localvllm", ""); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", w.Code, w.Body)
	}
	if _, ok := f.keys.m[router.KeySecret("localvllm")]; ok {
		t.Fatal("the removed profile's key stayed in the vault")
	}
	if w := f.do(http.MethodGet, "/api/providers/localvllm/models", ""); w.Code != http.StatusNotFound {
		t.Fatalf("removed still serves: %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/providers/meta", ""); w.Code != http.StatusConflict {
		t.Fatalf("remove last = %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/api/providers/meta/default", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST default = %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/api/providers/meta/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("bad route = %d", w.Code)
	}

	// Writes need a same-origin request.
	req := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(`{}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: f.session})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site add = %d", rec.Code)
	}

	var actions []string
	for _, e := range auditActions(t, f.mux, f.session) {
		actions = append(actions, e.Action)
		if strings.Contains(e.Detail["key"], "sk-") {
			t.Fatal("a key reached the audit log")
		}
	}
	all := strings.Join(actions, " ")
	for _, want := range []string{audit.ProviderAdd, audit.ProviderEdit, audit.ProviderDefault, audit.ProviderRemove} {
		if !strings.Contains(all, want) {
			t.Errorf("audit = %s, want %s", all, want)
		}
	}
}

func TestProfileProbe(t *testing.T) {
	t.Parallel()
	f := newProfileFix(t)
	typed := fakeKey("typed")
	w := f.do(http.MethodPost, "/api/providers/probe", `{"format":"anthropic","baseUrl":"`+f.up.URL+`/v1","key":"`+typed+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"models":["llama-4","qwen-3"]`) || f.auth.Load() != typed || strings.Contains(w.Body.String(), typed) {
		t.Fatalf("probe = %d %s with %v", w.Code, w.Body, f.auth.Load())
	}
	// A blank key is the saved one, on the saved host only.
	if w := f.do(http.MethodPost, "/api/providers/probe", `{"id":"meta","format":"openai","baseUrl":"`+f.up.URL+`"}`); w.Code != http.StatusOK || f.auth.Load() != "Bearer "+fakeKey("meta") {
		t.Fatalf("saved-key probe = %d %s", w.Code, w.Body)
	}
	for body, why := range map[string]string{
		`{"id":"meta","format":"openai","baseUrl":"https://elsewhere.example/v1"}`:     "only sent where it was saved for",
		`{"format":"openai","baseUrl":"` + f.up.URL + `"}`:                             "paste the API key",
		`{"format":"openai","baseUrl":"http://169.254.169.254","key":"` + typed + `"}`: "link-local",
		`{"format":"openai","baseUrl":"https://a.example","key":"bad key here"}`:       "whole API key",
		`{"format":"x","baseUrl":"https://a.example","key":"` + typed + `"}`:           "format",
		`nope`: "body must be",
	} {
		if w := f.do(http.MethodPost, "/api/providers/probe", body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), why) {
			t.Errorf("probe %s = %d %s, want 400 naming %q", body, w.Code, w.Body, why)
		}
	}
	if w := f.do(http.MethodPost, "/api/providers/probe", `{"format":"openai","baseUrl":"http://127.0.0.1:1","key":"`+typed+`"}`); w.Code != http.StatusBadGateway {
		t.Fatalf("unreachable = %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/api/providers/probe", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET probe = %d", w.Code)
	}
}

func TestOffers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	profiles := providers.New(s.Providers(), s.Meta())
	for _, p := range []providers.Profile{
		{ID: "meta", Name: "Meta", Format: "openai", AlsoAnthropic: true, BaseURL: "https://api.meta.ai"},
		{ID: "anthropic", Name: "Anthropic", Format: "anthropic", BaseURL: "https://api.anthropic.com"},
		{ID: "openai", Name: "OpenAI", Format: "openai", BaseURL: "https://api.openai.com"},
		{ID: "vllm", Name: "A vLLM", Format: "openai", API: "chat", BaseURL: "http://127.0.0.1:8000/v1"},
		{ID: "nebius", Name: "Nebius", Format: "openai", API: "responses", BaseURL: "https://api.studio.nebius.com/v1", Default: true},
		{ID: "proxy", Name: "Claude proxy", Format: "anthropic", BaseURL: "https://proxy.example/v1"},
	} {
		if _, err := profiles.Add(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	_ = profiles.SetDefault(ctx, "nebius")
	r, _ := redact.New(bytes.Repeat([]byte{7}, 32))
	settings, _ := redact.LoadSettings(ctx, s.Meta())
	rt, _ := router.New(router.Options{Catalog: profiles, Keys: metaKey{}, Tokens: launch.New(s), Redactor: r, Settings: settings})
	for _, c := range []struct {
		info terminals.AgentInfo
		want string
	}{
		// Its own first, then the default, then the rest by name.
		{terminals.AgentInfo{Provider: "anthropic", Wire: terminals.WireMessages}, "anthropic,proxy,meta"},
		{terminals.AgentInfo{Provider: "openai", Wire: terminals.WireResponses}, "openai,nebius,meta"},
		{terminals.AgentInfo{Provider: "meta", Wire: terminals.WireResponses, Only: []string{"meta"}}, "meta"},
		// A runner from before profiles: its fixed list, cut to what is served.
		{terminals.AgentInfo{Provider: "anthropic", Providers: []string{"anthropic", "meta", "gone"}}, "anthropic,meta"},
		{terminals.AgentInfo{Provider: "meta"}, "meta"},
	} {
		if got := strings.Join(offers(ctx, c.info, rt, profiles), ","); got != c.want {
			t.Errorf("offers(%+v) = %s, want %s", c.info, got, c.want)
		}
	}
	if got := offers(ctx, terminals.AgentInfo{Provider: "meta"}, nil, nil); len(got) != 0 {
		t.Errorf("no router = %v", got)
	}
	for info, want := range map[*terminals.AgentInfo]string{
		{Only: []string{"meta"}}:        "only speaks to meta",
		{Wire: terminals.WireMessages}:  "Anthropic-format",
		{Wire: terminals.WireResponses}: "/v1/responses",
		{Providers: []string{"a", "b"}}: "speaks a, b",
	} {
		if got := wireNeed(*info); !strings.Contains(got, want) {
			t.Errorf("wireNeed(%+v) = %q, want %q", *info, got, want)
		}
	}
}
