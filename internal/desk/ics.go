package desk

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
	return ParseMeetingsContext(ctx, io.LimitReader(resp.Body, 8<<20), from, to)
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

// Bounds on what one third-party feed can make the server expand.
const (
	maxSeriesInstances = 1000    // instances of one recurring VEVENT inside the window
	maxSeriesSteps     = 200_000 // occurrences walked per series (from DTSTART), so an old dense series cannot spin
	maxFeedMeetings    = 5000    // instances from one feed (and per merged response): the earliest ones are kept

	// Rule shapes that are walked at all (see walkableRule); anything
	// else shows only its first instance.
	maxRuleInterval = 1000 // INTERVAL
	maxRuleList     = 31   // entries in any one BY* list (duplicates are allowed by the parser)
	maxRuleTimes    = 24   // times per day, |BYHOUR|x|BYMINUTE|x|BYSECOND|, for DAILY and coarser
)

// expandLimits bound the work of one feed's expansion. The step budget is
// the main bound; the deadline backs it up on a loaded box. Both are
// checked between iterator calls, and walkableRule plus ruleIsEmpty keep
// every single iterator call short (see expandSeries).
type expandLimits struct {
	feedSteps int           // occurrences walked over all series of one feed
	feedTime  time.Duration // wall time for expanding one feed's recurring series
}

var defaultExpandLimits = expandLimits{feedSteps: 5_000_000, feedTime: 2 * time.Second}

// civilDays counts the calendar days between two dates (DST-proof).
func civilDays(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return int(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
}

// sortMeetings orders by start (stable, so equal starts keep file order).
func sortMeetings(ms []Meeting) {
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Start.Before(ms[j].Start) })
}

// seriesWalk is one recurring VEVENT to expand into [after, until).
type seriesWalk struct {
	set       *rrule.Set
	base      Meeting
	endOf     func(time.Time) time.Time
	from      time.Time      // an instance must end after this
	after     time.Time      // from widened by the longest span: earlier starts cannot overlap
	until     time.Time      // window end, or the feed cutoff once the cap is full
	overrides map[int64]bool // RECURRENCE-ID starts that have their own VEVENT
	maxSteps  int
	stop      func() bool // checked before every iterator call: deadline or ctx
}

type seriesResult struct {
	ms      []Meeting
	steps   int
	stopped bool // stop() fired: the walk is incomplete and its instances are dropped
}

// run walks the series lazily and bounded (set.Between would build the
// whole list first). The rule has passed walkableRule and ruleIsEmpty, so
// each iterator call returns within about one 400-year calendar cycle of
// periods, and stop is checked before every call.
func (w *seriesWalk) run() seriesResult {
	var res seriesResult
	if w.stop() {
		res.stopped = true
		return res
	}
	next := w.set.Iterator() // the first occurrence is computed here already
	for res.steps < w.maxSteps && len(res.ms) < maxSeriesInstances {
		if res.steps > 0 && w.stop() {
			res.stopped = true
			break
		}
		st, ok := next()
		res.steps++
		if !ok || !st.Before(w.until) {
			break
		}
		if st.Before(w.after) || w.overrides[st.Unix()] {
			continue
		}
		end := w.endOf(st)
		if !end.After(w.from) {
			continue
		}
		m := w.base
		m.Start, m.End = st, end
		res.ms = append(res.ms, m)
	}
	if res.stopped {
		res.ms = nil
	}
	return res
}

