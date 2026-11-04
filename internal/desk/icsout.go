package desk

import (
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
		if e.URL != "" && icsLineSafe(e.URL) {
			b.WriteString("URL:" + e.URL + "\r\n")
		}
		if e.Notes != "" {
			b.WriteString("DESCRIPTION:" + icsEscape(e.Notes) + "\r\n")
		}
		if e.RRule != "" && icsLineSafe(e.RRule) {
			b.WriteString("RRULE:" + strings.TrimPrefix(e.RRule, "RRULE:") + "\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// icsLineSafe reports whether a value written unescaped (a URL, an
// RRULE) stays on its own content line: no control characters, so no
// CR or LF can start a new property or VEVENT. An unsafe value is left
// out of the export.
func icsLineSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// icsEscape writes s as an RFC 5545 TEXT value on one content line.
// Every line break (CRLF, a lone LF or a lone CR) becomes the escaped
// \n, and every other control character (C0, DEL, C1) except HTAB is
// dropped: TEXT may not carry them, and a lenient reader would take a
// bare CR as a new line, so a title could start a new property or
// VEVENT.
func icsEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ';':
			b.WriteString(`\;`)
		case r == ',':
			b.WriteString(`\,`)
		case r == '\r':
			if i+1 < len(s) && s[i+1] == '\n' {
				continue // the LF writes the break
			}
			b.WriteString(`\n`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
