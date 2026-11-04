package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
)

type echoAssistant struct{}

func (echoAssistant) ID() string { return "echo" }
func (echoAssistant) Chat(_ context.Context, sess string, in string) (string, []assistants.Event, error) {
	if sess == "" {
		sess = "fedcba9876543210"
	}
	return sess, []assistants.Event{{Type: assistants.EventMessage, Text: "you said " + in}}, nil
}
func (echoAssistant) Session(sess string) (assistants.Session, error) {
	if sess != "0123456789abcdef" {
		return assistants.Session{}, store.ErrNotFound
	}
	return assistants.Session{ID: sess, Assistant: "echo"}, nil
}

// TestAssistantRoutesThroughMux drives the chat and conversation routes
// through the real mux: the prefix pattern carries no {id} wildcard, so
// the handler must read the id off the path itself.
func TestAssistantRoutesThroughMux(t *testing.T) {
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
	reg, err := assistants.NewRegistry(echoAssistant{})
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Assistants: reg})
	cookie := sessionCookie(t, mux, password)

	do := func(method, target, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = withSession(r, cookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := do(http.MethodPost, "/api/assistants/echo/chat", `{"message":"hi"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "you said hi") {
		t.Fatalf("chat = %d %s", w.Code, w.Body)
	}
	if w := do(http.MethodGet, "/api/assistants/echo/conversations/0123456789abcdef", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"assistant":"echo"`) {
		t.Fatalf("conversation = %d %s", w.Code, w.Body)
	}
	if w := do(http.MethodGet, "/api/assistants/echo/conversations/ffffffffffffffff", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown conversation = %d", w.Code)
	}
	if w := do(http.MethodPost, "/api/assistants/nope/chat", `{"message":"hi"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown assistant = %d", w.Code)
	}
	if w := do(http.MethodGet, "/api/assistants/echo/chat/extra", ""); w.Code != http.StatusNotFound {
		t.Fatalf("deep path = %d", w.Code)
	}
}
