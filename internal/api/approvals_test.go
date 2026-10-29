package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/push"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

type memKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKeys) Get(_ context.Context, name string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := k.m[name]; ok {
		return v, nil
	}
	return nil, vault.ErrNotFound
}

func (k *memKeys) Put(_ context.Context, name string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[name] = v
	return nil
}

type approvalsFixture struct {
	mux      http.Handler
	session  string
	token    string
	launchID string
	log      *audit.Log
	pushes   chan string
}

func newApprovalsFixture(t *testing.T) *approvalsFixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	tokens := launch.New(s)
	token, l, _ := tokens.MintOn(ctx, "laptop", "musecode", "", time.Hour)
	log := audit.New(s, nil)
	p, err := push.New(ctx, push.Options{Store: s, Keys: &memKeys{m: map[string][]byte{}}, Contact: "mailto:owner@example.test", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	a, err := approvals.New(ctx, approvals.Options{Store: s, AuditLog: log, Push: p, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	f := &approvalsFixture{token: token, launchID: l.ID, log: log, pushes: make(chan string, 10)}
	f.mux = Handler(Options{Store: s, Auth: svc, Web: testWeb(), Launches: tokens, AuditLog: log, ApprovalQueue: a, Push: p})
	f.session = sessionCookie(t, f.mux, password)
	return f
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return v
}

func hookGet(mux http.Handler, target, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

type outcome struct {
	Decision string              `json:"decision"`
	Reason   string              `json:"reason"`
	Approval *approvals.Approval `json:"approval"`
}

func TestApprovalsAPIFlow(t *testing.T) {
	f := newApprovalsFixture(t)
	call := `{"agent":"musecode","tool":"Bash","command":"git push --force origin main"}`

	// No mode chosen: the hook is refused with next steps.
	w := doJSONBearer(f.mux, "/hook/approvals", call, f.token)
	if o := decode[outcome](t, w); w.Code != 200 || o.Decision != "deny" || !strings.Contains(o.Reason, "no access mode") {
		t.Fatalf("unset = %d %s", w.Code, w.Body)
	}

	// Settings list the unbound row and the token's runner once it has a mode.
	w = doJSON(f.mux, http.MethodPut, "/api/approvals/modes", `{"target":"runner:laptop","mode":"critical"}`, f.session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"target":"runner:laptop","runner":"laptop","linked":false,"mode":"critical"`) ||
		!strings.Contains(w.Body.String(), `"target":"unbound"`) || !strings.Contains(w.Body.String(), `"timeoutSeconds":60`) {
		t.Fatalf("set mode = %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"target":"runner:laptop","mode":"maybe"}`, `{"target":"laptop","mode":"all"}`, `nope`} {
		if w := doJSON(f.mux, http.MethodPut, "/api/approvals/modes", bad, f.session); w.Code != 400 {
			t.Fatalf("mode %s = %d", bad, w.Code)
		}
	}

	// A plain call runs; the force push waits, then polls.
	w = doJSONBearer(f.mux, "/hook/approvals", `{"tool":"Bash","command":"go test ./..."}`, f.token)
	if o := decode[outcome](t, w); o.Decision != "allow" {
		t.Fatalf("plain = %s", w.Body)
	}
	r := httptest.NewRequest(http.MethodPost, "/hook/approvals?wait=0", strings.NewReader(call))
	r.Header.Set("Authorization", "Bearer "+f.token)
	w = httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	o := decode[outcome](t, w)
	if o.Decision != "ask" || o.Approval == nil || o.Approval.RuleName != "Force push" || !strings.Contains(o.Reason, "Force push") {
		t.Fatalf("critical = %s", w.Body)
	}
	id := o.Approval.ID

	w = doJSON(f.mux, http.MethodGet, "/api/approvals?status=pending", "", f.session)
	list := decode[struct {
		Approvals []approvals.Approval
		Pending   int
	}](t, w)
	if list.Pending != 1 || len(list.Approvals) != 1 || list.Approvals[0].ID != id {
		t.Fatalf("pending list = %s", w.Body)
	}
	if w := doJSON(f.mux, http.MethodGet, "/api/approvals?status=nope", "", f.session); w.Code != 400 {
		t.Fatalf("bad status = %d", w.Code)
	}

	// The hook long-polls; the phone answers.
	done := make(chan outcome)
	go func() {
		w := hookGet(f.mux, "/hook/approvals/"+id+"?wait=10", f.token)
		done <- decode[outcome](t, w)
	}()
	time.Sleep(30 * time.Millisecond)
	if w := doJSON(f.mux, http.MethodPost, "/api/approvals/"+id, `{"decision":"maybe"}`, f.session); w.Code != 400 {
		t.Fatalf("bad decision = %d", w.Code)
	}
	w = doJSON(f.mux, http.MethodPost, "/api/approvals/"+id, `{"decision":"allow"}`, f.session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"allowed"`) || !strings.Contains(w.Body.String(), `"answeredBy":"admin"`) {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if got := <-done; got.Decision != "allow" {
		t.Fatalf("hook got %+v", got)
	}
	if w := doJSON(f.mux, http.MethodPost, "/api/approvals/"+id, `{"decision":"deny"}`, f.session); w.Code != http.StatusConflict {
		t.Fatalf("second answer = %d", w.Code)
	}
	if w := doJSON(f.mux, http.MethodPost, "/api/approvals/nope", `{"decision":"deny"}`, f.session); w.Code != 404 {
		t.Fatalf("unknown = %d", w.Code)
	}
	if w := doJSON(f.mux, http.MethodPost, "/api/approvals/a/b", `{"decision":"deny"}`, f.session); w.Code != 404 {
		t.Fatalf("nested = %d", w.Code)
	}
	if w := hookGet(f.mux, "/hook/approvals/nope?wait=0", f.token); w.Code != 404 {
		t.Fatalf("unknown poll = %d", w.Code)
	}

	// The audit log holds the ask and the answer.
	entries, _ := f.log.Recent(context.Background(), 0, 50)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	for _, a := range []string{audit.ApprovalBlocked, audit.ApprovalMode, audit.ApprovalAsk, audit.ApprovalAllow} {
		if !seen[a] {
			t.Fatalf("audit has no %s: %+v", a, entries)
		}
	}
}

func TestApprovalRulesAPI(t *testing.T) {
	f := newApprovalsFixture(t)
	w := doJSON(f.mux, http.MethodGet, "/api/approvals/settings", "", f.session)
	settings := decode[struct {
		Rules []approvals.Rule
		Modes []string
	}](t, w)
	if len(settings.Rules) != len(approvals.DefaultRules()) || len(settings.Modes) != 4 {
		t.Fatalf("settings = %s", w.Body)
	}
	rules := append(settings.Rules, approvals.Rule{ID: "tf", Name: "Terraform destroy", Pattern: `terraform\s+destroy`, Enabled: true})
	body, _ := json.Marshal(map[string]any{"rules": rules})
	if w := doJSON(f.mux, http.MethodPut, "/api/approvals/rules", string(body), f.session); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"tf"`) {
		t.Fatalf("put rules = %d %s", w.Code, w.Body)
	}
	if w := doJSON(f.mux, http.MethodPut, "/api/approvals/rules", `{"rules":[{"id":"x","name":"x","pattern":"("}]}`, f.session); w.Code != 400 {
		t.Fatalf("bad rule = %d", w.Code)
	}
	if w := doJSON(f.mux, http.MethodPut, "/api/approvals/rules", `[`, f.session); w.Code != 400 {
		t.Fatalf("bad body = %d", w.Code)
	}
	w = doJSON(f.mux, http.MethodPost, "/api/approvals/check", `{"command":"terraform destroy -auto-approve"}`, f.session)
	if !strings.Contains(w.Body.String(), `"critical":true`) || !strings.Contains(w.Body.String(), `"id":"tf"`) {
		t.Fatalf("check = %s", w.Body)
	}
	if w := doJSON(f.mux, http.MethodPost, "/api/approvals/check", `{"command":"ls"}`, f.session); strings.TrimSpace(w.Body.String()) != `{"critical":false}` {
		t.Fatalf("check plain = %s", w.Body)
	}
	if w := doJSON(f.mux, http.MethodPost, "/api/approvals/check", `x`, f.session); w.Code != 400 {
		t.Fatalf("check bad = %d", w.Code)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/approvals/settings"}, {http.MethodGet, "/api/approvals/modes"},
		{http.MethodGet, "/api/approvals/rules"}, {http.MethodGet, "/api/approvals/check"},
		{http.MethodGet, "/api/approvals/x"}, {http.MethodPost, "/api/approvals"},
	} {
		if w := doJSON(f.mux, c.method, c.path, "{}", f.session); w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s = %d", c.method, c.path, w.Code)
		}
	}
	if w := doJSON(f.mux, http.MethodGet, "/api/approvals/settings", "", ""); w.Code != 401 {
		t.Fatalf("settings without session = %d", w.Code)
	}
}

func TestHookAuthAndValidation(t *testing.T) {
	f := newApprovalsFixture(t)
	if w := doJSONBearer(f.mux, "/hook/approvals", `{"tool":"Bash"}`, "dvl_nope"); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("bad token = %d", w.Code)
	}
	// A session cookie alone never works on the hook side.
	if w := doJSON(f.mux, http.MethodPost, "/hook/approvals", `{"tool":"Bash"}`, f.session); w.Code != 401 {
		t.Fatalf("session on hook = %d", w.Code)
	}
	for _, bad := range []string{`{"tool":""}`, `{"tool":"Bash","runner":"other"}`, `nope`, `{"tool":"Bash","command":"\u001b[2J"}`} {
		if w := doJSONBearer(f.mux, "/hook/approvals", bad, f.token); w.Code != 400 {
			t.Fatalf("hook %s = %d %s", bad, w.Code, w.Body)
		}
	}
	if w := hookGet(f.mux, "/hook/approvals", f.token); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET collection = %d", w.Code)
	}
	if w := doJSONBearer(f.mux, "/hook/approvals/x", "", f.token); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST one = %d", w.Code)
	}
}

