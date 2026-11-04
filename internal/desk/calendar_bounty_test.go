package desk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/teambition/rrule-go"
)

// Regression tests for the calendar-plan findings (GOSEC-06/07/10,
// GORT-06 parts 1-3, GORT-08 plan side, GORT-13). Helpers are prefixed
// b8 so they cannot clash with other batches' tests in this package.

func b8Ams(t *testing.T) *time.Location {
	t.Helper()
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip(err)
	}
	return ams
}

type b8List struct {
	Events    []calendarItem `json:"events"`
	Plans     int            `json:"plans"`
	Truncated bool           `json:"truncated"`
}

func b8Decode(t *testing.T, raw json.RawMessage) b8List {
	t.Helper()
	var l b8List
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// b8Store writes a plan straight to the store, past the service checks:
// a row stored before the checks existed.
func b8Store(t *testing.T, f deskFix, e *store.CalendarEvent) {
	t.Helper()
	now := time.Now()
	e.CreatedAt, e.UpdatedAt = now, now
	if err := f.st.Calendar().Upsert(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

// GOSEC-06: a weekly plan whose first instance is over 365 days before
// the window was filtered out by the store query and the export.
func TestOldRecurringPlanStillListed(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-400 * 24 * time.Hour).Truncate(time.Hour)
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "Weekly 1:1", Start: start, End: start.Add(30 * time.Minute), RRule: "FREQ=WEEKLY"}); err != nil {
		t.Fatal(err)
	}
	from := time.Now().UTC()
	raw, err := f.cal.List(ctx, from, from.Add(14*24*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(b8Decode(t, raw).Events); n != 2 {
		t.Fatalf("weekly plan started 400 days ago shows %d instances in the next 14 days, want 2", n)
	}
	ics, err := f.cal.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ics, "SUMMARY:Weekly 1:1\r\n") || !strings.Contains(ics, "RRULE:FREQ=WEEKLY\r\n") {
		t.Fatalf("old series missing from the export:\n%s", ics)
	}
}

// GORT-06 part 3, with fixed dates: a series begun in 2025-09 shows in
// the week of 2026-11-02.
func TestOldRecurringPlanFixedDates(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2025, 9, 1, 8, 0, 0, 0, time.UTC)
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "Weekly", Start: start, End: start.Add(time.Hour), RRule: "FREQ=WEEKLY"}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	raw, err := f.cal.List(ctx, from, from.AddDate(0, 0, 7), false)
	if err != nil {
		t.Fatal(err)
	}
	l := b8Decode(t, raw)
	if l.Plans != 1 || len(l.Events) != 1 || l.Events[0].Title != "Weekly" || !l.Events[0].Occurrence {
		t.Fatalf("list = %s", raw)
	}
}

