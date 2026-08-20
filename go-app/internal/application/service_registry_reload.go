package application

import (
	"context"
	"errors"
	"fmt"

	appconfig "github.com/ipiton/AMP/internal/config"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
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

// sharedLLMClient returns the process's single *llm.HTTPLLMClient, building it
// from r.config.LLM on first use.
//
// There must be exactly ONE (fix-round C1). Before this, initializeClassification
// and initializeInvestigation each built their own client from identical config,
// and only the investigation one was registered for hot reload — so an
// `llm.model`/`llm.api_key` edit + SIGHUP reported success while every alert
// classification, the far higher-traffic path, kept the old model and the old
// credential. On a key rotation that means 401s behind an HTTP 200 and an empty
// warning list.
//
// Any future LLM consumer MUST take its client from here. Building a second
// client is not a style question: LLMReloadable swaps exactly one object, and a
// client it does not hold is a silent hot-reload hole with no Share*-style guard
// to make it declare itself.
//
// Sharing is behaviour-neutral for the circuit breaker: only ClassifyAlert
// routes through it (InvestigateAlert/InvestigateWithTools do not), so the
// investigation path cannot trip the breaker that guards classification.
func (r *ServiceRegistry) sharedLLMClient() *llm.HTTPLLMClient {
	if r.llmClient != nil {
		return r.llmClient
	}

	cfg := llm.DefaultConfig()
	cfg.Provider = r.config.LLM.Provider
	cfg.BaseURL = r.config.LLM.BaseURL
	cfg.APIKey = r.config.LLM.APIKey
	cfg.Model = r.config.LLM.Model
	cfg.MaxTokens = r.config.LLM.MaxTokens
	cfg.Temperature = r.config.LLM.Temperature
	cfg.Timeout = r.config.LLM.Timeout
	cfg.MaxRetries = r.config.LLM.MaxRetries

	r.llmClient = llm.NewHTTPLLMClient(cfg, r.logger)
	return r.llmClient
}

// MetricsGate returns the runtime on/off switch for the /metrics endpoint,
// driven by metrics.enabled and hot-reloadable. Never nil after Initialize;
// nil before it (the router must be built after Initialize — a nil gate is
// treated as "always exposed" by ExpositionGate, so an out-of-order caller
// degrades to the pre-gate behaviour rather than crashing).
func (r *ServiceRegistry) MetricsGate() *metricsv2.ExpositionGate {
	return r.metricsGate
}

// RestartWarnings returns the outstanding W6xx findings, lowest code first.
//
// These are STICKY, not per-attempt (fix-round I2): a component re-raises its
// warning on every reload attempt while its live state still differs from the
// config, and resolves it only when they agree again — so a change that needs a
// restart stays reported until it is applied or reverted, and an unrelated
// reload in between cannot erase it. W610/W611 (split state, failed post-commit
// stage) are never resolved by a config edit at all; only a restart clears
// those.
//
// Slice 2's /health/reload endpoint reports these; until then this is what
// makes "the reload returned 200 but your change is not live" discoverable
// without grepping logs.
func (r *ServiceRegistry) RestartWarnings() []appconfig.RestartRequiredWarning {
	return r.restartWarnings.List()
}

// rollbackPostCommit undoes a reload that the coordinator already committed,
// because one of the sections ServiceRegistry applies OUTSIDE the component
// registry (routing, templates, receivers, inhibition) then failed.
//
// Fix-round I4. Before this, such a failure returned an error — the operator
// saw SIGHUP/`/-/reload` fail — while `currentConfig`, `r.config` and all five
// registered components were already on the new config. That is the exact
// inverse of an incomplete rollback: "rejected" reported over a fully applied
// change. Slice 2 is about to expose reload status over HTTP, so a status that
// can lie in this direction is worth undoing rather than documenting.
//
// It restores this registry's own view first (so the re-applied appliers read
// the previous config), then asks the coordinator to roll back the committed
// config and every component. Applier rollback is best effort and its failures
// are folded into the returned error, for the same reason RollbackAll is best
// effort: abandoning the rest guarantees more divergence.
func (r *ServiceRegistry) rollbackPostCommit(
	ctx context.Context,
	previousConfig *appconfig.Config,
	stage string,
	cause error,
) error {
	r.logger.Error("post-commit reload stage failed; rolling the whole reload back",
		"stage", stage, "error", cause)

	// Reason is a FIXED string (re-review I5). The cause is logged just above:
	// it is the routing engine's own error text, which names receivers and
	// echoes matcher validation messages, and RestartRequiredWarning is served
	// verbatim by the unauthenticated /health/reload. The stage — the part an
	// operator needs to act — is already structured, in Fields.
	r.restartWarnings.Record(appconfig.RestartRequiredWarning{
		Code:      appconfig.WarnReloadPostCommitFailed,
		Component: "service-registry",
		Fields:    []string{stage},
		Reason: "a config section applied after the commit (named in fields) failed; the reload was " +
			"rolled back to the previous config — see the server log for the underlying error, then re-apply",
	})

	if previousConfig == nil {
		// Nothing to restore to (ReloadConfig always has a previous config, so
		// this is defensive). Report the split rather than pretending.
		return fmt.Errorf("%s reload failed and no previous config was captured to roll back to: %w", stage, cause)
	}

	// Restore this registry's view, then re-run the appliers against it.
	r.config = previousConfig
	r.applyKnownReceivers(previousConfig)

	var rollbackErrs []error
	if err := r.reloadRoutingTree(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("routing rollback failed: %w", err))
	}
	r.reloadTemplates()
	r.applyConfigTargets()

	if err := r.reloadCoordinator.RollbackCommitted(ctx, previousConfig); err != nil {
		rollbackErrs = append(rollbackErrs, err)
	}

	if len(rollbackErrs) > 0 {
		return fmt.Errorf("%s reload failed (%w) AND rollback was incomplete — the process is in a split state, restart to converge: %w",
			stage, cause, errors.Join(rollbackErrs...))
	}

	return fmt.Errorf("%s reload failed, rolled back to the previous config: %w", stage, cause)
}

