package services

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // BusinessMetrics/MetricsManager have no pkg/metrics/v2 equivalent yet; migration tracked separately
)

// LLMClient defines the interface for LLM classification
type LLMClient interface {
	ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
	Health(ctx context.Context) error
}

// FilterEngine defines the interface for alert filtering
type FilterEngine interface {
	ShouldBlock(alert *core.Alert, classification *core.ClassificationResult) (bool, string)
}

// Publisher defines the interface for alert publishing
type Publisher interface {
	PublishToAll(ctx context.Context, alert *core.Alert) error
	PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error
}

// InvestigationSubmitter is the minimal interface AlertProcessor needs to fire-and-forget investigations.
type InvestigationSubmitter interface {
	Submit(alert *core.Alert, classification *core.ClassificationResult)
}

// GroupManager is the AlertProcessor's view of the alert-grouping subsystem
// (task 2.3, alertmanager-parity): only AddAlertToGroup is needed to route an
// alert into its group instead of publishing it directly. Defined locally
// (interface segregation) rather than depending on the full
// grouping.AlertGroupManager interface (lifecycle + query + metrics, ~9
// methods) so test fakes only need this one method.
//
// grouping.DefaultGroupManager (and anything else implementing
// grouping.AlertGroupManager) satisfies this interface automatically.
type GroupManager interface {
	AddAlertToGroup(ctx context.Context, alert *core.Alert, groupKey grouping.GroupKey) (*grouping.AlertGroup, error)
}

// AlertProcessor handles alert processing with enrichment mode support
type AlertProcessor struct {
	enrichmentManager  EnrichmentModeManager
	llmClient          LLMClient
	filterEngine       FilterEngine
	publisher          Publisher
	deduplication      DeduplicationService              // TN-036 Phase 3: Deduplication service
	investigationQueue InvestigationSubmitter            // PHASE-5A: async investigation pipeline
	inhibitionCache    inhibition.ActiveAlertCache       // TN-130 PARITY-A2: cache of firing alerts
	inhibitionMatcher  inhibition.InhibitionMatcher      // TN-130 Phase 6: Inhibition checking
	inhibitionState    inhibition.InhibitionStateManager // TN-130 Phase 6: State tracking
	businessMetrics    *metrics.BusinessMetrics          // TN-130 Phase 6: Business metrics for inhibition
	routeEvaluator     RouteEvaluator                    // task 1.4: optional, nil in lite/legacy mode (no route: section)
	logger             *slog.Logger
	metrics            *metrics.MetricsManager

	// Grouping subsystem wiring (task 2.3, alertmanager-parity). groupingEnabled
	// mirrors config.Grouping.Enabled and is tracked separately from
	// groupManager/groupKeyGenerator being non-nil: an operator can set
	// grouping.enabled=true without a `route:` tree configured (ServiceRegistry
	// then leaves groupManager nil — see initializeGrouping's "no route tree"
	// skip). shouldGroup() treats that combination as "can't group this alert"
	// and falls back to direct publish with a warning (see warnGroupingFallback)
	// rather than silently dropping the intent to group.
	groupingEnabled   bool
	groupManager      GroupManager
	groupKeyGenerator *grouping.GroupKeyGenerator

	// lastRoutingDecision holds the most recently computed RoutingDecision
	// (task 1.4). Observability/testing only: Phase 2 (task 2.3) will carry
	// per-alert-group decisions into grouping/timers/publishing instead of
	// this single latest value. Holds *RoutingDecision; nil until the first
	// alert is evaluated (or forever, if routeEvaluator is nil).
	//
	// Single shared slot, NOT safe under concurrent ProcessAlert calls: two
	// alerts processed at the same time race on this value and the "last"
	// one observed via LastRoutingDecision() may not correspond to either
	// call's own alert. This is fine for observability/testing scaffolding
	// but must not be extended into real routing state — Phase 2 needs a
	// per-group decision store, not a bigger version of this field.
	lastRoutingDecision atomic.Value
}

