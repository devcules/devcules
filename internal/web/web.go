package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Embedded serves the production build compiled into the binary.
func Embedded() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: dist always exists (at worst the placeholder
		// stands in until `npm run build` replaces it). A misbuilt
		// binary must fail loudly, never serve an empty app.
		panic("web: embedded dist missing: " + err.Error())
	}
	return Handler(sub)
}

// Handler serves a built app from files, usually the embedded dist.
// Only GET and HEAD are served; anything else is 405. A missing
// index.html means the app was never built, so every request answers
// 503 with the build command. Otherwise an exact file serves with
// long-lived caching for hashed assets and revalidation for the
// rest; a missing file on a page navigation (GET accepting HTML)
// falls back to index.html, and anything else 404s.
func Handler(files fs.FS) http.Handler {
	inner := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		raw, err := fs.ReadFile(files, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("web UI not built: run npm --prefix web run build"))
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if info, err := fs.Stat(files, path); err == nil && !info.IsDir() {
				cache(w, r.URL.Path)
				inner.ServeHTTP(w, r)
				return
			}
		}
		if acceptsHTML(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
		http.NotFound(w, r)
	})
}

// cache sets long-lived immutable caching for hashed build assets
// and revalidation for everything else (index, manifest, icons carry
// no hash, so they must revalidate).
func cache(w http.ResponseWriter, path string) {
	if strings.HasPrefix(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// acceptsHTML reports a page navigation: the fallback serves
// index.html only to those, never to data fetches.
func acceptsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
