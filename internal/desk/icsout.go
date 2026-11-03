package desk

import (
	"fmt"
	"strings"
	"time"
)

// ExportEvent is what the ICS export needs from a desk plan.
type ExportEvent struct {
	UID, Title, Location, URL, Notes, RRule string
	Start, End                              time.Time
	AllDay                                  bool
	Updated                                 time.Time
}

// ICSExport renders desk plans as a calendar other apps can subscribe to.
func ICSExport(name string, events []ExportEvent) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Devcules//Desk calendar//EN\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\n")
	b.WriteString("X-WR-CALNAME:" + icsEscape(name) + "\r\n")
	for _, e := range events {
		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString("UID:" + icsEscape(e.UID) + "@devcules\r\n")
		stamp := e.Updated
		if stamp.IsZero() {
			stamp = time.Now()
		}
		b.WriteString("DTSTAMP:" + stamp.UTC().Format("20060102T150405Z") + "\r\n")
		if e.AllDay {
			b.WriteString("DTSTART;VALUE=DATE:" + e.Start.Format("20060102") + "\r\n")
			b.WriteString("DTEND;VALUE=DATE:" + e.End.Format("20060102") + "\r\n")
		} else {
			b.WriteString("DTSTART:" + e.Start.UTC().Format("20060102T150405Z") + "\r\n")
			b.WriteString("DTEND:" + e.End.UTC().Format("20060102T150405Z") + "\r\n")
		}
		b.WriteString("SUMMARY:" + icsEscape(e.Title) + "\r\n")
		if e.Location != "" {
			b.WriteString("LOCATION:" + icsEscape(e.Location) + "\r\n")
		}
		if e.URL != "" {
			b.WriteString("URL:" + e.URL + "\r\n")
		}
		if e.Notes != "" {
			b.WriteString("DESCRIPTION:" + icsEscape(e.Notes) + "\r\n")
		}
		if e.RRule != "" {
			b.WriteString("RRULE:" + strings.TrimPrefix(e.RRule, "RRULE:") + "\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func icsEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\r\n", `\n`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return fmt.Sprint(s)
}
