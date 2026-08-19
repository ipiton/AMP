package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
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

var (
	_ services.Publisher                  = (*MetricsOnlyPublisher)(nil)
	_ grouping.GroupNotificationPublisher = (*MetricsOnlyPublisher)(nil)
)

func (p *MetricsOnlyPublisher) PublishToAll(ctx context.Context, alert *core.Alert) error {
	p.logSkip(ctx, alert, nil)
	return nil
}

// PublishGroup implements grouping.GroupNotificationPublisher (task 2.4;
// signature widened by task fwb for per-target outcomes and by task fu4 for
// per-alert ones): no-op
// delivery, same posture as PublishToAll in degraded/metrics-only runtime
// modes.
//
// Returns grouping.ErrDeliveryNotConfirmed (final review finding 4). A nil
// error return here would mean "delivered" to
// DefaultGroupManager.publishGroupAlerts, which then writes per-target
// entries into the SHARED cross-replica notification log with TTL =
// repeat_interval — so a single replica running in metrics-only mode
// (publishing.enabled: false, the lite profile, or a transient Kubernetes
// failure at startup) would silence the group on every HEALTHY replica for
// a full repeat_interval. The sentinel keeps this a non-error, non-delivery
// outcome: no dedup entry is recorded (the caller never reaches its
// RecordSent loop when err != nil), and the next replica to fire the
// group's timer publishes for real. targetAlerts and groupLabels are unused:
// a publisher that delivers nothing has no targets to check dedup state
// for and nothing to format a wire payload with.
//
// The empty-alerts early return stays nil (no outcomes, no error): there is
// genuinely nothing to deliver, and publishGroupAlerts never reaches the
// publisher with an empty slice anyway.
func (p *MetricsOnlyPublisher) PublishGroup(ctx context.Context, _ string, alerts []*core.Alert, receiver string, _ map[string]string, _ func(string, []*core.Alert) []*core.Alert) ([]grouping.TargetPublishOutcome, error) {
	if len(alerts) == 0 {
		return nil, nil
	}
	if p.logger != nil {
		p.logger.InfoContext(ctx, "Group publishing skipped (metrics-only publisher)",
			"reason", p.reason,
			"receiver", receiver,
			"alert_count", len(alerts),
		)
	}
	return nil, fmt.Errorf("%w: %s", grouping.ErrDeliveryNotConfirmed, p.reason)
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
