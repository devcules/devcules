package api

import (
	"net/http"

	"github.com/devcules/devcules/internal/store"
)

// Handler returns the M0.5 mux: health endpoints only. The store is
// needed for readiness; liveness depends on nothing.
func Handler(s store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz(s))
	return mux
}

// healthz answers 200 whenever the process is up. Only GET is allowed.
func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyz answers 200 when the store answers. Open already applied
// migrations, so reachable means migrated. A down store answers 503
// with a fixed reason: the underlying error can name paths, so it is
// never repeated to the caller.
func readyz(s store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: store unreachable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
