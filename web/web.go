package web

import (
	_ "embed"
	"net/http"
	"strings"
)

// Embedded HTML UI matching MiXplorer dark theme and functionality.
//
//go:embed index.html
var IndexHTML []byte

// Handler returns an HTTP handler that serves the embedded web file manager UI.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only handle GET/HEAD
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(IndexHTML)
	})
}

// RegisterRoutes attaches the web UI handler to an http.ServeMux for root and UI paths.
func RegisterRoutes(mux *http.ServeMux) {
	h := Handler()
	mux.Handle("/ui", h)
	mux.Handle("/ui/", h)
}

// IsWebBrowserRequest checks if an HTTP request is coming from a web browser.
func IsWebBrowserRequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}
