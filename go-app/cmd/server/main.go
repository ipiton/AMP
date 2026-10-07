package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ipiton/AMP/internal/application"
	"github.com/ipiton/AMP/internal/buildinfo"
	"github.com/ipiton/AMP/internal/config"
	pkglogger "github.com/ipiton/AMP/pkg/logger"
)

const runtimeConfigFileEnv = "AMP_CONFIG_FILE"

func main() {
	// -web.route-prefix mirrors upstream Alertmanager's flag of the same
	// name (PARITY-B6). When set, it overrides server.route_prefix from
	// config.
	routePrefixFlag := flag.String("web.route-prefix", "",
		"Prefix for the internal routes of web endpoints. Overrides server.route_prefix in config when set.")
	// -web.config.file mirrors upstream's flag (PROD-AUTH): a web config file
	// with basic_auth_users enables HTTP basic authentication.
	webConfigFlag := flag.String("web.config.file", "",
		"Path to a web config file (basic_auth_users) that enables HTTP basic authentication. Overrides server.web_config_file in config when set.")
	flag.Parse()

	// Bootstrap logging: stdout/json/info, because config has not been read
	// yet. installLogging below replaces this with the operator's `log:`
	// settings — sink included — as soon as it has.
	logHandler, err := pkglogger.NewSwappableHandler(os.Stdout, slog.LevelInfo, "json")
	if err != nil {
		// Cannot happen for the literals above, but a nil handler would panic
		// on the first log line, so fail loudly instead.
		fmt.Fprintf(os.Stderr, "failed to build log handler: %v\n", err)
		os.Exit(1)
	}
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	slog.Info("🚀 Starting Alertmanager++",
		"version", buildinfo.Version,
		"profile", "OSS Core",
	)

	// Load configuration. Any failure is fatal: starting on a partial or
	// built-in config would silently drop routes, receivers and auth.
	configPath, explicitConfigPath := resolveRuntimeConfigPath()
	if err := checkConfigPath(configPath, explicitConfigPath); err != nil {
		slog.Error("failed to load configuration", "path", configPath, "error", err)
		os.Exit(1)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Error("failed to load configuration", "path", configPath, "error", err)
		os.Exit(1)
	}
	// PARITY-B6: effective route prefix — explicit -web.route-prefix flag
	// wins; otherwise use server.route_prefix, falling back to inheriting
	// the path component of server.external_url (upstream's own default
	// derivation for --web.route-prefix), or no prefix if neither is set.
	cfg.Server.RoutePrefix = application.ResolveRoutePrefix(cfg.Server.RoutePrefix, cfg.Server.ExternalURL)
	if *routePrefixFlag != "" {
		cfg.Server.RoutePrefix = *routePrefixFlag
	}

	cfg.Server.WebConfigFile = resolveWebConfigFile(*webConfigFlag, cfg.Server.WebConfigFile)

	// Install the operator's log.* settings now that config exists. Before
	// INF-A slice 1 the logger was hardcoded to JSON/info and cfg.Log was
	// never read at all, so `log.level: debug` did nothing even across a
	// restart.
	logger, logHandler = installLogging(logger, logHandler, cfg)
	slog.SetDefault(logger)

	// Load the web config before anything slow: a requested but unusable
	// authentication setup must stop the process, never degrade to open.
	webAuth, err := newWebAuth(cfg, logger, nil)
	if err != nil {
		slog.Error("Failed to load web config; refusing to start without the requested authentication", "error", err)
		os.Exit(1)
	}
	logWebAuthState(logger, cfg, webAuth)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Service Registry
	registry, err := application.NewServiceRegistry(cfg, logger)
	if err != nil {
		slog.Error("Failed to create service registry", "error", err)
		os.Exit(1)
	}

	// Hand the swappable handler over BEFORE Initialize: that is when the
	// registry registers LoggerReloadable, and a nil handler there would make
	// every log.level change report "restart required".
	if err := registry.SetLogHandler(logHandler); err != nil {
		slog.Error("Failed to wire the swappable log handler", "error", err)
		os.Exit(1)
	}

	if err := registry.SetWebConfigFlag(*webConfigFlag); err != nil {
		slog.Error("Failed to wire the web config flag", "error", err)
		os.Exit(1)
	}

	if err := registry.Initialize(ctx); err != nil {
		slog.Error("Failed to initialize services", "error", err)
		os.Exit(1)
	}

	// Initialize templates (dashboard)
	initTemplates()

	// Create HTTP mux and router
	mux := http.NewServeMux()
	router := application.NewRouter(registry)
	router.SetupRoutes(mux)

	// Dashboard and static files (legacy/compatibility)
	registerLegacyDashboardRoutes(mux, registry)

	// PARITY-B6: mount everything under server.route_prefix / -web.route-prefix
	// when configured. Empty prefix (the default) leaves mux unwrapped.
	// PROD-AUTH: auth sits INSIDE the prefix, so unauthenticated_paths are
	// written without it and do not depend on route_prefix.
	var apiHandler http.Handler = mux
	if webAuth != nil {
		apiHandler = webAuth.Wrap(mux)
	}
	rootHandler := application.WithRoutePrefix(apiHandler, cfg.Server.RoutePrefix)

	// Start server
	port := cfg.Server.Port

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      rootHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// SIGHUP → hot config reload, matching upstream Alertmanager (which
	// reloads on SIGHUP as well as on POST /-/reload). Final review finding
	// 1: internal/config/reload_coordinator.go and the compatibility docs
	// both described SIGHUP as the reload trigger, but no handler was ever
	// installed — only the HTTP endpoint worked.
	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)
	go watchReloadSignal(ctx, hupChan, registry, slog.Default())

	// Graceful shutdown (PROD-GRACEFUL-SHUTDOWN): readiness off, HTTP drain,
	// then services. main waits on shutdownDone after ListenAndServe returns,
	// so the drain and the final snapshot are never cut off by process exit.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- runShutdown(sigChan, registry, server, registry,
			effectiveShutdownTimeout(cfg.Server.GracefulShutdownTimeout), slog.Default())
	}()

	// alertmanager-parity wave-5 item 4 (FU-DOUBLE-NORMALIZE-ROUTES): computed
	// once and reused below — this used to call NormalizeRoutePrefix twice on
	// the exact same cfg.Server.RoutePrefix value (once for the log field,
	// once inline in the dashboard URL). NormalizeRoutePrefix is pure/
	// idempotent (see TestNormalizeRoutePrefix_Idempotent), so the duplicate
	// call was never a correctness bug, just redundant work on every startup.
	effectiveRoutePrefix := application.NormalizeRoutePrefix(cfg.Server.RoutePrefix)
	slog.Info("🎯 Server listening",
		"port", port,
		"routePrefix", effectiveRoutePrefix,
		"dashboard", fmt.Sprintf("http://localhost:%d%s/dashboard", port, effectiveRoutePrefix),
	)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	if err := <-shutdownDone; err != nil {
		slog.Error("Shutdown finished with errors", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

// webConfigFileEnv is also read directly, not only through viper
// (server.web_config_file). The direct read predates config loading from the
// environment without a file (PROD-CONFIG-FILE-FALLBACK) and is kept as a
// second path to the auth setting.
const webConfigFileEnv = "SERVER_WEB_CONFIG_FILE"

// resolveWebConfigFile picks the web config path: the -web.config.file flag,
// then server.web_config_file, then the environment variable.
func resolveWebConfigFile(flagValue, configValue string) string {
	if path := strings.TrimSpace(flagValue); path != "" {
		return path
	}
	if path := strings.TrimSpace(configValue); path != "" {
		return path
	}
	return strings.TrimSpace(os.Getenv(webConfigFileEnv))
}

// newWebAuth builds the basic-auth middleware, or returns nil when no web
// config is set. reg nil means the default Prometheus registry.
func newWebAuth(cfg *config.Config, logger *slog.Logger, reg prometheus.Registerer) (*application.WebAuth, error) {
	if cfg.Server.WebConfigFile == "" {
		return nil, nil
	}
	return application.NewWebAuth(application.WebAuthOptions{
		Path:                 cfg.Server.WebConfigFile,
		UnauthenticatedPaths: cfg.Server.Auth.UnauthenticatedPaths,
		Logger:               logger,
		Registerer:           reg,
	})
}

// logWebAuthState says loudly when the API is open, and flags the legacy
// webhook.authentication keys, which look like protection but are not read.
func logWebAuthState(logger *slog.Logger, cfg *config.Config, webAuth *application.WebAuth) {
	if webAuth == nil {
		logger.Warn("HTTP API authentication is DISABLED: anyone with network access can create silences, post alerts and trigger reloads. Set -web.config.file or server.web_config_file.")
	} else {
		logger.Info("HTTP API basic authentication enabled",
			"web_config_file", cfg.Server.WebConfigFile,
			"users", webAuth.UserCount(),
			"unauthenticated_paths", cfg.Server.Auth.UnauthenticatedPaths,
		)
	}
	if cfg.Webhook.Authentication.Enabled {
		logger.Warn("webhook.authentication.* is not enforced by AMP; use -web.config.file or server.web_config_file for HTTP authentication")
	}
}

// configReloader is the slice of ServiceRegistry that watchReloadSignal needs.
// Declared as an interface purely so the SIGHUP loop is unit-testable without
// standing up a full registry.
type configReloader interface {
	ReloadConfig(ctx context.Context) error
}

// watchReloadSignal reloads configuration once per signal received on sigChan,
// until ctx is cancelled or the channel is closed. Reload failures are logged
// and the loop continues: a bad config on disk must not stop the process from
// picking up a later, fixed one (and the previous config stays active, per the
// reload pipeline's rollback semantics).
//
// Runs for the process lifetime because SIGHUP is repeatable, unlike the
// one-shot shutdown signals.
func watchReloadSignal(ctx context.Context, sigChan <-chan os.Signal, reloader configReloader, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-sigChan:
			if !ok {
				return
			}

			logger.Info("SIGHUP received, reloading configuration")
			// Bounded: a wedged reload must not block the next SIGHUP
			// forever. 30s matches the reload pipeline's own lock TTL.
			reloadCtx, reloadCancel := context.WithTimeout(ctx, 30*time.Second)
			err := reloader.ReloadConfig(reloadCtx)
			reloadCancel()

			if err != nil {
				logger.Error("SIGHUP config reload failed; previous configuration remains active", "error", err)
				continue
			}
			logger.Info("SIGHUP config reload applied")
		}
	}
}