// ReloadStatus returns the full outcome of the last reload attempt: the
// coordinator's own status plus the outstanding W6xx findings.
//
// This is what /health/reload serves and what the config-reloader sidecar
// verifies against (slice 2). Composed here rather than in the coordinator
// because only the registry holds both halves — the coordinator knows the phase
// outcome, the registry owns the warning collector that the components and the
// post-commit appliers write to.
func (r *ServiceRegistry) ReloadStatus() appconfig.ReloadStatusSnapshot {
	snapshot := appconfig.ReloadStatusSnapshot{
		RestartRequired: r.restartWarnings.List(),
	}
	if r.reloadCoordinator != nil {
		snapshot.CoordinatorStatus = r.reloadCoordinator.StatusSnapshot()
	}
	return snapshot
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

	// bootCfg is r.config: what every one of these components is running right
	// now. They compare against it (not against the coordinator's old/new
	// pair) so a declined change stays reported until it is applied or
	// reverted — see config.ResyncReloadable.
	bootCfg := r.config

	reloader.Register(appconfig.NewLoggerReloadable(r.logHandler, bootCfg, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewMetricsReloadable(r.metricsGate, bootCfg, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewLLMReloadable(r.llmClient, bootCfg, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewRedisReloadable(redisCache, bootCfg, r.restartWarnings, r.logger))
	reloader.Register(appconfig.NewDatabaseReloadable(r.database, bootCfg, r.restartWarnings, r.logger))

	// Give the coordinator the same collector, so an incomplete rollback's
	// W610 split-state warning is queryable and not just a log line.
	if r.reloadCoordinator != nil {
		r.reloadCoordinator.SetRestartWarnings(r.restartWarnings)
	}

	r.logger.Info("Hot-reload components registered",
		"components", reloader.GetRegisteredComponents(),
		"log_handler_swappable", r.logHandler != nil,
		"redis_client_available", redisCache != nil,
		"postgres_pool_available", r.database != nil,
		"llm_client_available", r.llmClient != nil,
	)
}