func TestPushAPI(t *testing.T) {
	f := newApprovalsFixture(t)
	w := doJSON(f.mux, http.MethodGet, "/api/push", "", f.session)
	info := decode[struct {
		PublicKey string
		Devices   []push.Device
	}](t, w)
	if len(info.PublicKey) < 80 || len(info.Devices) != 0 {
		t.Fatalf("push info = %s", w.Body)
	}

	got := make(chan int, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- len(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	sub := map[string]any{"endpoint": srv.URL + "/p/1", "label": "phone",
		"keys": map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}
	body, _ := json.Marshal(sub)
	w = doJSON(f.mux, http.MethodPost, "/api/push/devices", string(body), f.session)
	if w.Code != http.StatusCreated {
		t.Fatalf("subscribe = %d %s", w.Code, w.Body)
	}
	id := decode[struct{ ID string }](t, w).ID
	if w := doJSON(f.mux, http.MethodPost, "/api/push/devices", `{"endpoint":"ftp://x"}`, f.session); w.Code != 400 {
		t.Fatalf("bad subscribe = %d", w.Code)
	}
	w = doJSON(f.mux, http.MethodPost, "/api/push/test", "", f.session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":1`) {
		t.Fatalf("test push = %d %s", w.Code, w.Body)
	}
	if n := <-got; n < 100 {
		t.Fatalf("push body %d bytes", n)
	}

	// An approval reaches the device too.
	_ = doJSON(f.mux, http.MethodPut, "/api/approvals/modes", `{"target":"runner:laptop","mode":"all"}`, f.session)
	r := httptest.NewRequest(http.MethodPost, "/hook/approvals?wait=0", strings.NewReader(`{"tool":"Bash","command":"ls"}`))
	r.Header.Set("Authorization", "Bearer "+f.token)
	f.mux.ServeHTTP(httptest.NewRecorder(), r)
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no push for the approval")
	}

	if w := doJSON(f.mux, http.MethodDelete, "/api/push/devices/"+id, "", f.session); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", w.Code)
	}
	if w := doJSON(f.mux, http.MethodDelete, "/api/push/devices/"+id, "", f.session); w.Code != 404 {
		t.Fatalf("remove again = %d", w.Code)
	}
	if w := doJSON(f.mux, http.MethodGet, "/api/push/nope", "", f.session); w.Code != 404 {
		t.Fatalf("unknown = %d", w.Code)
	}
	if w := doJSON(f.mux, http.MethodGet, "/api/push/test", "", f.session); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET test = %d", w.Code)
	}
	entries, _ := f.log.Recent(context.Background(), 0, 50)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	if !seen[audit.PushSubscribe] || !seen[audit.PushRemove] {
		t.Fatalf("push audit = %+v", entries)
	}
}

func TestApprovalsNotConfigured(t *testing.T) {
	mux, password := fixture(t, "")
	session := sessionCookie(t, mux, password)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/approvals"}, {http.MethodGet, "/api/approvals/settings"}, {http.MethodGet, "/api/push"},
		{http.MethodPost, "/api/push/test"},
	} {
		if w := doJSON(mux, c.method, c.path, "", session); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d", c.method, c.path, w.Code)
		}
	}
	if w := doJSONBearer(mux, "/hook/approvals", "{}", "x"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("hook = %d", w.Code)
	}
}
