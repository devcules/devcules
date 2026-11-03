package desk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
)

func TestCalendarCreateGetUpdateDelete(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	start := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "  ", Start: start, End: start}); err == nil {
		t.Fatal("blank title accepted")
	}
	if _, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "x", Start: start, End: start.Add(-time.Hour)}); err == nil {
		t.Fatal("end before start accepted")
	}
	e, err := f.cal.Create(ctx, &store.CalendarEvent{Title: "Dentist", Start: start, End: start.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.ID, "ev_") || e.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", e)
	}
	got, err := f.cal.Get(ctx, e.ID)
	if err != nil || got.Title != "Dentist" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	got.Title = "Dentist (moved)"
	if _, err := f.cal.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	bad := *got
	bad.End = bad.Start.Add(-time.Minute)
	if _, err := f.cal.Update(ctx, &bad); err == nil {
		t.Fatal("update with end before start accepted")
	}
	got, err = f.cal.Get(ctx, e.ID)
	if err != nil || got.Title != "Dentist (moved)" {
		t.Fatalf("after update = %+v, %v", got, err)
	}
	if err := f.cal.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cal.Get(ctx, e.ID); err == nil {
		t.Fatal("deleted event still readable")
	}
}

func TestCalendarListMergesPlansAndFeeds(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.net.set("cal.example.com", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, feedICS) })
	if err := f.svc.WriteSources(ctx, Sources{ICSFeeds: []ICSFeed{{Label: "Work", URL: "https://cal.example.com/w.ics"}}}); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for _, e := range []*store.CalendarEvent{
		{Title: "Gym", Start: day, End: day.Add(time.Hour), RRule: "RRULE:FREQ=DAILY;COUNT=3"},
		{Title: "Trip", Start: day, End: day.Add(48 * time.Hour), AllDay: true},
		{Title: "Far away", Start: day.Add(200 * 24 * time.Hour), End: day.Add(201 * 24 * time.Hour)},
	} {
		if _, err := f.cal.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	raw, err := f.cal.List(ctx, from, from.Add(7*24*time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Events     []calendarItem `json:"events"`
		Configured bool           `json:"configured"`
		Plans      int            `json:"plans"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	gym, feeds := 0, 0
	for i, e := range got.Events {
		if i > 0 && e.Start.Before(got.Events[i-1].Start) {
			t.Fatalf("events out of order: %s", raw)
		}
		if e.Title == "Gym" {
			gym++
			if !e.Occurrence || !e.Plan {
				t.Errorf("gym instance = %+v", e)
			}
		}
		if e.Feed == "Work" {
			feeds++
		}
		if e.Title == "Far away" {
			t.Error("event outside the window listed")
		}
	}
	if !got.Configured || gym != 3 || feeds != 2 || got.Plans != 3 {
		t.Fatalf("list = %s", raw)
	}
}

func TestExpandPlansSkipsBrokenRules(t *testing.T) {
	day := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	from, to := day.Add(-time.Hour), day.Add(72*time.Hour)
	out := expandPlans([]*store.CalendarEvent{
		{ID: "a", Title: "no length", Start: day, End: day, RRule: "FREQ=DAILY"},
		{ID: "b", Title: "bad rule", Start: day, End: day.Add(time.Hour), RRule: "FREQ=SOMETIMES"},
		{ID: "c", Title: "before", Start: day.Add(-48 * time.Hour), End: day.Add(-47 * time.Hour)},
	}, from, to)
	if len(out) != 0 {
		t.Fatalf("expanded = %+v", out)
	}
}

func TestCalendarQuickAddAndExport(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	if _, err := f.cal.QuickAdd(ctx, "", time.UTC); err == nil {
		t.Fatal("empty quick add accepted")
	}
	e, err := f.cal.QuickAdd(ctx, "dentist tomorrow 8:30 30m", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(e.Title), "dentist") || e.End.Sub(e.Start) != 30*time.Minute {
		t.Fatalf("quick add = %+v", e)
	}
	ics, err := f.cal.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ics, "BEGIN:VCALENDAR") || !strings.Contains(ics, "UID:"+e.ID) {
		t.Fatalf("export = %s", ics)
	}
}