// walkableRule says whether a recurrence rule from an untrusted feed may be
// handed to the rrule iterator. rrule-go (v1.8.2) has shapes that panic,
// loop forever, or allocate without bound inside a single iterator call,
// where no step count or deadline can reach them; those, and shapes no
// calendar app writes for a meeting, show only their first instance.
// reason is empty when the rule may be walked.
func walkableRule(o rrule.ROption, start time.Time) (reason string) {
	n := max(o.Interval, 1)
	switch {
	case o.Freq >= rrule.MINUTELY:
		// 86,400 instances a day, and the iterator's minute and second
		// loops never end when INTERVAL cannot reach BYMINUTE/BYSECOND.
		return "sub-hourly"
	case n > maxRuleInterval:
		// MONTHLY's month counter overflows (index panic) for huge values.
		return "interval"
	case len(o.Byeaster) > 0:
		// Not RFC 5545; an offset past the year indexes out of range (panic).
		return "byeaster"
	}
	for _, l := range [][]int{o.Bysetpos, o.Bymonth, o.Bymonthday, o.Byyearday, o.Byweekno, o.Byhour, o.Byminute, o.Bysecond} {
		if len(l) > maxRuleList {
			return "list"
		}
	}
	if len(o.Byweekday) > maxRuleList {
		return "list"
	}
	// An nth weekday inside a month (BYDAY=-53MO with MONTHLY, or YEARLY
	// with BYMONTH) indexes before or past the year's masks (panic). A
	// month holds at most 5 of any weekday.
	if o.Freq == rrule.MONTHLY || o.Freq == rrule.YEARLY && len(o.Bymonth) > 0 {
		for _, wd := range o.Byweekday {
			if k := wd.N(); k > 5 || k < -5 {
				return "byday-nth"
			}
		}
	}
	count := func(l []int) int { return max(len(l), 1) } // an empty list means DTSTART's value
	if o.Freq == rrule.HOURLY {
		if len(o.Bysetpos) > 0 {
			// BYSETPOS past an hour's set never matches, and the iterator
			// then rebuilds every hour up to year 9999 (minutes per call).
			return "hourly-bysetpos"
		}
		if count(o.Byminute)*count(o.Bysecond) > 1 {
			return "sub-hourly"
		}
		if 24%n != 0 {
			// Its periods only repeat after 400*n years (beyond year
			// 9999), and without whole days the iterator cannot skip a
			// day that does not match: ruleIsEmpty would walk millions
			// of hours.
			return "hourly-interval"
		}
		if len(o.Byhour) > 0 {
			// The hour advances by INTERVAL mod 24, so it only ever takes
			// values congruent to DTSTART's hour mod gcd(INTERVAL, 24);
			// with no BYHOUR among them the iterator loops forever.
			g, h0 := gcd(n, 24), start.Hour()
			reach := false
			for _, h := range o.Byhour {
				if (h-h0)%g == 0 {
					reach = true
				}
			}
			if !reach {
				return "byhour-unreachable"
			}
		}
	} else if count(o.Byhour)*count(o.Byminute)*count(o.Bysecond) > maxRuleTimes {
		// Every time of a period is built in one call: 86,400 a day,
		// times 366 days for YEARLY, before the first one is returned.
		return "times"
	}
	return ""
}

// seriesRule builds a VEVENT's recurrence rule from its RRULE value with
// DTSTART set to the VEVENT's own start: the one walkableRule and
// ruleIsEmpty classify. rrule-go accepts a DTSTART= part inside the RRULE
// value, and a rule carrying its own DTSTART overrides the set's
// (Set.RRule), so the walk would start somewhere else (another hour, or
// another time altogether); it is ignored here.
func seriesRule(value string, start time.Time) (*rrule.RRule, error) {
	o, err := rrule.StrToROption(value)
	if err != nil {
		return nil, err
	}
	o.Dtstart = start
	return rrule.NewRRule(*o)
}

// seriesStartReason refuses starts rrule-go cannot walk from as given.
// It treats the zero time as unset and walks from time.Now() instead (a
// clock-dependent start nothing classified), and its year search stops
// at 9999 (MAXYEAR), so starts outside years 1 to 9998 (in the start's
// own zone or in UTC) are shown as their first instance only.
func seriesStartReason(start time.Time) string {
	if start.IsZero() {
		return "dtstart-zero"
	}
	for _, y := range []int{start.Year(), start.UTC().Year()} {
		if y < 1 || y > rrule.MAXYEAR-1 {
			return "dtstart-range"
		}
	}
	return ""
}

