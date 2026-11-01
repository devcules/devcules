package desk

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// QuickAdd is a deterministic parser for phrases like
//
//	"standup tomorrow 10:00 30m"      "1-1 with Eze thursday 16:00-16:45"
//	"dentist on 5 nov 8:30"           "weekly sync every monday 10:00 45m remind 15m"
//	"offsite 2026-11-12 all day"      "focus block next tue 14h 2h"
//
// Words that are not understood stay in the title. No model, no network.
type QuickAdd struct {
	Title       string    `json:"title"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	AllDay      bool      `json:"all_day"`
	ReminderMin int       `json:"reminder_min"`
	RRule       string    `json:"rrule,omitempty"`
	// Understood lists what the parser took out of the text, for the UI.
	Understood []string `json:"understood"`
}

var (
	reTimeRange = regexp.MustCompile(`(?i)\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*[-–]\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
	reTime      = regexp.MustCompile(`(?i)\b(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm|h)\b|\b(?:at\s+)?(\d{1,2}):(\d{2})\b`)
	reDur       = regexp.MustCompile(`(?i)\b(?:for\s+)?(\d+(?:\.\d+)?)\s*(m|min|mins|minutes|h|hr|hrs|hours)\b`)
	reISO       = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	reDayMon    = regexp.MustCompile(`(?i)\b(?:on\s+)?(\d{1,2})(?:st|nd|rd|th)?\s+(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\b`)
	reMonDay    = regexp.MustCompile(`(?i)\b(?:on\s+)?(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
	reWeekday   = regexp.MustCompile(`(?i)\b(?:on\s+)?(next\s+)?(mon|tue|wed|thu|fri|sat|sun)[a-z]*\b`)
	reRemind    = regexp.MustCompile(`(?i)\bremind(?:er)?\s+(\d+)\s*(m|min|mins|h|hr)?\b`)
	reEvery     = regexp.MustCompile(`(?i)\b(every\s+(weekday|day|week|month|mon|tue|wed|thu|fri|sat|sun)[a-z]*)\b`)
	reEveryWord = regexp.MustCompile(`(?i)\b(daily|weekly|monthly)\b`)
	reAllDay    = regexp.MustCompile(`(?i)\ball[\s-]?day\b`)
	reToday     = regexp.MustCompile(`(?i)\b(today|tonight)\b`)
	reTomorrow  = regexp.MustCompile(`(?i)\btomorrow\b`)
	reNextWeek  = regexp.MustCompile(`(?i)\bnext\s+week\b`)
)

var months = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12}
var weekdays = map[string]time.Weekday{"mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6, "sun": 0}

// ParseQuickAdd interprets text relative to now in loc.
func ParseQuickAdd(text string, now time.Time, loc *time.Location) (QuickAdd, error) {
	q := QuickAdd{ReminderMin: 10, Understood: []string{}}
	s := " " + strings.Join(strings.Fields(text), " ") + " "
	if strings.TrimSpace(s) == "" {
		return q, fmt.Errorf("nothing to add")
	}
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dateSet := false
	cut := func(m []int, note string) {
		q.Understood = append(q.Understood, note)
		s = s[:m[0]] + " " + s[m[1]:]
	}

	// recurrence first (its weekday must not be read as the date). "every X"
	// wins over a bare daily/weekly/monthly, which may be part of the title
	// ("weekly sync every monday").
	m := reEvery.FindStringSubmatchIndex(s)
	if m == nil {
		m = reEveryWord.FindStringSubmatchIndex(s)
	}
	if m != nil {
		word := strings.ToLower(s[m[2]:m[3]])
		unit := ""
		if len(m) > 5 && m[4] >= 0 {
			unit = strings.ToLower(s[m[4]:m[5]])
		}
		switch {
		case word == "daily" || unit == "day":
			q.RRule = "FREQ=DAILY"
		case word == "weekly" || unit == "week":
			q.RRule = "FREQ=WEEKLY"
		case word == "monthly" || unit == "month":
			q.RRule = "FREQ=MONTHLY"
		case unit == "weekday":
			q.RRule = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
		default:
			if wd, ok := weekdays[unit[:3]]; ok {
				q.RRule = "FREQ=WEEKLY;BYDAY=" + strings.ToUpper([]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[wd])
				// first occurrence: next such weekday (today counts)
				d := day
				for d.Weekday() != wd {
					d = d.AddDate(0, 0, 1)
				}
				day, dateSet = d, true
			}
		}
		cut(m, "repeats "+strings.TrimSpace(strings.ToLower(s[m[0]:m[1]])))
	}
	if m := reRemind.FindStringSubmatchIndex(s); m != nil {
		n, _ := strconv.Atoi(s[m[2]:m[3]])
		if m[4] >= 0 && strings.HasPrefix(strings.ToLower(s[m[4]:m[5]]), "h") {
			n *= 60
		}
		q.ReminderMin = n
		cut(m, fmt.Sprintf("reminder %d min before", n))
	}
	if m := reAllDay.FindStringSubmatchIndex(s); m != nil {
		q.AllDay = true
		cut(m, "all day")
	}
	// dates
	if m := reISO.FindStringSubmatchIndex(s); m != nil {
		y, _ := strconv.Atoi(s[m[2]:m[3]])
		mo, _ := strconv.Atoi(s[m[4]:m[5]])
		d, _ := strconv.Atoi(s[m[6]:m[7]])
		day, dateSet = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc), true
		cut(m, day.Format("Mon 2 Jan 2006"))
	} else if m := reDayMon.FindStringSubmatchIndex(s); m != nil {
		d, _ := strconv.Atoi(s[m[2]:m[3]])
		mo := months[strings.ToLower(s[m[4]:m[5]])[:3]]
		day, dateSet = nextDate(now, mo, d, loc), true
		cut(m, day.Format("Mon 2 Jan"))
	} else if m := reMonDay.FindStringSubmatchIndex(s); m != nil {
		mo := months[strings.ToLower(s[m[2]:m[3]])[:3]]
		d, _ := strconv.Atoi(s[m[4]:m[5]])
		day, dateSet = nextDate(now, mo, d, loc), true
		cut(m, day.Format("Mon 2 Jan"))
	} else if m := reTomorrow.FindStringSubmatchIndex(s); m != nil {
		day, dateSet = day.AddDate(0, 0, 1), true
		cut(m, "tomorrow")
	} else if m := reToday.FindStringSubmatchIndex(s); m != nil {
		dateSet = true
		cut(m, "today")
	} else if m := reWeekday.FindStringSubmatchIndex(s); m != nil {
		next := m[2] >= 0
		wd := weekdays[strings.ToLower(s[m[4]:m[5]])[:3]]
		d := day.AddDate(0, 0, 1) // "monday" means the coming one, never today
		for d.Weekday() != wd {
			d = d.AddDate(0, 0, 1)
		}
		if next && d.Sub(day) < 7*24*time.Hour && d.Weekday() <= day.Weekday() {
			d = d.AddDate(0, 0, 7)
		}
		day, dateSet = d, true
		cut(m, day.Format("Mon 2 Jan"))
	} else if m := reNextWeek.FindStringSubmatchIndex(s); m != nil {
		d := day.AddDate(0, 0, 1)
		for d.Weekday() != time.Monday {
			d = d.AddDate(0, 0, 1)
		}
		day, dateSet = d, true
		cut(m, "next week · "+day.Format("Mon 2 Jan"))
	}
	// times
	var startH, startM, endH, endM = 9, 0, -1, 0
	timeSet := false
	if m := timeRange(s); m != nil {
		startH, startM = clock(s, m, 2, 4, 6)
		endH, endM = clock(s, m, 8, 10, 12)
		timeSet = true
		cut(m, fmt.Sprintf("%02d:%02d–%02d:%02d", startH, startM, endH, endM))
	} else if m := reTime.FindStringSubmatchIndex(s); m != nil {
		if m[2] >= 0 {
			startH, startM = clock(s, m, 2, 4, 6)
		} else {
			startH, _ = strconv.Atoi(s[m[8]:m[9]])
			startM, _ = strconv.Atoi(s[m[10]:m[11]])
		}
		timeSet = true
		cut(m, fmt.Sprintf("at %02d:%02d", startH, startM))
	}
	dur := 60 * time.Minute
	if m := reDur.FindStringSubmatchIndex(s); m != nil {
		n, _ := strconv.ParseFloat(s[m[2]:m[3]], 64)
		if strings.HasPrefix(strings.ToLower(s[m[4]:m[5]]), "h") {
			dur = time.Duration(n * float64(time.Hour))
		} else {
			dur = time.Duration(n * float64(time.Minute))
		}
		cut(m, "for "+dur.String())
	}
	if !dateSet && timeSet {
		// a time earlier than now today means tomorrow
		if t := time.Date(day.Year(), day.Month(), day.Day(), startH, startM, 0, 0, loc); t.Before(now) {
			day = day.AddDate(0, 0, 1)
			q.Understood = append(q.Understood, "tomorrow (that time has passed today)")
		}
	}
	q.Title = strings.TrimSpace(strings.Trim(strings.Join(strings.Fields(s), " "), " ,.-–"))
	if q.Title == "" {
		q.Title = "(untitled)"
	}
	if q.AllDay {
		q.Start = day
		q.End = day.AddDate(0, 0, 1)
		return q, nil
	}
	q.Start = time.Date(day.Year(), day.Month(), day.Day(), startH, startM, 0, 0, loc)
	if endH >= 0 {
		q.End = time.Date(day.Year(), day.Month(), day.Day(), endH, endM, 0, 0, loc)
		if !q.End.After(q.Start) {
			q.End = q.End.AddDate(0, 0, 1)
		}
	} else {
		q.End = q.Start.Add(dur)
	}
	return q, nil
}

// timeRange finds the first "H[:MM][am|pm]-H[:MM][am|pm]" where at least one
// side carries minutes or am/pm; "1-1" or "3-4" are titles, not times.
func timeRange(s string) []int {
	for _, m := range reTimeRange.FindAllStringSubmatchIndex(s, -1) {
		if m[4] >= 0 || m[6] >= 0 || m[10] >= 0 || m[12] >= 0 {
			return m
		}
	}
	return nil
}

// clock reads an hour, optional minutes and optional am/pm/h from match groups.
func clock(s string, m []int, hi, mi, api int) (int, int) {
	h, _ := strconv.Atoi(s[m[hi]:m[hi+1]])
	mn := 0
	if m[mi] >= 0 {
		mn, _ = strconv.Atoi(s[m[mi]:m[mi+1]])
	}
	if m[api] >= 0 {
		switch strings.ToLower(s[m[api]:m[api+1]]) {
		case "pm":
			if h < 12 {
				h += 12
			}
		case "am":
			if h == 12 {
				h = 0
			}
		}
	}
	return h, mn
}

// nextDate is the next occurrence of day/month from now (this year, else next).
func nextDate(now time.Time, mo time.Month, d int, loc *time.Location) time.Time {
	t := time.Date(now.Year(), mo, d, 0, 0, 0, 0, loc)
	if t.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)) {
		t = t.AddDate(1, 0, 0)
	}
	return t
}
