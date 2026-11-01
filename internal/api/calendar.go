package api

import (
	"encoding/json"
	"errors"
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
			body, err := svc.List(r.Context(), from, to, r.URL.Query().Get("refresh") == "1")
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		case http.MethodPost:
			e, ok := decodeEvent(w, r)
			if !ok {
				return
			}
			created, err := svc.Create(r.Context(), e)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			rec.record(r, audit.SecretPut, "calendar.event", map[string]string{"title": created.Title})
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
				writeAuthError(w, http.StatusInternalServerError, err.Error())
				return
			}
			// A PATCH sends only the fields it changes; same decode shape as
			// POST, applied on top of the stored row.
			e, ok := decodeEvent(w, r)
			if !ok {
				return
			}
			e.ID = cur.ID
			e.CreatedAt = cur.CreatedAt
			if e.Title == "" {
				e.Title = cur.Title
			}
			if e.Start.IsZero() {
				e.Start = cur.Start
			}
			if e.End.IsZero() {
				e.End = cur.End
			}
			updated, err := svc.Update(r.Context(), e)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			rec.record(r, audit.SecretPut, "calendar.event", map[string]string{"title": updated.Title})
			writeJSON(w, http.StatusOK, updated)
		case http.MethodDelete:
			if err := svc.Delete(r.Context(), id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeAuthError(w, http.StatusNotFound, "no event "+id)
					return
				}
				writeAuthError(w, http.StatusInternalServerError, err.Error())
				return
			}
			rec.record(r, audit.SecretDelete, "calendar.event", nil)
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
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		rec.record(r, audit.SecretPut, "calendar.event", map[string]string{"title": e.Title})
		writeJSON(w, http.StatusCreated, e)
	}
}

func calendarICS(svc *desk.CalendarService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		doc, err := svc.Export(r.Context())
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Content-Disposition", `inline; filename="devcules.ics"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(doc))
	}
}

// decodeEvent reads the create/update body. Title/start/end validate; the
// rest are optional. start+end parse RFC3339, an all-day event's times are
// day-rounded.
func decodeEvent(w http.ResponseWriter, r *http.Request) (*store.CalendarEvent, bool) {
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
		e.Start = time.Date(e.Start.Year(), e.Start.Month(), e.Start.Day(), 0, 0, 0, 0, e.Start.Location())
		e.End = time.Date(e.End.Year(), e.End.Month(), e.End.Day(), 0, 0, 0, 0, e.End.Location())
	}
	if !e.Start.IsZero() && !e.End.IsZero() && e.End.Before(e.Start) {
		writeAuthError(w, http.StatusBadRequest, "end before start")
		return nil, false
	}
	return e, true
}

// parseRange reads ?from=&to= (RFC3339). Missing bounds default to a
// centred window so a grid asking for "this month" gets it.
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
	return from, to, true
}
