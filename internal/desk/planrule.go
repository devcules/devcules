package desk

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Plan recurrence rules. A plan's RRULE comes from the user (the grid
// editor, the API, quick-add) and is expanded on every calendar read
// and written into the ICS export other calendars subscribe to, so it
// is checked on create and update and re-checked when it is read:
//
//   - FREQ is DAILY or coarser. A sub-daily rule is up to 86,400
//     instances a day of window, and rrule-go spins for a long time on
//     sub-daily shapes that never match.
//   - The time of day comes from the plan's start: BYHOUR, BYMINUTE and
//     BYSECOND are refused (their product is a sub-daily rule again),
//     as are the rarely used BYYEARDAY, BYWEEKNO and BYEASTER.
//   - Shapes that never match are refused. rrule-go only checks UNTIL
//     and COUNT when the rule produces an instance, so a rule that never
//     matches iterates to the year 9999 on every read. BYMONTH and
//     BYMONTHDAY must meet; a numbered BYDAY (2MO) is MONTHLY or YEARLY
//     only, at most ±5 and without BYMONTHDAY; BYSETPOS is MONTHLY or
//     YEARLY only; a DAILY or WEEKLY rule with INTERVAL over 1 takes no
//     BY filter but a WEEKLY BYDAY (an interval can step past every day
//     a filter allows). What is left either matches regularly or is a
//     MONTHLY/YEARLY shape whose iteration is cheap; create and update
//     also probe for a first instance and refuse a rule with none.
//   - Shapes rrule-go v1.8.2 gets wrong are refused here too, so a row
//     stored before these checks is refused on read: a BYDAY that mixes
//     numbered and plain days (rrule-go intersects them, so MO,1TU
//     never matches) and a BYSETPOS beyond what one period can hold.
//   - UNTIL is clamped to the end of maxRecurringYear.
//   - The stored string is the re-serialised rule: no control
//     characters, no DTSTART, nothing that can break out of the ICS
//     line it is exported on.
//
// Expansion (planWalker) never hands rrule-go a DTSTART of its own or a
// zero one, starts near the window, and runs 400-year cycles ahead so a
// rule with no further instance stops within 400 years of the window
// instead of at the year 9999. It is capped per plan and per response as
// well, deterministically (an instance count, never a timer).

// ErrInvalidPlan marks a plan the caller must correct (an HTTP 400), as
// opposed to a store failure (a 500 with no detail).
var ErrInvalidPlan = errors.New("invalid plan")

type invalidPlan string

func (e invalidPlan) Error() string        { return string(e) }
func (e invalidPlan) Is(target error) bool { return target == ErrInvalidPlan }

func badPlan(format string, a ...any) error { return invalidPlan(fmt.Sprintf(format, a...)) }

const (
	maxRRuleLen      = 256
	maxRRuleInterval = 1000
	maxRRuleCount    = 5000
	// maxPlanInstances caps one plan's instances in one answer;
	// maxPlanItems caps all plan items in one answer. A valid rule gives
	// at most one instance a day, and a window is at most 400 days.
	maxPlanInstances = 2000
	maxPlanItems     = 2000
	// A recurring plan's start stays within these years, so the
	// iteration from the start to a window stays short.
	minRecurringYear = 1900
	maxRecurringYear = 2200
	// maxWalkInstances caps the instances one walk emits, in the window
	// or before it (a COUNT rule is walked from its start).
	maxWalkInstances = maxRRuleCount + maxPlanInstances + 1000
	// maxAnchorSpan bounds how far before a window the walk starts for
	// an instance that began earlier and is still running.
	maxAnchorSpan = 400 * 24 * time.Hour
)

// maxRuleUntil is the latest UNTIL a plan rule keeps.
var maxRuleUntil = time.Date(maxRecurringYear, 12, 31, 23, 59, 59, 0, time.UTC)

