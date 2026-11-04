package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/desk"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/vault"
)

// Calendar route regressions for GOSEC-07 (range span, rrule checks),
// GOSEC-10, GOSEC-11 (audit actions), GOSEC-12 (PATCH merges) and
// GOSEC-19 (no raw store errors). Helpers are prefixed b8.

type b8CalFix struct {
	deskFix
	st store.Store
}

func b8NewCalFix(t *testing.T) b8CalFix {
	t.Helper()
	return b8NewCalFixIn(t, nil)
}

// b8NewCalFixIn is b8NewCalFix with the calendar's user zone set to loc.
func b8NewCalFixIn(t *testing.T, loc *time.Location) b8CalFix {
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
	cal := desk.NewCalendarService(d, s, hc)
	cal.Loc = loc
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Vault: v, Desk: d,
		DeskCalendar: cal, AuditLog: audit.New(s, nil)})
	return b8CalFix{deskFix: deskFix{mux: mux, session: sessionCookie(t, mux, password)}, st: s}
}

// b8Actions answers the audit actions recorded so far, oldest first.
func b8Actions(t *testing.T, st store.Store) []store.AuditEntry {
	t.Helper()
	list, err := st.Audit().After(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func b8Create(t *testing.T, f b8CalFix, body string) map[string]any {
	t.Helper()
	w := f.do(http.MethodPost, "/api/calendar/events", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %s = %d %s", body, w.Code, w.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// GOSEC-12: a PATCH changes only the fields it sends.
func TestCalendarPatchKeepsUnsentFields(t *testing.T) {
	t.Parallel()
	f := b8NewCalFix(t)
	ev := b8Create(t, f, `{"title":"1:1","start":"2026-11-03T10:00:00Z","end":"2026-11-03T10:30:00Z","location":"room 1","url":"https://meet.example/x","notes":"agenda","reminder_min":10,"rrule":"FREQ=WEEKLY"}`)
	id := ev["id"].(string)
	w := f.do(http.MethodPatch, "/api/calendar/events/"+id, `{"title":"x"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", w.Code, w.Body)
	}
	var got store.CalendarEvent
	got = store.CalendarEvent{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Title != "x" || got.Notes != "agenda" || got.RRule != "FREQ=WEEKLY" || got.Location != "room 1" ||
		got.URL != "https://meet.example/x" || got.ReminderMin != 10 || got.AllDay ||
		!got.Start.Equal(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("after PATCH title = %+v", got)
	}
	// A drag sends start and end only: the series and notes stay.
	w = f.do(http.MethodPatch, "/api/calendar/events/"+id, `{"start":"2026-11-03T11:00:00Z","end":"2026-11-03T11:30:00Z"}`)
	got = store.CalendarEvent{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got.RRule != "FREQ=WEEKLY" || got.Notes != "agenda" || got.Title != "x" {
		t.Fatalf("after PATCH times = %d %+v", w.Code, got)
	}
	// A field sent empty is cleared: "rrule":"" ends the series.
	w = f.do(http.MethodPatch, "/api/calendar/events/"+id, `{"rrule":"","notes":""}`)
	got = store.CalendarEvent{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got.RRule != "" || got.Notes != "" || got.Location != "room 1" {
		t.Fatalf("after PATCH clear = %d %+v", w.Code, got)
	}
	// PATCH refusals.
	for body, want := range map[string]int{
		`{"start":"monday"}`:                     http.StatusBadRequest,
		`{"end":"2026-11-03T09:00:00Z"}`:         http.StatusBadRequest,
		`{"title":"  "}`:                         http.StatusBadRequest,
		`{"rrule":"FREQ=SECONDLY"}`:              http.StatusBadRequest,
		`{"rrule":"FREQ=DAILY\r\nSUMMARY:evil"}`: http.StatusBadRequest,
	} {
		if w := f.do(http.MethodPatch, "/api/calendar/events/"+id, body); w.Code != want {
			t.Errorf("PATCH %s = %d, want %d (%s)", body, w.Code, want, w.Body)
		}
	}
	// PUT replaces the plan: unsent optional fields go.
	w = f.do(http.MethodPut, "/api/calendar/events/"+id, `{"title":"y"}`)
	got = store.CalendarEvent{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got.Title != "y" || got.Location != "" || !got.Start.Equal(time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("after PUT = %d %+v", w.Code, got)
	}
}

// GOSEC-11: plan writes are audited as calendar actions, never as
// secret.put / secret.delete.
func TestCalendarWritesAuditCalendarActions(t *testing.T) {
	t.Parallel()
	f := b8NewCalFix(t)
	if w := f.do(http.MethodPost, "/api/calendar/quick", `{"text":"dentist tomorrow 9:00"}`); w.Code != http.StatusCreated {
		t.Fatalf("quick = %d %s", w.Code, w.Body)
	}
	ev := b8Create(t, f, `{"title":"standup","start":"2026-11-03T10:00:00Z","end":"2026-11-03T10:30:00Z"}`)
	id := ev["id"].(string)
	if w := f.do(http.MethodPatch, "/api/calendar/events/"+id, `{"title":"stand-up"}`); w.Code != http.StatusOK {
		t.Fatalf("patch = %d", w.Code)
	}
	if w := f.do(http.MethodDelete, "/api/calendar/events/"+id, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	var got []string
	for _, e := range b8Actions(t, f.st) {
		if strings.HasPrefix(e.Action, "secret.") {
			t.Errorf("calendar write audited as %s (target %q)", e.Action, e.Target)
		}
		if strings.HasPrefix(e.Action, "calendar.") {
			got = append(got, e.Action+" "+e.Target+" "+e.Detail["title"])
		}
	}
	want := []string{
		"calendar.add " + "ev_", // quick add: id varies, checked by prefix below
		"calendar.add " + id + " standup",
		"calendar.edit " + id + " stand-up",
		"calendar.remove " + id + " ",
	}
	if len(got) != 4 || !strings.HasPrefix(got[0], want[0]) || !strings.HasSuffix(got[0], " dentist") || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("calendar audit rows = %q", got)
	}
}

// GOSEC-07: a read over more than 400 days is refused; a create with a
// sub-daily or injected rule is refused.
func TestCalendarRangeAndRuleRefusals(t *testing.T) {
	t.Parallel()
	f := b8NewCalFix(t)
	for path, want := range map[string]int{
		"/api/calendar/events?from=2026-01-01T00:00:00Z&to=2027-02-05T00:00:00Z": http.StatusOK, // 400 days
		"/api/calendar/events?from=2026-01-01T00:00:00Z&to=2027-02-06T00:00:00Z": http.StatusBadRequest,
		"/api/calendar/events?from=0001-01-01T00:00:00Z&to=9999-12-31T00:00:00Z": http.StatusBadRequest,
		"/api/calendar/events?to=2099-01-01T00:00:00Z":                           http.StatusBadRequest,
		"/api/calendar/events?from=9998-11-01T00:00:00Z&to=9999-12-05T00:00:00Z": http.StatusBadRequest, // far window
		"/api/calendar/events?from=1899-12-01T00:00:00Z&to=1900-01-05T00:00:00Z": http.StatusBadRequest,
		"/api/calendar/events?from=2200-12-01T00:00:00Z&to=2201-01-01T00:00:00Z": http.StatusOK,
		"/api/calendar/events?from=2200-12-01T00:00:00Z&to=2201-01-02T00:00:00Z": http.StatusBadRequest,
	} {
		if w := f.do(http.MethodGet, path, ""); w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
	for _, rr := range []string{"FREQ=SECONDLY", "FREQ=MINUTELY", "FREQ=HOURLY", `FREQ=DAILY\r\nEND:VEVENT\r\nBEGIN:VEVENT\r\nSUMMARY:injected`, "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30"} {
		body := `{"title":"x","start":"2026-11-03T10:00:00Z","end":"2026-11-03T10:30:00Z","rrule":"` + rr + `"}`
		w := f.do(http.MethodPost, "/api/calendar/events", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "rrule") {
			t.Errorf("POST rrule %q = %d %s", rr, w.Code, w.Body)
		}
	}
	// What the web client sends: a whole event, all-day and recurring.
	b8Create(t, f, `{"id":"","title":"gym","start":"2026-11-06T00:00:00.000Z","end":"2026-11-07T00:00:00.000Z","all_day":true,"rrule":"FREQ=WEEKLY;BYDAY=FR","plan":true}`)
	w := f.do(http.MethodGet, "/api/calendar/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", "")
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `"title":"gym"`) != 4 {
		t.Fatalf("all-day weekly over November = %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodGet, "/api/calendar.ics", ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "injected") {
		t.Fatalf("ics = %d %s", w.Code, w.Body)
	}
}

// b8BrokenCal is a store whose calendar repo fails with a driver-like
// error naming a table and a path.
type b8BrokenCal struct{ store.Store }

func (b8BrokenCal) Calendar() store.CalendarRepo { return b8BrokenRepo{} }

type b8BrokenRepo struct{}

var errB8Driver = errors.New("sqlite: no such table: calendar_events (/var/lib/devcules/devcules.db)")

func (b8BrokenRepo) List(context.Context, time.Time, time.Time) ([]*store.CalendarEvent, error) {
	return nil, errB8Driver
}
func (b8BrokenRepo) Get(_ context.Context, id string) (*store.CalendarEvent, error) {
	if id == "known" {
		return &store.CalendarEvent{ID: id, Title: "t"}, nil
	}
	return nil, errB8Driver
}
func (b8BrokenRepo) Upsert(context.Context, *store.CalendarEvent) error { return errB8Driver }
func (b8BrokenRepo) Delete(context.Context, string) error               { return errB8Driver }

// GOSEC-19: a store failure answers a fixed line, never the driver's
// error text.
func TestCalendarStoreErrorsStayServerSide(t *testing.T) {
	t.Parallel()
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
		DeskCalendar: desk.NewCalendarService(d, b8BrokenCal{s}, hc), AuditLog: audit.New(s, nil)})
	session := sessionCookie(t, mux, password)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/calendar/events", ""},
		{http.MethodPost, "/api/calendar/events", `{"title":"x","start":"2026-11-03T10:00:00Z","end":"2026-11-03T10:30:00Z"}`},
		{http.MethodPost, "/api/calendar/quick", `{"text":"lunch tomorrow 12:00"}`},
		{http.MethodPatch, "/api/calendar/events/other", `{"title":"x"}`},
		{http.MethodPatch, "/api/calendar/events/known", `{"title":"x"}`},
		{http.MethodDelete, "/api/calendar/events/known", ""},
		{http.MethodGet, "/api/calendar.ics", ""},
	} {
		w := doJSON(mux, c.method, c.path, c.body, session)
		if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
			t.Errorf("%s %s = %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
}

// Check-1 item 1, live shape: a series from 1900 with UNTIL in 9999 is
// stored with UNTIL clamped to 2200; a read over the year 9998 is
// refused, and the latest window allowed answers quickly: one a day from
// 1 Dec 2199 to the clamped UNTIL, 31 Dec 2200 (396 days).
func TestCalendarFarWindowBounded(t *testing.T) {
	t.Parallel()
	f := b8NewCalFix(t)
	ev := b8Create(t, f, `{"title":"old","start":"1900-01-01T09:00:00Z","end":"1900-01-01T10:00:00Z","rrule":"FREQ=DAILY;UNTIL=99991231T000000Z"}`)
	if ev["rrule"] != "FREQ=DAILY;UNTIL=22001231T235959Z" {
		t.Fatalf("stored rrule %v", ev["rrule"])
	}
	if w := f.do(http.MethodGet, "/api/calendar/events?from=9998-11-01T00:00:00Z&to=9999-12-05T00:00:00Z", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("far window = %d", w.Code)
	}
	t0 := time.Now()
	w := f.do(http.MethodGet, "/api/calendar/events?from=2199-12-01T00:00:00Z&to=2201-01-01T00:00:00Z", "")
	if d := time.Since(t0); w.Code != http.StatusOK || strings.Count(w.Body.String(), `"title":"old"`) != 396 || d > time.Second {
		t.Fatalf("Dec 2200 window = %d in %v, %d items", w.Code, d, strings.Count(w.Body.String(), `"title":"old"`))
	}
}

// b8-check-2 item 1: every occurrence of a series carries the series' own
// start and end (series_start, series_end), and a one-off plan carries
// neither. With them the editor saves a title-only change without moving
// the series, and shifts a time change by its delta from the series'
// start, so the earlier instances stay.
func TestCalendarOccurrenceCarriesSeriesTimes(t *testing.T) {
	t.Parallel()
	f := b8NewCalFix(t)
	ser := b8Create(t, f, `{"title":"sync","start":"2026-11-02T09:00:00Z","end":"2026-11-02T09:45:00Z","rrule":"FREQ=WEEKLY;BYDAY=MO"}`)
	b8Create(t, f, `{"title":"dentist","start":"2026-11-25T08:00:00Z","end":"2026-11-25T08:30:00Z"}`)
	id := ser["id"].(string)
	type item struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		Start       time.Time  `json:"start"`
		Occurrence  bool       `json:"occurrence"`
		SeriesStart *time.Time `json:"series_start"`
		SeriesEnd   *time.Time `json:"series_end"`
	}
	list := func() []item {
		t.Helper()
		w := f.do(http.MethodGet, "/api/calendar/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", "")
		if w.Code != http.StatusOK {
			t.Fatalf("list = %d %s", w.Code, w.Body)
		}
		var b struct {
			Events []item `json:"events"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b.Events
	}
	occ := func(items []item) (out []item) {
		for _, it := range items {
			if it.ID == id {
				out = append(out, it)
			}
		}
		return out
	}
	s0 := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	items := list()
	got := occ(items)
	if len(got) != 5 {
		t.Fatalf("series instances = %d, want 5 Mondays", len(got))
	}
	for _, it := range got {
		if !it.Occurrence || it.SeriesStart == nil || it.SeriesEnd == nil ||
			!it.SeriesStart.Equal(s0) || !it.SeriesEnd.Equal(s0.Add(45*time.Minute)) {
			t.Fatalf("occurrence %v series = %v..%v", it.Start, it.SeriesStart, it.SeriesEnd)
		}
	}
	for _, it := range items {
		if it.Title == "dentist" && (it.Occurrence || it.SeriesStart != nil || it.SeriesEnd != nil) {
			t.Fatalf("one-off plan carries series fields: %+v", it)
		}
	}
	// A title-only save from the 23 Nov occurrence (what the tile sends).
	if w := f.do(http.MethodPatch, "/api/calendar/events/"+id, `{"title":"sync renamed"}`); w.Code != http.StatusOK {
		t.Fatalf("patch title = %d %s", w.Code, w.Body)
	}
	if got := occ(list()); len(got) != 5 || got[0].Title != "sync renamed" || !got[0].Start.Equal(s0) {
		t.Fatalf("after title edit: %d instances, first %+v", len(got), got)
	}
	// A +30 min time change from that occurrence: the tile sends
	// series_start + delta, series_end + delta.
	if w := f.do(http.MethodPatch, "/api/calendar/events/"+id, `{"start":"2026-11-02T09:30:00Z","end":"2026-11-02T10:15:00Z"}`); w.Code != http.StatusOK {
		t.Fatalf("patch time = %d %s", w.Code, w.Body)
	}
	got = occ(list())
	if len(got) != 5 || !got[0].Start.Equal(s0.Add(30*time.Minute)) || !got[0].SeriesStart.Equal(s0.Add(30*time.Minute)) {
		t.Fatalf("after time shift: %d instances, first %+v", len(got), got)
	}
}

// An all-day plan arrives as the user's local midnight in UTC: the tile
// and FullCalendar send "6 Nov" as 2026-11-05T23:00:00Z in Amsterdam.
// Create and PATCH day-round it in the user's zone (CalendarService.Loc),
// not in the string's UTC offset, which stored the day before (check-3
// item 1: quick-add yoga, and an all-day series moved a day by a
// same-day time edit). Run with TZ=Europe/Amsterdam and with TZ=UTC: the
// user's zone decides, not the server's.
func TestCalendarAllDayRoundsInUserZone(t *testing.T) {
	t.Parallel()
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	f := b8NewCalFixIn(t, ams)
	day := func(v any) string {
		t.Helper()
		tm, err := time.Parse(time.RFC3339, v.(string))
		if err != nil {
			t.Fatal(err)
		}
		l := tm.In(ams)
		if l.Hour() != 0 || l.Minute() != 0 {
			t.Fatalf("all-day time %v is not local midnight", l)
		}
		return l.Format("2006-01-02")
	}
	// Saved at local midnight: Fri 6 Nov 00:00 CET.
	ev := b8Create(t, f, `{"title":"yoga","all_day":true,"start":"2026-11-05T23:00:00Z","end":"2026-11-06T23:00:00Z","rrule":"FREQ=WEEKLY"}`)
	if s, e := day(ev["start"]), day(ev["end"]); s != "2026-11-06" || e != "2026-11-07" {
		t.Fatalf("created all-day plan on %s..%s, want 2026-11-06..2026-11-07", s, e)
	}
	id := ev["id"].(string)
	patch := func(body string) map[string]any {
		t.Helper()
		w := f.do(http.MethodPatch, "/api/calendar/events/"+id, body)
		if w.Code != http.StatusOK {
			t.Fatalf("patch %s = %d %s", body, w.Code, w.Body)
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	// A move to Sat 7 Nov sent as its local midnight lands on 7 Nov.
	if m := patch(`{"start":"2026-11-06T23:00:00.000Z","end":"2026-11-07T23:00:00.000Z"}`); day(m["start"]) != "2026-11-07" || day(m["end"]) != "2026-11-08" {
		t.Fatalf("patched all-day plan on %s..%s, want 2026-11-07..2026-11-08", day(m["start"]), day(m["end"]))
	}
	// 00:30 local on 7 Nov (23:30Z on the 6th) is still 7 Nov.
	if m := patch(`{"start":"2026-11-06T23:30:00.000Z"}`); day(m["start"]) != "2026-11-07" {
		t.Fatalf("same-day time on an all-day plan moved it to %s", day(m["start"]))
	}
	// The expanded instances fall on Saturdays in the user's zone.
	w := f.do(http.MethodGet, "/api/calendar/events?from=2026-11-01T00:00:00Z&to=2026-11-30T00:00:00Z", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	var b struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range b.Events {
		if it["id"] == id {
			got = append(got, day(it["start"]))
		}
	}
	if want := []string{"2026-11-07", "2026-11-14", "2026-11-21", "2026-11-28"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instances %v, want %v", got, want)
	}
}
