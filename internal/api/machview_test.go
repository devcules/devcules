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
	"time"

	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/machines"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
	"github.com/devcules/devcules/internal/store/sqlite"
)

const machviewTestToken = "machview-test-token-0123456789a"

// testMachineKeys enrols the fake machines with the test hubs.
var testMachineKeys = linktest.NewMachineKeys()

// machviewFixture builds the full mux (store, auth, hub, machine
// relay) on a pipe server, with one fake machine linked from the
// relay package's canned table. The fake lives in the relay tests;
// here a smaller stand-in answers over the same linktest pipe.
func machviewFixture(t *testing.T, linked bool) (http.Handler, string) {
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
	h := hub.New(hub.HashToken(machviewTestToken), hub.Options{
		HelloTimeout: 5 * time.Second,
		PingInterval: time.Second,
		PingTimeout:  5 * time.Second,
		Machines:     testMachineKeys,
	})
	r := machrelay.New(h)
	r.Run()
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Link: h, Enrol: machines.NewIssuer(s), Machines: r})
	if linked {
		pipe := linktest.Serve(t, mux)
		linkFakeMachine(t, pipe, "kitchen-mac")
		// The dial registers asynchronously; wait for the hub's view
		// so a request never races the link into "no machine linked".
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if len(h.Machines()) == 1 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return mux, sessionCookie(t, mux, password)
}

// machviewRequest issues one REST call with the session cookie.
func machviewRequest(t *testing.T, mux http.Handler, method, target, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestMachviewRoutesNeedSession(t *testing.T) {
	t.Parallel()
	mux, _ := machviewFixture(t, false)
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/api/machines"},
		{http.MethodGet, "/api/machines-budget"},
		{http.MethodGet, "/api/machines-screen"},
		{http.MethodGet, "/api/machines-tree"},
		{http.MethodPost, "/api/machines-input"},
		{http.MethodPost, "/api/machines-ground"},
		{http.MethodPost, "/api/machines-verify"},
	} {
		r := httptest.NewRequest(tc.method, tc.target, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.target, w.Code)
		}
	}
}

func TestMachviewNilRelayIs503(t *testing.T) {
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
	session := sessionCookie(t, mux, password)
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/api/machines"},
		{http.MethodGet, "/api/machines-budget"},
		{http.MethodGet, "/api/machines-screen"},
		{http.MethodGet, "/api/machines-tree"},
		{http.MethodPost, "/api/machines-input"},
		{http.MethodPost, "/api/machines-ground"},
		{http.MethodPost, "/api/machines-verify"},
	} {
		w := machviewRequest(t, mux, tc.method, tc.target, session, "")
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", tc.method, tc.target, w.Code)
		}
	}
}

func TestMachviewNoMachineIs503(t *testing.T) {
	t.Parallel()
	mux, session := machviewFixture(t, false)
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/machines-screen", ""},
		{http.MethodGet, "/api/machines-tree", ""},
		{http.MethodPost, "/api/machines-input", `{"kind":"tap"}`},
		{http.MethodPost, "/api/machines-ground", `{"phrase":"Pay"}`},
		{http.MethodPost, "/api/machines-verify", ""},
	} {
		w := machviewRequest(t, mux, tc.method, tc.target, session, tc.body)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503 (body %q)", tc.method, tc.target, w.Code, w.Body.String())
		}
	}
}

func TestMachviewHappyPath(t *testing.T) {
	t.Parallel()
	mux, session := machviewFixture(t, true)

	// The list names the machine.
	w := machviewRequest(t, mux, http.MethodGet, "/api/machines", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d (body %q)", w.Code, w.Body.String())
	}
	var list struct {
		Machines []struct {
			Name   string `json:"name"`
			Online bool   `json:"online"`
		} `json:"machines"`
		Budget int `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("list body: %q", w.Body.String())
	}
	if len(list.Machines) != 1 || list.Machines[0].Name != "kitchen-mac" || !list.Machines[0].Online || list.Budget != 40 {
		t.Fatalf("list = %+v", list)
	}

	// One PNG screen.
	w = machviewRequest(t, mux, http.MethodGet, "/api/machines-screen", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("screen = %d (body %q)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("screen content-type = %q", got)
	}
	// A frame that does not name a scale is full size.
	if got := w.Header().Get("X-Devcules-Screen-Scale"); got != "1" {
		t.Fatalf("screen scale = %q, want 1", got)
	}
	if len(w.Body.Bytes()) == 0 {
		t.Fatal("screen body is empty")
	}

	// The tree.
	w = machviewRequest(t, mux, http.MethodGet, "/api/machines-tree", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("tree = %d (body %q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"Pay"`) {
		t.Fatalf("tree body = %q", w.Body.String())
	}

	// One input event (take control): 204 and the budget mirror drops.
	w = machviewRequest(t, mux, http.MethodPost, "/api/machines-input", session, `{"kind":"tap","x":6,"y":7}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("input = %d (body %q)", w.Code, w.Body.String())
	}
	w = machviewRequest(t, mux, http.MethodGet, "/api/machines-budget", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("budget = %d", w.Code)
	}
	var budget struct {
		Remaining int `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &budget); err != nil || budget.Remaining != 39 {
		t.Fatalf("budget body = %q (remaining %d)", w.Body.String(), budget.Remaining)
	}

	// Grounding answers its matches.
	w = machviewRequest(t, mux, http.MethodPost, "/api/machines-ground", session, `{"phrase":"green Pay button"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("ground = %d (body %q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"clickX":6`) {
		t.Fatalf("ground body = %q", w.Body.String())
	}

	// The done-verifier: png base64, tree and note together.
	w = machviewRequest(t, mux, http.MethodPost, "/api/machines-verify", session, "")
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d (body %q)", w.Code, w.Body.String())
	}
	var verify struct {
		PNG  string `json:"png"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &verify); err != nil {
		t.Fatalf("verify body: %q", w.Body.String())
	}
	if _, err := base64.StdEncoding.DecodeString(verify.PNG); err != nil {
		t.Fatalf("verify png is not base64: %s", err)
	}
	if verify.Note == "" {
		t.Fatal("verify note is empty")
	}
}

func TestMachviewBadInputIs400(t *testing.T) {
	t.Parallel()
	mux, session := machviewFixture(t, true)
	for _, body := range []string{`{}`, `{"x":1}`, `bad json`} {
		w := machviewRequest(t, mux, http.MethodPost, "/api/machines-input", session, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("input %q = %d, want 400", body, w.Code)
		}
	}
	w := machviewRequest(t, mux, http.MethodPost, "/api/machines-ground", session, `{"phrase":""}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("ground empty phrase = %d, want 400", w.Code)
	}
}
