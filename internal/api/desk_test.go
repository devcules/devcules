package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/desk"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

// offline refuses every request: the desk tests never leave the box.
type offline struct{}

func (offline) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline in tests")
}

type deskFix struct {
	mux     http.Handler
	session string
}

func newDeskFix(t *testing.T) deskFix {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	key, err := vault.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(ctx, s, key)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: offline{}, Timeout: time.Second}
	d := desk.NewService(v, hc, hc)
	svc := auth.New(s)
	password, _, _ := svc.EnsureAdmin(ctx)
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Vault: v, Desk: d,
		DeskCalendar: desk.NewCalendarService(d, s, hc), AuditLog: audit.New(s, nil)})
	return deskFix{mux: mux, session: sessionCookie(t, mux, password)}
}

func (f deskFix) do(method, path, body string) *httptest.ResponseRecorder {
	return doJSON(f.mux, method, path, body, f.session)
}

func TestSecretsRoutes(t *testing.T) {
	t.Parallel()
	f := newDeskFix(t)
	for body, want := range map[string]int{
		`nope`:                       http.StatusBadRequest,
		`{"name":"","value":"x"}`:    http.StatusBadRequest,
		`{"name":"a/b","value":"x"}`: http.StatusBadRequest,
		`{"name":"TAVILY_KEY"}`:      http.StatusBadRequest,
	} {
		if w := f.do(http.MethodPut, "/api/secrets", body); w.Code != want {
			t.Errorf("PUT %s = %d, want %d", body, w.Code, want)
		}
	}
	if w := f.do(http.MethodPut, "/api/secrets", `{"name":"TAVILY_KEY","value":"tv-123"}`); w.Code != http.StatusNoContent {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	w := f.do(http.MethodGet, "/api/secrets", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "TAVILY_KEY") || strings.Contains(w.Body.String(), "tv-123") {
		t.Fatalf("list = %d %s: names only, never a value", w.Code, w.Body)
	}
	if w := f.do(http.MethodGet, "/api/secrets/TAVILY_KEY", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("read one = %d, want 405: values never leave", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/secrets/TAVILY_KEY", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	// Deleting again is a no-op, like every delete here.
	if w := f.do(http.MethodDelete, "/api/secrets/TAVILY_KEY", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete again = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/secrets/", ""); w.Code != http.StatusNotFound {
		t.Fatalf("no name = %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/api/secrets", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", w.Code)
	}
}

func TestDeskRoutes(t *testing.T) {
	t.Parallel()
	f := newDeskFix(t)
	if w := f.do(http.MethodGet, "/api/desk/prefs", ""); w.Code != http.StatusOK {
		t.Fatalf("prefs = %d", w.Code)
	}
	many := `{"version":1,"widgets":[` + strings.Repeat(`{},`, 64) + `{}]}`
	for body, want := range map[string]int{`nope`: 400, many: 400, `{"version":1,"widgets":[{"id":"clock"}]}`: 204} {
		if w := f.do(http.MethodPut, "/api/desk/prefs", body); w.Code != want {
			t.Errorf("PUT prefs = %d, want %d", w.Code, want)
		}
	}
	if w := f.do(http.MethodGet, "/api/desk/prefs", ""); !strings.Contains(w.Body.String(), "clock") {
		t.Fatalf("prefs not kept: %s", w.Body)
	}
	if w := f.do(http.MethodPost, "/api/desk/prefs", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST prefs = %d", w.Code)
	}

	// Sources: a token is write-only; blank keeps it, *_clear wipes it.
	if w := f.do(http.MethodPut, "/api/desk/sources", `nope`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad sources = %d", w.Code)
	}
	if w := f.do(http.MethodPut, "/api/desk/sources", `{"location":"Berlin","tavily_key":"tv-1"}`); w.Code != http.StatusNoContent {
		t.Fatalf("put sources = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPut, "/api/desk/sources", `{"location":"Paris"}`); w.Code != http.StatusNoContent {
		t.Fatalf("put sources again = %d", w.Code)
	}
	var src desk.Sources
	w := f.do(http.MethodGet, "/api/desk/sources", "")
	_ = json.Unmarshal(w.Body.Bytes(), &src)
	if src.Location != "Paris" || !src.HasTavily || strings.Contains(w.Body.String(), "tv-1") {
		t.Fatalf("sources = %s: want the key kept and never shown", w.Body)
	}
	_ = f.do(http.MethodPut, "/api/desk/sources", `{"location":"Paris","tavily_clear":true,"ticket_clear":true}`)
	w = f.do(http.MethodGet, "/api/desk/sources", "")
	if strings.Contains(w.Body.String(), "has_tavily") {
		t.Fatalf("cleared key still shown: %s", w.Body)
	}
	if w := f.do(http.MethodDelete, "/api/desk/sources", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE sources = %d", w.Code)
	}

	// Data tiles: unconfigured is 400 with a readable line, an
	// upstream failure 502; only GET.
	if w := f.do(http.MethodGet, "/api/desk/news/nope", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown feed = %d %s", w.Code, w.Body)
	}
	// No ticket provider: an empty, unconfigured answer the tile shows.
	if w := f.do(http.MethodGet, "/api/desk/tickets", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("no tickets = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodGet, "/api/desk/weather?refresh=1", ""); w.Code != http.StatusBadGateway && w.Code != http.StatusBadRequest {
		t.Fatalf("weather offline = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodGet, "/api/desk/meetings", ""); w.Code != http.StatusOK {
		t.Fatalf("meetings with no feeds = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPost, "/api/desk/weather", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST weather = %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/api/desk/news/tech", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST news = %d", w.Code)
	}
}

func TestCalendarRoutes(t *testing.T) {
	t.Parallel()
	f := newDeskFix(t)
	for path, want := range map[string]int{
		"/api/calendar/events": 200,
		"/api/calendar/events?from=2026-11-01T00:00:00Z&to=2026-11-08T00:00:00Z": 200,
		"/api/calendar/events?from=nope":                                          400,
		"/api/calendar/events?to=nope":                                            400,
		"/api/calendar/events?from=2026-11-08T00:00:00Z&to=2026-11-01T00:00:00Z": 400,
	} {
		if w := f.do(http.MethodGet, path, ""); w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	for body, want := range map[string]int{
		`nope`: 400,
		`{"title":"x","start":"monday"}`: 400,
		`{"title":"x","start":"2026-11-03T10:00:00Z","end":"tuesday"}`:              400,
		`{"title":"x","start":"2026-11-03T10:00:00Z","end":"2026-11-03T09:00:00Z"}`: 400,
	} {
		if w := f.do(http.MethodPost, "/api/calendar/events", body); w.Code != want {
			t.Errorf("POST %s = %d, want %d", body, w.Code, want)
		}
	}
	w := f.do(http.MethodPost, "/api/calendar/events", `{"title":"standup","start":"2026-11-03T10:00:00Z","end":"2026-11-03T10:30:00Z","location":" room 1 "}`)
	var ev struct {
		ID, Title string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ev)
	if w.Code != http.StatusCreated || ev.ID == "" || ev.Title != "standup" {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPost, "/api/calendar/events", `{"title":"offsite","all_day":true,"start":"2026-11-05T13:00:00Z","end":"2026-11-06T13:00:00Z"}`); w.Code != http.StatusCreated {
		t.Fatalf("all-day = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPatch, "/api/calendar/events/"+ev.ID, `{"title":"stand-up"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "stand-up") {
		t.Fatalf("patch = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPatch, "/api/calendar/events/nope", `{"title":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("patch missing = %d", w.Code)
	}
	if w := f.do(http.MethodPatch, "/api/calendar/events/"+ev.ID, `nope`); w.Code != http.StatusBadRequest {
		t.Fatalf("patch bad = %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/api/calendar.ics", ""); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/calendar") || !strings.Contains(w.Body.String(), "stand-up") {
		t.Fatalf("ics = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := f.do(http.MethodPost, "/api/calendar.ics", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST ics = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/calendar/events/"+ev.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/calendar/events/"+ev.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete again = %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/api/calendar/events/"+ev.ID, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET one = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/calendar/events/", ""); w.Code != http.StatusNotFound {
		t.Fatalf("no id = %d", w.Code)
	}
	if w := f.do(http.MethodPut, "/api/calendar/events", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT list = %d", w.Code)
	}
	// Quick add: typed text becomes a plan.
	if w := f.do(http.MethodPost, "/api/calendar/quick", `{"text":"lunch tomorrow 12:00 1h"}`); w.Code != http.StatusCreated {
		t.Fatalf("quick = %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"text":"  "}`, `nope`} {
		if w := f.do(http.MethodPost, "/api/calendar/quick", body); w.Code != http.StatusBadRequest {
			t.Errorf("quick %s = %d", body, w.Code)
		}
	}
	if w := f.do(http.MethodGet, "/api/calendar/quick", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET quick = %d", w.Code)
	}
}
