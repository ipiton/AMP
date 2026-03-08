package application

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
)

// MetricsOnlyPublisher is an explicit no-op publisher used for degraded runtime modes.
type MetricsOnlyPublisher struct {
	reason string
	logger *slog.Logger
}

// NewMetricsOnlyPublisher creates a no-op publisher with an explicit reason.
func NewMetricsOnlyPublisher(reason string, logger *slog.Logger) *MetricsOnlyPublisher {
	if logger == nil {
		logger = slog.Default()
	}

	return &MetricsOnlyPublisher{
		reason: reason,
		logger: logger,
	}
}

var _ services.Publisher = (*MetricsOnlyPublisher)(nil)

func (p *MetricsOnlyPublisher) PublishToAll(ctx context.Context, alert *core.Alert) error {
	p.logSkip(ctx, alert, nil)
	return nil
}

func (p *MetricsOnlyPublisher) PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	p.logSkip(ctx, alert, classification)
	return nil
}

func (p *MetricsOnlyPublisher) logSkip(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) {
	if p.logger == nil {
		return
	}

	fields := []any{"reason", p.reason}
	if alert != nil {
		fields = append(fields,
			"alert", alert.AlertName,
			"fingerprint", alert.Fingerprint,
		)
	}
	if classification != nil {
		fields = append(fields, "classification", classification.Severity)
	}

	p.logger.InfoContext(ctx, "Publishing skipped (metrics-only publisher)", fields...)
}
