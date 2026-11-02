package desk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/store"
	"github.com/teambition/rrule-go"
)

// calendarItem is one visible event: a desk plan (own row) or an ICS feed
// meeting, expanded over the requested range by the route. Occurrence marks
// a recurrence instance so an edit can say "the whole series".
type calendarItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	AllDay      bool      `json:"all_day"`
	Location    string    `json:"location,omitempty"`
	URL         string    `json:"url,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	ReminderMin int       `json:"reminder_min,omitempty"`
	RRule       string    `json:"rrule,omitempty"`
	Feed        string    `json:"feed,omitempty"`
	Plan        bool      `json:"plan,omitempty"`
	Occurrence  bool      `json:"occurrence,omitempty"`
}

// CalendarService adds the user's own plans (stored) on top of the desk
// Service's feeds. One owner per devcules; no per-user rows.
type CalendarService struct {
	Desk  *Service
	Store store.Store
	hc    *http.Client
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
// expanding each RRule into instances. A plan with no recurrence is one
// item; a recurring one is stamped Occurrence so the editor knows.
func expandPlans(plans []*store.CalendarEvent, from, to time.Time) []calendarItem {
	out := []calendarItem{}
	for _, e := range plans {
		base := calendarItem{
			ID: e.ID, Title: e.Title, Start: e.Start, End: e.End, AllDay: e.AllDay,
			Location: e.Location, URL: e.URL, Notes: e.Notes, ReminderMin: e.ReminderMin,
			RRule: e.RRule, Plan: true,
		}
		if e.AllDay || e.RRule == "" {
			if !e.End.Before(from) && e.Start.Before(to) {
				out = append(out, base)
			}
			continue
		}
		dur := e.End.Sub(e.Start)
		if dur <= 0 {
			continue
		}
		rule, err := rrule.StrToRRule(strings.TrimPrefix(e.RRule, "RRULE:"))
		if err != nil {
			continue
		}
		set := rrule.Set{}
		set.DTStart(e.Start)
		set.RRule(rule)
		for _, st := range set.Between(from.Add(-dur), to, true) {
			end := st.Add(dur)
			if !end.After(from) || !st.Before(to) {
				continue
			}
			it := base
			it.Start, it.End, it.Occurrence = st, end, true
			out = append(out, it)
		}
	}
	return out
}

// List answers the visible window: plans expanded on the left, feeds
// expanded on the right, sorted together, with the feed status flags the
// tile shows ("3 feeds · updated 14:02").
func (c *CalendarService) List(ctx context.Context, from, to time.Time, refresh bool) (json.RawMessage, error) {
	plans, err := c.Store.Calendar().List(ctx, from.Add(-365*24*time.Hour), to.Add(365*24*time.Hour))
	if err != nil {
		return nil, err
	}
	items := expandPlans(plans, from, to)

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
		"configured": configured, "plans": len(plans),
	})
}

func (c *CalendarService) Create(ctx context.Context, e *store.CalendarEvent) (*store.CalendarEvent, error) {
	if strings.TrimSpace(e.Title) == "" {
		return nil, errors.New("title required")
	}
	if e.End.Before(e.Start) {
		return nil, errors.New("end before start")
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
	if e.End.Before(e.Start) {
		return nil, errors.New("end before start")
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
		return nil, err
	}
	e := &store.CalendarEvent{
		Title: q.Title, Start: q.Start, End: q.End, AllDay: q.AllDay,
		RRule: q.RRule, ReminderMin: q.ReminderMin,
	}
	return c.Create(ctx, e)
}

// Export renders the plans as an ICS feed another calendar can subscribe to.
func (c *CalendarService) Export(ctx context.Context) (string, error) {
	plans, err := c.Store.Calendar().List(ctx, time.Now().Add(-365*24*time.Hour), time.Now().Add(365*24*time.Hour))
	if err != nil {
		return "", err
	}
	var out []ExportEvent
	for _, e := range plans {
		out = append(out, ExportEvent{UID: e.ID, Title: e.Title, Location: e.Location, URL: e.URL, Notes: e.Notes, RRule: e.RRule, Start: e.Start, End: e.End, AllDay: e.AllDay, Updated: e.UpdatedAt})
	}
	return ICSExport("Devcules desk plans", out), nil
}

var _ = fmt.Sprint // keep fmt used if a future path drops it
