package desk

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

const fixture = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:single@test
DTSTART;TZID=Europe/Amsterdam:20261031T140000
DTEND;TZID=Europe/Amsterdam:20261031T143000
SUMMARY:Standup\, team A
LOCATION:https://meet.google.com/abc-defg-hij
END:VEVENT
BEGIN:VEVENT
UID:later@test
DTSTART:20261105T090000Z
DTEND:20261105T100000Z
SUMMARY:Next week
END:VEVENT
BEGIN:VEVENT
UID:allday@test
DTSTART;VALUE=DATE:20261101
DTEND;VALUE=DATE:20261102
SUMMARY:Public holiday
END:VEVENT
BEGIN:VEVENT
UID:weekly@test
DTSTART;TZID=Europe/Amsterdam:20261003T100000
DTEND;TZID=Europe/Amsterdam:20261003T104500
RRULE:FREQ=WEEKLY;BYDAY=SA
EXDATE;TZID=Europe/Amsterdam:20261024T100000
SUMMARY:Weekly sync
DESCRIPTION:Join here: https://zoom.us/j/123456789?pwd=x\nAgenda follows.
END:VEVENT
BEGIN:VEVENT
UID:weekly@test
RECURRENCE-ID;TZID=Europe/Amsterdam:20261031T100000
DTSTART;TZID=Europe/Amsterdam:20261031T160000
DTEND;TZID=Europe/Amsterdam:20261031T164500
SUMMARY:Weekly sync (moved)
END:VEVENT
BEGIN:VEVENT
UID:cancelled@test
DTSTART;TZID=Europe/Amsterdam:20261031T110000
DTEND;TZID=Europe/Amsterdam:20261031T113000
STATUS:CANCELLED
SUMMARY:Cancelled thing
END:VEVENT
END:VCALENDAR
`

func TestParseMeetings_Window(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	from := time.Date(2026, 10, 31, 0, 0, 0, 0, ams)
	to := from.Add(48 * time.Hour) // Oct 31 + Nov 1
	ms, err := ParseMeetings(strings.NewReader(fixture), from, to)
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, m := range ms {
		titles = append(titles, m.Title)
	}
	want := []string{"Standup, team A", "Weekly sync (moved)", "Public holiday"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %v; want %v", titles, want)
	}
	// single event: times, link from LOCATION
	if ms[0].Start.In(ams).Format("15:04") != "14:00" || ms[0].End.Sub(ms[0].Start) != 30*time.Minute || ms[0].URL != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("standup = %+v", ms[0])
	}
	// the Oct 31 weekly instance is replaced by its RECURRENCE-ID override (moved to 16:00)
	if ms[1].Start.In(ams).Format("2006-01-02 15:04") != "2026-10-31 16:00" || ms[1].UID != "weekly@test" {
		t.Fatalf("override = %+v", ms[1])
	}
	if !ms[2].AllDay || ms[2].Start.In(ams).Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("all-day = %+v", ms[2])
	}
}

func TestParseMeetings_RecurrenceExdateAndLink(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	// a window covering Saturdays Oct 17, Oct 24 (EXDATE) and Nov 7
	from := time.Date(2026, 10, 17, 0, 0, 0, 0, ams)
	to := time.Date(2026, 11, 8, 0, 0, 0, 0, ams)
	ms, err := ParseMeetings(strings.NewReader(fixture), from, to)
	if err != nil {
		t.Fatal(err)
	}
	var weekly []string
	for _, m := range ms {
		if m.UID == "weekly@test" && m.Title == "Weekly sync" {
			weekly = append(weekly, m.Start.In(ams).Format("01-02 15:04"))
			if m.URL != "https://zoom.us/j/123456789?pwd=x" {
				t.Fatalf("zoom link not picked from DESCRIPTION: %q", m.URL)
			}
			if m.End.Sub(m.Start) != 45*time.Minute {
				t.Fatalf("instance duration = %v", m.End.Sub(m.Start))
			}
		}
	}
	// Oct 17 yes, Oct 24 excluded, Oct 31 overridden (not under this title), Nov 7 yes
	if strings.Join(weekly, ",") != "10-17 10:00,11-07 10:00" {
		t.Fatalf("weekly instances = %v", weekly)
	}
}

func TestICSDuration(t *testing.T) {
	if d := icsDuration("PT1H30M"); d != 90*time.Minute {
		t.Fatalf("PT1H30M = %v", d)
	}
	if d := icsDuration("P1DT2H"); d != 26*time.Hour {
		t.Fatalf("P1DT2H = %v", d)
	}
}

// A calendar address is a secret; errors name only its host.
func TestFetchMeetings_ErrorsNeverLeakTheAddress(t *testing.T) {
	secret := "https://outlook.office365.com/owa/calendar/abc@x.com/verysecrettoken/calendar.ics"
	hc := &http.Client{Timeout: 50 * time.Millisecond}
	// an unroutable address times out quickly
	_, err := FetchMeetings(context.Background(), hc, "https://10.255.255.1/owa/calendar/abc/verysecrettoken/calendar.ics", time.Now(), time.Now().Add(time.Hour))
	if err == nil || strings.Contains(err.Error(), "verysecrettoken") {
		t.Fatalf("error must not contain the token: %v", err)
	}
	if !strings.Contains(err.Error(), "10.255.255.1") {
		t.Fatalf("error should name the host: %v", err)
	}
	if got := scrubURL(`Get "`+secret+`": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`, secret); got != "timed out waiting for the calendar server" {
		t.Fatalf("scrubURL = %q", got)
	}
	if got := scrubURL("dial tcp: lookup outlook.office365.com: no such host", secret); strings.Contains(got, "verysecrettoken") {
		t.Fatalf("scrubURL leaked: %q", got)
	}
	if feedHost(secret) != "outlook.office365.com" || feedHost("::not a url") != "calendar" {
		t.Fatalf("feedHost wrong: %q %q", feedHost(secret), feedHost("::not a url"))
	}
}

// Exchange feeds carry Windows zone names and custom VTIMEZONE blocks; both
// must resolve, or events land hours off in the server's local zone.
func TestParseMeetings_WindowsAndCustomTimezones(t *testing.T) {
	const feed = `BEGIN:VCALENDAR
