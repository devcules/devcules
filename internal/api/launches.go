package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
)

// launchesRoot serves /api/launch-tokens: GET lists tokens (never
// their secrets), POST mints one and shows it once. Agents normally
// get theirs at launch (M3.3); this is the manual door, for scripts
// and for testing a provider with curl.
func launchesRoot(tokens *launch.Tokens, providers func() []string, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokens == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "launch tokens not configured")
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, err := tokens.List(r.Context())
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if list == nil {
				list = []launch.Launch{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Tokens []launch.Launch `json:"tokens"`
			}{list})
		case http.MethodPost:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Label      string `json:"label"`
					Provider   string `json:"provider"`
					TTLMinutes int    `json:"ttlMinutes"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
					writeAuthError(w, http.StatusBadRequest, `body must be {"label": "...", "provider": "meta", "ttlMinutes": 720}`)
					return
				}
				if body.Provider != "" && !slices.Contains(providers(), body.Provider) {
					writeAuthError(w, http.StatusBadRequest, "unknown provider "+body.Provider+" (known: "+strings.Join(providers(), ", ")+")")
					return
				}
				token, l, err := tokens.Mint(r.Context(), body.Label, body.Provider, time.Duration(body.TTLMinutes)*time.Minute)
				if err != nil {
					if strings.HasPrefix(err.Error(), "launch: ") {
						writeAuthError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "launch: "))
						return
					}
					writeAuthError(w, http.StatusInternalServerError, "internal error")
					return
				}
				rec.record(r, audit.LaunchMint, l.ID, map[string]string{"label": l.Label, "provider": l.Provider, "expires": l.Expires.Format(time.RFC3339)})
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(struct {
					Token  string        `json:"token"`
					Launch launch.Launch `json:"launch"`
				}{token, l})
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// launchOne serves /api/launch-tokens/{id}: DELETE revokes it (204,
// also when already revoked; 404 for an unknown id).
func launchOne(tokens *launch.Tokens, publicURL string, rec auditor) http.Handler {
	return csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokens == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "launch tokens not configured")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/launch-tokens/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		if !requireMethod(w, r, http.MethodDelete) {
			return
		}
		switch err := tokens.Revoke(r.Context(), id); {
		case err == nil:
			rec.record(r, audit.LaunchRevoke, id, nil)
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, launch.ErrNotFound):
			writeAuthError(w, http.StatusNotFound, "no such launch token")
		default:
			writeAuthError(w, http.StatusInternalServerError, "internal error")
		}
	}))
}