// GOSEC-07 / GORT-08: a sub-daily rule is refused on create and update.
// One already stored shows once and does not expand.
func TestRRuleSecondlyRefusedAndStoredOneCapped(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	_, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Second), RRule: "FREQ=SECONDLY"})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("create FREQ=SECONDLY = %v, want ErrInvalidPlan", err)
	}
	ok, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "ok", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY"})
	if err != nil {
		t.Fatal(err)
	}
	ok.RRule = "FREQ=MINUTELY"
	if _, err := f.cal.Update(ctx, ok); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("update FREQ=MINUTELY = %v, want ErrInvalidPlan", err)
	}
	if err := f.cal.Delete(ctx, ok.ID); err != nil {
		t.Fatal(err)
	}

	b8Store(t, f, &store.CalendarEvent{ID: "ev_old", Title: "legacy", Start: start, End: start.Add(time.Second), RRule: "FREQ=SECONDLY"})
	raw, err := f.cal.List(ctx, start.Add(-time.Hour), start.Add(24*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	l := b8Decode(t, raw)
	if len(l.Events) != 1 || l.Events[0].Occurrence || len(raw) > 4096 {
		t.Fatalf("stored FREQ=SECONDLY plan: %d items, %d bytes", len(l.Events), len(raw))
	}
}

// The per-answer cap: plan items stop at maxPlanItems and say so.
func TestExpandPlansCapsTheAnswer(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(400 * 24 * time.Hour)
	var plans []*store.CalendarEvent
	for i := 0; i < 6; i++ {
		st := from.Add(time.Duration(9+i) * time.Hour)
		plans = append(plans, &store.CalendarEvent{ID: "p" + string(rune('a'+i)), Title: "daily", Start: st, End: st.Add(time.Hour), RRule: "FREQ=DAILY"})
	}
	out, truncated := expandPlansIn(plans, from, to, time.UTC)
	if len(out) != maxPlanItems || !truncated {
		t.Fatalf("6 daily plans over 400 days = %d items (truncated %v), want %d and true", len(out), truncated, maxPlanItems)
	}
	out, truncated = expandPlansIn(plans[:1], from, to, time.UTC)
	if len(out) != 400 || truncated {
		t.Fatalf("1 daily plan over 400 days = %d items (truncated %v), want 400", len(out), truncated)
	}
}

// GOSEC-10: an RRULE with CRLF is refused; one already stored is left
// out of the export, so nothing breaks out of its line.
func TestRRuleInjectionRefusedAndExportClean(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	evil := "FREQ=DAILY\r\nEND:VEVENT\r\nBEGIN:VEVENT\r\nSUMMARY:injected"
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Hour), RRule: evil}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("create with CRLF rrule = %v, want ErrInvalidPlan", err)
	}
	b8Store(t, f, &store.CalendarEvent{ID: "ev_evil", Title: "legacy", Start: start, End: start.Add(time.Hour), RRule: evil, URL: "https://x\r\nATTENDEE:mailto:a@b"})
	ics, err := f.cal.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ics, "injected") || strings.Contains(ics, "ATTENDEE") || strings.Count(ics, "BEGIN:VEVENT") != 1 || strings.Contains(ics, "RRULE") {
		t.Fatalf("export not clean:\n%s", ics)
	}
	// ICSExport itself never writes an unsafe raw value.
	doc := ICSExport("x", []ExportEvent{{UID: "u", Title: "t", Start: start, End: start.Add(time.Hour), RRule: evil}})
	if strings.Contains(doc, "injected") {
		t.Fatalf("ICSExport wrote a CRLF rrule:\n%s", doc)
	}
}

// Rule shapes that are refused: sub-daily, clock filters, shapes that
// never match (rrule-go would iterate to the year 9999 on every read),
// and bad syntax. Each is refused before any long iteration.
func TestPlanRuleRefusals(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC) // a Monday in January
	for _, rr := range []string{
		"FREQ=HOURLY",
		"FREQ=MINUTELY;BYSETPOS=1",
		"FREQ=SECONDLY;BYSETPOS=2;UNTIL=20270101T000000Z",
		"FREQ=DAILY;BYMINUTE=0,1,2,3,4,5,6,7,8,9;BYSECOND=0,1,2,3,4,5,6,7,8,9",
		"FREQ=DAILY;BYHOUR=9",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30",
		"FREQ=DAILY;BYMONTH=4,6,9,11;BYMONTHDAY=31,-31",
		"FREQ=MONTHLY;BYDAY=1MO;BYMONTHDAY=20",
		"FREQ=MONTHLY;BYDAY=6MO",
		"FREQ=WEEKLY;BYDAY=2MO",
		"FREQ=DAILY;BYSETPOS=2",
		"FREQ=DAILY;INTERVAL=7;BYDAY=TU",
		"FREQ=WEEKLY;INTERVAL=2;BYMONTHDAY=1",
		"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=6",   // never 6 Mondays: probe
		"FREQ=MONTHLY;INTERVAL=12;BYMONTH=3", // only Januaries visited: probe
		"FREQ=DAILY;UNTIL=20250101T000000Z",  // ends before the start: probe
		"FREQ=YEARLY;BYYEARDAY=100",
		"FREQ=YEARLY;BYWEEKNO=53",
		"FREQ=DAILY;COUNT=99999",
		"FREQ=DAILY;INTERVAL=5000",
		"FREQ=DAILY;DTSTART=20200101T000000Z",
		"FREQ=DAILY\nRRULE:FREQ=WEEKLY",
		"FREQ=DAILY;X-FOO=1",
		"FREQ=SOMETIMES",
		"BYDAY=MO",
		"FREQ=WEEKLY;BYDAY=MO\u00e9",
		"FREQ=DAILY;" + strings.Repeat("BYMONTH=1;", 40),
	} {
		_, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Hour), RRule: rr})
		if !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("create rrule %q = %v, want ErrInvalidPlan", rr, err)
		}
	}
	// A recurring plan's start stays within a sane year range.
	old := time.Date(1, 1, 1, 9, 0, 0, 0, time.UTC)
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: old, End: old.Add(time.Hour), RRule: "FREQ=DAILY"}); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("recurring plan in year 1 = %v, want ErrInvalidPlan", err)
	}
}

