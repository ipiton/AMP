package application

import (
	"fmt"

	appconfig "github.com/ipiton/AMP/internal/config"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
	pkglogger "github.com/ipiton/AMP/pkg/logger"
	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ================================================================================
// config.Reloadable registration (INF-A slice 1)
// ================================================================================
// ServiceRegistry owns every long-lived component, so it is the only place
// that can hand a Reloadable the live object it must swap. Registration order
// in the code below is irrelevant — DefaultConfigReloader sorts by
// ReloadPriority — but it is written in reload order anyway so the intent is
// readable:
//
//	logger (10)  -> so every later reload line uses the new level/format
//	metrics (20) -> atomic bool, cannot fail
//	llm (50)     -> in-process config swap
//	redis (80)   -> connection rebuild
//	database (90)-> connection rebuild, the most disruptive step last
//
// Components whose target object does not exist in this deployment (no
// Postgres pool in the lite profile, no Redis cache when it degraded to
// memory, no LLM client when the investigation pipeline was skipped) are still
// registered with a nil target ON PURPOSE: they then report the change as
// restart-required (W6xx) instead of the change vanishing without a trace.

// SetLogHandler supplies the process's swappable slog handler, which
// cmd/server/main.go owns because it builds the logger before config is even
// loaded. Must be called before Initialize; calling it twice, or after
// Initialize, is refused rather than silently ignored (write-once, same
// posture as the leader elector).
//
// Without it, LoggerReloadable reports log.level/log.format changes as
// restart-required.
func (r *ServiceRegistry) SetLogHandler(handler *pkglogger.SwappableHandler) error {
	if r.initialized {
		return fmt.Errorf("log handler must be set before Initialize")
	}
	if r.logHandler != nil {
		return fmt.Errorf("log handler already set")
	}
	r.logHandler = handler
	return nil
}

// MetricsGate returns the runtime on/off switch for the /metrics endpoint,
// driven by metrics.enabled and hot-reloadable. Never nil after Initialize;
// nil before it (the router must be built after Initialize — a nil gate is
// treated as "always exposed" by ExpositionGate, so an out-of-order caller
// degrades to the pre-gate behaviour rather than crashing).
func (r *ServiceRegistry) MetricsGate() *metricsv2.ExpositionGate {
	return r.metricsGate
}

// RestartWarnings returns the W6xx restart-required findings raised by the
// most recent reload attempt, oldest code first. Empty when the last reload
// applied cleanly (ReloadConfig clears the set before each attempt).
//
// Slice 2's /health/reload endpoint reports these; until then this is what
// makes "the reload returned 200 but your change is not live" discoverable
// without grepping logs.
func (r *ServiceRegistry) RestartWarnings() []appconfig.RestartRequiredWarning {
	return r.restartWarnings.List()
}

// registerReloadables wires the five infrastructure Reloadable components into
// the reloader used by the ReloadCoordinator.
func (r *ServiceRegistry) registerReloadables(reloader *appconfig.DefaultConfigReloader) {
	if reloader == nil {
		return
	}

	// A *cache.RedisCache only exists when the standard profile actually
	// reached Redis; initializeCache falls back to the in-memory cache
	// otherwise, and a nil here makes RedisReloadable say so.
	redisCache, _ := r.cache.(*infrastructurecache.RedisCache)

	reloader.Register(appconfig.NewLoggerReloadable(r.logHandler, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewMetricsReloadable(r.metricsGate, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewLLMReloadable(r.llmClient, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewRedisReloadable(redisCache, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewDatabaseReloadable(r.database, r.restartWarnings, r.logger))

	r.logger.Info("Hot-reload components registered",
		"components", reloader.GetRegisteredComponents(),
		"log_handler_swappable", r.logHandler != nil,
		"redis_client_available", redisCache != nil,
		"postgres_pool_available", r.database != nil,
		"llm_client_available", r.llmClient != nil,
	)
}
