package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/terminals"
)

// providersRoot serves GET /api/providers: each router provider and
// whether its API key is in the vault, so the new-terminal form can
// offer only providers that will answer.
func providersRoot(rt *router.Router) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		type row struct {
			ID    string `json:"id"`
			Ready bool   `json:"ready"`
		}
		ids := rt.Providers()
		slices.Sort(ids)
		out := make([]row, 0, len(ids))
		for _, id := range ids {
			ok, _ := rt.Ready(r.Context(), id)
			out = append(out, row{ID: id, Ready: ok})
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": out})
	})
}

// providerModels serves GET /api/providers/{id}/models: the model ids
// the provider lists, read by serve with the vault key and reused for
// five minutes.
func providerModels(rt *router.Router) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "model router not configured")
			return
		}
		id, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/providers/"), "/")
		if !terminals.ValidAgentID(id) || rest != "models" {
			writeAuthError(w, http.StatusNotFound, "unknown provider route")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		list, err := rt.Models(r.Context(), id)
		switch {
		case errors.Is(err, router.ErrUnknownProvider):
			writeAuthError(w, http.StatusNotFound, "unknown provider")
			return
		case errors.Is(err, router.ErrNoKey):
			writeAuthError(w, http.StatusConflict, "the "+id+" provider has no API key yet: add its key in Settings")
			return
		case err != nil:
			writeAuthError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": list})
	})
}
