package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/desk"
	"github.com/devcules/devcules/internal/store"
)

// Calendar routes: the user's own plans (stored) merged with the read-only
// ICS feeds at range-read time. A plan can be created on the grid (click-
// drag), typed (quick-add), edited (PATCH) and deleted; GET events answers
// the visible window. GET .ics exports the plans for another calendar to
// subscribe to. Times parse as RFC3339.

func registerCalendar(mux *http.ServeMux, o Options, rec auditor) {
	mux.Handle("/api/calendar/events", csrf(o.PublicURL, requireSession(o.Auth, calendarEvents(o.DeskCalendar, rec))))
	mux.Handle("/api/calendar/events/", csrf(o.PublicURL, requireSession(o.Auth, calendarOne(o.DeskCalendar, rec))))
	mux.Handle("/api/calendar/quick", csrf(o.PublicURL, requireSession(o.Auth, calendarQuick(o.DeskCalendar, rec))))
	mux.Handle("/api/calendar.ics", requireSession(o.Auth, calendarICS(o.DeskCalendar)))
}

func calendarEvents(svc *desk.CalendarService, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			from, to, ok := parseRange(w, r)
			if !ok {
				return
			}
			// The feeds behind the plans can take up to their client
			// timeout (30 s), past serve's 10 s write deadline.
			w = liftWrite(w)
			body, err := svc.List(r.Context(), from, to, r.URL.Query().Get("refresh") == "1")
			if err != nil {
				calendarInternal(w, "list", err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		case http.MethodPost:
			e, ok := decodeEvent(w, r, svc.Zone())
			if !ok {
				return
			}
			created, err := svc.Create(r.Context(), e)
			if err != nil {
				calendarWriteError(w, "create", err)
				return
			}
			rec.record(r, audit.CalendarAdd, created.ID, map[string]string{"title": created.Title})
			writeJSON(w, http.StatusCreated, created)
		default:
			writeAuthError(w, http.StatusMethodNotAllowed, "want GET or POST")
		}
	}
}

func calendarOne(svc *desk.CalendarService, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/calendar/events/")
		if id == "" {
			writeAuthError(w, http.StatusNotFound, "no event id")
			return
		}
		switch r.Method {
		case http.MethodPatch, http.MethodPut:
			cur, err := svc.Get(r.Context(), id)
			if errors.Is(err, store.ErrNotFound) {
				writeAuthError(w, http.StatusNotFound, "no event "+id)
				return
			}
			if err != nil {
				calendarInternal(w, "get", err)
				return
			}
			var e *store.CalendarEvent
			var ok bool
			if r.Method == http.MethodPatch {
				// A PATCH sends only the fields it changes: each field
				// present in the body replaces the stored one, the rest
				// stay as stored.
				e, ok = decodePatch(w, r, cur, svc.Zone())
			} else {
				// A PUT replaces the plan; title, start and end fall
				// back to the stored row when left out.
				e, ok = decodeEvent(w, r, svc.Zone())
				if ok {
					if e.Title == "" {
						e.Title = cur.Title
					}
					if e.Start.IsZero() {
						e.Start = cur.Start
					}
					if e.End.IsZero() {
						e.End = cur.End
					}
				}
			}
			if !ok {
				return
			}
			e.ID = cur.ID
			e.CreatedAt = cur.CreatedAt
			updated, err := svc.Update(r.Context(), e)
			if err != nil {
				calendarWriteError(w, "update", err)
				return
			}
			rec.record(r, audit.CalendarEdit, updated.ID, map[string]string{"title": updated.Title})
			writeJSON(w, http.StatusOK, updated)
		case http.MethodDelete:
			if err := svc.Delete(r.Context(), id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeAuthError(w, http.StatusNotFound, "no event "+id)
					return
				}
				calendarInternal(w, "delete", err)
				return
			}
			rec.record(r, audit.CalendarRemove, id, nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeAuthError(w, http.StatusMethodNotAllowed, "want PATCH or DELETE")
		}
	}
}

func calendarQuick(svc *desk.CalendarService, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
			writeAuthError(w, http.StatusBadRequest, "text required")
			return
		}
		e, err := svc.QuickAdd(r.Context(), body.Text, time.Local)
		if err != nil {
			calendarWriteError(w, "quick add", err)
			return
		}
		rec.record(r, audit.CalendarAdd, e.ID, map[string]string{"title": e.Title})
		writeJSON(w, http.StatusCreated, e)
	}
}

func calendarICS(svc *desk.CalendarService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w = liftWrite(w)
		doc, err := svc.Export(r.Context())
		if err != nil {
			calendarInternal(w, "export", err)
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Content-Disposition", `inline; filename="devcules.ics"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(doc))
	}
}

// calendarInternal answers a store failure: the error is logged, the
// client gets a fixed line (storage details stay server-side).
func calendarInternal(w http.ResponseWriter, op string, err error) {
	slog.Default().Warn("calendar: "+op+" failed", "err", err)
	writeAuthError(w, http.StatusInternalServerError, "internal error")
}

// calendarWriteError answers a create/update/quick-add error: a plan the
// caller must correct is a 400 with its reason, anything else a 500.
func calendarWriteError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, desk.ErrInvalidPlan) {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	calendarInternal(w, op, err)
}

