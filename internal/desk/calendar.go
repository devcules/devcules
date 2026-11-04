package desk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
)

// calendarItem is one visible event: a desk plan (own row) or an ICS feed
// meeting, expanded over the requested range by the route. Occurrence marks
// a recurrence instance so an edit can say "the whole series"; an
// occurrence also carries the series' own stored start and end, so the
// editor can shift or keep the series rather than restart it at the
// occurrence it was opened from.
type calendarItem struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Start       time.Time  `json:"start"`
	End         time.Time  `json:"end"`
	AllDay      bool       `json:"all_day"`
	Location    string     `json:"location,omitempty"`
	URL         string     `json:"url,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	ReminderMin int        `json:"reminder_min,omitempty"`
	RRule       string     `json:"rrule,omitempty"`
	Feed        string     `json:"feed,omitempty"`
	Plan        bool       `json:"plan,omitempty"`
	Occurrence  bool       `json:"occurrence,omitempty"`
	SeriesStart *time.Time `json:"series_start,omitempty"`
	SeriesEnd   *time.Time `json:"series_end,omitempty"`
}

// CalendarService adds the user's own plans (stored) on top of the desk
// Service's feeds. One owner per devcules; no per-user rows.
type CalendarService struct {
	Desk  *Service
	Store store.Store
	// Loc is the user's zone: plans expand, and all-day plans export,
	// in it. Nil means the server's zone (time.Local), the zone
	// quick-add already parses in.
	Loc *time.Location
	hc  *http.Client
}

func NewCalendarService(d *Service, st store.Store, hc *http.Client) *CalendarService {
	return &CalendarService{Desk: d, Store: st, hc: hc}
}

func newEventID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ev_" + hex.EncodeToString(b)
}

// expandPlans turns stored plans into visible items over [from, to],
// expanding each RRule into instances in the server's zone.
func expandPlans(plans []*store.CalendarEvent, from, to time.Time) []calendarItem {
	out, _ := expandPlansIn(plans, from, to, time.Local)
	return out
}

// expandPlansIn expands in loc, the user's zone: a weekly 09:00 plan
// stays at 09:00 local across a DST change (plans are stored in UTC).
// A plan with no recurrence is one item; a recurring one is stamped
// Occurrence so the editor knows. An all-day plan recurs by calendar
// date. A stored rule that fails parsePlanRule, or a series that starts
// outside the recurring years, shows as its first instance only.
// truncated reports a cap was hit (maxPlanInstances per plan,
// maxPlanItems in all).
func expandPlansIn(plans []*store.CalendarEvent, from, to time.Time, loc *time.Location) (out []calendarItem, truncated bool) {
	out, truncated, _ = expandPlansCtx(context.Background(), plans, from, to, loc)
	return out, truncated
}

// expandPlansCtx is expandPlansIn that stops when ctx is done. Each
// plan's walk is bounded (planWalker) wherever the window sits.
func expandPlansCtx(ctx context.Context, plans []*store.CalendarEvent, from, to time.Time, loc *time.Location) (out []calendarItem, truncated bool, err error) {
	out = []calendarItem{}
	add := func(it calendarItem) bool {
		if len(out) >= maxPlanItems {
			truncated = true
			return false
		}
		out = append(out, it)
		return true
	}
	for _, e := range plans {
		if err := ctx.Err(); err != nil {
			return out, truncated, err
		}
		base := calendarItem{
			ID: e.ID, Title: e.Title, Start: e.Start, End: e.End, AllDay: e.AllDay,
			Location: e.Location, URL: e.URL, Notes: e.Notes, ReminderMin: e.ReminderMin,
			RRule: e.RRule, Plan: true,
		}
		s0, zone := planStart(e.Start, e.AllDay, loc)
		opt, err := parsePlanRule(e.RRule)
		var next func() (time.Time, bool)
		if err == nil && opt != nil {
			// The span an instance covers: how far before the window
			// one that is still running may have begun.
			span := e.End.Sub(e.Start)
			if span > maxAnchorSpan {
				span = maxAnchorSpan
			}
			fromC := civil(from, loc).Add(-span - 48*time.Hour)
			toC := civil(to, loc).Add(48 * time.Hour)
			next, err = planWalker(*opt, s0, untilCivil(opt, e.AllDay, loc), fromC, toC)
		}
		if next == nil || err != nil {
			if !e.End.Before(from) && e.Start.Before(to) && !add(base) {
				return out, truncated, nil
			}
			continue
		}
		var build func(c time.Time) calendarItem
		if e.AllDay {
			// By calendar date: each instance is built at midnight in
			// the zone the plan was stored in.
			d1, _ := civilDate(e.End, loc)
			days := int(d1.Sub(s0).Round(24*time.Hour) / (24 * time.Hour))
			if days < 1 {
				days = 1
			}
			build = func(c time.Time) calendarItem {
				it := base
				it.Start = time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, zone).UTC()
				it.End = time.Date(c.Year(), c.Month(), c.Day()+days, 0, 0, 0, 0, zone).UTC()
				return it
			}
		} else {
			dur := e.End.Sub(e.Start)
			if dur <= 0 {
				continue
			}
			build = func(c time.Time) calendarItem {
				st := time.Date(c.Year(), c.Month(), c.Day(), c.Hour(), c.Minute(), c.Second(), 0, loc)
				it := base
				it.Start, it.End = st.UTC(), st.Add(dur).UTC()
				return it
			}
		}
		seriesStart, seriesEnd := e.Start, e.End
		n, walked := 0, 0
		for c, ok := next(); ok; c, ok = next() {
			if walked++; walked%256 == 0 {
				if err := ctx.Err(); err != nil {
					return out, truncated, err
				}
			}
			it := build(c)
			if !it.Start.Before(to) {
				break
			}
			if !it.End.After(from) {
				continue
			}
			if n == maxPlanInstances {
				truncated = true
				break
			}
			it.Occurrence = true
			it.SeriesStart, it.SeriesEnd = &seriesStart, &seriesEnd
			if !add(it) {
				return out, truncated, nil
			}
			n++
		}
	}
	return out, truncated, nil
}

// List answers the visible window: plans expanded on the left, feeds
// expanded on the right, sorted together, with the feed status flags the
// tile shows ("3 feeds · updated 14:02").
func (c *CalendarService) List(ctx context.Context, from, to time.Time, refresh bool) (json.RawMessage, error) {
	// The store answers every recurring plan, however old its first
	// instance, plus the one-offs near the window.
	plans, err := c.Store.Calendar().List(ctx, from.Add(-365*24*time.Hour), to.Add(365*24*time.Hour))
	if err != nil {
		return nil, err
	}
	items, truncated, err := expandPlansCtx(ctx, plans, from, to, c.loc())
	if err != nil {
		return nil, err
	}

	feedsRaw, ferr := c.Desk.FeedMeetings(ctx, from, to, refresh, 0)
	var feeds []Meeting
	configured := false
	if ferr == nil {
		var f struct {
			Configured bool      `json:"configured"`
			Items      []Meeting `json:"items"`
		}
		if json.Unmarshal(feedsRaw, &f) == nil {
			configured = f.Configured
			feeds = f.Items
		}
	}
	for _, m := range feeds {
		items = append(items, calendarItem{
			ID: m.UID, Title: m.Title, Start: m.Start, End: m.End, AllDay: m.AllDay,
			Location: m.Location, URL: m.URL, Feed: m.Feed,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Start.Before(items[j].Start) })
	return json.Marshal(map[string]any{
		"events": items, "from": from, "to": to,
		"configured": configured, "plans": len(plans), "truncated": truncated,
	})
}

// Zone is the user's zone (Loc, or the server's): the zone the API
// day-rounds all-day plans in.
func (c *CalendarService) Zone() *time.Location {
	if c == nil {
		return time.Local
	}
	return c.loc()
}

// loc is the zone plans expand and export in: Loc, or the server's.
func (c *CalendarService) loc() *time.Location {
	if c.Loc != nil {
		return c.Loc
	}
	return time.Local
}

// check validates a plan for create and update, and stores its rule in
// re-serialised form. Its errors are ErrInvalidPlan.
func (c *CalendarService) check(e *store.CalendarEvent) error {
	if strings.TrimSpace(e.Title) == "" {
		return badPlan("title required")
	}
	if e.End.Before(e.Start) {
		return badPlan("end before start")
	}
	if e.RRule == "" {
		return nil
	}
	start, _ := planStart(e.Start, e.AllDay, c.loc())
	rule, err := normalizePlanRule(e.RRule, start, e.AllDay, c.loc())
	if err != nil {
		return err
	}
	e.RRule = rule
	return nil
}

func (c *CalendarService) Create(ctx context.Context, e *store.CalendarEvent) (*store.CalendarEvent, error) {
	if err := c.check(e); err != nil {
		return nil, err
	}
	e.ID = newEventID()
	now := time.Now()
	e.CreatedAt, e.UpdatedAt = now, now
	if err := c.Store.Calendar().Upsert(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

func (c *CalendarService) Get(ctx context.Context, id string) (*store.CalendarEvent, error) {
	return c.Store.Calendar().Get(ctx, id)
}

func (c *CalendarService) Update(ctx context.Context, e *store.CalendarEvent) (*store.CalendarEvent, error) {
	if err := c.check(e); err != nil {
		return nil, err
	}
	e.UpdatedAt = time.Now()
	if err := c.Store.Calendar().Upsert(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

func (c *CalendarService) Delete(ctx context.Context, id string) error {
	return c.Store.Calendar().Delete(ctx, id)
}

// QuickAdd parses "dentist tomorrow 8:30 30m" into a stored plan.
func (c *CalendarService) QuickAdd(ctx context.Context, text string, loc *time.Location) (*store.CalendarEvent, error) {
	q, err := ParseQuickAdd(text, time.Now(), loc)
	if err != nil {
		return nil, invalidPlan(err.Error())
	}
	e := &store.CalendarEvent{
		Title: q.Title, Start: q.Start, End: q.End, AllDay: q.AllDay,
		RRule: q.RRule, ReminderMin: q.ReminderMin,
	}
	return c.Create(ctx, e)
}

// Export renders the plans as an ICS feed another calendar can subscribe
// to: every recurring plan, and the one-offs within a year of now. A
// rule is exported re-serialised; one that fails parsePlanRule (stored
// before the checks) is left out, so the plan exports as one event. An
// all-day plan exports its calendar date (DATE values are floating).
func (c *CalendarService) Export(ctx context.Context) (string, error) {
	plans, err := c.Store.Calendar().List(ctx, time.Now().Add(-365*24*time.Hour), time.Now().Add(365*24*time.Hour))
	if err != nil {
		return "", err
	}
	var out []ExportEvent
	for _, e := range plans {
		ev := ExportEvent{UID: e.ID, Title: e.Title, Location: e.Location, URL: e.URL, Notes: e.Notes, Start: e.Start, End: e.End, AllDay: e.AllDay, Updated: e.UpdatedAt}
		if opt, err := parsePlanRule(e.RRule); err == nil && opt != nil {
			ev.RRule = opt.RRuleString()
		}
		if e.AllDay {
			ev.Start, _ = civilDate(e.Start, c.loc())
			ev.End, _ = civilDate(e.End, c.loc())
		}
		out = append(out, ev)
	}
	return ICSExport("Devcules desk plans", out), nil
}

var _ = fmt.Sprint // keep fmt used if a future path drops it