BEGIN:VTIMEZONE
TZID:Customized Time Zone
BEGIN:STANDARD
DTSTART:16010101T000000
TZOFFSETFROM:+0530
TZOFFSETTO:+0530
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
UID:pst@test
DTSTART;TZID=Pacific Standard Time:20261103T090000
DTEND;TZID=Pacific Standard Time:20261103T093000
SUMMARY:West coast standup
END:VEVENT
BEGIN:VEVENT
UID:custom@test
DTSTART;TZID=Customized Time Zone:20261103T140000
DTEND;TZID=Customized Time Zone:20261103T143000
SUMMARY:Custom zone call
END:VEVENT
BEGIN:VEVENT
UID:iana@test
DTSTART;TZID=Asia/Tokyo:20261103T100000
DTEND;TZID=Asia/Tokyo:20261103T103000
SUMMARY:Tokyo sync
END:VEVENT
END:VCALENDAR
`
	from := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	ms, err := ParseMeetings(strings.NewReader(feed), from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byUID := map[string]Meeting{}
	for _, m := range ms {
		byUID[m.UID] = m
	}
	// 09:00 Pacific (standard time, UTC-8 on Nov 3) = 17:00 UTC
	if got := byUID["pst@test"].Start.UTC().Format("15:04"); got != "17:00" {
		t.Fatalf("Pacific Standard Time not mapped: start UTC = %s", got)
	}
	// 14:00 at +05:30 = 08:30 UTC
	if got := byUID["custom@test"].Start.UTC().Format("15:04"); got != "08:30" {
		t.Fatalf("custom VTIMEZONE offset not applied: start UTC = %s", got)
	}
	// 10:00 Tokyo = 01:00 UTC
	if got := byUID["iana@test"].Start.UTC().Format("15:04"); got != "01:00" {
		t.Fatalf("IANA zone wrong: start UTC = %s", got)
	}
	if secs, ok := parseUTCOffset("-0530"); !ok || secs != -(5*3600+30*60) {
		t.Fatalf("parseUTCOffset = %d %v", secs, ok)
	}
}
