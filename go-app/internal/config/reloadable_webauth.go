package config

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
)

// ================================================================================
// WebAuthReloadable (PROD-AUTH)
// ================================================================================

// webAuthReloadPriority: a warning-only component, as cheap as metrics.
const webAuthReloadPriority = 20

// WebAuthReloadable reports, and never applies, changes to where HTTP
// authentication comes from.
//
// What is real: the web config file's CONTENT hot-reloads by itself
// (application.WebAuth re-reads it when it changes), so rotating a password
// needs no reload at all.
//
// What needs a restart (W605): server.web_config_file and
// server.auth.unauthenticated_paths. The middleware is built once around the
// HTTP server. Adopting a new exempt list through /-/reload is deliberately
// not offered either: a config push would then be able to open paths.
//
// A -web.config.file flag pins the path: the flag beats the config key, so a
// config-side path change is not a change the process would ever make.
type WebAuthReloadable struct {
	logger     *slog.Logger
	warnings   *RestartWarnings
	pinnedPath string

	mu      sync.Mutex
	applied webAuthState
}

type webAuthState struct {
	Path                 string
	UnauthenticatedPaths []string
}

// NewWebAuthReloadable wires a WebAuthReloadable. bootCfg is the config the
// HTTP server was built from; pinnedPath is the -web.config.file flag value
// ("" when the flag was not given).
func NewWebAuthReloadable(bootCfg *Config, pinnedPath string, warnings *RestartWarnings, logger *slog.Logger) *WebAuthReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	w := &WebAuthReloadable{logger: logger, warnings: warnings, pinnedPath: pinnedPath}
	if bootCfg != nil {
		w.applied = w.requested(bootCfg)
	}
	return w
}

func (w *WebAuthReloadable) requested(cfg *Config) webAuthState {
	state := webAuthState{
		Path:                 cfg.Server.WebConfigFile,
		UnauthenticatedPaths: cfg.Server.Auth.UnauthenticatedPaths,
	}
	if w.pinnedPath != "" {
		state.Path = w.pinnedPath
	}
	return state
}

// Name implements Reloadable.
func (w *WebAuthReloadable) Name() string { return "web_auth" }

// RelevantSections implements Reloadable.
func (w *WebAuthReloadable) RelevantSections() []string { return []string{"server"} }

// IsCritical implements Reloadable: it never fails.
func (w *WebAuthReloadable) IsCritical() bool { return false }

// ReloadPriority implements OrderedReloadable.
func (w *WebAuthReloadable) ReloadPriority() int { return webAuthReloadPriority }

// NeedsResync implements ResyncReloadable.
func (w *WebAuthReloadable) NeedsResync(newCfg *Config) bool {
	return newCfg != nil && len(w.drift(newCfg)) > 0
}

func (w *WebAuthReloadable) drift(newCfg *Config) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	requested := w.requested(newCfg)
	fields := make([]string, 0, 2)
	if requested.Path != w.applied.Path {
		fields = append(fields, "server.web_config_file")
	}
	// nil and empty both mean "no exempt paths".
	if (len(requested.UnauthenticatedPaths) != 0 || len(w.applied.UnauthenticatedPaths) != 0) &&
		!reflect.DeepEqual(requested.UnauthenticatedPaths, w.applied.UnauthenticatedPaths) {
		fields = append(fields, "server.auth.unauthenticated_paths")
	}
	return fields
}

// Reload implements Reloadable.
func (w *WebAuthReloadable) Reload(_ context.Context, _, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("web auth reload: nil config")
	}
	fields := w.drift(newCfg)
	if len(fields) == 0 {
		w.warnings.Resolve(WarnWebAuthRestartRequired, w.Name())
		return nil
	}
	warnRestartRequired(w.logger, w.warnings, RestartRequiredWarning{
		Code:      WarnWebAuthRestartRequired,
		Component: w.Name(),
		Fields:    fields,
		Reason:    "the HTTP authentication middleware is built once at startup; restart to apply. Password changes inside the web config file apply without a restart",
	})
	return nil
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*WebAuthReloadable)(nil)
	_ OrderedReloadable = (*WebAuthReloadable)(nil)
	_ ResyncReloadable  = (*WebAuthReloadable)(nil)
)
