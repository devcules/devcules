package desk

import (
	"strings"
	"testing"
	"time"
)

// What we export, we can read back with our own parser (and so can Outlook).
func TestICSExport_RoundTrip(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, ams)
	out := ICSExport("Kaithos plans", []ExportEvent{
		{UID: "e1", Title: "Standup, team A; daily", Start: start, End: start.Add(30 * time.Minute), Location: "https://meet.google.com/x", Notes: "line1\nline2"},
		{UID: "e2", Title: "Offsite", Start: time.Date(2026, 11, 12, 0, 0, 0, 0, ams), End: time.Date(2026, 11, 13, 0, 0, 0, 0, ams), AllDay: true},
		{UID: "e3", Title: "Weekly", Start: start, End: start.Add(time.Hour), RRule: "FREQ=WEEKLY;BYDAY=TU"},
	})
	if !strings.Contains(out, `SUMMARY:Standup\, team A\; daily`) || !strings.Contains(out, "DTSTART;VALUE=DATE:20261112") || !strings.Contains(out, "RRULE:FREQ=WEEKLY;BYDAY=TU") {
		t.Fatalf("export:\n%s", out)
	}
	ms, err := ParseMeetings(strings.NewReader(out), start.Add(-time.Hour), start.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, m := range ms {
		titles = append(titles, m.Title)
	}
	joined := strings.Join(titles, "|")
	if !strings.Contains(joined, "Standup, team A; daily") || !strings.Contains(joined, "Offsite") || strings.Count(joined, "Weekly") != 2 {
		t.Fatalf("round trip = %v", titles)
	}
	if ms[0].Start.In(ams).Format("15:04") != "10:00" {
		t.Fatalf("time drifted: %s", ms[0].Start.In(ams))
	}
}
