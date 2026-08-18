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
//
// Contract (task 2.4 fix round 1, Findings 1+2): the caller
// (grouping.DefaultGroupManager.publishGroupAlerts) records this
// notification as "sent" in its Dedup log ONLY when PublishGroup returns a
// nil error — so nil must mean "every resolved target confirmed the
// enqueue," not merely "the coordinator call itself didn't error."
// Concretely:
//
//   - Empty results with a nil error (coordinator's metrics-only-mode
//     fallback — see PublishingCoordinator.PublishGroupToTargets) used to
//     read as "success" here, so Dedup recorded a send that never actually
//     happened. Once metrics-only mode ends, the group would stay silent
//     for a full repeat_interval. Now treated as a failure.
//   - Partial failure (N of M targets succeeded) used to return nil as
//     long as at least one target succeeded, which also let Dedup record
//     "sent" — starving the failed target(s) until repeat_interval elapses
//     instead of retrying on the next timer tick like every other publish
//     path in this codebase. Now treated as a failure too.
//
// This is the minimal, honest fix, not per-target dedup (deferred — see
// task 2.4 report): the dedup log is still one entry per GroupKey, not per
// (GroupKey, target). The trade-off is that a partial failure causes the
// NEXT tick to resend to every target again, including the ones that
// already succeeded (a possible duplicate for those, in exchange for never
// silently dropping the ones that failed). A per-target notification log
// would avoid that duplicate but is a larger change, deferred.
func (p *ApplicationPublishingAdapter) PublishGroup(ctx context.Context, alerts []*core.Alert, receiver string) error {
	if len(alerts) == 0 {
		return nil
	}

	results, err := p.coordinator.PublishGroupToTargets(ctx, alerts, receiver)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		// No result to confirm delivery against (e.g. metrics-only mode) —
		// do not let the caller treat this as "sent".
		return fmt.Errorf("group publish for receiver %q produced no results (no confirmed delivery)", receiver)
	}

	// counted is the number of NON-NIL results (wave re-review, Minor 4). The
	// comparisons below must be against this, not len(results): a nil entry
	// carries no target and no outcome, so counting it as a denominator would
	// synthesize a "partial failure" out of a run where every real target
	// succeeded.
	counted := 0
	successful := 0
	var lastErr error
	for _, result := range results {
		if result == nil {
			continue
		}
		counted++
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

	if counted == 0 {
		// Every entry was nil: same posture as the len(results) == 0 check
		// above — nothing confirmed delivery, so the caller must not record a
		// send.
		return fmt.Errorf("group publish for receiver %q produced no usable results (no confirmed delivery)", receiver)
	}

	if successful < counted {
		// Partial or total failure — never nil here, so the caller never
		// records this group notification as delivered (see doc comment).
		if lastErr == nil {
			lastErr = fmt.Errorf("group publish for receiver %q confirmed only %d/%d targets", receiver, successful, counted)
		}
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

	// counted: non-nil results only — see the same note in PublishGroup above
	// (wave re-review, Minor 4).
	counted := 0
	successful := 0
	var lastErr error
	for _, result := range results {
		if result == nil {
			continue
		}
		counted++
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

	// Final review finding 11: this used to return nil whenever at least ONE
	// target succeeded, diverging from PublishGroup, which treats any
	// unconfirmed target as a failure. Aligned: a partial failure is a failure
	// here too, so the caller (AlertProcessor -> the webhook handler) answers
	// 5xx and Prometheus/Alertmanager retries the alert, rather than the failed
	// targets being silently dropped.
	if successful < counted {
		if lastErr == nil {
			lastErr = fmt.Errorf("publish for alert %q confirmed only %d/%d targets", alert.Fingerprint, successful, counted)
		}
		return lastErr
	}

	// DELIBERATE DIVERGENCE from PublishGroup: an empty result set — or one
	// holding only nil entries, which is the same thing — stays nil here. PublishGroup must error on it because its caller records the group
	// in the shared, cross-replica notification log on nil and would then
	// suppress the group for a whole repeat_interval (see that method's doc
	// comment). This single-alert path has no such log to poison, while "no
	// targets resolved" is a legitimate steady state — an operator who has not
	// yet created any amp.receiver-scoped Secret, or metrics-only mode. Erroring
	// would make every ingested alert answer 5xx and put Prometheus into a
	// permanent retry loop over a working, deliberately-configured deployment.
	return nil
}