// AlertProcessorConfig holds configuration for AlertProcessor
type AlertProcessorConfig struct {
	EnrichmentManager  EnrichmentModeManager
	LLMClient          LLMClient // optional, required only for enriched mode
	FilterEngine       FilterEngine
	Publisher          Publisher
	Deduplication      DeduplicationService              // TN-036 Phase 3: optional, recommended for production
	InvestigationQueue InvestigationSubmitter            // PHASE-5A: optional, fire-and-forget investigation
	InhibitionCache    inhibition.ActiveAlertCache       // TN-130 PARITY-A2: optional, cache of firing alerts
	InhibitionMatcher  inhibition.InhibitionMatcher      // TN-130 Phase 6: optional, recommended for inhibition
	InhibitionState    inhibition.InhibitionStateManager // TN-130 Phase 6: optional, for state tracking
	BusinessMetrics    *metrics.BusinessMetrics          // TN-130 Phase 6: required if using inhibition
	RouteEvaluator     RouteEvaluator                    // task 1.4: optional, nil in lite/legacy mode (no route: section)

	// GroupingEnabled mirrors config.Grouping.Enabled (task 2.3). When true,
	// alerts with a computed RoutingDecision AND a non-nil GroupManager flow
	// into groups instead of being published directly — see shouldGroup().
	// When true but GroupManager/GroupKeyGenerator/the per-alert
	// RoutingDecision aren't available, ProcessAlert falls back to direct
	// publish and logs a warning (grouping without a GroupBy/receiver
	// decision has nothing to group by).
	GroupingEnabled bool
	// GroupManager routes alerts into groups (task 2.3). Optional: nil when
	// grouping is disabled or no route: tree is configured.
	GroupManager GroupManager
	// GroupKeyGenerator derives group keys from RoutingDecision.GroupBy +
	// alert labels (task 2.3). Required alongside GroupManager: both must be
	// set together, or both left nil.
	GroupKeyGenerator *grouping.GroupKeyGenerator

	Logger  *slog.Logger
	Metrics *metrics.MetricsManager
}