// Shapes people use are accepted and stored re-serialised.
func TestPlanRuleAccepted(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	for rr, want := range map[string]string{
		"RRULE:FREQ=WEEKLY":                             "FREQ=WEEKLY",
		" FREQ=DAILY ":                                  "FREQ=DAILY",
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR":              "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE":            "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE",
		"FREQ=MONTHLY;BYDAY=-1FR":                       "FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1": "FREQ=MONTHLY;BYSETPOS=-1;BYDAY=MO,TU,WE,TH,FR",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29":           "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29",
		"FREQ=MONTHLY;BYMONTHDAY=31":                    "FREQ=MONTHLY;BYMONTHDAY=31",
		"FREQ=DAILY;COUNT=10":                           "FREQ=DAILY;COUNT=10",
		"FREQ=DAILY;UNTIL=20271231T000000Z":             "FREQ=DAILY;UNTIL=20271231T000000Z",
		"FREQ=DAILY;INTERVAL=3":                         "FREQ=DAILY;INTERVAL=3",
	} {
		e, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Hour), RRule: rr})
		if err != nil {
			t.Errorf("create rrule %q = %v", rr, err)
			continue
		}
		if e.RRule != want {
			t.Errorf("rrule %q stored as %q, want %q", rr, e.RRule, want)
		}
	}
	// Quick-add phrases still validate, all-day ones included.
	for _, text := range []string{"standup every day 9:30", "gym every friday 7am", "review every weekday 16:00", "rent every month 9am", "gym all day every friday", "team sync weekly 10am"} {
		e, err := f.cal.QuickAdd(ctx, text, time.UTC)
		if err != nil {
			t.Errorf("quick add %q = %v", text, err)
			continue
		}
		if e.RRule == "" {
			t.Logf("quick add %q made no rule", text)
		}
	}
}

