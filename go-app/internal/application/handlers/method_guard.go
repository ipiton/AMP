package handlers

import "net/http"

// getOnly rejects everything but GET and HEAD with 405 (upstream Alertmanager
// enforces methods on read-only endpoints; POST used to fall through to GET).
func getOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}