// NewAlertProcessor creates a new alert processor
func NewAlertProcessor(config AlertProcessorConfig) (*AlertProcessor, error) {
	// EnrichmentManager can be nil for basic mode
	if config.FilterEngine == nil {
		return nil, fmt.Errorf("filter engine is required")
	}
	if config.Publisher == nil {
		return nil, fmt.Errorf("publisher is required")
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// GroupManager and GroupKeyGenerator are a pair (task 2.3): AddAlertToGroup
	// needs a key to add the alert under, so wiring one without the other is a
	// caller bug, not a valid "partially enabled" state.
	if (config.GroupManager == nil) != (config.GroupKeyGenerator == nil) {
		return nil, fmt.Errorf("group manager and group key generator must be set together (both or neither)")
	}

	return &AlertProcessor{
		enrichmentManager:  config.EnrichmentManager,
		llmClient:          config.LLMClient,
		filterEngine:       config.FilterEngine,
		publisher:          config.Publisher,
		deduplication:      config.Deduplication,
		investigationQueue: config.InvestigationQueue, // PHASE-5A
		inhibitionCache:    config.InhibitionCache,    // TN-130 PARITY-A2
		inhibitionMatcher:  config.InhibitionMatcher,  // TN-130 Phase 6
		inhibitionState:    config.InhibitionState,    // TN-130 Phase 6
		businessMetrics:    config.BusinessMetrics,    // TN-130 Phase 6
		routeEvaluator:     config.RouteEvaluator,     // task 1.4
		groupingEnabled:    config.GroupingEnabled,    // task 2.3
		groupManager:       config.GroupManager,       // task 2.3
		groupKeyGenerator:  config.GroupKeyGenerator,  // task 2.3
		logger:             config.Logger,
		metrics:            config.Metrics,
	}, nil
}

// LastRoutingDecision returns the most recently computed routing decision
// (task 1.4), or nil if no route tree is configured (routeEvaluator is nil)
// or no alert has been evaluated yet. Intended for observability and tests;
// it is not part of the publish path.
//
// Single shared slot: NOT safe to rely on under concurrent ProcessAlert
// calls — with alerts in flight on multiple goroutines, the value returned
// here can belong to any of them, not necessarily the caller's own alert.
// This is observability/testing scaffolding only; do not extend it into a
// real per-alert or per-group decision store — Phase 2 (task 2.3) must
// build that separately.
func (p *AlertProcessor) LastRoutingDecision() *RoutingDecision {
	v := p.lastRoutingDecision.Load()
	if v == nil {
		return nil
	}
	return v.(*RoutingDecision)
}

// evaluateRoute computes and logs a RoutingDecision for the alert, when a
// RouteEvaluator is configured (task 1.4), and returns it so callers can
// thread it explicitly through this alert's own processing call (task 2.3 —
// see the LastRoutingDecision doc comment for why the atomic slot below is
// observability-only and must not be read back as processing state). Errors
// are non-fatal (fail open, same posture as the inhibition check above): a
// failed/absent routing decision degrades this alert to direct publish, it
// never blocks processing.
func (p *AlertProcessor) evaluateRoute(alert *core.Alert) *RoutingDecision {
	if p.routeEvaluator == nil {
		return nil
	}

	decision, err := p.routeEvaluator.Evaluate(alert.Labels)
	if err != nil {
		p.logger.Warn("Route evaluation failed, continuing without a routing decision",
			"error", err,
			"alert", alert.AlertName,
			"fingerprint", alert.Fingerprint)
		return nil
	}

	p.lastRoutingDecision.Store(decision)
	p.logger.Info("Routing decision computed",
		"alert", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"receiver", decision.Receiver,
		"matched_route", decision.MatchedRoute,
		"group_by", decision.GroupBy,
		"group_wait", decision.GroupWait,
		"group_interval", decision.GroupInterval,
		"repeat_interval", decision.RepeatInterval)
	return decision
}

// shouldGroup reports whether alert should be routed into a group instead of
// published directly (task 2.3).
//
// Mutual exclusion contract: the grouping path is taken only when ALL of the
// following hold — config.Grouping.Enabled (groupingEnabled), a GroupManager
// is wired (requires a `route:` tree at startup, see ServiceRegistry.
// initializeGrouping), and a RoutingDecision was computed for THIS alert
// (route evaluation succeeded). Any gap falls back to direct publish — see
// warnGroupingFallback — never both paths for the same alert.
func (p *AlertProcessor) shouldGroup(decision *RoutingDecision) bool {
	return p.groupingEnabled && p.groupManager != nil && p.groupKeyGenerator != nil && decision != nil
}

// warnGroupingFallback logs when grouping is enabled but this specific alert
// can't take the grouping path (no GroupManager/GroupKeyGenerator wired, or
// route evaluation failed/was never configured for this alert) — task 2.3
// constraint: grouping.enabled=true without a usable routing decision falls
// back to direct publish LOUDLY rather than silently.
func (p *AlertProcessor) warnGroupingFallback(decision *RoutingDecision) {
	if !p.groupingEnabled || p.shouldGroup(decision) {
		return
	}
	p.logger.Warn("Grouping enabled but this alert has no usable routing decision/group manager; falling back to direct publish",
		"has_group_manager", p.groupManager != nil,
		"has_routing_decision", decision != nil)
}

// routeAlertToGroup adds alert to its group instead of publishing it
// directly (task 2.3). Callers must only invoke this when shouldGroup(decision)
// is true.
//
// Timer semantics — divergence from the task brief, documented per task
// instructions: the brief asks for `ResetTimer` when the alert lands in an
// EXISTING group. Upstream Alertmanager does NOT extend an in-progress
// group_interval wait when a new alert arrives: aggrGroup.insert() only ever
// resets its dispatch timer to fire SOONER (to 0, after the group's first
// flush, for an alert newer than that flush), never to restart the full
// wait. Unconditionally calling
// ResetTimer(groupKey, GroupIntervalTimer, decision.GroupInterval) on every
// alert added to an existing group would restart the FULL group_interval
// wait each time — under a steady stream of alerts arriving faster than
// group_interval, the timer would never expire and notifications would
// starve indefinitely. grouping.DefaultGroupManager.AddAlertToGroup (task
// 2.2) already starts the group_wait timer for brand-new groups and
// persists the alert into the group's storage before any pending timer
// fires, so an existing group's already-running timer naturally picks up
// the new alert at its next scheduled boundary — no extra timer call is
// needed or correct here.
func (p *AlertProcessor) routeAlertToGroup(ctx context.Context, alert *core.Alert, decision *RoutingDecision) error {
	key := p.groupKeyGenerator.GenerateKeyOrDefault(alert.Labels, decision.GroupBy)

	if _, err := p.groupManager.AddAlertToGroup(ctx, alert, key); err != nil {
		return fmt.Errorf("add alert to group: %w", err)
	}

	p.logger.Info("Alert routed to group",
		"alert", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"group_key", key,
		"receiver", decision.Receiver,
		"group_wait", decision.GroupWait,
		"group_interval", decision.GroupInterval)

	return nil
}

// ProcessAlert processes an alert based on current enrichment mode
func (p *AlertProcessor) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	startTime := time.Now()

	// TN-036 Phase 3: Step 0 - Deduplication (before enrichment/filtering)
	if p.deduplication != nil {
		dedupResult, err := p.deduplication.ProcessAlert(ctx, alert)
		if err != nil {
			// SPLIT-BRAIN-RISK: dedup owns the database write. Swallowing this
			// error used to let the alert continue into the in-memory store and
			// publishers while the DB had nothing — the two stores diverged
			// silently. Fail the alert instead so the sender retries.
			p.logger.Error("Deduplication failed", "error", err, "alert", alert.AlertName)
			return fmt.Errorf("alert persistence failed: %w", err)
		} else {
			p.logger.Info("Deduplication result",
				"action", dedupResult.Action,
				"alert", alert.AlertName,
				"fingerprint", alert.Fingerprint,
				"processing_time", dedupResult.ProcessingTime)

			// If alert was ignored (exact duplicate), skip further processing
			if dedupResult.Action == ProcessActionIgnored {
				p.logger.Info("Alert ignored as duplicate, skipping processing",
					"alert", alert.AlertName,
					"fingerprint", alert.Fingerprint)
				return nil // Not an error, just a duplicate
			}

			// Use deduplicated alert for further processing (may be updated)
			alert = dedupResult.Alert
		}
	}

	// TN-130 PARITY-A2: Step 0.5 — Update inhibition cache and cleanup on status change
	if p.inhibitionCache != nil {
		switch alert.Status {
		case core.StatusFiring:
			if err := p.inhibitionCache.AddFiringAlert(ctx, alert); err != nil {
				p.logger.Warn("Failed to add alert to inhibition cache",
					"error", err,
					"alert", alert.AlertName,
					"fingerprint", alert.Fingerprint)
				// Non-critical: continue processing
			}
		case core.StatusResolved:
			if err := p.inhibitionCache.RemoveAlert(ctx, alert.Fingerprint); err != nil {
				p.logger.Warn("Failed to remove alert from inhibition cache",
					"error", err,
					"alert", alert.AlertName,
					"fingerprint", alert.Fingerprint)
				// Non-critical: continue processing
			}
			if p.inhibitionState != nil {
				p.cleanupInhibitionsForSource(ctx, alert.Fingerprint)
			}
		}
	}

	// TN-130 Phase 6: Step 1 - Inhibition check (after dedup, before classification)
	if p.inhibitionMatcher != nil && alert.Status == core.StatusFiring {
		inhibitionResult, err := p.inhibitionMatcher.ShouldInhibit(ctx, alert)
		if err != nil {
			p.logger.Warn("Inhibition check failed, continuing with processing",
				"error", err,
				"alert", alert.AlertName,
				"fingerprint", alert.Fingerprint)
			// Fail-safe: continue processing on inhibition error
		} else if inhibitionResult != nil && inhibitionResult.Matched {
			p.logger.Info("Alert inhibited by rule",
				"alert", alert.AlertName,
				"fingerprint", alert.Fingerprint,
				"inhibited_by", inhibitionResult.InhibitedBy.Fingerprint,
				"rule", inhibitionResult.Rule.Name,
				"duration", inhibitionResult.MatchDuration)

			// Record inhibition state for tracking
			if p.inhibitionState != nil {
				inhibitionStateRecord := &inhibition.InhibitionState{
					TargetFingerprint: alert.Fingerprint,
					SourceFingerprint: inhibitionResult.InhibitedBy.Fingerprint,
					RuleName:          inhibitionResult.Rule.Name,
					InhibitedAt:       time.Now(),
					// ExpiresAt: nil means lasts until source resolves
				}
				if err := p.inhibitionState.RecordInhibition(ctx, inhibitionStateRecord); err != nil {
					p.logger.Warn("Failed to record inhibition state", "error", err)
					// Non-critical: inhibition still happens
				}
			}

			// Record inhibition metrics
			if p.businessMetrics != nil {
				p.businessMetrics.RecordInhibitionCheck("inhibited")
				p.businessMetrics.RecordInhibitionMatch(inhibitionResult.Rule.Name)
				p.businessMetrics.RecordInhibitionDuration("check", inhibitionResult.MatchDuration.Seconds())
			}

			// Skip publishing - alert is inhibited
			return nil
		} else {
			// Alert is NOT inhibited, continue processing
			p.logger.Debug("Alert not inhibited, continuing processing",
				"alert", alert.AlertName,
				"fingerprint", alert.Fingerprint)

			// Record allowed metric
			if p.businessMetrics != nil {
				p.businessMetrics.RecordInhibitionCheck("allowed")
			}
		}
	}

	// Task 1.4 computes a routing decision from the full route tree, when
	// configured. Task 2.3 threads it explicitly into this call's own
	// processing (see evaluateRoute's doc comment on why NOT the
	// lastRoutingDecision atomic slot): each mode handler below decides
	// per-alert whether to publish directly or route into a group.
	decision := p.evaluateRoute(alert)

	// Get current enrichment mode
	mode, err := p.enrichmentManager.GetMode(ctx)
	if err != nil {
		p.logger.Error("Failed to get enrichment mode", "error", err)
		// Fallback to default mode (enriched)
		mode = EnrichmentModeEnriched
	}

	p.logger.Info("Processing alert",
		"alert", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"mode", mode,
	)

	// Route to appropriate handler based on mode
	var processErr error
	switch mode {
	case EnrichmentModeTransparentWithRecommendations:
		processErr = p.processTransparentWithRecommendations(ctx, alert, decision)
	case EnrichmentModeTransparent:
		processErr = p.processTransparent(ctx, alert, decision)
	case EnrichmentModeEnriched:
		processErr = p.processEnriched(ctx, alert, decision)
	default:
		p.logger.Warn("Unknown enrichment mode, falling back to enriched", "mode", mode)
		processErr = p.processEnriched(ctx, alert, decision)
	}

	// Record metrics
	duration := time.Since(startTime)
	if p.metrics != nil {
		// TODO: Add alert processing metrics
		_ = duration
	}

	if processErr != nil {
		p.logger.Error("Alert processing failed",
			"alert", alert.AlertName,
			"mode", mode,
			"error", processErr,
			"duration", duration,
		)
		return processErr
	}

	p.logger.Info("Alert processed successfully",
		"alert", alert.AlertName,
		"mode", mode,
		"duration", duration,
	)

	return nil
}