// A stored rule that never matches (stored before the checks) is shown
// once rather than iterated: parsePlanRule refuses it on read too.
func TestStoredNeverMatchingRuleShownOnce(t *testing.T) {
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	out := expandPlans([]*store.CalendarEvent{
		{ID: "n", Title: "never", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY;BYMONTH=2;BYMONTHDAY=30"},
	}, start.Add(-time.Hour), start.AddDate(0, 1, 0))
	if len(out) != 1 || out[0].Occurrence {
		t.Fatalf("never-matching stored rule = %+v", out)
	}
}

// GORT-06 part 1: an all-day weekly plan expands by date (4 Fridays in
// November 2026), from the web's UTC-midnight form.
func TestAllDayRecurringPlanExpands(t *testing.T) {
	q, err := ParseQuickAdd("gym all day every friday", time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !q.AllDay || q.RRule == "" {
		t.Fatalf("quickadd = %+v", q)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	out := expandPlans([]*store.CalendarEvent{{ID: "g", Title: q.Title, Start: q.Start, End: q.End, AllDay: true, RRule: q.RRule}}, from, to)
	if len(out) != 4 {
		t.Fatalf("all-day weekly plan = %d items over a month, want 4: %+v", len(out), out)
	}
	for _, it := range out {
		if it.Start.Weekday() != time.Friday || it.End.Sub(it.Start) != 24*time.Hour || !it.AllDay || !it.Occurrence {
			t.Errorf("instance = %+v", it)
		}
		// b8-check-2 item 1: each instance carries the series' stored start.
		if it.SeriesStart == nil || it.SeriesEnd == nil || !it.SeriesStart.Equal(q.Start) || !it.SeriesEnd.Equal(q.End) {
			t.Errorf("instance %v series = %v..%v, want %v..%v", it.Start, it.SeriesStart, it.SeriesEnd, q.Start, q.End)
		}
	}
	// A one-off plan is not an occurrence and carries no series times.
	one := expandPlans([]*store.CalendarEvent{{ID: "o", Title: "x", Start: from.Add(time.Hour), End: from.Add(2 * time.Hour)}}, from, to)
	if len(one) != 1 || one[0].Occurrence || one[0].SeriesStart != nil || one[0].SeriesEnd != nil {
		t.Errorf("one-off = %+v", one)
	}
}

// GORT-06 part 1, quick-add's form: local midnight in Europe/Amsterdam
// (23:00Z the day before), across the DST end on 25 Oct 2026. Every
// instance stays at local midnight, one day long.
func TestAllDayRecurringPlanLocalMidnightAcrossDST(t *testing.T) {
	ams := b8Ams(t)
	q, err := ParseQuickAdd("gym all day every friday", time.Date(2026, 10, 5, 10, 0, 0, 0, ams), ams)
	if err != nil {
		t.Fatal(err)
	}
	e := &store.CalendarEvent{ID: "g", Title: q.Title, Start: q.Start.UTC(), End: q.End.UTC(), AllDay: true, RRule: q.RRule}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, ams)
	out, _ := expandPlansIn([]*store.CalendarEvent{e}, from, from.AddDate(0, 2, 0), ams)
	if len(out) < 8 {
		t.Fatalf("got %d instances over two months", len(out))
	}
	for _, it := range out {
		s, en := it.Start.In(ams), it.End.In(ams)
		if s.Hour() != 0 || en.Hour() != 0 || s.Weekday() != time.Friday || en.YearDay()-s.YearDay() != 1 {
			t.Errorf("instance %s .. %s", s, en)
		}
	}
}

// GORT-06 part 2: a weekly 09:00 Europe/Amsterdam plan created in summer
// (stored in UTC) stays at 09:00 local after the DST change.
func TestRecurringPlanKeepsLocalTimeAcrossDST(t *testing.T) {
	ams := b8Ams(t)
	start := time.Date(2026, 9, 7, 9, 0, 0, 0, ams).UTC() // as the web client sends it
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, ams)
	out, _ := expandPlansIn([]*store.CalendarEvent{{ID: "s", Title: "standup", Start: start, End: start.Add(30 * time.Minute), RRule: "FREQ=WEEKLY"}}, from, from.AddDate(0, 0, 7), ams)
	if len(out) != 1 {
		t.Fatalf("got %d", len(out))
	}
	if local := out[0].Start.In(ams); local.Hour() != 9 || local.Minute() != 0 || out[0].End.Sub(out[0].Start) != 30*time.Minute {
		t.Errorf("weekly 09:00 plan shows at %s local after DST", local)
	}
	// And in summer, before the change, it is 09:00 too.
	summer := time.Date(2026, 9, 14, 0, 0, 0, 0, ams)
	out, _ = expandPlansIn([]*store.CalendarEvent{{ID: "s", Title: "standup", Start: start, End: start.Add(30 * time.Minute), RRule: "FREQ=WEEKLY"}}, summer, summer.AddDate(0, 0, 7), ams)
	if len(out) != 1 || out[0].Start.In(ams).Hour() != 9 {
		t.Fatalf("summer instance = %+v", out)
	}
}

// GORT-13: quick-add's all-day plan on 6 Nov in Europe/Amsterdam is
// exported on 6 Nov (DTEND 7 Nov), not the UTC date of local midnight.
func TestExportAllDayPlanOnItsDate(t *testing.T) {
	ams := b8Ams(t)
	f := newFix(t)
	f.cal.Loc = ams
	ctx := context.Background()
	if _, err := f.cal.QuickAdd(ctx, "holiday all day 2026-11-06", ams); err != nil {
		t.Fatal(err)
	}
	// The web grid's form: the date as UTC midnight.
	web := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "offsite", Start: web, End: web.AddDate(0, 0, 2), AllDay: true}); err != nil {
		t.Fatal(err)
	}
	doc, err := f.cal.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DTSTART;VALUE=DATE:20261106\r\n", "DTEND;VALUE=DATE:20261107\r\n", "DTSTART;VALUE=DATE:20261109\r\n", "DTEND;VALUE=DATE:20261111\r\n"} {
		if !strings.Contains(doc, want) {
			t.Errorf("export lacks %q:\n%s", want, doc)
		}
	}
}

