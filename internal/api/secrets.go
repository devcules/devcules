package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/vault"
)

// Secrets routes: the browser can list names, store a value under a
// name, or delete a name. It can never read a value back — a token the
// desk proxy uses is write-only from the outside, so a stolen session
// cannot harvest the store.

func secretsListPut(v *vault.Vault, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "vault not configured")
			return
		}
		switch r.Method {
		case http.MethodGet:
			names, err := v.Names(r.Context())
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "secrets unreadable")
				return
			}
			if names == nil {
				names = []string{}
			}
			writeJSON(w, http.StatusOK, map[string][]string{"names": names})
		case http.MethodPut:
			var body struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
				writeAuthError(w, http.StatusBadRequest, "secret JSON refused")
				return
			}
			name := strings.TrimSpace(body.Name)
			if name == "" || len(name) > 128 || strings.ContainsAny(name, "/\x00") || body.Value == "" || len(body.Value) > 1<<12 {
				writeAuthError(w, http.StatusBadRequest, "want name (1-128, no slashes) and a value")
				return
			}
			if err := v.Put(r.Context(), name, []byte(body.Value)); err != nil {
				writeAuthError(w, http.StatusInternalServerError, "secret not stored")
				return
			}
			rec.record(r, audit.SecretPut, name, nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeAuthError(w, http.StatusMethodNotAllowed, "want GET or PUT")
		}
	}
}

func secretsGetDelete(v *vault.Vault, rec auditor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "vault not configured")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/secrets/")
		if name == "" {
			writeAuthError(w, http.StatusNotFound, "no secret named")
			return
		}
		switch r.Method {
		case http.MethodDelete:
			if err := v.Delete(r.Context(), name); err != nil {
				if errors.Is(err, vault.ErrNotFound) {
					writeAuthError(w, http.StatusNotFound, "no secret named "+name)
					return
				}
				writeAuthError(w, http.StatusInternalServerError, "secret not deleted")
				return
			}
			rec.record(r, audit.SecretDelete, name, nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			// GET a single secret is deliberately absent: values never
			// leave the vault over HTTP.
			writeAuthError(w, http.StatusMethodNotAllowed, "secrets are write-only")
		}
	}
}