// processTransparentWithRecommendations bypasses all processing (emergency mode)
func (p *AlertProcessor) processTransparentWithRecommendations(ctx context.Context, alert *core.Alert, decision *RoutingDecision) error {
	p.logger.Info("Processing in transparent_with_recommendations mode (bypass all)",
		"alert", alert.AlertName,
	)

	// NO LLM classification
	// NO filtering
	// Task 2.3: grouping.enabled routes into a group instead of publishing
	// directly (mutually exclusive — see shouldGroup).
	if p.shouldGroup(decision) {
		return p.routeAlertToGroup(ctx, alert, decision)
	}
	p.warnGroupingFallback(decision)

	// Publish to ALL targets immediately
	return p.publisher.PublishToAll(ctx, alert)
}

// processTransparent processes without LLM but with filtering
func (p *AlertProcessor) processTransparent(ctx context.Context, alert *core.Alert, decision *RoutingDecision) error {
	p.logger.Info("Processing in transparent mode (no LLM, with filtering)",
		"alert", alert.AlertName,
	)

	// NO LLM classification
	// Apply filters
	blocked, reason := p.filterEngine.ShouldBlock(alert, nil)
	if blocked {
		p.logger.Info("Alert blocked by filter",
			"alert", alert.AlertName,
			"reason", reason,
		)
		// TODO: Record filter metrics
		return nil // Not an error, just filtered out
	}

	// Task 2.3: grouping.enabled routes into a group instead of publishing
	// directly (mutually exclusive — see shouldGroup).
	if p.shouldGroup(decision) {
		return p.routeAlertToGroup(ctx, alert, decision)
	}
	p.warnGroupingFallback(decision)

	// Publish to ALL configured targets
	return p.publisher.PublishToAll(ctx, alert)
}