// walksFrom says whether the set will walk from exactly start: the
// effective start the bound rule uses (after rrule-go's defaults and
// truncation) and the set's own.
func walksFrom(set *rrule.Set, start time.Time) bool {
	r := set.GetRRule()
	return r != nil && r.Options.Dtstart.Equal(start) && r.OrigOptions.Dtstart.Equal(start) && set.GetDTStart().Equal(start)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// ruleIsEmpty says whether a walkable rule can never produce an instance
// (FREQ=DAILY;BYMONTH=2;BYMONTHDAY=30, or BYSETPOS past the set). rrule-go
// checks UNTIL and COUNT only against instances it finds, so on such a
// rule a single iterator call searches period by period up to year 9999:
// seconds to minutes, depending on the shape.
//
// The Gregorian calendar repeats exactly every 400 years (146,097 days, a
// whole number of weeks), and RRULE semantics depend on nothing else
// (BYEASTER is refused by walkableRule). So the rule's periods repeat
// every R = 400*n/gcd(C, n) years, where n is INTERVAL and C the number
// of periods in 400 years; a rule that matches at all matches within R
// plus one period of DTSTART. The probe moves DTSTART forward by whole
// 400-year cycles until just that much room is left before year 9999 and
// asks for the first instance (without COUNT or UNTIL): it either finds
// one, or the iterator stops at 9999. Either way one call walks at most
// about two cycles of periods. Once a rule is known to match, the gap
// between two of its instances is at most about one cycle too, so every
// call of the real walk is short.
func ruleIsEmpty(o rrule.ROption, start time.Time) (bool, error) {
	n := max(o.Interval, 1)
	var cycle, perYears int // periods in 400 years; one period, in whole years (rounded up)
	switch o.Freq {
	case rrule.YEARLY:
		cycle, perYears = 400, n
	case rrule.MONTHLY:
		cycle, perYears = 4800, (n+11)/12
	case rrule.WEEKLY:
		cycle, perYears = 146097/7, (7*n+364)/365
	case rrule.DAILY:
		cycle, perYears = 146097, (n+364)/365
	default: // HOURLY (walkableRule refuses finer)
		cycle, perYears = 146097*24, 1
	}
	if len(o.Bysetpos) > 0 {
		// BYSETPOS picks from one period's set: days in the period times
		// the times of day. When every position lies past the largest
		// such set, nothing ever matches (FREQ=DAILY;BYSETPOS=2); say so
		// without walking.
		days := map[rrule.Frequency]int{rrule.YEARLY: 366, rrule.MONTHLY: 31, rrule.WEEKLY: 7}[o.Freq]
		size := max(days, 1) * max(len(o.Byhour), 1) * max(len(o.Byminute), 1) * max(len(o.Bysecond), 1)
		inRange := false
		for _, p := range o.Bysetpos {
			if p <= size && -p <= size {
				inRange = true
			}
		}
		if !inRange {
			return true, nil
		}
	}
	repeat := 400 * (n / gcd(cycle, n)) // R, in years
	if last := rrule.MAXYEAR - repeat - perYears - 2; start.Year() < last {
		start = start.AddDate(400*((last-start.Year())/400), 0, 0)
	}
	o.Dtstart = start
	o.Count = 0
	o.Until = time.Date(rrule.MAXYEAR+1, 1, 1, 0, 0, 0, 0, time.UTC) // never reached: the year limit ends the search
	r, err := rrule.NewRRule(o)
	if err != nil {
		return false, err
	}
	_, ok := r.Iterator()()
	return !ok, nil
}

// ParseMeetings expands single and recurring VEVENTs (RRULE, EXDATE,
// RECURRENCE-ID overrides) into instances overlapping [from, to), sorted.
// See ParseMeetingsContext for the bounds.
func ParseMeetings(r io.Reader, from, to time.Time) ([]Meeting, error) {
	return ParseMeetingsContext(context.Background(), r, from, to)
}

// ParseMeetingsContext is ParseMeetings under ctx. A third-party feed is
// untrusted, so the expansion is bounded: rule shapes the iterator cannot
// walk safely show only their first instance (walkableRule), rules that
// can never match show nothing (ruleIsEmpty), and the walk takes at most
// maxSeriesInstances per series, maxSeriesSteps per series and a step
// budget and deadline per feed (series past those are skipped; single
// events are still read). Nothing runs after it returns. The result holds
// the EARLIEST maxFeedMeetings instances: once twice that many are
// collected they are sorted and trimmed, and later instances starting at
// or after the trimmed set's last start are not collected (series walks
// stop there).
func ParseMeetingsContext(ctx context.Context, r io.Reader, from, to time.Time) ([]Meeting, error) {
	ms, _, err := parseMeetings(ctx, r, from, to, defaultExpandLimits)
	return ms, err
}

// expandStats says how a feed's recurring series were expanded.
type expandStats struct {
	steps     int // occurrences walked over all series
	skipped   int // recurring series not expanded: the feed's step budget or deadline was spent
	collapsed int // rule shapes shown as their first instance only (walkableRule)
	empty     int // rules that can never match (ruleIsEmpty)
	failed    int // the iterator panicked on the rule
}

func parseMeetings(ctx context.Context, r io.Reader, from, to time.Time, lim expandLimits) ([]Meeting, expandStats, error) {
	var stats expandStats
	cal, err := ics.ParseCalendar(r)
	if err != nil {
		return nil, stats, fmt.Errorf("ics: parse: %w", err)
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
	var cutoff time.Time // set once the cap is full: nothing starting at or after it can make the result
	trim := func() {
		sortMeetings(out)
		if len(out) > maxFeedMeetings {
			out = out[:maxFeedMeetings]
		}
		cutoff = out[len(out)-1].Start
	}
	// The feed deadline rides on a context: checking it is a cheap
	// ctx.Err() before every iterator call (time.Now costs about as much
	// as a step here). cancel stops its timer when the parse returns.
	expandCtx, cancel := context.WithTimeout(ctx, lim.feedTime)
	defer cancel()
	spent := func() bool { return expandCtx.Err() != nil }
	for _, e := range cal.Events() {
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
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
		days := 0 // all-day events: length in calendar days, not hours
		if endP := e.GetProperty(ics.ComponentPropertyDtEnd); endP != nil {
			if end, _, err := tz.time(endP.Value, endP.ICalParameters); err == nil {
				dur = end.Sub(start)
				if allDay {
					days = civilDays(start, end)
				}
			}
		} else if durP := e.GetProperty(ics.ComponentPropertyDuration); durP != nil {
			dur = icsDuration(durP.Value)
			if allDay {
				days = int(dur / (24 * time.Hour))
			}
		}
		if dur <= 0 {
			if allDay {
				dur = 24 * time.Hour
			} else {
				dur = time.Hour
			}
		}
		if allDay && days <= 0 {
			days = 1
		}
		// endOf gives an instance's end. For all-day events it counts
		// calendar days: a fixed duration taken from an instance spanning
		// a DST change (23 h or 25 h) would make every later instance end
		// an hour off midnight and bleed into the next day.
		endOf := func(st time.Time) time.Time {
			if allDay {
				return st.AddDate(0, 0, days)
			}
			return st.Add(dur)
		}
		if allDay {
			dur = time.Duration(days)*24*time.Hour + time.Hour // the longest such span, for window widening
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
		until := to
		if !cutoff.IsZero() && cutoff.Before(until) {
			until = cutoff
		}

		rr := e.GetProperty(ics.ComponentPropertyRrule)
		isOverride := e.GetProperty(ics.ComponentPropertyRecurrenceId) != nil
		var rule *rrule.RRule
		if rr != nil && !isOverride {
			if rule, err = seriesRule(strings.TrimPrefix(rr.Value, "RRULE:"), start); err != nil {
				continue
			}
			why := seriesStartReason(start)
			if why == "" {
				why = walkableRule(rule.OrigOptions, start)
			}
			if why != "" {
				stats.collapsed++
				slog.Debug("ics: recurrence shown as its first instance", "uid", base.UID, "rule", why)
				rule = nil
			}
		}
		if rule == nil {
			end := endOf(start)
			if end.After(from) && start.Before(until) {
				m := base
				m.Start, m.End = start, end
				out = append(out, m)
				if len(out) >= 2*maxFeedMeetings {
					trim()
				}
			}
			continue
		}
		if stats.steps >= lim.feedSteps || spent() {
			if err := ctx.Err(); err != nil {
				return nil, stats, err
			}
			stats.skipped++ // expansion budget spent: single events are still read
			continue
		}
		set := rrule.Set{}
		set.DTStart(start)
		set.RRule(rule)
		if !walksFrom(&set, start) {
			// Never walk a start other than the one classified and probed:
			// on another hour an HOURLY BYHOUR the classifier found
			// reachable may not be, and the iterator then never returns.
			stats.collapsed++
			slog.Debug("ics: recurrence shown as its first instance", "uid", base.UID, "rule", "dtstart-mismatch")
			if end := endOf(start); end.After(from) && start.Before(until) {
				m := base
				m.Start, m.End = start, end
				out = append(out, m)
				if len(out) >= 2*maxFeedMeetings {
					trim()
				}
			}
			continue
		}
		for _, ex := range e.GetProperties(ics.ComponentPropertyExdate) {
			for _, v := range strings.Split(ex.Value, ",") {
				if t, _, err := tz.time(strings.TrimSpace(v), ex.ICalParameters); err == nil {
					set.ExDate(t)
				}
			}
		}
		// Instances that START before `from` but still overlap it are
		// found by widening the window by the duration.
		w := &seriesWalk{
			set: &set, base: base, endOf: endOf,
			from: from, after: from.Add(-dur), until: until,
			overrides: overrides[e.Id()],
			maxSteps:  min(maxSeriesSteps, lim.feedSteps-stats.steps),
			stop:      spent,
		}
		res, empty, perr := expandSeries(w, rule.OrigOptions, start)
		switch {
		case perr != nil:
			stats.failed++
			slog.Warn("ics: recurrence rule broke the expander, skipped", "uid", base.UID, "err", perr)
			continue
		case empty:
			stats.empty++
			continue
		case res.stopped:
			if err := ctx.Err(); err != nil {
				return nil, stats, err
			}
			stats.skipped++
		}
		stats.steps += res.steps
		out = append(out, res.ms...)
		if len(out) >= 2*maxFeedMeetings {
			trim()
		}
	}
	if stats.skipped > 0 || stats.failed > 0 {
		// A feed this heavy loses events silently otherwise.
		slog.Warn("ics: feed expansion budget spent, recurring events skipped",
			"skipped", stats.skipped, "steps", stats.steps, "failed", stats.failed,
			"collapsed", stats.collapsed, "empty", stats.empty)
	}
	if len(out) > 0 {
		trim()
	}
	return out, stats, nil
}

// expandSeries probes the rule for emptiness and walks it, on the calling
// goroutine. A panic inside rrule-go (it has some on hostile input;
// walkableRule refuses the known ones) is returned as an error, so one
// rule cannot take the process down.
func expandSeries(w *seriesWalk, o rrule.ROption, start time.Time) (res seriesResult, empty bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, empty, err = seriesResult{}, false, fmt.Errorf("rrule panic: %v", p)
		}
	}()
	if empty, err = ruleIsEmpty(o, start); err != nil || empty {
		return res, empty, err
	}
	return w.run(), false, nil
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
		// Unknown zone: fall back to local time. The resolver is per parse,
		// so this is logged once per TZID per feed load (no shared state:
		// parses run concurrently on request goroutines).
		slog.Debug("ics: unknown TZID, using local time", "tzid", tzid)
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
