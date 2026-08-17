package application

import (
	"net/http"
	"strings"
)

// NormalizeRoutePrefix cleans a configured/flag-provided web route prefix
// (PARITY-B6, upstream's --web.route-prefix) into the canonical form used by
// WithRoutePrefix: either "" (no prefix) or a leading-slash,
// no-trailing-slash path like "/amp".
func NormalizeRoutePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return ""
	}
	return prefix
}

// WithRoutePrefix mounts handler under prefix, mirroring upstream
// Alertmanager's --web.route-prefix. An empty (or "/") prefix returns
// handler unchanged.
//
// With a non-empty prefix:
//   - requests to {prefix} or {prefix}/... are stripped of the prefix and
//     forwarded to handler (net/http.ServeMux's built-in subtree redirect
//     sends a bare {prefix} to {prefix}/ with a 301);
//   - a bare GET / is redirected to {prefix}/ (302), matching the common
//     --web.route-prefix UX of pointing visitors at the mounted UI;
//   - any other unprefixed path 404s — the prefix is the only mount point.
func WithRoutePrefix(handler http.Handler, prefix string) http.Handler {
	prefix = NormalizeRoutePrefix(prefix)
	if prefix == "" {
		return handler
	}

	outer := http.NewServeMux()
	outer.Handle(prefix+"/", http.StripPrefix(prefix, handler))
	outer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, prefix+"/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	return outer
}
