package main

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/webauth"
)

// wrapWebAuth puts basic auth from --web.config.file (PROD-AUTH, ADR-011) in
// front of handler. Empty path: no auth, with a startup warning, as upstream
// without the flag. The core's handler serves under the --web.external-url
// path, so the exempt paths (server.auth.unauthenticated_paths, without the
// prefix) are matched with it. An invalid file is a startup error.
func wrapWebAuth(handler http.Handler, path, routePrefix string, exempt []string, logger *slog.Logger, reg prometheus.Registerer) (http.Handler, error) {
	if path == "" {
		logger.Warn("HTTP authentication is off: set --web.config.file to require basic auth")
		return handler, nil
	}
	if exempt == nil {
		exempt = appconfig.DefaultUnauthenticatedPaths()
	}
	prefixed := make([]string, 0, len(exempt))
	for _, p := range exempt {
		prefixed = append(prefixed, routePrefix+p)
	}
	auth, err := webauth.New(webauth.Options{
		Path:                 path,
		UnauthenticatedPaths: prefixed,
		Logger:               logger,
		Registerer:           reg,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("HTTP authentication enabled", "path", path, "users", auth.UserCount(), "unauthenticated_paths", prefixed)
	return auth.Wrap(handler), nil
}