// processEnriched processes with full LLM classification and filtering (production mode)
func (p *AlertProcessor) processEnriched(ctx context.Context, alert *core.Alert, decision *RoutingDecision) error {
	p.logger.Info("Processing in enriched mode (full LLM + filtering)",
		"alert", alert.AlertName,
	)

	// Check if LLM client is available
	if p.llmClient == nil {
		p.logger.Warn("LLM client not configured, falling back to transparent mode")
		return p.processTransparent(ctx, alert, decision)
	}

	// Step 1: Classify with LLM
	classification, err := p.llmClient.ClassifyAlert(ctx, alert)
	if err != nil {
		p.logger.Error("LLM classification failed, falling back to transparent mode",
			"alert", alert.AlertName,
			"error", err,
		)
		// Graceful degradation: fall back to transparent mode
		return p.processTransparent(ctx, alert, decision)
	}

	p.logger.Info("Alert classified",
		"alert", alert.AlertName,
		"severity", classification.Severity,
		"confidence", classification.Confidence,
	)

	// PHASE-5A: Submit fire-and-forget investigation (does not block Phase 1).
	if p.investigationQueue != nil {
		p.investigationQueue.Submit(alert, classification)
	}

	// Step 2: Apply filters (with classification context)
	blocked, reason := p.filterEngine.ShouldBlock(alert, classification)
	if blocked {
		p.logger.Info("Alert blocked by filter",
			"alert", alert.AlertName,
			"reason", reason,
			"severity", classification.Severity,
		)
		// TODO: Record filter metrics
		return nil // Not an error, just filtered out
	}

	// Task 2.3: grouping.enabled routes into a group instead of publishing
	// directly (mutually exclusive — see shouldGroup). Classification is not
	// carried into the group (AlertGroup stores raw *core.Alert only) — the
	// notify chain built on top of groups (task 2.4) does not yet have a
	// classification-aware path either, so this is a scoped, documented gap
	// rather than a regression: today's direct-publish PublishWithClassification
	// is unaffected when grouping is disabled.
	if p.shouldGroup(decision) {
		return p.routeAlertToGroup(ctx, alert, decision)
	}
	p.warnGroupingFallback(decision)

	// Step 3: Publish with classification (smart routing)
	return p.publisher.PublishWithClassification(ctx, alert, classification)
}

