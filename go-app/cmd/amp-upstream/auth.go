package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/webauth"
)

// webConfigFileEnv is the last fallback for the web config file, after the
// flag and server.web_config_file (PROD-AUTH).
const webConfigFileEnv = "SERVER_WEB_CONFIG_FILE"

// resolveWebConfigFile picks the web config file: --web.config.file, then
// server.web_config_file of the AMP configuration, then SERVER_WEB_CONFIG_FILE.
// All three enabled auth on the removed cmd/server; dropping one would leave
// the API open without a word.
func resolveWebConfigFile(flagValue, configValue string) string {
	if path := strings.TrimSpace(flagValue); path != "" {
		return path
	}
	if path := strings.TrimSpace(configValue); path != "" {
		return path
	}
	return strings.TrimSpace(os.Getenv(webConfigFileEnv))
}

// wrapWebAuth puts basic auth from --web.config.file (PROD-AUTH, ADR-011) in
// front of handler. Empty path: no auth, with a startup warning, as upstream
// without the flag. The core's handler serves under the --web.external-url
// path, so the exempt paths (server.auth.unauthenticated_paths, without the
// prefix) are matched with it. An invalid file is a startup error.
func wrapWebAuth(handler http.Handler, path, routePrefix string, exempt []string, logger *slog.Logger, reg prometheus.Registerer) (http.Handler, error) {
	if path == "" {
		logger.Warn("HTTP authentication is off: set --web.config.file (or server.web_config_file, SERVER_WEB_CONFIG_FILE) to require basic auth")
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
