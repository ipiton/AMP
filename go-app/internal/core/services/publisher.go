package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/ipiton/AMP/internal/core"
)

// SimplePublisher is a STUB implementation of Publisher for development only.
//
// Deprecated: This publisher does NOT actually send alerts to external systems.
// It only logs the publishing intent. Use infrastructure/publishing.PublisherFactory
// to create real publishers (Slack, PagerDuty, Rootly, Webhook).
//
// In production environments, this publisher will panic on first use to prevent
// silent alert loss. Set APP_ENV=development to allow stub usage.
type SimplePublisher struct {
	logger      *slog.Logger
	environment string
	warnOnce    atomic.Bool
}

// SimplePublisherOption configures SimplePublisher behavior
type SimplePublisherOption func(*SimplePublisher)

// WithEnvironment sets the environment for the publisher
func WithEnvironment(env string) SimplePublisherOption {
	return func(p *SimplePublisher) {
		p.environment = env
	}
}

// NewSimplePublisher creates a new simple publisher (STUB - development only)
//
// WARNING: This is a stub that does NOT publish alerts to external systems.
// Use infrastructure/publishing.PublisherFactory for production publishing.
//
// Parameters:
//   - logger: structured logger for output
//   - opts: optional configuration (WithEnvironment, etc.)
//
// Behavior by environment:
//   - production/prod: PANICS on creation to prevent silent alert loss
//   - staging/stage: logs warning, allows operation
//   - development/dev/test: logs debug, allows operation
//   - empty/unknown: checks APP_ENV, defaults to development behavior with warning
func NewSimplePublisher(logger *slog.Logger, opts ...SimplePublisherOption) *SimplePublisher {
	if logger == nil {
		logger = slog.Default()
	}

	p := &SimplePublisher{
		logger:      logger,
		environment: os.Getenv("APP_ENV"),
	}

	for _, opt := range opts {
		opt(p)
	}

	// Normalize environment
	switch p.environment {
	case "production", "prod":
		// In production, panic immediately to prevent misconfiguration
		panic(fmt.Sprintf(
			"FATAL: SimplePublisher is a STUB and must NOT be used in production!\n" +
				"Alerts will NOT be sent to external systems (Slack, PagerDuty, Rootly).\n" +
				"Use infrastructure/publishing.PublisherFactory to create real publishers.\n" +
				"If this is intentional (dry-run mode), set APP_ENV=development",
		))

	case "staging", "stage":
		logger.Warn("DEPRECATED: SimplePublisher is a stub - alerts will NOT be published",
			slog.String("component", "SimplePublisher"),
			slog.String("environment", p.environment),
			slog.String("recommendation", "Use infrastructure/publishing.PublisherFactory"),
		)

	case "development", "dev", "test", "":
		logger.Debug("SimplePublisher stub initialized (development mode)",
			slog.String("component", "SimplePublisher"),
			slog.String("environment", p.environment),
			slog.String("note", "Alerts will be logged but NOT sent to external systems"),
		)

	default:
		logger.Warn("SimplePublisher stub initialized with unknown environment",
			slog.String("component", "SimplePublisher"),
			slog.String("environment", p.environment),
			slog.String("warning", "Alerts will NOT be published to external systems"),
		)
	}

	return p
}

// PublishToAll logs alert publishing intent but does NOT actually publish.
//
// WARNING: This is a STUB - no alerts are sent to external systems.
func (p *SimplePublisher) PublishToAll(ctx context.Context, alert *core.Alert) error {
	p.logStubWarning()

	p.logger.Info("[STUB] Would publish alert to all targets",
		slog.String("component", "SimplePublisher"),
		slog.String("alert", alert.AlertName),
		slog.String("fingerprint", alert.Fingerprint),
		slog.String("status", string(alert.Status)),
		slog.String("stub_warning", "Alert NOT actually published"),
	)

	return nil
}

// PublishWithClassification logs enriched alert publishing intent but does NOT actually publish.
//
// WARNING: This is a STUB - no alerts are sent to external systems.
func (p *SimplePublisher) PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	p.logStubWarning()

	p.logger.Info("[STUB] Would publish enriched alert",
		slog.String("component", "SimplePublisher"),
		slog.String("alert", alert.AlertName),
		slog.String("fingerprint", alert.Fingerprint),
		slog.String("status", string(alert.Status)),
		slog.String("severity", string(classification.Severity)),
		slog.Float64("confidence", classification.Confidence),
		slog.String("stub_warning", "Alert NOT actually published"),
	)

	// Log intended routing for debugging
	p.logger.Debug("[STUB] Intended routing based on classification",
		slog.String("component", "SimplePublisher"),
		slog.String("severity", string(classification.Severity)),
		slog.String("route_critical", "Would send to PagerDuty + Slack"),
		slog.String("route_warning", "Would send to Slack"),
		slog.String("route_info", "Would send to Rootly"),
	)

	return nil
}

// logStubWarning logs a warning once per publisher instance
func (p *SimplePublisher) logStubWarning() {
	if p.warnOnce.CompareAndSwap(false, true) {
		p.logger.Warn("SimplePublisher STUB is being used - alerts are NOT being published!",
			slog.String("component", "SimplePublisher"),
			slog.String("action", "Replace with real publisher using infrastructure/publishing.PublisherFactory"),
		)
	}
}