// A recurring plan's error is a plan error (400), a store one is not.
func TestCalendarErrorsAreClassified(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: " "}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("no title = %v", err)
	}
	if _, err := f.cal.Update(ctx, &store.CalendarEvent{ID: "x", Title: ""}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("update no title = %v", err)
	}
	if _, err := f.cal.QuickAdd(ctx, "", time.UTC); err != nil && !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("quick add parse error = %v", err)
	}
	_ = f.st.Close()
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start}); err == nil || errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("store failure = %v, want a non-plan error", err)
	}
}

// b8Timed fails the test when f takes longer than limit. The limits are
// far above the measured cost (single milliseconds), so a loaded box
// does not flake them; an unbounded walk takes seconds.
func b8Timed(t *testing.T, what string, limit time.Duration, f func()) {
	t.Helper()
	t0 := time.Now()
	f()
	if d := time.Since(t0); d > limit {
		t.Errorf("%s took %v, want under %v", what, d, limit)
	}
}

// Check-1 item 1: a series begun in 1900 with UNTIL in the year 9999,
// read over a window in the year 9998, walked every instance from 1900
// (5.4 s per plan). The walk now starts near the window, UNTIL is
// clamped to 2200, and a rule with no instance after the window stops
// within 400 years.
func TestFarWindowExpansionIsBounded(t *testing.T) {
	start := time.Date(1900, 1, 1, 9, 0, 0, 0, time.UTC)
	from := time.Date(9998, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(400 * 24 * time.Hour)
	plans := []*store.CalendarEvent{
		{ID: "u", Title: "until 9999", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY;UNTIL=99991231T000000Z"},
		{ID: "w", Title: "wkst", Start: time.Date(1937, 3, 1, 9, 0, 0, 0, time.UTC), End: time.Date(1937, 3, 1, 10, 0, 0, 0, time.UTC), RRule: "FREQ=DAILY;WKST=WE;UNTIL=88580318T000000Z"},
	}
	var out []calendarItem
	b8Timed(t, "1900 series over a 9998 window", 100*time.Millisecond, func() { out, _ = expandPlansIn(plans, from, to, time.UTC) })
	if len(out) != 0 {
		t.Fatalf("UNTIL is clamped to 2200, so nothing in 9998; got %d", len(out))
	}
	// Without UNTIL the series runs on: one a day, still fast.
	open := []*store.CalendarEvent{{ID: "o", Title: "open", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY"}}
	b8Timed(t, "open 1900 series over a 9998 window", 100*time.Millisecond, func() { out, _ = expandPlansIn(open, from, to, time.UTC) })
	if len(out) != 400 {
		t.Fatalf("open daily series over 400 days in 9998 = %d, want 400", len(out))
	}
	// The latest window a read may ask for, every legal shape at its
	// slowest: a rule with no instance left walks at most 400 years.
	from, to = time.Date(2199, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2201, 1, 1, 0, 0, 0, 0, time.UTC)
	slow := []*store.CalendarEvent{
		{ID: "a", Title: "a", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY"},
		{ID: "b", Title: "b", Start: start, End: start.Add(time.Hour), RRule: "FREQ=MONTHLY;INTERVAL=12;BYMONTH=3"}, // never
		{ID: "c", Title: "c", Start: start, End: start.Add(time.Hour), RRule: "FREQ=MONTHLY;INTERVAL=2;BYMONTH=2"},  // never
		{ID: "d", Title: "d", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO"},
		{ID: "e", Title: "e", Start: start, End: start.Add(time.Hour), RRule: "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29;COUNT=5000"},
		{ID: "f", Title: "f", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY;BYMONTH=2;BYMONTHDAY=29;BYDAY=MO;COUNT=5000"},
		{ID: "g", Title: "g", Start: start, End: start.Add(time.Hour), RRule: "FREQ=WEEKLY;BYMONTH=2;BYMONTHDAY=29"},
	}
	b8Timed(t, "slowest legal shapes in 2200", 100*time.Millisecond, func() { out, _ = expandPlansIn(slow, from, to, time.UTC) })
	if len(out) < 31 {
		t.Fatalf("daily plan in Dec 2199..2200 = %d items", len(out))
	}
}

// The walk skips ahead and runs 400-year cycles ahead; it must answer
// exactly what rrule-go answers walking the whole series from its start.
func TestPlanWalkMatchesFullWalk(t *testing.T) {
	ams := b8Ams(t)
	rules := []string{
		"FREQ=DAILY", "FREQ=DAILY;INTERVAL=3", "FREQ=DAILY;INTERVAL=1000",
		"FREQ=DAILY;BYMONTH=2;BYDAY=MO", "FREQ=DAILY;UNTIL=20310320T000000Z",
		"FREQ=WEEKLY", "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE", "FREQ=WEEKLY;INTERVAL=3;WKST=SU;BYDAY=SU,SA",
		"FREQ=WEEKLY;BYMONTH=3", "FREQ=WEEKLY;COUNT=200;BYDAY=TU",
		"FREQ=MONTHLY", "FREQ=MONTHLY;BYMONTHDAY=31", "FREQ=MONTHLY;INTERVAL=5;BYDAY=-1FR",
		"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", "FREQ=MONTHLY;INTERVAL=7;BYMONTH=2,3",
		"FREQ=MONTHLY;COUNT=400;BYDAY=2TU", "FREQ=MONTHLY;BYDAY=5MO",
		"FREQ=YEARLY", "FREQ=YEARLY;INTERVAL=3", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29",
		"FREQ=YEARLY;BYMONTH=3;BYDAY=2SU", "FREQ=YEARLY;BYDAY=1MO", "FREQ=YEARLY;BYMONTH=6,7;BYDAY=FR;BYSETPOS=1,-1",
	}
	starts := []time.Time{
		time.Date(1987, 5, 31, 9, 30, 0, 0, ams),
		time.Date(2025, 1, 31, 23, 30, 0, 0, ams),
		time.Date(1912, 2, 29, 0, 15, 0, 0, ams),
	}
	windows := [][2]time.Time{
		{time.Date(2026, 11, 1, 0, 0, 0, 0, ams), time.Date(2026, 12, 6, 0, 0, 0, 0, ams)},
		{time.Date(2031, 1, 1, 0, 0, 0, 0, ams), time.Date(2032, 2, 1, 0, 0, 0, 0, ams)},
	}
	compared := 0
	for _, rr := range rules {
		opt, err := parsePlanRule(rr)
		if err != nil {
			t.Fatalf("%s: %v", rr, err)
		}
		for _, st := range starts {
			for _, w := range windows {
				dur := 45 * time.Minute
				got, _ := expandPlansIn([]*store.CalendarEvent{{ID: "p", Title: "p", Start: st.UTC(), End: st.Add(dur).UTC(), RRule: rr}}, w[0], w[1], ams)
				o := *opt
				o.Dtstart = st
				full, err := rrule.NewRRule(o)
				if err != nil {
					t.Fatal(err)
				}
				var want []time.Time
				next := full.Iterator()
				for i := 0; i < 100000; i++ {
					s, ok := next()
					if !ok || !s.Before(w[1]) {
						break
					}
					if s.Add(dur).After(w[0]) {
						want = append(want, s)
					}
				}
				if len(got) != len(want) {
					t.Errorf("%s from %s over %s: %d instances, full walk %d", rr, st, w[0].Format("2006-01"), len(got), len(want))
					continue
				}
				compared += len(got)
				for i := range got {
					if !got[i].Start.Equal(want[i]) {
						t.Errorf("%s from %s: instance %d at %s, full walk %s", rr, st, i, got[i].Start.In(ams), want[i])
						break
					}
				}
			}
		}
	}
	if compared < 1000 {
		t.Fatalf("compared only %d instances", compared)
	}
	t.Logf("compared %d instances", compared)
}

// Check-1 item 2: shapes rrule-go v1.8.2 never matches are refused
// structurally (no walk), on create and on read, each in well under
// 50 ms. They used to cost 0.3-1.3 s each.
func TestRRuleGoTrapShapesRefusedFast(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	for _, rr := range []string{
		"FREQ=MONTHLY;BYDAY=MO,1TU",
		"FREQ=MONTHLY;BYMONTH=11;BYDAY=SA,3MO",
		"FREQ=MONTHLY;UNTIL=31311018T000000Z;BYDAY=WE,-1MO,TU",
		"FREQ=YEARLY;BYDAY=1MO,FR",
		"FREQ=MONTHLY;BYDAY=4SA;BYSETPOS=7",
		"FREQ=MONTHLY;BYDAY=5TU;BYSETPOS=9",
		"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=6",
		"FREQ=MONTHLY;BYMONTHDAY=1,15;BYSETPOS=3",
		"FREQ=MONTHLY;BYSETPOS=2",
		"FREQ=YEARLY;BYMONTH=3;BYSETPOS=-2",
		"FREQ=YEARLY;BYDAY=2MO;BYSETPOS=2",
	} {
		b8Timed(t, "refusing "+rr, 50*time.Millisecond, func() {
			if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Hour), RRule: rr}); !errors.Is(err, ErrInvalidPlan) {
				t.Errorf("create %q = %v, want ErrInvalidPlan", rr, err)
			}
		})
		if _, err := parsePlanRule(rr); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("parsePlanRule %q = %v, want refused on read too", rr, err)
		}
		var out []calendarItem
		b8Timed(t, "reading stored "+rr, 50*time.Millisecond, func() {
			out = expandPlans([]*store.CalendarEvent{{ID: "s", Title: "s", Start: start, End: start.Add(time.Hour), RRule: rr}}, start.Add(-time.Hour), start.AddDate(0, 1, 0))
		})
		if len(out) != 1 || out[0].Occurrence {
			t.Errorf("stored %q = %+v, want its first instance only", rr, out)
		}
	}
	// A legal BYSETPOS at its bound still works.
	for _, rr := range []string{"FREQ=MONTHLY;BYDAY=MO;BYSETPOS=5", "FREQ=MONTHLY;BYMONTHDAY=1,15;BYSETPOS=-2", "FREQ=YEARLY;BYMONTH=3,9;BYDAY=1MO;BYSETPOS=2"} {
		if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Hour), RRule: rr}); err != nil {
			t.Errorf("create %q = %v", rr, err)
		}
	}
	// A stored series that starts outside the recurring years (year 311)
	// shows once, without a walk.
	old := time.Date(311, 6, 1, 9, 0, 0, 0, time.UTC)
	var out []calendarItem
	b8Timed(t, "reading a year-311 series", 50*time.Millisecond, func() {
		out = expandPlans([]*store.CalendarEvent{{ID: "o", Title: "o", Start: old, End: old.Add(time.Hour), RRule: "FREQ=DAILY;UNTIL=19430526T000000Z"}}, old.Add(-time.Hour), old.AddDate(0, 1, 0))
	})
	if len(out) != 1 || out[0].Occurrence {
		t.Errorf("year-311 series = %+v", out)
	}
}

