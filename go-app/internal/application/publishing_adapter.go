package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

type publishingCoordinator interface {
	PublishToAll(ctx context.Context, enrichedAlert *core.EnrichedAlert) ([]*infrapublishing.PublishingResult, error)
	// PublishGroupToTargets is the notify-stage chain's batch publish call
	// (task 2.4) — see grouping.GroupNotificationPublisher.PublishGroup.
	PublishGroupToTargets(ctx context.Context, alerts []*core.Alert, receiverName string) ([]*infrapublishing.PublishingResult, error)
}

// ApplicationPublishingAdapter bridges AlertProcessor and the queue-based publishing stack.
type ApplicationPublishingAdapter struct {
	coordinator publishingCoordinator
	logger      *slog.Logger
}

// NewApplicationPublishingAdapter creates a publisher compatible with AlertProcessor.
func NewApplicationPublishingAdapter(coordinator publishingCoordinator, logger *slog.Logger) (*ApplicationPublishingAdapter, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("publishing coordinator is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &ApplicationPublishingAdapter{
		coordinator: coordinator,
		logger:      logger,
	}, nil
}

var (
	_ services.Publisher                  = (*ApplicationPublishingAdapter)(nil)
	_ grouping.GroupNotificationPublisher = (*ApplicationPublishingAdapter)(nil)
)

func (p *ApplicationPublishingAdapter) PublishToAll(ctx context.Context, alert *core.Alert) error {
	return p.publish(ctx, alert, nil)
}

// PublishGroup implements grouping.GroupNotificationPublisher (task 2.4):
// delivers alerts (already filtered by the notify-chain's Inhibit/Silence/
// Dedup steps) for one alert group as a single logical group notification.
func (p *ApplicationPublishingAdapter) PublishGroup(ctx context.Context, alerts []*core.Alert, receiver string) error {
	if len(alerts) == 0 {
		return nil
	}

	results, err := p.coordinator.PublishGroupToTargets(ctx, alerts, receiver)
	if err != nil {
		return err
	}

	successful := 0
	var lastErr error
	for _, result := range results {
		if result == nil {
			continue
		}
		if result.Success {
			successful++
			continue
		}
		if result.Error != nil {
			lastErr = result.Error
			p.logger.Warn("Group publishing enqueue failed",
				"target", result.Target.Name,
				"receiver", receiver,
				"alert_count", len(alerts),
				"error", result.Error,
			)
		}
	}

	if len(results) > 0 && successful == 0 && lastErr != nil {
		return lastErr
	}

	return nil
}

func (p *ApplicationPublishingAdapter) PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	return p.publish(ctx, alert, classification)
}

func (p *ApplicationPublishingAdapter) publish(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	if alert == nil {
		return fmt.Errorf("alert is required")
	}

	now := time.Now().UTC()
	results, err := p.coordinator.PublishToAll(ctx, &core.EnrichedAlert{
		Alert:               alert,
		Classification:      classification,
		ProcessingTimestamp: &now,
	})
	if err != nil {
		return err
	}

	successful := 0
	var lastErr error
	for _, result := range results {
		if result == nil {
			continue
		}
		if result.Success {
			successful++
			continue
		}
		if result.Error != nil {
			lastErr = result.Error
			p.logger.Warn("Publishing enqueue failed",
				"target", result.Target.Name,
				"fingerprint", alert.Fingerprint,
				"error", result.Error,
			)
		}
	}

	if len(results) > 0 && successful == 0 && lastErr != nil {
		return lastErr
	}

	return nil
}
