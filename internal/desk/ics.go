package desk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

// Meeting is one calendar instance inside the requested window.
type Meeting struct {
	UID      string    `json:"uid"`
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	AllDay   bool      `json:"all_day,omitempty"`
	Location string    `json:"location,omitempty"`
	URL      string    `json:"url,omitempty"`  // meeting link: URL property, else first https link in location/description
	Feed     string    `json:"feed,omitempty"` // label of the calendar feed it came from (set by the API layer)
}

// FetchICSRaw downloads an ICS feed (≤8 MB) and returns its text, so one
// download can be expanded over any range.
func FetchICSRaw(ctx context.Context, hc *http.Client, icsURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(icsURL), nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ics: %s: %s", feedHost(icsURL), scrubURL(err.Error(), icsURL))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("ics: %s: HTTP %d", feedHost(icsURL), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("ics: %s: %s", feedHost(icsURL), err.Error())
	}
	return string(b), nil
}

// FetchMeetings downloads an ICS feed and expands it into [from, to).
func FetchMeetings(ctx context.Context, hc *http.Client, icsURL string, from, to time.Time) ([]Meeting, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(icsURL), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ics: %s: %s", feedHost(icsURL), scrubURL(err.Error(), icsURL))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("ics: %s: HTTP %d", feedHost(icsURL), resp.StatusCode)
	}
	return ParseMeetings(io.LimitReader(resp.Body, 8<<20), from, to)
}

// feedHost is the only part of a calendar address that may appear in an
// error: the full URL is the secret that grants read access to the calendar.
func feedHost(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Host != "" {
		return u.Host
	}
	return "calendar"
}

// scrubURL removes the secret address (and net/http's quoted form) from an
// error message, and shortens the usual timeout wording.
func scrubURL(msg, raw string) string {
	raw = strings.TrimSpace(raw)
	for _, v := range []string{`"` + raw + `"`, raw} {
		msg = strings.ReplaceAll(msg, v, "<calendar address>")
	}
	msg = strings.ReplaceAll(msg, "Get <calendar address>: ", "")
	if strings.Contains(msg, "Client.Timeout exceeded") || strings.Contains(msg, "context deadline exceeded") {
		return "timed out waiting for the calendar server"
	}
	return msg
}

