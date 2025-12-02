package services

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/core"
)

// SimplePublisher is a basic implementation of Publisher
// TODO: Implement actual publishing to Rootly, PagerDuty, Slack
type SimplePublisher struct {
	logger *slog.Logger
}

// NewSimplePublisher creates a new simple publisher
func NewSimplePublisher(logger *slog.Logger) *SimplePublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &SimplePublisher{
		logger: logger,
	}
}

// PublishToAll publishes alert to all configured targets
func (p *SimplePublisher) PublishToAll(ctx context.Context, alert *core.Alert) error {
	p.logger.Info("Publishing alert to all targets",
		"alert", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"status", alert.Status,
	)

	// TODO: Implement actual publishing
	// - Rootly
	// - PagerDuty
	// - Slack

	return nil
}

// PublishWithClassification publishes enriched alert with classification
func (p *SimplePublisher) PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	p.logger.Info("Publishing enriched alert",
		"alert", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"status", alert.Status,
		"severity", classification.Severity,
		"confidence", classification.Confidence,
	)

	// TODO: Implement smart routing based on classification:
	// - Critical → PagerDuty + Slack
	// - Warning → Slack
	// - Info → Rootly only

	return nil
}
