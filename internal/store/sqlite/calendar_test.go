package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// A recurring plan whose first instance is long before the window is
// still listed (the caller expands it); a one-off outside the window is
// not.
func TestCalendarListKeepsOldRecurringPlans(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	old := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	for _, e := range []*store.CalendarEvent{
		{ID: "series", Title: "weekly", Start: old, End: old.Add(time.Hour), RRule: "FREQ=WEEKLY"},
		{ID: "past", Title: "one-off", Start: old, End: old.Add(time.Hour)},
		{ID: "near", Title: "this week", Start: now, End: now.Add(time.Hour)},
	} {
		e.CreatedAt, e.UpdatedAt = now, now
		if err := st.Calendar().Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.Calendar().List(ctx, now.AddDate(0, 0, -7), now.AddDate(0, 0, 7))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range list {
		got[e.ID] = true
	}
	if !got["series"] || !got["near"] || got["past"] || len(list) != 2 {
		t.Fatalf("List = %v, want series and near only", got)
	}
}