// ParseMeetings expands single and recurring VEVENTs (RRULE, EXDATE,
// RECURRENCE-ID overrides) into instances overlapping [from, to), sorted.
func ParseMeetings(r io.Reader, from, to time.Time) ([]Meeting, error) {
	cal, err := ics.ParseCalendar(r)
	if err != nil {
		return nil, fmt.Errorf("ics: parse: %w", err)
	}
	tz := newTZResolver(cal)
	// overrides: UID -> set of RECURRENCE-ID starts that have their own event
	overrides := map[string]map[int64]bool{}
	for _, e := range cal.Events() {
		if p := e.GetProperty(ics.ComponentPropertyRecurrenceId); p != nil {
			t, _, err := tz.time(p.Value, p.ICalParameters)
			if err == nil {
				if overrides[e.Id()] == nil {
					overrides[e.Id()] = map[int64]bool{}
				}
				overrides[e.Id()][t.Unix()] = true
			}
		}
	}
	var out []Meeting
	for _, e := range cal.Events() {
		if st := propValue(e, ics.ComponentPropertyStatus); strings.EqualFold(st, "CANCELLED") {
			continue
		}
		startP := e.GetProperty(ics.ComponentPropertyDtStart)
		if startP == nil {
			continue
		}
		start, allDay, err := tz.time(startP.Value, startP.ICalParameters)
		if err != nil {
			continue
		}
		var dur time.Duration
		if endP := e.GetProperty(ics.ComponentPropertyDtEnd); endP != nil {
			if end, _, err := tz.time(endP.Value, endP.ICalParameters); err == nil {
				dur = end.Sub(start)
			}
		} else if durP := e.GetProperty(ics.ComponentPropertyDuration); durP != nil {
			dur = icsDuration(durP.Value)
		}
		if dur <= 0 {
			if allDay {
				dur = 24 * time.Hour
			} else {
				dur = time.Hour
			}
		}
		base := Meeting{
			UID:      e.Id(),
			Title:    unescapeICS(propValue(e, ics.ComponentPropertySummary)),
			AllDay:   allDay,
			Location: unescapeICS(propValue(e, ics.ComponentPropertyLocation)),
		}
		base.URL = meetingURL(propValue(e, ics.ComponentPropertyUrl), base.Location, propValue(e, ics.ComponentPropertyDescription))
		if base.Title == "" {
			base.Title = "(no title)"
		}

		rr := e.GetProperty(ics.ComponentPropertyRrule)
		isOverride := e.GetProperty(ics.ComponentPropertyRecurrenceId) != nil
		if rr == nil || isOverride {
			end := start.Add(dur)
			if end.After(from) && start.Before(to) {
				m := base
				m.Start, m.End = start, end
				out = append(out, m)
			}
			continue
		}
		rule, err := rrule.StrToRRule(strings.TrimPrefix(rr.Value, "RRULE:"))
		if err != nil {
			continue
		}
		set := rrule.Set{}
		set.DTStart(start)
		set.RRule(rule)
		for _, ex := range e.GetProperties(ics.ComponentPropertyExdate) {
			for _, v := range strings.Split(ex.Value, ",") {
				if t, _, err := tz.time(strings.TrimSpace(v), ex.ICalParameters); err == nil {
					set.ExDate(t)
				}
			}
		}
		// instances that START before `from` but still overlap it are found by
		// widening the window by the duration
		for _, st := range set.Between(from.Add(-dur), to, true) {
			if overrides[e.Id()][st.Unix()] {
				continue
			}
			end := st.Add(dur)
			if !end.After(from) || !st.Before(to) {
				continue
			}
			m := base
			m.Start, m.End = st, end
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func propValue(e *ics.VEvent, p ics.ComponentProperty) string {
	if v := e.GetProperty(p); v != nil {
		return v.Value
	}
	return ""
}

// tzResolver turns a TZID parameter into a location. Outlook/Exchange feeds
// use Windows names ("W. Europe Standard Time", "Pacific Standard Time")
// that time.LoadLocation does not know; without this every such event
// would silently land in the server's local zone, hours off.
// Order: IANA name → Windows name mapping → the feed's own VTIMEZONE block
// (fixed standard offset) → server local time.
type tzResolver struct {
	cache  map[string]*time.Location
	custom map[string]*time.Location // from VTIMEZONE blocks, keyed by TZID
}

func newTZResolver(cal *ics.Calendar) *tzResolver {
	r := &tzResolver{cache: map[string]*time.Location{}, custom: map[string]*time.Location{}}
	for _, c := range cal.Components {
		vt, ok := c.(*ics.VTimezone)
		if !ok {
			continue
		}
		id := ""
		if p := vt.GetProperty(ics.ComponentPropertyTzid); p != nil {
			id = p.Value
		}
		if id == "" {
			continue
		}
		// prefer the STANDARD sub-component's offset; DAYLIGHT only if that is all there is
		var std, dl string
		for _, sub := range vt.Components {
			switch x := sub.(type) {
			case *ics.Standard:
				if p := x.GetProperty(ics.ComponentProperty(ics.PropertyTzoffsetto)); p != nil {
					std = p.Value
				}
			case *ics.Daylight:
				if p := x.GetProperty(ics.ComponentProperty(ics.PropertyTzoffsetto)); p != nil {
					dl = p.Value
				}
			}
		}
		off := std
		if off == "" {
			off = dl
		}
		if secs, ok := parseUTCOffset(off); ok {
			r.custom[id] = time.FixedZone(id, secs)
		}
	}
	return r
}

// Unresolved counts TZIDs that fell back to local time (for diagnostics).
var Unresolved = map[string]int{}

func (r *tzResolver) location(tzid string) *time.Location {
	if tzid == "" {
		return time.Local
	}
	if l, ok := r.cache[tzid]; ok {
		return l
	}
	var loc *time.Location
	if l, err := time.LoadLocation(tzid); err == nil {
		loc = l
	} else if l := ics.WindowsTimezoneToIANA(tzid); l != nil {
		loc = l
	} else if l, ok := r.custom[tzid]; ok {
		loc = l
	} else {
		Unresolved[tzid]++
		loc = time.Local
	}
	r.cache[tzid] = loc
	return loc
}

// time parses DATE-TIME (local, UTC "Z", or TZID=) and DATE values.
func (r *tzResolver) time(v string, params map[string][]string) (time.Time, bool, error) {
	v = strings.TrimSpace(v)
	loc := time.Local
	if tz := params["TZID"]; len(tz) > 0 && tz[0] != "" {
		loc = r.location(tz[0])
	}
	if vt := params["VALUE"]; len(vt) > 0 && strings.EqualFold(vt[0], "DATE") || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v, loc)
		return t, true, err
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t, false, err
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t, false, err
}

// parseUTCOffset reads "+0100", "-0530", "+01:00" into seconds.
func parseUTCOffset(s string) (int, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ":", "")
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	var h, m int
	if _, err := fmt.Sscanf(s[1:], "%02d%02d", &h, &m); err != nil {
		return 0, false
	}
	secs := h*3600 + m*60
	if s[0] == '-' {
		secs = -secs
	}
	return secs, true
}

// icsDuration parses the subset of ISO 8601 durations ICS uses (PnDTnHnMnS).
func icsDuration(v string) time.Duration {
	v = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(v)), "P")
	var d time.Duration
	num := 0
	inTime := false
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			num = num*10 + int(c-'0')
		case c == 'T':
			inTime = true
		case c == 'W':
			d += time.Duration(num) * 7 * 24 * time.Hour
			num = 0
		case c == 'D':
			d += time.Duration(num) * 24 * time.Hour
			num = 0
		case c == 'H':
			d += time.Duration(num) * time.Hour
			num = 0
		case c == 'M' && inTime:
			d += time.Duration(num) * time.Minute
			num = 0
		case c == 'S':
			d += time.Duration(num) * time.Second
			num = 0
		}
	}
	return d
}

func unescapeICS(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\,`, ",")
	s = strings.ReplaceAll(s, `\;`, ";")
	return strings.ReplaceAll(s, `\\`, `\`)
}

// meetingURL picks the join link: the URL property, else the first https
// link found in the location or description (Google/Zoom/Teams put it there).
func meetingURL(urlProp, location, description string) string {
	if u := strings.TrimSpace(urlProp); strings.HasPrefix(u, "http") {
		return u
	}
	for _, txt := range []string{location, unescapeICS(description)} {
		for _, f := range strings.FieldsFunc(txt, func(r rune) bool { return r == ' ' || r == '\n' || r == '<' || r == '>' || r == '"' }) {
			if strings.HasPrefix(f, "https://") {
				return strings.TrimRight(f, ".,;)")
			}
		}
	}
	return ""
}