// cleanupInhibitionsForSource removes all active inhibitions caused by the given source alert.
// Called when a source (inhibitor) alert resolves.
func (p *AlertProcessor) cleanupInhibitionsForSource(ctx context.Context, sourceFingerprint string) {
	if p.inhibitionState == nil {
		return
	}

	inhibitions, err := p.inhibitionState.GetActiveInhibitions(ctx)
	if err != nil {
		p.logger.Warn("Failed to get active inhibitions for cleanup",
			"error", err,
			"source_fingerprint", sourceFingerprint)
		return
	}

	for _, state := range inhibitions {
		if state.SourceFingerprint == sourceFingerprint {
			if err := p.inhibitionState.RemoveInhibition(ctx, state.TargetFingerprint); err != nil {
				p.logger.Warn("Failed to remove inhibition",
					"error", err,
					"target_fingerprint", state.TargetFingerprint,
					"source_fingerprint", sourceFingerprint)
			} else {
				p.logger.Info("Inhibition removed (source resolved)",
					"target_fingerprint", state.TargetFingerprint,
					"source_fingerprint", sourceFingerprint,
					"rule", state.RuleName)
			}
		}
	}
}

// Health checks if all dependencies are healthy
func (p *AlertProcessor) Health(ctx context.Context) error {
	// Check enrichment manager
	if _, err := p.enrichmentManager.GetMode(ctx); err != nil {
		return fmt.Errorf("enrichment manager unhealthy: %w", err)
	}

	// Check LLM client (if configured)
	if p.llmClient != nil {
		if err := p.llmClient.Health(ctx); err != nil {
			p.logger.Warn("LLM client unhealthy (non-critical)", "error", err)
			// Not critical - we can fall back to transparent mode
		}
	}

	// TODO: Check filter engine health
	// TODO: Check publisher health

	return nil
}