// UNTIL past 2200 is stored clamped.
func TestPlanRuleUntilClamped(t *testing.T) {
	f := newFix(t)
	start := time.Date(1900, 1, 1, 9, 0, 0, 0, time.UTC)
	e, err := f.cal.Create(context.Background(), &store.CalendarEvent{Title: "x", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY;UNTIL=99991231T000000Z"})
	if err != nil {
		t.Fatal(err)
	}
	if e.RRule != "FREQ=DAILY;UNTIL=22001231T235959Z" {
		t.Fatalf("stored %q", e.RRule)
	}
}

// B3 round-3 lesson: rrule-go must never walk a DTSTART other than the
// one checked. A DTSTART inside the rule text is refused (and ignored by
// the walker), and a zero start, which rrule-go reads as "now", is never
// walked.
func TestPlanWalkerOwnsTheStart(t *testing.T) {
	if _, err := parsePlanRule("FREQ=DAILY;DTSTART=20261104T100000Z"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("embedded DTSTART = %v", err)
	}
	opt, err := parsePlanRule("FREQ=WEEKLY;BYDAY=WE")
	if err != nil {
		t.Fatal(err)
	}
	s0 := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	o := *opt
	o.Dtstart = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC) // as if the rule carried one
	next, err := planWalker(o, s0, time.Time{}, s0, s0.AddDate(0, 0, 15))
	if err != nil {
		t.Fatal(err)
	}
	var got []time.Time
	for c, ok := next(); ok; c, ok = next() {
		got = append(got, c)
	}
	if len(got) != 3 || got[0] != s0 || got[1] != s0.AddDate(0, 0, 7) {
		t.Fatalf("walk = %v, want 3 Wednesdays at 01:00 from %s", got, s0)
	}
	if _, err := planWalker(*opt, time.Time{}, time.Time{}, time.Time{}, s0); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("zero start walked: %v", err)
	}
	zero := time.Time{}
	var out []calendarItem
	b8Timed(t, "reading a zero-start series", 50*time.Millisecond, func() {
		out = expandPlans([]*store.CalendarEvent{{ID: "z", Title: "z", Start: zero, End: zero.Add(time.Hour), RRule: "FREQ=DAILY"}}, s0, s0.AddDate(0, 1, 0))
	})
	if len(out) != 0 {
		t.Fatalf("zero-start series in 2026 = %+v", out)
	}
	if _, err := newFix(t).cal.Create(context.Background(), &store.CalendarEvent{Title: "z", Start: zero, End: zero.Add(time.Hour), RRule: "FREQ=DAILY"}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("create zero-start series = %v", err)
	}
}

