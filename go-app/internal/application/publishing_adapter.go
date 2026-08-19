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
	// PublishToTargets resolves targets by RECEIVER (empty receiver = every
	// enabled target). Slice-1 review finding C1: this interface used to
	// declare PublishToAll, which applies no receiver filtering at all, so the
	// non-grouped publish path fanned every alert out to every receiver's
	// targets — harmless-ish while targets were hand-made Secrets, a
	// cross-receiver leak once `receivers:` auto-provisions them.
	PublishToTargets(ctx context.Context, enrichedAlert *core.EnrichedAlert, targetNames []string, receiverName string) ([]*infrapublishing.PublishingResult, error)
	// PublishGroupToTargets is the notify-stage chain's batch publish call
	// (task 2.4) — see grouping.GroupNotificationPublisher.PublishGroup.
	// targetAlerts implements task fwb's per-target nflog dedup AND task fu4's
	// per-alert refinement of it: called once per candidate target before a job
	// is submitted for it, it returns the alerts that target is still owed
	// (empty ⇒ skip the target entirely). groupLabels (review finding 1, fwb
	// fix round 1) is forwarded into the wire payload's "groupLabels" field for
	// batched targets.
	PublishGroupToTargets(ctx context.Context, alerts []*core.Alert, receiverName string, groupKey string, groupLabels map[string]string, targetAlerts func(target string, alerts []*core.Alert) []*core.Alert) ([]*infrapublishing.PublishingResult, error)
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

// PublishToReceiver implements services.Publisher: the non-grouped publish
// path, scoped to the receiver the route tree picked (review finding C1).
func (p *ApplicationPublishingAdapter) PublishToReceiver(ctx context.Context, alert *core.Alert, receiver string) error {
	return p.publish(ctx, alert, nil, receiver)
}

// PublishGroup implements grouping.GroupNotificationPublisher (task 2.4,
// per-target outcomes added by task fwb): delivers alerts (already filtered
// by the notify-chain's Inhibit/Silence/Dedup steps) for one alert group as
// a single logical group notification.
//
// Contract (task fwb): the caller
// (grouping.DefaultGroupManager.publishGroupAlerts) records an nflog entry
// per TARGET, scoped to exactly the outcomes with Success == true — so a
// non-nil error here must mean "nothing usable happened at all" (no target
// even got a chance to report an outcome — e.g. metrics-only mode, or "no
// targets found for receiver"), while a partial result (some targets
// succeeded, some didn't) is reported through outcomes with a nil error,
// letting the caller record the successes and retry only the failures on
// the next scheduled fire. This replaces task 2.4's all-or-nothing
// contract, where a partial failure had to be surfaced as a whole-call
// error because the dedup log had no per-target granularity to record a
// partial success into.
//
// Delivery confirmation (task rec, wave 3): this call now blocks for as
// long as the actual wire-level delivery takes (bounded per target by
// CoordinatorConfig.DeliveryConfirmationTimeout), because an outcome may
// only be reported as Success once the target has really accepted the
// notification — the caller turns Success into a shared, cross-replica nflog
// entry that suppresses the group for a whole repeat_interval.
//
// Per-alert outcomes (task fu4, wave 4): for a NON-BATCH target the outcome
// additionally carries which individual alerts did land when the target as a
// whole is unconfirmed, so the caller can retry only the rest. That travels
// straight through from infrapublishing.PublishingResult.DeliveredAlerts.
//
// targetAlerts and groupLabels are forwarded to
// PublishingCoordinator.PublishGroupToTargets unchanged — see
// grouping.GroupNotificationPublisher's doc comment for the per-target/
// per-alert dedup protocol and the groupLabels contract it implements.
func (p *ApplicationPublishingAdapter) PublishGroup(ctx context.Context, groupKey string, alerts []*core.Alert, receiver string, groupLabels map[string]string, targetAlerts func(target string, alerts []*core.Alert) []*core.Alert) ([]grouping.TargetPublishOutcome, error) {
	if len(alerts) == 0 {
		return nil, nil
	}

	results, err := p.coordinator.PublishGroupToTargets(ctx, alerts, receiver, groupKey, groupLabels, targetAlerts)
	if err != nil {
		return nil, err
	}

	outcomes := make([]grouping.TargetPublishOutcome, 0, len(results))
	for _, result := range results {
		if result == nil || result.Target == nil {
			// A nil entry (or one with no target) carries no outcome to
			// report — skip it rather than synthesizing a target-less
			// failure (wave re-review, Minor 4's same concern, carried
			// forward).
			continue
		}

		// result.Success is a CONFIRMED delivery for this target since task
		// rec (alertmanager-parity wave 3): PublishGroupToTargets blocks
		// until the target's queued job reports its final outcome, so this
		// is the publisher's HTTP result (after in-queue retries), not the
		// pre-rec "job enqueued" signal. That is exactly what
		// TargetPublishOutcome.Success promises the notify chain, which
		// writes an nflog entry per successful target.
		outcomes = append(outcomes, grouping.TargetPublishOutcome{
			Target:  result.Target.Name,
			Success: result.Success,
			// Task fu4: non-empty only for an unconfirmed NON-BATCH target
			// that still got some of its per-alert wire messages through.
			DeliveredAlerts: result.DeliveredAlerts,
		})

		if !result.Success && result.Error != nil {
			p.logger.Warn("Group publishing delivery not confirmed for target; no dedup entry will be recorded, target retried on next fire",
				"target", result.Target.Name,
				"receiver", receiver,
				"alert_count", len(alerts),
				"error", result.Error,
			)
		}
	}

	return outcomes, nil
}

// PublishToReceiverWithClassification implements services.Publisher (enriched
// mode), likewise receiver-scoped.
func (p *ApplicationPublishingAdapter) PublishToReceiverWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult, receiver string) error {
	return p.publish(ctx, alert, classification, receiver)
}

func (p *ApplicationPublishingAdapter) publish(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult, receiver string) error {
	if alert == nil {
		return fmt.Errorf("alert is required")
	}

	now := time.Now().UTC()
	// targetNames nil + receiver => receiver-scoped resolution
	// (PublishingCoordinator.targetMatchesReceiver). receiver "" keeps the
	// legacy "all enabled targets" behaviour for deployments with no route
	// tree.
	results, err := p.coordinator.PublishToTargets(ctx, &core.EnrichedAlert{
		Alert:               alert,
		Classification:      classification,
		ProcessingTimestamp: &now,
	}, nil, receiver)
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