// decodePatch reads a PATCH body onto a copy of the stored plan: a field
// present in the body (even empty, "rrule":"" ends a series) replaces
// the stored value; a field left out is kept. Times parse and all-day
// plans day-round in the user's zone (loc) as in decodeEvent.
func decodePatch(w http.ResponseWriter, r *http.Request, cur *store.CalendarEvent, loc *time.Location) (*store.CalendarEvent, bool) {
	var body struct {
		Title       *string `json:"title"`
		Start       *string `json:"start"`
		End         *string `json:"end"`
		AllDay      *bool   `json:"all_day"`
		Location    *string `json:"location"`
		URL         *string `json:"url"`
		Notes       *string `json:"notes"`
		ReminderMin *int    `json:"reminder_min"`
		RRule       *string `json:"rrule"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "event JSON refused")
		return nil, false
	}
	e := *cur
	if body.Title != nil {
		e.Title = strings.TrimSpace(*body.Title)
	}
	if body.AllDay != nil {
		e.AllDay = *body.AllDay
	}
	if body.Location != nil {
		e.Location = strings.TrimSpace(*body.Location)
	}
	if body.URL != nil {
		e.URL = strings.TrimSpace(*body.URL)
	}
	if body.Notes != nil {
		e.Notes = *body.Notes
	}
	if body.ReminderMin != nil {
		e.ReminderMin = *body.ReminderMin
	}
	if body.RRule != nil {
		e.RRule = strings.TrimSpace(*body.RRule)
	}
	for _, f := range []struct {
		name string
		val  *string
		dst  *time.Time
	}{{"start", body.Start, &e.Start}, {"end", body.End, &e.End}} {
		if f.val == nil || *f.val == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, *f.val)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, f.name+" is not RFC3339")
			return nil, false
		}
		*f.dst = t
	}
	if e.AllDay && (body.AllDay != nil || body.Start != nil || body.End != nil) {
		e.Start, e.End = dayIn(e.Start, loc), dayIn(e.End, loc)
	}
	if e.End.Before(e.Start) {
		writeAuthError(w, http.StatusBadRequest, "end before start")
		return nil, false
	}
	return &e, true
}

// dayIn rounds an all-day plan's time to the start of its date in loc,
// the user's zone. A client sends an all-day day as its local midnight
// in UTC ("2026-11-05T23:00:00Z" is 6 Nov in Amsterdam); rounding in the
// string's own offset would store the day before.
func dayIn(t time.Time, loc *time.Location) time.Time {
	if t.IsZero() {
		return t
	}
	if loc == nil {
		loc = time.Local
	}
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// decodeEvent reads the create/update body. Title/start/end validate; the
// rest are optional. start+end parse RFC3339, an all-day event's times are
// day-rounded in the user's zone (loc).
func decodeEvent(w http.ResponseWriter, r *http.Request, loc *time.Location) (*store.CalendarEvent, bool) {
	var body struct {
		Title       string `json:"title"`
		Start       string `json:"start"`
		End         string `json:"end"`
		AllDay      bool   `json:"all_day"`
		Location    string `json:"location"`
		URL         string `json:"url"`
		Notes       string `json:"notes"`
		ReminderMin int    `json:"reminder_min"`
		RRule       string `json:"rrule"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "event JSON refused")
		return nil, false
	}
	e := &store.CalendarEvent{
		Title: strings.TrimSpace(body.Title), AllDay: body.AllDay,
		Location: strings.TrimSpace(body.Location), URL: strings.TrimSpace(body.URL),
		Notes: body.Notes, ReminderMin: body.ReminderMin, RRule: strings.TrimSpace(body.RRule),
	}
	if body.Start != "" {
		t, err := time.Parse(time.RFC3339, body.Start)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, "start is not RFC3339")
			return nil, false
		}
		e.Start = t
	}
	if body.End != "" {
		t, err := time.Parse(time.RFC3339, body.End)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, "end is not RFC3339")
			return nil, false
		}
		e.End = t
	}
	if e.AllDay {
		e.Start, e.End = dayIn(e.Start, loc), dayIn(e.End, loc)
	}
	if !e.Start.IsZero() && !e.End.IsZero() && e.End.Before(e.Start) {
		writeAuthError(w, http.StatusBadRequest, "end before start")
		return nil, false
	}
	return e, true
}

// maxRangeSpan bounds one calendar read: recurring plans and feeds
// expand over the whole window, so an open span (years, centuries) is
// refused rather than expanded.
const maxRangeSpan = 400 * 24 * time.Hour

// A read's window stays within these instants: the years a recurring
// plan may start in (desk.CalendarService refuses others), so no read
// asks for a stretch thousands of years past every series.
var (
	minRangeFrom = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	maxRangeTo   = time.Date(2201, 1, 1, 0, 0, 0, 0, time.UTC)
)

// parseRange reads ?from=&to= (RFC3339). Missing bounds default to a
// centred window so a grid asking for "this month" gets it. A span over
// maxRangeSpan, or a window outside minRangeFrom..maxRangeTo, is refused.
func parseRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	q := r.URL.Query()
	now := time.Now()
	from := now.Add(-14 * 24 * time.Hour)
	to := now.Add(45 * 24 * time.Hour)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, "from is not RFC3339")
			return time.Time{}, time.Time{}, false
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, "to is not RFC3339")
			return time.Time{}, time.Time{}, false
		}
		to = t
	}
	if !to.After(from) {
		writeAuthError(w, http.StatusBadRequest, "to must be after from")
		return time.Time{}, time.Time{}, false
	}
	if from.Before(minRangeFrom) || to.After(maxRangeTo) {
		writeAuthError(w, http.StatusBadRequest, "range must lie within the years 1900 to 2200")
		return time.Time{}, time.Time{}, false
	}
	if to.Sub(from) > maxRangeSpan {
		writeAuthError(w, http.StatusBadRequest, "range is over 400 days")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}