// A read whose request is gone stops expanding.
func TestListStopsWhenTheRequestIsGone(t *testing.T) {
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := expandPlansCtx(ctx, []*store.CalendarEvent{{ID: "a", Title: "a", Start: start, End: start.Add(time.Hour), RRule: "FREQ=DAILY"}}, start, start.AddDate(1, 0, 0), time.UTC)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// Check-1 item 3: SUMMARY, LOCATION and DESCRIPTION carried a bare CR
// (and other controls) raw into the export, which a lenient reader takes
// as a new line: new properties or a new VEVENT.
func TestExportTextFieldsCannotBreakTheirLine(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{
		Title:    "a\rEND:VEVENT\rBEGIN:VEVENT\rSUMMARY:injected",
		Location: "l\rATTENDEE:mailto:x@evil",
		Notes:    "n\u000bX-EVIL:1\r\nline two\ttab\u0085",
		Start:    start, End: start.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := f.cal.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(doc, "\r\n", "")
	if strings.ContainsAny(body, "\r\n\u000b\u0000\u0085") {
		t.Fatalf("export carries a control character outside CRLF line ends: %q", doc)
	}
	lines := strings.Split(strings.TrimSuffix(doc, "\r\n"), "\r\n")
	want := map[string]bool{
		`SUMMARY:a\nEND:VEVENT\nBEGIN:VEVENT\nSUMMARY:injected`: false,
		`LOCATION:l\nATTENDEE:mailto:x@evil`:                    false,
		"DESCRIPTION:nX-EVIL:1\\nline two\ttab":                 false,
	}
	for _, l := range lines {
		if _, ok := want[l]; ok {
			want[l] = true
		}
		for _, bad := range []string{"ATTENDEE", "X-EVIL", "SUMMARY:injected"} {
			if strings.HasPrefix(l, bad) {
				t.Errorf("injected line %q", l)
			}
		}
	}
	for l, seen := range want {
		if !seen {
			t.Errorf("export lacks line %q:\n%q", l, doc)
		}
	}
	if n := strings.Count(doc, "\r\nBEGIN:VEVENT\r\n"); n != 1 {
		t.Errorf("export has %d VEVENTs", n)
	}
}

func TestICSEscape(t *testing.T) {
	for in, want := range map[string]string{
		"a\r\nb":                     `a\nb`,
		"a\nb":                       `a\nb`,
		"a\rb":                       `a\nb`,
		"a\r\rb":                     `a\n\nb`,
		"tab\there":                  "tab\there",
		"v\u000bf\u000cx":            "vfx",
		"del\u007fc1\u0085nul\u0000": "delc1nul",
		`back\slash;semi,comma`:      `back\\slash\;semi\,comma`,
		"caf\u00e9 \u2603":           "caf\u00e9 \u2603",
	} {
		if got := icsEscape(in); got != want {
			t.Errorf("icsEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
