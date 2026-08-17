package application

import (
	"net/http"
	"net/url"
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

// ResolveRoutePrefix computes the effective web route prefix the way
// upstream Alertmanager does: --web.route-prefix defaults to the path
// component of --web.external-url when not explicitly set.
//
// LIMITATION: this repo's config layer (viper, unmarshaled into a plain
// string field) cannot distinguish "route prefix left unset" from "route
// prefix explicitly set to the empty string" — both come out as "". So the
// rule here is: an empty routePrefix means "unset, inherit from
// externalURL's path"; a non-empty routePrefix (including the explicit
// value "/") always wins and skips inheritance — "/" is upstream's own
// spelling for "root, no prefix", so it doubles as the escape hatch for a
// deployment that sets external_url with a path but wants no route prefix.
// A malformed externalURL is treated the same as an absent one (no prefix)
// rather than erroring, since this only affects routing, not correctness.
func ResolveRoutePrefix(routePrefix, externalURL string) string {
	routePrefix = strings.TrimSpace(routePrefix)
	if routePrefix != "" {
		return NormalizeRoutePrefix(routePrefix)
	}

	externalURL = strings.TrimSpace(externalURL)
	if externalURL == "" {
		return ""
	}
	u, err := url.Parse(externalURL)
	if err != nil {
		return ""
	}
	return NormalizeRoutePrefix(u.Path)
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