// monthDays is the longest each month gets (February in a leap year).
var monthDays = [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// parsePlanRule checks a stored or submitted RRULE without expanding it.
// An empty rule is (nil, nil). It is cheap, so reads use it too: a rule
// stored before these checks that fails them is shown as its first
// instance only.
func parsePlanRule(s string) (*rrule.ROption, error) {
	if len(s) > maxRRuleLen {
		return nil, badPlan("rrule is longer than %d characters", maxRRuleLen)
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return nil, badPlan("rrule has a control or non-ASCII character")
		}
	}
	s = trimRRule(s)
	if s == "" {
		return nil, nil
	}
	opt, err := rrule.StrToROption(s)
	if err != nil {
		return nil, badPlan("rrule refused: %v", err)
	}
	if !opt.Dtstart.IsZero() {
		return nil, badPlan("rrule must not carry DTSTART: the plan's start begins the series")
	}
	switch opt.Freq {
	case rrule.DAILY, rrule.WEEKLY, rrule.MONTHLY, rrule.YEARLY:
	default:
		return nil, badPlan("rrule FREQ=%v is too fine: a plan repeats daily at most", opt.Freq)
	}
	if len(opt.Byhour)+len(opt.Byminute)+len(opt.Bysecond) > 0 {
		return nil, badPlan("rrule BYHOUR, BYMINUTE and BYSECOND are not supported: the plan's start sets the time")
	}
	if len(opt.Byyearday)+len(opt.Byweekno)+len(opt.Byeaster) > 0 {
		return nil, badPlan("rrule BYYEARDAY, BYWEEKNO and BYEASTER are not supported")
	}
	if opt.Interval < 0 || opt.Interval > maxRRuleInterval {
		return nil, badPlan("rrule INTERVAL must be 1 to %d", maxRRuleInterval)
	}
	if opt.Count < 0 || opt.Count > maxRRuleCount {
		return nil, badPlan("rrule COUNT must be 1 to %d", maxRRuleCount)
	}
	subMonthly := opt.Freq == rrule.DAILY || opt.Freq == rrule.WEEKLY
	if subMonthly && opt.Interval > 1 {
		if len(opt.Bymonth)+len(opt.Bymonthday)+len(opt.Bysetpos) > 0 || (opt.Freq == rrule.DAILY && len(opt.Byweekday) > 0) {
			return nil, badPlan("rrule with INTERVAL over 1 on a daily or weekly plan takes no BY filter (weekly may name BYDAY)")
		}
	}
	if subMonthly && len(opt.Bysetpos) > 0 {
		return nil, badPlan("rrule BYSETPOS needs FREQ=MONTHLY or YEARLY")
	}
	numbered, plain := 0, 0
	for _, wd := range opt.Byweekday {
		if wd.N() == 0 {
			plain++
		} else {
			numbered++
		}
	}
	if numbered > 0 && plain > 0 {
		return nil, badPlan("rrule BYDAY cannot mix numbered and plain days (write two plans)")
	}
	for _, wd := range opt.Byweekday {
		n := wd.N()
		if n == 0 {
			continue
		}
		if subMonthly {
			return nil, badPlan("rrule numbered BYDAY needs FREQ=MONTHLY or YEARLY")
		}
		if n > 5 || n < -5 {
			return nil, badPlan("rrule numbered BYDAY must be within ±5")
		}
		if len(opt.Bymonthday) > 0 {
			return nil, badPlan("rrule numbered BYDAY cannot be combined with BYMONTHDAY")
		}
	}
	if len(opt.Bymonthday) > 0 && !monthsMeetDays(opt.Bymonth, opt.Bymonthday) {
		return nil, badPlan("rrule BYMONTH and BYMONTHDAY never meet")
	}
	if len(opt.Bysetpos) > 0 {
		n := setposBound(opt)
		for _, p := range opt.Bysetpos {
			if p == 0 || p > n || p < -n {
				return nil, badPlan("rrule BYSETPOS must be within ±%d for this rule", n)
			}
		}
	}
	if opt.Until.After(maxRuleUntil) {
		opt.Until = maxRuleUntil
	}
	if _, err := rrule.NewRRule(*opt); err != nil {
		return nil, badPlan("rrule refused: %v", err)
	}
	return opt, nil
}

func trimRRule(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "RRULE:")
}