// resolveRuntimeConfigPath returns the config file path and whether it was
// set explicitly via AMP_CONFIG_FILE rather than defaulted to ./config.yaml.
func resolveRuntimeConfigPath() (string, bool) {
	path := strings.TrimSpace(os.Getenv(runtimeConfigFileEnv))
	if path != "" {
		return path, true
	}
	return "config.yaml", false
}

// checkConfigPath rejects an explicit AMP_CONFIG_FILE that does not exist:
// that is almost always a broken mount, and running on env and defaults
// instead would start without the operator's routes, receivers and auth.
// A missing default ./config.yaml is fine — configuration then comes from
// the environment (the Helm chart's default, configFile.enabled: false).
func checkConfigPath(path string, explicit bool) error {
	_, err := os.Stat(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		// Other stat errors surface from LoadConfig with full context.
		return nil
	}
	if explicit {
		return fmt.Errorf("config file %q from %s does not exist", path, runtimeConfigFileEnv)
	}
	slog.Info("no config file, using environment and defaults", "path", path)
	return nil
}

// installLogging replaces the bootstrap logger with one built from the
// operator's `log:` section, and returns the logger plus the swappable handler
// that LoggerReloadable will later swap.
//
// A whole new handler is built rather than only swapping level/format, because
// the output SINK (log.output/log.filename and the lumberjack rotation knobs)
// is fixed at handler construction — pkglogger.SetupWriter is the only thing
// that honours it. Fix-round I1: before this, SetupWriter had no caller in the
// server binary at all, so the process always logged to stdout and
// LoggerReloadable's W602 "restart to apply" was a lie. Now a restart really
// does apply those fields, which is what makes that warning honest.
//
// On failure the bootstrap logger is kept: losing the requested format or sink
// is a nuisance, exiting because of one YAML typo is an outage.
func installLogging(
	bootstrapLogger *slog.Logger,
	bootstrapHandler *pkglogger.SwappableHandler,
	cfg *config.Config,
) (*slog.Logger, *pkglogger.SwappableHandler) {
	if cfg == nil {
		return bootstrapLogger, bootstrapHandler
	}

	logger, handler, err := pkglogger.NewSwappableLogger(config.LoggerConfigFrom(cfg.Log))
	if err != nil {
		slog.Warn("Ignoring log configuration; keeping the bootstrap stdout/json/info logger",
			"level", cfg.Log.Level, "format", cfg.Log.Format, "output", cfg.Log.Output, "error", err)
		return bootstrapLogger, bootstrapHandler
	}

	logger.Info("Log configuration applied",
		"level", handler.Level().String(),
		"format", handler.Format(),
		"output", cfg.Log.Output,
	)
	return logger, handler
}
