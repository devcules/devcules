package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/coder/websocket"

	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/relay"
)

// terminalsRoot serves the /api/terminals collection: GET lists the
// runner's terminals, POST opens one. Reads need a session; writes
// need a session plus the CSRF origin check.
func terminalsRoot(relay *relay.Relay, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "no runner linked")
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, rerr := relay.List(r.Context(), r.URL.Query().Get("runner"))
			if rerr != nil {
				writeAuthError(w, rerr.Status, rerr.Msg)
				return
			}
			if list == nil {
				list = []terminals.TermInfo{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Terminals []terminals.TermInfo `json:"terminals"`
			}{Terminals: list})
		case http.MethodPost:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id, rerr := relay.Open(r.Context(), r.URL.Query().Get("runner"))
				if rerr != nil {
					writeAuthError(w, rerr.Status, rerr.Msg)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":` + strconv.Quote(id) + "}\n"))
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// terminalOne serves one terminal under /api/terminals/{id}: DELETE
// closes it (session plus CSRF), and GET on the /stream suffix
// upgrades to the terminal WebSocket (session plus the relay's Origin
// check against cross-site hijacking).
func terminalOne(relay *relay.Relay, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "no runner linked")
			return
		}
		id, isStream := terminalPath(r.URL.Path)
		if id == "" {
			http.NotFound(w, r)
			return
		}
		if isStream {
			if !requireMethod(w, r, http.MethodGet) {
				return
			}
			streamTerminal(relay, w, r, id)
			return
		}
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rerr := relay.Delete(r.Context(), r.URL.Query().Get("runner"), id); rerr != nil {
				writeAuthError(w, rerr.Status, rerr.Msg)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, r)
	})
}

// terminalPath splits /api/terminals/{id} from
// /api/terminals/{id}/stream. Anything else is not a terminal route.
func terminalPath(path string) (id string, isStream bool) {
	const prefix = "/api/terminals/"
	rest := path
	if len(rest) < len(prefix) || rest[:len(prefix)] != prefix {
		return "", false
	}
	rest = rest[len(prefix):]
	if rest == "" {
		return "", false
	}
	if len(rest) > len("/stream") && rest[len(rest)-len("/stream"):] == "/stream" {
		id = rest[:len(rest)-len("/stream")]
		if id == "" || hasSlash(id) {
			return "", false
		}
		return id, true
	}
	if hasSlash(rest) {
		return "", false
	}
	return rest, false
}

func hasSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

// streamTerminal upgrades the browser socket and hands it to the
// relay, which attaches it to the runner over the link. Validation
// failures after the upgrade have no HTTP status left to use, so
// they close the socket with 4404 instead.
func streamTerminal(relay *relay.Relay, w http.ResponseWriter, r *http.Request, id string) {
	conn, err := websocket.Accept(w, r, relay.AcceptOptions(r))
	if err != nil {
		return
	}
	fail := func(message string) {
		_ = conn.Close(websocket.StatusCode(4404), message)
	}
	cols, rows := 80, 24
	if v := r.URL.Query().Get("cols"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fail("cols and rows must each be 1-500")
			return
		}
		cols = n
	}
	if v := r.URL.Query().Get("rows"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fail("cols and rows must each be 1-500")
			return
		}
		rows = n
	}
	runner, rerr := relay.Resolve(r.URL.Query().Get("runner"), id, cols, rows)
	if rerr != nil {
		_ = conn.Close(rerr.CloseCode(), rerr.Msg)
		return
	}
	relay.AttachStream(r.Context(), runner, id, cols, rows, conn)
}
