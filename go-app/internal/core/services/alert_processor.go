package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	"github.com/ipiton/AMP/pkg/metrics"
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

// AlertProcessor handles alert processing with enrichment mode support
type AlertProcessor struct {
	enrichmentManager EnrichmentModeManager
	llmClient         LLMClient
	filterEngine      FilterEngine
	publisher         Publisher
	deduplication     DeduplicationService                  // TN-036 Phase 3: Deduplication service
	inhibitionMatcher inhibition.InhibitionMatcher          // TN-130 Phase 6: Inhibition checking
	inhibitionState   inhibition.InhibitionStateManager     // TN-130 Phase 6: State tracking
	businessMetrics   *metrics.BusinessMetrics              // TN-130 Phase 6: Business metrics for inhibition
	logger            *slog.Logger
	metrics           *metrics.MetricsManager
}

// AlertProcessorConfig holds configuration for AlertProcessor
type AlertProcessorConfig struct {
	EnrichmentManager EnrichmentModeManager
	LLMClient         LLMClient // optional, required only for enriched mode
	FilterEngine      FilterEngine
	Publisher         Publisher
	Deduplication     DeduplicationService                  // TN-036 Phase 3: optional, recommended for production
	InhibitionMatcher inhibition.InhibitionMatcher          // TN-130 Phase 6: optional, recommended for inhibition
	InhibitionState   inhibition.InhibitionStateManager     // TN-130 Phase 6: optional, for state tracking
	BusinessMetrics   *metrics.BusinessMetrics              // TN-130 Phase 6: required if using inhibition
	Logger            *slog.Logger
	Metrics           *metrics.MetricsManager
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

	return &AlertProcessor{
		enrichmentManager: config.EnrichmentManager,
		llmClient:         config.LLMClient,
		filterEngine:      config.FilterEngine,
		publisher:         config.Publisher,
		deduplication:     config.Deduplication,
		inhibitionMatcher: config.InhibitionMatcher, // TN-130 Phase 6
		inhibitionState:   config.InhibitionState,   // TN-130 Phase 6
		businessMetrics:   config.BusinessMetrics,   // TN-130 Phase 6
		logger:            config.Logger,
		metrics:           config.Metrics,
	}, nil
}

// ProcessAlert processes an alert based on current enrichment mode
func (p *AlertProcessor) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	startTime := time.Now()

	// TN-036 Phase 3: Step 0 - Deduplication (before enrichment/filtering)
	if p.deduplication != nil {
		dedupResult, err := p.deduplication.ProcessAlert(ctx, alert)
		if err != nil {
			p.logger.Error("Deduplication failed", "error", err, "alert", alert.AlertName)
			// Continue with processing even if deduplication fails (graceful degradation)
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
		processErr = p.processTransparentWithRecommendations(ctx, alert)
	case EnrichmentModeTransparent:
		processErr = p.processTransparent(ctx, alert)
	case EnrichmentModeEnriched:
		processErr = p.processEnriched(ctx, alert)
	default:
		p.logger.Warn("Unknown enrichment mode, falling back to enriched", "mode", mode)
		processErr = p.processEnriched(ctx, alert)
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
func (p *AlertProcessor) processTransparentWithRecommendations(ctx context.Context, alert *core.Alert) error {
	p.logger.Info("Processing in transparent_with_recommendations mode (bypass all)",
		"alert", alert.AlertName,
	)

	// NO LLM classification
	// NO filtering
	// Publish to ALL targets immediately
	return p.publisher.PublishToAll(ctx, alert)
}

// processTransparent processes without LLM but with filtering
func (p *AlertProcessor) processTransparent(ctx context.Context, alert *core.Alert) error {
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

	// Publish to ALL configured targets
	return p.publisher.PublishToAll(ctx, alert)
}

// processEnriched processes with full LLM classification and filtering (production mode)
func (p *AlertProcessor) processEnriched(ctx context.Context, alert *core.Alert) error {
	p.logger.Info("Processing in enriched mode (full LLM + filtering)",
		"alert", alert.AlertName,
	)

	// Check if LLM client is available
	if p.llmClient == nil {
		p.logger.Warn("LLM client not configured, falling back to transparent mode")
		return p.processTransparent(ctx, alert)
	}

	// Step 1: Classify with LLM
	classification, err := p.llmClient.ClassifyAlert(ctx, alert)
	if err != nil {
		p.logger.Error("LLM classification failed, falling back to transparent mode",
			"alert", alert.AlertName,
			"error", err,
		)
		// Graceful degradation: fall back to transparent mode
		return p.processTransparent(ctx, alert)
	}

	p.logger.Info("Alert classified",
		"alert", alert.AlertName,
		"severity", classification.Severity,
		"confidence", classification.Confidence,
	)

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

	// Step 3: Publish with classification (smart routing)
	return p.publisher.PublishWithClassification(ctx, alert, classification)
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