// monthsMeetDays reports whether some month in months (every month when
// empty) has some day in days (negative days count from the month's end).
func monthsMeetDays(months, days []int) bool {
	if len(months) == 0 {
		months = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	for _, m := range months {
		if m < 1 || m > 12 {
			continue
		}
		for _, d := range days {
			if d < 0 {
				d = -d
			}
			if d >= 1 && d <= monthDays[m] {
				return true
			}
		}
	}
	return false
}

// setposBound is the most candidates one MONTHLY or YEARLY period of
// opt can hold (the plan's start fixes the time, so one a day). A
// BYSETPOS beyond it never matches, and rrule-go would then iterate to
// the year 9999.
func setposBound(opt *rrule.ROption) int {
	months := distinctInts(opt.Bymonth)
	if len(months) == 0 {
		months = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	days := map[int]bool{}
	numbered := false
	for _, wd := range opt.Byweekday {
		days[wd.Day()*100+wd.N()] = true
		numbered = numbered || wd.N() != 0
	}
	if numbered && opt.Freq == rrule.YEARLY && len(opt.Bymonth) == 0 {
		return len(days) // numbered days of the year: one each
	}
	// inMonth is the most candidates month m can hold.
	inMonth := func(m int) int {
		md := 31
		if m >= 1 && m <= 12 {
			md = monthDays[m]
		}
		switch {
		case len(opt.Bymonthday) > 0:
			return min(len(distinctInts(opt.Bymonthday)), md)
		case numbered:
			return len(days) // one day each per month
		case len(days) > 0:
			return min(5*len(days), md)
		default:
			return 1 // the start's day
		}
	}
	total, most := 0, 0
	for _, m := range months {
		n := inMonth(m)
		total += n
		most = max(most, n)
	}
	if opt.Freq == rrule.YEARLY {
		if len(opt.Bymonth) == 0 && len(opt.Bymonthday) == 0 && len(days) == 0 {
			return 1 // the start's date
		}
		return total
	}
	return most
}

func distinctInts(xs []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// normalizePlanRule is the create/update check: parsePlanRule, the
// start's year range, and a probe for an instance within 400 years of
// s0 (the series start as a civil time, see planStart). It answers the
// re-serialised rule to store.
func normalizePlanRule(s string, s0 time.Time, allDay bool, loc *time.Location) (string, error) {
	opt, err := parsePlanRule(s)
	if err != nil || opt == nil {
		return "", err
	}
	if err := recurringStartOK(s0); err != nil {
		return "", err
	}
	next, err := planWalker(*opt, s0, untilCivil(opt, allDay, loc), s0, s0.AddDate(400, 0, 0))
	if err != nil {
		return "", err
	}
	if _, ok := next(); !ok {
		return "", badPlan("rrule has no instance within 400 years of the plan's start")
	}
	return opt.RRuleString(), nil
}

// recurringStartOK refuses a series start outside the recurring years.
// A zero start is refused too: rrule-go reads it as "now".
func recurringStartOK(s0 time.Time) error {
	if y := s0.Year(); s0.IsZero() || y < minRecurringYear || y > maxRecurringYear {
		return badPlan("a recurring plan starts between %d and %d", minRecurringYear, maxRecurringYear)
	}
	return nil
}

// civil answers t's wall clock in loc as a floating time: the same
// fields, held in UTC, so walking it has no DST gaps or zone history.
func civil(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
}

// planStart answers where a plan's series starts as a civil time: the
// date (at midnight) for an all-day plan, the wall clock in loc for a
// timed one. zone is where an all-day plan's midnights live.
func planStart(start time.Time, allDay bool, loc *time.Location) (s0 time.Time, zone *time.Location) {
	if allDay {
		return civilDate(start, loc)
	}
	return civil(start, loc), loc
}

// untilCivil answers the rule's UNTIL as a civil time comparable with
// planStart's (zero when the rule has none). An all-day rule's UNTIL is
// a date, which rrule-go reads as UTC midnight.
func untilCivil(opt *rrule.ROption, allDay bool, loc *time.Location) time.Time {
	if opt.Until.IsZero() {
		return time.Time{}
	}
	if allDay {
		u := opt.Until.UTC()
		return time.Date(u.Year(), u.Month(), u.Day(), u.Hour(), u.Minute(), u.Second(), 0, time.UTC)
	}
	return civil(opt.Until, loc)
}

var pyWeekdays = [7]rrule.Weekday{rrule.SU, rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR, rrule.SA}

// shiftYears moves a civil time by whole years (a multiple of 400 keeps
// every date on the same weekday and Feb 29 a leap day).
func shiftYears(t time.Time, years int) time.Time {
	return time.Date(t.Year()+years, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// planWalker walks opt from the civil start s0 and answers the civil
// instances before toC, in order. Instances before fromC may be
// skipped. The walk is bounded wherever the window is:
//
//   - rrule-go is given a rule built here: never a DTSTART from the rule
//     text and never a zero one (it reads zero as "now"), with the
//     values it would derive from the start (time of day, day of month,
//     month, weekday) written out, so moving the start keeps the series.
//   - Without COUNT the start moves forward by whole periods to just
//     before fromC, so no time is spent before the window.
//   - Everything runs 400·n years ahead so toC lands in the last 400
//     years before rrule-go's year 9999 (the Gregorian calendar repeats
//     every 400 years). A rule with no instance after the window then
//     stops within 400 years instead of iterating to 9999.
//   - At most maxWalkInstances instances are emitted.
func planWalker(opt rrule.ROption, s0, untilC, fromC, toC time.Time) (func() (time.Time, bool), error) {
	if err := recurringStartOK(s0); err != nil {
		return nil, err
	}
	if toC.Year() > rrule.MAXYEAR || !toC.After(s0) {
		return func() (time.Time, bool) { return time.Time{}, false }, nil
	}
	o := opt
	o.Dtstart = time.Time{}
	o.Byhour, o.Byminute, o.Bysecond = []int{s0.Hour()}, []int{s0.Minute()}, []int{s0.Second()}
	if len(o.Byweekno)+len(o.Byyearday)+len(o.Bymonthday)+len(o.Byweekday)+len(o.Byeaster) == 0 {
		switch o.Freq {
		case rrule.YEARLY:
			if len(o.Bymonth) == 0 {
				o.Bymonth = []int{int(s0.Month())}
			}
			o.Bymonthday = []int{s0.Day()}
		case rrule.MONTHLY:
			o.Bymonthday = []int{s0.Day()}
		case rrule.WEEKLY:
			o.Byweekday = []rrule.Weekday{pyWeekdays[s0.Weekday()]}
		}
	}
	start := s0
	if o.Count == 0 {
		start = skipAhead(o, s0, fromC)
	}
	n := (rrule.MAXYEAR - toC.Year()) / 400 * 400
	o.Dtstart = shiftYears(start, n)
	o.Until = time.Date(rrule.MAXYEAR, 12, 31, 23, 59, 59, 0, time.UTC)
	if !untilC.IsZero() && untilC.Year()+n <= rrule.MAXYEAR {
		o.Until = shiftYears(untilC, n)
	}
	r, err := rrule.NewRRule(o)
	if err != nil {
		return nil, badPlan("rrule refused: %v", err)
	}
	if !r.GetDTStart().Equal(o.Dtstart) {
		return nil, badPlan("rrule start did not bind")
	}
	iter := r.Iterator()
	stop := shiftYears(toC, n)
	emitted := 0
	return func() (time.Time, bool) {
		if emitted >= maxWalkInstances {
			return time.Time{}, false
		}
		t, ok := iter()
		if !ok || !t.Before(stop) {
			return time.Time{}, false
		}
		emitted++
		return shiftYears(t, -n), true
	}, nil
}

// skipAhead moves the civil start s0 forward by whole periods of o to a
// period start at or before anchor, keeping the series: the periods stay
// aligned with the original start's, and the values rrule-go would
// derive from the start are already written into o.
func skipAhead(o rrule.ROption, s0, anchor time.Time) time.Time {
	if !s0.Before(anchor) {
		return s0
	}
	iv := o.Interval
	if iv < 1 {
		iv = 1
	}
	h, mi, sec := s0.Clock()
	switch o.Freq {
	case rrule.DAILY, rrule.WEEKLY:
		step := iv
		if o.Freq == rrule.WEEKLY {
			step *= 7
		}
		days := int((anchor.Unix() - s0.Unix()) / 86400)
		return s0.AddDate(0, 0, days/step*step)
	case rrule.MONTHLY:
		months := (anchor.Year()-s0.Year())*12 + int(anchor.Month()-s0.Month())
		k := months/iv - 1
		if k < 1 {
			return s0
		}
		return time.Date(s0.Year(), s0.Month()+time.Month(k*iv), 1, h, mi, sec, 0, time.UTC)
	case rrule.YEARLY:
		k := (anchor.Year()-s0.Year())/iv - 1
		if k < 1 {
			return s0
		}
		return time.Date(s0.Year()+k*iv, 1, 1, h, mi, sec, 0, time.UTC)
	}
	return s0
}

// civilDate answers an all-day plan's calendar date as UTC midnight.
// All-day plans are stored two ways: the web grid sends a date as UTC
// midnight, quick-add stores midnight in the server's zone (stored as
// UTC, so 23:00Z the day before in Europe/Amsterdam). A time exactly at
// UTC midnight is taken as that date; anything else is read in loc.
// zone is where the plan's midnights live, so expanded instances keep
// the stored form.
func civilDate(t time.Time, loc *time.Location) (date time.Time, zone *time.Location) {
	u := t.UTC()
	if u.Hour() == 0 && u.Minute() == 0 && u.Second() == 0 && u.Nanosecond() == 0 {
		return u, time.UTC
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC), loc
}
