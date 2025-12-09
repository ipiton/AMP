package publishing

import (
	"context"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// BaseEnhancedPublisher provides common functionality for all enhanced publishers
// This reduces code duplication across Slack, PagerDuty, Rootly, and Webhook publishers
type BaseEnhancedPublisher struct {
	metrics   *v2.PublishingMetrics
	formatter AlertFormatter
	logger    *slog.Logger
}

// NewBaseEnhancedPublisher creates a new base publisher with common dependencies
func NewBaseEnhancedPublisher(
	metrics *v2.PublishingMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) *BaseEnhancedPublisher {
	return &BaseEnhancedPublisher{
		metrics:   metrics,
		formatter: formatter,
		logger:    logger,
	}
}

// GetMetrics returns the metrics instance (can be nil)
func (b *BaseEnhancedPublisher) GetMetrics() *v2.PublishingMetrics {
	return b.metrics
}

// GetFormatter returns the alert formatter
func (b *BaseEnhancedPublisher) GetFormatter() AlertFormatter {
	return b.formatter
}

// GetLogger returns the logger
func (b *BaseEnhancedPublisher) GetLogger() *slog.Logger {
	return b.logger
}

// RecordPublishAttempt records a publish attempt metric (no-op for now, can be extended)
func (b *BaseEnhancedPublisher) RecordPublishAttempt(provider, target string) {
	// Metrics are recorded at a more granular level in provider-specific code
}

// RecordPublishSuccess records a successful publish metric (no-op for now, can be extended)
func (b *BaseEnhancedPublisher) RecordPublishSuccess(provider, target string, duration time.Duration) {
	// Metrics are recorded at a more granular level in provider-specific code
}

// RecordPublishFailure records a failed publish metric (no-op for now, can be extended)
func (b *BaseEnhancedPublisher) RecordPublishFailure(provider, target, errorType string) {
	// Metrics are recorded at a more granular level in provider-specific code
}

// RecordCacheHit records a cache hit metric
func (b *BaseEnhancedPublisher) RecordCacheHit(provider string) {
	if b.metrics != nil {
		b.metrics.RecordCacheHit(provider)
	}
}

// RecordCacheMiss records a cache miss metric
func (b *BaseEnhancedPublisher) RecordCacheMiss(provider string) {
	if b.metrics != nil {
		b.metrics.RecordCacheMiss(provider)
	}
}

// LogPublishStart logs the start of a publish operation
func (b *BaseEnhancedPublisher) LogPublishStart(ctx context.Context, provider string, enrichedAlert *core.EnrichedAlert) {
	if b.logger != nil {
		b.logger.InfoContext(ctx, "Publishing alert",
			slog.String("provider", provider),
			slog.String("fingerprint", enrichedAlert.Alert.Fingerprint),
			slog.String("alert_name", enrichedAlert.Alert.AlertName),
			slog.String("status", string(enrichedAlert.Alert.Status)))
	}
}

// LogPublishSuccess logs a successful publish
func (b *BaseEnhancedPublisher) LogPublishSuccess(ctx context.Context, provider, fingerprint string, duration time.Duration) {
	if b.logger != nil {
		b.logger.InfoContext(ctx, "Alert published successfully",
			slog.String("provider", provider),
			slog.String("fingerprint", fingerprint),
			slog.Duration("duration", duration))
	}
}

// LogPublishError logs a publish error
func (b *BaseEnhancedPublisher) LogPublishError(ctx context.Context, provider, fingerprint string, err error) {
	if b.logger != nil {
		b.logger.ErrorContext(ctx, "Failed to publish alert",
			slog.String("provider", provider),
			slog.String("fingerprint", fingerprint),
			slog.String("error", err.Error()))
	}
}

// GetErrorType extracts error type for metrics from an error
func (b *BaseEnhancedPublisher) GetErrorType(err error) string {
	return GetPublishingErrorType(err)
}
