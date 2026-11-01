package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/desk"
)

// Desk routes: prefs and sources are whole-document session JSON; the
// data routes proxy the configured providers. GET answers masked
// sources (token presence only); PUT replaces the document. Writes
// record an audit row; reads don't (30s tile refresh would drown it).

func registerDesk(mux *http.ServeMux, o Options, rec auditor) {
	mux.Handle("/api/desk/prefs", csrf(o.PublicURL, requireSession(o.Auth, deskPrefs(o.Desk, rec))))
	mux.Handle("/api/desk/sources", csrf(o.PublicURL, requireSession(o.Auth, deskSources(o.Desk, rec))))
	mux.Handle("/api/desk/weather", requireSession(o.Auth, deskData(o.Desk.Weather)))
	mux.Handle("/api/desk/news/", requireSession(o.Auth, deskNews(o.Desk)))
	mux.Handle("/api/desk/tickets", requireSession(o.Auth, deskData(o.Desk.Tickets)))
	mux.Handle("/api/desk/meetings", requireSession(o.Auth, deskData(o.Desk.Meetings)))
}

func deskPrefs(svc *desk.Service, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			p, err := svc.ReadPrefs(r.Context())
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "desk prefs unreadable")
				return
			}
			writeJSON(w, http.StatusOK, p)
		case http.MethodPut:
			var p desk.Prefs
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
				writeAuthError(w, http.StatusBadRequest, "prefs JSON refused")
				return
			}
			if len(p.Widgets) > 64 {
				writeAuthError(w, http.StatusBadRequest, "too many widgets")
				return
			}
			if err := svc.WritePrefs(r.Context(), p); err != nil {
				writeAuthError(w, http.StatusInternalServerError, "desk prefs not saved")
				return
			}
			rec.record(r, audit.DeskPrefs, "", map[string]string{"widgets": strconv.Itoa(len(p.Widgets))})
			w.WriteHeader(http.StatusNoContent)
		default:
			writeAuthError(w, http.StatusMethodNotAllowed, "want GET or PUT")
		}
	}
}

func deskSources(svc *desk.Service, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			src, err := svc.ReadSources(r.Context())
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "desk sources unreadable")
				return
			}
			writeJSON(w, http.StatusOK, src.Masked())
		case http.MethodPut:
			var src desk.Sources
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&src); err != nil {
				writeAuthError(w, http.StatusBadRequest, "sources JSON refused")
				return
			}
			// Blank keeps the stored token (the drawer never round-trips a
			// secret), except an explicit X_clear, which removes it.
			cur, _ := svc.ReadSources(r.Context())
			if src.ClearTavily {
				src.TavilyKey = ""
			} else if src.TavilyKey == "" {
				src.TavilyKey = cur.TavilyKey
			}
			if src.ClearTicket {
				src.TicketToken = ""
			} else if src.TicketToken == "" {
				src.TicketToken = cur.TicketToken
			}
			// Never persist the control flags themselves.
			src.ClearTavily = false
			src.ClearTicket = false
			if err := svc.WriteSources(r.Context(), src); err != nil {
				writeAuthError(w, http.StatusInternalServerError, "desk sources not saved")
				return
			}
			rec.record(r, audit.DeskSources, "", nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeAuthError(w, http.StatusMethodNotAllowed, "want GET or PUT")
		}
	}
}

type deskFetch func(ctx context.Context, refresh bool) (json.RawMessage, error)

func deskData(fn deskFetch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		body, err := fn(r.Context(), r.URL.Query().Get("refresh") == "1")
		writeData(w, body, err)
	}
}

func deskNews(svc *desk.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		which := strings.TrimPrefix(r.URL.Path, "/api/desk/news/")
		body, err := svc.News(r.Context(), which, r.URL.Query().Get("refresh") == "1")
		writeData(w, body, err)
	}
}

func writeData(w http.ResponseWriter, body json.RawMessage, err error) {
	if err != nil {
		writeDataError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeDataError maps a provider answer: "not configured" is 400 with a
// readable line the tile shows; an upstream failure is 502 so a tile
// keeping a cached answer instead of thrashing reads as stale, not down.
func writeDataError(w http.ResponseWriter, err error) {
	msg := strings.TrimSpace(err.Error())
	low := strings.ToLower(msg)
	if strings.Contains(low, "no tavily key") || strings.Contains(low, "no tickets") || strings.Contains(low, "unknown news feed") {
		writeAuthError(w, http.StatusBadRequest, msg)
		return
	}
	writeAuthError(w, http.StatusBadGateway, msg)
}
