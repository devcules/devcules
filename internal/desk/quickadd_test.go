package desk

import (
	"strings"
	"testing"
	"time"
)

func TestParseQuickAdd(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 10, 31, 15, 0, 0, 0, ams) // a Saturday, 15:00
	f := func(tm time.Time) string { return tm.In(ams).Format("Mon 2006-01-02 15:04") }
	cases := []struct {
		in, title, start, end, rrule string
		remind                       int
		allDay                       bool
	}{
		{"standup tomorrow 10:00 30m", "standup", "Sun 2026-11-01 10:00", "Sun 2026-11-01 10:30", "", 10, false},
		{"1-1 with Eze thursday 16:00-16:45", "1-1 with Eze", "Thu 2026-11-05 16:00", "Thu 2026-11-05 16:45", "", 10, false},
		{"dentist on 5 nov 8:30", "dentist", "Thu 2026-11-05 08:30", "Thu 2026-11-05 09:30", "", 10, false},
		{"weekly sync every monday 10:00 45m remind 15m", "weekly sync", "Mon 2026-11-02 10:00", "Mon 2026-11-02 10:45", "FREQ=WEEKLY;BYDAY=MO", 15, false},
		{"offsite 2026-11-12 all day", "offsite", "Thu 2026-11-12 00:00", "Fri 2026-11-13 00:00", "", 10, true},
		{"focus block next tue 14h 2h", "focus block", "Tue 2026-11-10 14:00", "Tue 2026-11-10 16:00", "", 10, false},
		{"call mum 9am", "call mum", "Sun 2026-11-01 09:00", "Sun 2026-11-01 10:00", "", 10, false}, // 9am has passed today → tomorrow
		{"review PRs 4pm", "review PRs", "Sat 2026-10-31 16:00", "Sat 2026-10-31 17:00", "", 10, false},
		{"gym daily 7:00 1h remind 0m", "gym", "Sun 2026-11-01 07:00", "Sun 2026-11-01 08:00", "FREQ=DAILY", 0, false},
		{"plan the week next week", "plan the week", "Mon 2026-11-02 09:00", "Mon 2026-11-02 10:00", "", 10, false},
	}
	for _, c := range cases {
		q, err := ParseQuickAdd(c.in, now, ams)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if q.Title != c.title || f(q.Start) != c.start || f(q.End) != c.end || q.RRule != c.rrule || q.ReminderMin != c.remind || q.AllDay != c.allDay {
			t.Errorf("%q → title=%q start=%s end=%s rrule=%q remind=%d allDay=%v; want %q %s %s %q %d %v (understood: %s)",
				c.in, q.Title, f(q.Start), f(q.End), q.RRule, q.ReminderMin, q.AllDay, c.title, c.start, c.end, c.rrule, c.remind, c.allDay, strings.Join(q.Understood, " | "))
		}
	}
	if _, err := ParseQuickAdd("   ", now, ams); err == nil {
		t.Fatal("blank must error")
	}
}
