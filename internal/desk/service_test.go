package desk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

// fakeNet answers every outbound request in-process, by host, so the
// service paths run against the real provider URLs without a network.
type fakeNet struct {
	mu    sync.Mutex
	hosts map[string]http.HandlerFunc
	seen  []string
}

func (f *fakeNet) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	h, ok := f.hosts[r.URL.Host]
	f.seen = append(f.seen, r.URL.Host+r.URL.Path)
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("no route to " + r.URL.Host)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	res := rec.Result()
	res.Request = r // as a real transport does
	return res, nil
}

func (f *fakeNet) set(host string, h http.HandlerFunc) {
	f.mu.Lock()
	f.hosts[host] = h
	f.mu.Unlock()
}

type deskFix struct {
	st  store.Store
	v   *vault.Vault
	net *fakeNet
	hc  *http.Client
	svc *Service
	cal *CalendarService
}

func newFix(t *testing.T) deskFix {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "desk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := vault.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(ctx, st, key)
	if err != nil {
		t.Fatal(err)
	}
	fn := &fakeNet{hosts: map[string]http.HandlerFunc{}}
	hc := &http.Client{Transport: fn}
	svc := NewService(v, hc, hc)
	return deskFix{st: st, v: v, net: fn, hc: hc, svc: svc, cal: NewCalendarService(svc, st, hc)}
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

func TestSourcesRoundTripAndMask(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	got, err := f.svc.ReadSources(ctx)
	if err != nil || got.Location != "" {
		t.Fatalf("empty sources = %+v, %v", got, err)
	}
	src := Sources{Location: "Amsterdam", TavilyKey: "tv-" + "one", TicketToken: "   "}
	if err := f.svc.WriteSources(ctx, src); err != nil {
		t.Fatal(err)
	}
	// A fresh service has no memo and reads the sealed document back.
	fresh := NewService(f.v, f.hc, f.hc)
	back, err := fresh.ReadSources(ctx)
	if err != nil || back.Location != "Amsterdam" || back.TavilyKey != src.TavilyKey {
		t.Fatalf("read back = %+v, %v", back, err)
	}
	m := back.Masked()
	if !m.HasTavily || m.HasTicketTok || m.TavilyKey != "" || m.TicketToken != "" {
		t.Fatalf("masked = %+v", m)
	}
}

func TestUnreadableDocuments(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	for _, k := range []string{sourcesKey, prefsKey} {
		if err := f.v.Put(ctx, k, []byte("{not json")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.ReadSources(ctx); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("sources err = %v", err)
	}
	if _, err := f.svc.ReadPrefs(ctx); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("prefs err = %v", err)
	}
	// Every read path that needs sources passes the error on.
	if _, err := f.svc.Weather(ctx, false); err == nil {
		t.Error("Weather ignored unreadable sources")
	}
	if _, err := f.svc.News(ctx, "tech", false); err == nil {
		t.Error("News ignored unreadable sources")
	}
	if _, err := f.svc.Tickets(ctx, false); err == nil {
		t.Error("Tickets ignored unreadable sources")
	}
	if _, err := f.svc.Meetings(ctx, false); err == nil {
		t.Error("Meetings ignored unreadable sources")
	}
}

func TestPrefs(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	p, err := f.svc.ReadPrefs(ctx)
	if err != nil || p.Version != 2 {
		t.Fatalf("default prefs = %+v, %v", p, err)
	}
	in := Prefs{Version: 1, Widgets: []json.RawMessage{json.RawMessage(`{"id":"clock"}`)}}
	if err := f.svc.WritePrefs(ctx, in); err != nil {
		t.Fatal(err)
	}
	p, err = f.svc.ReadPrefs(ctx) // memo
	if err != nil || p.Version != 2 || len(p.Widgets) != 1 {
		t.Fatalf("memo prefs = %+v, %v", p, err)
	}
	// A stored version 0 reads as the current version.
	if err := f.v.Put(ctx, prefsKey, []byte(`{"widgets":[]}`)); err != nil {
		t.Fatal(err)
	}
	fresh := NewService(f.v, f.hc, f.hc)
	p, err = fresh.ReadPrefs(ctx)
	if err != nil || p.Version != 2 {
		t.Fatalf("v0 prefs = %+v, %v", p, err)
	}
}

func TestWeatherConfiguredAndStale(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	raw, err := f.svc.Weather(ctx, false)
	if err != nil || decode(t, raw)["configured"] != false {
		t.Fatalf("unconfigured weather = %s, %v", raw, err)
	}
	down := false
	f.net.set("geocoding-api.open-meteo.com", func(w http.ResponseWriter, _ *http.Request) {
		if down {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"name":"Amsterdam","latitude":52.37,"longitude":4.89,"timezone":"Europe/Amsterdam"}]}`)
	})
	f.net.set("api.open-meteo.com", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"current":{"temperature_2m":9,"weather_code":3},"daily":{"time":["2026-10-31"],"temperature_2m_max":[11],"temperature_2m_min":[5],"weather_code":[3],"precipitation_probability_max":[20]}}`)
	})
	if err := f.svc.WriteSources(ctx, Sources{Location: "Amsterdam"}); err != nil {
		t.Fatal(err)
	}
	raw, err = f.svc.Weather(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, raw)
	if m["configured"] != true || m["location"] != "Amsterdam" || m["stale"] != nil {
		t.Fatalf("weather = %s", raw)
	}
	// The provider goes down: a forced refresh serves the old answer, marked stale.
	down = true
	raw, err = f.svc.Weather(ctx, true)
	if err != nil || decode(t, raw)["stale"] != true {
		t.Fatalf("stale weather = %s, %v", raw, err)
	}
	// With nothing cached, a failure is an error.
	if err := f.svc.WriteSources(ctx, Sources{Location: "Utrecht"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Weather(ctx, false); err == nil {
		t.Fatal("weather with provider down and no cache should fail")
	}
}

func TestNewsSources(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	if _, err := f.svc.News(ctx, "sports", false); err == nil {
		t.Fatal("unknown feed accepted")
	}
	f.net.set("news.google.com", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "q=zero+day") {
			t.Errorf("rss query = %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `<rss><channel><item><title>A</title><link>https://a.example/1</link><source>A News</source><pubDate>Mon, 02 Nov 2026 10:00:00 +0000</pubDate></item></channel></rss>`)
	})
	tavilyUp := true
	f.net.set("api.tavily.com", func(w http.ResponseWriter, _ *http.Request) {
		if !tavilyUp {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"title":"T","url":"https://t.example/x","content":"c","score":0.9}]}`)
	})

	// No key: public RSS.
	if err := f.svc.WriteSources(ctx, Sources{NewsSecurity: "zero day"}); err != nil {
		t.Fatal(err)
	}
	raw, err := f.svc.News(ctx, "security", false)
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, raw)
	if m["tavily"] != false || m["configured"] != true || len(m["items"].([]any)) != 1 {
		t.Fatalf("rss news = %s", raw)
	}

	// A key: Tavily.
	if err := f.svc.WriteSources(ctx, Sources{NewsTech: "go", TavilyKey: "tv-" + "two"}); err != nil {
		t.Fatal(err)
	}
	raw, err = f.svc.News(ctx, "tech", false)
	if err != nil || decode(t, raw)["tavily"] != true {
		t.Fatalf("tavily news = %s, %v", raw, err)
	}

	// A key that stopped working falls back to RSS.
	tavilyUp = false
	if err := f.svc.WriteSources(ctx, Sources{NewsCustom: "zero day", TavilyKey: "tv-" + "old"}); err != nil {
		t.Fatal(err)
	}
	raw, err = f.svc.News(ctx, "custom", false)
	if err != nil || decode(t, raw)["tavily"] != false {
		t.Fatalf("fallback news = %s, %v", raw, err)
	}

	// RSS down with nothing cached is an error.
	f.net.set("news.google.com", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	if err := f.svc.WriteSources(ctx, Sources{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.News(ctx, "tech", false); err == nil {
		t.Fatal("rss down should fail")
	}
}

func TestRSSNewsDefaultsAndBadXML(t *testing.T) {
	fn := &fakeNet{hosts: map[string]http.HandlerFunc{}}
	hc := &http.Client{Transport: fn}
	fn.set("news.google.com", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "q=technology") {
			t.Errorf("default query = %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `<rss><channel><item><title>1</title></item><item><title>2</title></item></channel></rss>`)
	})
	items, err := RSSNews(context.Background(), hc, "  ", 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	fn.set("news.google.com", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<rss><channel>") })
	if _, err := RSSNews(context.Background(), hc, "x", 5); err == nil {
		t.Fatal("broken xml accepted")
	}
	if _, err := RSSNews(context.Background(), &http.Client{Transport: &fakeNet{hosts: map[string]http.HandlerFunc{}}}, "x", 5); err == nil {
		t.Fatal("unreachable host accepted")
	}
}

func TestTicketProviders(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	raw, err := f.svc.Tickets(ctx, false)
	if err != nil || decode(t, raw)["configured"] != false {
		t.Fatalf("unconfigured tickets = %s, %v", raw, err)
	}

	f.net.set("acme.atlassian.net", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") || !strings.Contains(r.URL.RawQuery, "currentUser") {
			t.Errorf("jira request auth=%q query=%s", r.Header.Get("Authorization"), r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"issues":[{"key":"DC-1","fields":{"summary":"Fix it","status":{"name":"To Do"},"priority":{"Name":"High"},"updated":"2026-11-01"}},{"key":"DC-2","fields":{"summary":"Later","status":{"name":"Done"}}}]}`)
	})
	if err := f.svc.WriteSources(ctx, Sources{TicketProvider: "jira", TicketSite: "https://acme.atlassian.net/", TicketEmail: "k@example.com", TicketToken: "jt"}); err != nil {
		t.Fatal(err)
	}
	raw, err = f.svc.Tickets(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Provider string   `json:"provider"`
		Items    []Ticket `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "jira" || len(got.Items) != 2 || got.Items[0].Priority != "High" || got.Items[0].URL != "https://acme.atlassian.net/browse/DC-1" {
		t.Fatalf("jira tickets = %s", raw)
	}

	f.net.set("app.asana.com", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" || r.URL.Query().Get("workspace") != "123" {
			t.Errorf("asana request = %v %s", r.Header, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"data":[{"gid":"1","name":"Open","permalink_url":"https://app.asana.com/0/1","memberships":[{"section":{"name":"Doing"}}]},{"gid":"2","name":"Gone","completed":true}]}`)
	})
	ts, err := Tickets(ctx, f.hc, TicketQuery{Provider: "Asana", Token: "at", Project: "123"})
	if err != nil || len(ts) != 1 || ts[0].Status != "Doing" {
		t.Fatalf("asana = %+v, %v", ts, err)
	}

	linear := `{"data":{"viewer":{"assignedIssues":{"nodes":[{"identifier":"ENG-7","title":"Ship","url":"https://linear.app/x/ENG-7","state":{"name":"In Progress"},"priorityLabel":"Urgent"}]}}}}`
	f.net.set("api.linear.app", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "lt" {
			t.Errorf("linear request = %s %q", r.Method, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, linear)
	})
	ts, err = Tickets(ctx, f.hc, TicketQuery{Provider: "linear", Token: "lt"})
	if err != nil || len(ts) != 1 || ts[0].Key != "ENG-7" || ts[0].Priority != "Urgent" {
		t.Fatalf("linear = %+v, %v", ts, err)
	}
	linear = `{"errors":[{"message":"bad token"}]}`
	if _, err := Tickets(ctx, f.hc, TicketQuery{Provider: "linear", Token: "lt"}); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("linear error = %v", err)
	}
	linear = `not json`
	if _, err := Tickets(ctx, f.hc, TicketQuery{Provider: "linear", Token: "lt"}); err == nil {
		t.Fatal("linear junk accepted")
	}
	f.net.set("api.linear.app", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusForbidden) })
	if _, err := Tickets(ctx, f.hc, TicketQuery{Provider: "linear", Token: "lt"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("linear 403 = %v", err)
	}
	f.net.set("app.asana.com", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusUnauthorized) })
	if _, err := Tickets(ctx, f.hc, TicketQuery{Provider: "asana", Token: "at"}); err == nil {
		t.Fatal("asana 401 accepted")
	}
	f.net.set("acme.atlassian.net", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusUnauthorized) })
	if _, err := Tickets(ctx, f.hc, TicketQuery{Site: "https://acme.atlassian.net", Token: "jt", JQL: "project = DC"}); err == nil {
		t.Fatal("jira 401 accepted")
	}
	// Linear unreachable.
	if _, err := Tickets(ctx, &http.Client{Transport: &fakeNet{hosts: map[string]http.HandlerFunc{}}}, TicketQuery{Provider: "linear", Token: "lt"}); err == nil {
		t.Fatal("unreachable linear accepted")
	}

	for _, q := range []TicketQuery{{Provider: "jira"}, {Provider: "asana"}, {Provider: "linear"}} {
		if _, err := Tickets(ctx, f.hc, q); !errors.Is(err, ErrNoTicketSource) {
			t.Errorf("%s without token = %v", q.Provider, err)
		}
	}
	if _, err := Tickets(ctx, f.hc, TicketQuery{Provider: "trello"}); err == nil {
		t.Fatal("unknown provider accepted")
	}

	// The service passes an upstream failure on when nothing is cached.
	if err := f.svc.WriteSources(ctx, Sources{TicketProvider: "asana", TicketToken: "at"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Tickets(ctx, false); err == nil {
		t.Fatal("service tickets ignored the failure")
	}
}

const feedICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:a@test
DTSTART:20261105T090000Z
DTEND:20261105T100000Z
SUMMARY:Review
END:VEVENT
BEGIN:VEVENT
UID:b@test
DTSTART:20261104T090000Z
DTEND:20261104T093000Z
SUMMARY:Standup
END:VEVENT
END:VCALENDAR
`

func TestFeedMeetingsMergeSkipAndFail(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	raw, err := f.svc.Meetings(ctx, false)
	if err != nil || decode(t, raw)["configured"] != false {
		t.Fatalf("unconfigured meetings = %s, %v", raw, err)
	}
	f.net.set("cal.example.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad.ics" {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, feedICS)
	})
	if err := f.svc.WriteSources(ctx, Sources{ICSFeeds: []ICSFeed{
		{Label: "Broken", URL: "https://cal.example.com/bad.ics"},
		{Label: "Work", URL: "https://cal.example.com/work.ics"},
	}}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(14 * 24 * time.Hour)
	raw, err = f.svc.FeedMeetings(ctx, from, to, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items []Meeting `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Standup" || got.Items[0].Feed != "Work" {
		t.Fatalf("meetings = %s", raw)
	}
	// Every feed failing is an error, and changing the feeds drops the
	// cached window: the old feed's meetings must not linger.
	if err := f.svc.WriteSources(ctx, Sources{ICSFeeds: []ICSFeed{{Label: "Broken", URL: "https://cal.example.com/bad.ics"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.FeedMeetings(ctx, from, to, false, 0); err == nil {
		t.Fatal("all feeds failing should fail")
	}
}

func TestFetchICSRaw(t *testing.T) {
	fn := &fakeNet{hosts: map[string]http.HandlerFunc{}}
	hc := &http.Client{Transport: fn}
	fn.set("cal.example.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad.ics" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, feedICS)
	})
	ctx := context.Background()
	s, err := FetchICSRaw(ctx, hc, " https://cal.example.com/work.ics ")
	if err != nil || !strings.Contains(s, "UID:a@test") {
		t.Fatalf("raw = %q, %v", s, err)
	}
	if _, err := FetchICSRaw(ctx, hc, "https://cal.example.com/bad.ics"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403 = %v", err)
	}
	_, err = FetchICSRaw(ctx, hc, "https://other.example.com/secret-token.ics")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unreachable error leaks the address or is nil: %v", err)
	}
	if _, err := FetchICSRaw(ctx, hc, "://bad"); err == nil {
		t.Fatal("bad url accepted")
	}
}

func TestWithStaleKeepsOddValues(t *testing.T) {
	if withStale(nil, true) != nil {
		t.Error("nil should stay nil")
	}
	if got := string(withStale(json.RawMessage(`[1]`), true)); got != `[1]` {
		t.Errorf("non-object = %s", got)
	}
}

func TestCacheForget(t *testing.T) {
	c := NewCache[int](time.Hour, time.Minute)
	n := 0
	fetch := func(context.Context) (int, error) { n++; return n, nil }
	ctx := context.Background()
	c.Get(ctx, "k", false, fetch)
	c.Get(ctx, "k", false, fetch)
	c.Forget("k")
	if e := c.Get(ctx, "k", false, fetch); e.Value != 2 {
		t.Fatalf("after Forget value = %d, want a refetch", e.Value)
	}
	c.Get(ctx, "j", false, fetch)
	c.Clear()
	if e := c.Get(ctx, "k", false, fetch); e.Value != 4 {
		t.Fatalf("after Clear value = %d, want a refetch", e.Value)
	}
}

func TestNewHardenedClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	hc := NewHardenedClient(2 * time.Second)
	if _, err := hc.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("loopback = %v", err)
	}
}
