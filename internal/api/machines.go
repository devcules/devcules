package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/devcules/devcules/internal/machines"
)

func machinesEnrolCode(iss *machines.Issuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if iss == nil { writeAuthError(w, http.StatusServiceUnavailable, "machines not configured"); return }
		if !requireMethod(w, r, http.MethodPost) { return }
		var body struct{ Label string `json:"label"`}
		_ = json.NewDecoder(r.Body).Decode(&body)
		code, err := iss.Mint(r.Context(), body.Label)
		if err != nil { writeAuthError(w, 500, "mint failed"); return }
		writeJSON(w, 201, map[string]string{"code": code})
	})
}

func machinesRedeem(iss *machines.Issuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if iss == nil { writeAuthError(w, 500, "machines not configured"); return }
		if !requireMethod(w, r, http.MethodPost) { return }
		var req struct {
			Code   string `json:"code"`
			Name   string `json:"name"`
			PubKey string `json:"pubkey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
			writeAuthError(w, 400, "bad request"); return
		}
		pub, err := base64.StdEncoding.DecodeString(req.PubKey)
		if err != nil { writeAuthError(w, 400, "bad pubkey"); return }
		m, err := iss.Redeem(r.Context(), req.Code, pub, req.Name)
		if err != nil { writeAuthError(w, 400, err.Error()); return }
		writeJSON(w, 201, m)
	})
}
