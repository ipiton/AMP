package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// DefaultDeliveryConfirmationTimeout bounds how long
// PublishGroupToTargets waits for ONE target's queued job to report its
// final delivery outcome (task rec, alertmanager-parity wave 3).
//
// SIZING — this value is coupled to grouping.notifyLogClaimTTL: the notify
// chain holds a cross-replica publish claim (GroupNotifyLog.TryClaim) across
// the whole PublishGroup call, without renewal, so the claim TTL MUST stay
// comfortably larger than this timeout or the claim can expire mid-publish
// and let a second replica publish the same group. Keep the two in step
// (claim TTL 60s vs. 45s here leaves ~15s of margin for the chain's own
// Redis round-trips); see grouping.notifyLogClaimTTL's doc comment.
//
// It is deliberately SMALLER than the queue's worst-case retry budget
// (MaxRetries+1 attempts × the publisher's 30s HTTP timeout + exponential
// backoff ≈ 2min): a target that is retrying that long has already missed
// this group's next tick, and waiting the full budget would mean holding the
// claim — and blocking this group's timer goroutine — for minutes. Exceeding
// the timeout is reported as ErrDeliveryWaitTimeout, i.e. unconfirmed, which
// is the safe direction (see that sentinel's doc comment).
const DefaultDeliveryConfirmationTimeout = 45 * time.Second

// PublishingResult represents the result of publishing to a single target
type PublishingResult struct {
	Target  *core.PublishingTarget
	Success bool
	Error   error
}

// PublishingCoordinator manages concurrent publishing to multiple targets
type PublishingCoordinator struct {
	queue            *PublishingQueue
	discoveryManager TargetDiscoveryManager
	modeManager      ModeManager // TN-060: Mode manager for metrics-only fallback
	semaphore        chan struct{}
	deliveryTimeout  time.Duration // task rec: per-target delivery-confirmation wait
	logger           *slog.Logger
}

// CoordinatorConfig holds configuration for publishing coordinator
type CoordinatorConfig struct {
	// MaxConcurrent bounds concurrent publish SUBMISSIONS per fan-out.
	//
	// Semantics note (task rec fix round 1, review finding M2): for the
	// single-alert paths (PublishToAll/PublishToTargets) this is unchanged —
	// those enqueue and return, so "submission" and "operation" are the same
	// thing. For PublishGroupToTargets, which since task rec blocks until
	// delivery is confirmed, the semaphore deliberately covers the enqueue
	// only and NOT the confirmation wait — see submitGroupJob for why holding
	// it across the wait would stall targets that were never even attempted.
	// Concurrency of the delivery work itself is bounded by the queue's
	// worker pool (publishing.queue.worker_count).
	MaxConcurrent int

	// DeliveryConfirmationTimeout bounds the per-target wait in
	// PublishGroupToTargets (task rec). Zero/negative falls back to
	// DefaultDeliveryConfirmationTimeout — see that constant for the sizing
	// constraint against grouping.notifyLogClaimTTL.
	DeliveryConfirmationTimeout time.Duration
}

// DefaultCoordinatorConfig returns default configuration
func DefaultCoordinatorConfig() CoordinatorConfig {
	return CoordinatorConfig{
		MaxConcurrent:               5, // Publish to max 5 targets concurrently
		DeliveryConfirmationTimeout: DefaultDeliveryConfirmationTimeout,
	}
}

// NewPublishingCoordinator creates a new publishing coordinator
func NewPublishingCoordinator(
	queue *PublishingQueue,
	discoveryManager TargetDiscoveryManager,
	modeManager ModeManager,
	config CoordinatorConfig,
	logger *slog.Logger,
) *PublishingCoordinator {
	if logger == nil {
		logger = slog.Default()
	}

	deliveryTimeout := config.DeliveryConfirmationTimeout
	if deliveryTimeout <= 0 {
		deliveryTimeout = DefaultDeliveryConfirmationTimeout
	}

	return &PublishingCoordinator{
		queue:            queue,
		discoveryManager: discoveryManager,
		modeManager:      modeManager,
		semaphore:        make(chan struct{}, config.MaxConcurrent),
		deliveryTimeout:  deliveryTimeout,
		logger:           logger,
	}
}

// DeliveryConfirmationTimeout reports the per-target confirmation wait this
// coordinator applies in PublishGroupToTargets (task rec fix round 1, review
// finding I3). Exported so wiring code can assert at startup that the
// notify chain's timer-callback deadline and publish-claim TTL cover it —
// see ServiceRegistry.validateNotifyTimingBudget.
func (c *PublishingCoordinator) DeliveryConfirmationTimeout() time.Duration {
	return c.deliveryTimeout
}

// PublishToAll publishes alert to all enabled targets concurrently
func (c *PublishingCoordinator) PublishToAll(ctx context.Context, enrichedAlert *core.EnrichedAlert) ([]*PublishingResult, error) {
	// TN-060: Check mode before publishing (metrics-only mode fallback)
	if c.modeManager != nil && c.modeManager.IsMetricsOnly() {
		c.logger.Info("Publishing skipped (metrics-only mode)",
			"fingerprint", enrichedAlert.Alert.Fingerprint,
		)
		// Return empty results (no publishing attempts)
		return []*PublishingResult{}, nil
	}

	// Get all enabled targets
	targets := c.discoveryManager.ListTargets()
	if len(targets) == 0 {
		c.logger.Warn("No publishing targets available")
		return nil, fmt.Errorf("no publishing targets available")
	}

	// Filter enabled targets
	enabledTargets := make([]*core.PublishingTarget, 0, len(targets))
	for _, t := range targets {
		if t.Enabled {
			enabledTargets = append(enabledTargets, t)
		}
	}

	if len(enabledTargets) == 0 {
		c.logger.Warn("No enabled publishing targets")
		return nil, fmt.Errorf("no enabled publishing targets")
	}

	c.logger.Info("Publishing to multiple targets",
		"total_targets", len(enabledTargets),
		"fingerprint", enrichedAlert.Alert.Fingerprint,
	)

	// Publish to all targets concurrently
	results := make([]*PublishingResult, len(enabledTargets))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, target := range enabledTargets {
		wg.Add(1)

		go func(idx int, t *core.PublishingTarget) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case c.semaphore <- struct{}{}:
				defer func() { <-c.semaphore }()
			case <-ctx.Done():
				mu.Lock()
				results[idx] = &PublishingResult{
					Target:  t,
					Success: false,
					Error:   ctx.Err(),
				}
				mu.Unlock()
				return
			}

			// Submit to queue
			err := c.queue.Submit(enrichedAlert, t)

			mu.Lock()
			results[idx] = &PublishingResult{
				Target:  t,
				Success: err == nil,
				Error:   err,
			}
			mu.Unlock()
		}(i, target)
	}

	// Wait for all publishing operations to complete
	wg.Wait()

	// Count successes
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	c.logger.Info("Parallel publishing completed",
		"total", len(results),
		"successful", successCount,
		"failed", len(results)-successCount,
	)

	return results, nil
}

// targetMatchesReceiver decides whether a publishing target should receive
// alerts routed to receiverName. Rules (Task 1.5 — receiver → publishing
// targets mapping):
//
//  1. Empty receiverName -> matches everything (caller wants all enabled
//     targets; preserves pre-receiver-routing behavior for callers that
//     don't yet know about receivers).
//  2. Target has a non-empty Receivers list (i.e. its Secret carries the
//     `amp.receiver` label) -> matches only if receiverName is in that
//     list, OR the target's own Name equals receiverName (name fallback:
//     lets a target still serve "its" receiver even if a label lists
//     other receivers instead/additionally).
//  3. Target has NO Receivers list at all (no label on the Secret) ->
//     matches unconditionally. Backward compatibility: targets predating
//     receiver-based routing keep receiving every alert.
//
// Matching is exact and case-SENSITIVE (both the Receivers-list membership
// check and the Name fallback): "Slack-Critical" does NOT match
// "slack-critical". This is intentional — Alertmanager receiver names are
// case-sensitive — not an oversight.
func targetMatchesReceiver(target *core.PublishingTarget, receiverName string) bool {
	if receiverName == "" {
		return true
	}

	if len(target.Receivers) == 0 {
		return true
	}

	for _, r := range target.Receivers {
		if r == receiverName {
			return true
		}
	}

	return target.Name == receiverName
}

// PublishToTargets publishes alert either to explicitly named targets, or —
// when targetNames is empty — to all targets belonging to receiverName per
// targetMatchesReceiver. An empty receiverName in that second mode means
// "all enabled targets" (current/legacy behavior, no receiver filtering).
//
// Explicit targetNames resolution (used by the manual per-target test
// endpoint) is unaffected by receiverName — it is a distinct, exact-name
// lookup mode kept for backward compatibility.
//
// If receiver-based filtering matches zero targets, this returns an error
// and publishes to nothing. It deliberately does NOT fall back to
// publishing to all targets — a receiver with no matching target is a
// configuration gap that should be visible (logged + surfaced as an
// error), not silently masked by a broad fan-out.
func (c *PublishingCoordinator) PublishToTargets(ctx context.Context, enrichedAlert *core.EnrichedAlert, targetNames []string, receiverName string) ([]*PublishingResult, error) {
	// TN-060: Check mode before publishing (metrics-only mode fallback)
	if c.modeManager != nil && c.modeManager.IsMetricsOnly() {
		c.logger.Info("Publishing skipped (metrics-only mode)",
			"fingerprint", enrichedAlert.Alert.Fingerprint,
			"targets", targetNames,
			"receiver", receiverName,
		)
		// Return empty results (no publishing attempts)
		return []*PublishingResult{}, nil
	}

	var targets []*core.PublishingTarget

	if len(targetNames) > 0 {
		// Explicit target-name resolution (existing behavior).
		targets = make([]*core.PublishingTarget, 0, len(targetNames))
		for _, name := range targetNames {
			target, err := c.discoveryManager.GetTarget(name)
			if err != nil {
				c.logger.Warn("Target not found", "name", name)
				continue
			}
			if target.Enabled {
				targets = append(targets, target)
			}
		}

		if len(targets) == 0 {
			return nil, fmt.Errorf("no valid targets found")
		}

		c.logger.Info("Publishing to specific targets",
			"requested", len(targetNames),
			"found", len(targets),
			"fingerprint", enrichedAlert.Alert.Fingerprint,
		)
	} else {
		// Receiver-based selection over all discovered targets.
		all := c.discoveryManager.ListTargets()
		targets = make([]*core.PublishingTarget, 0, len(all))
		for _, t := range all {
			if !t.Enabled {
				continue
			}
			if targetMatchesReceiver(t, receiverName) {
				targets = append(targets, t)
			}
		}

		if len(targets) == 0 {
			c.logger.Warn("No publishing targets matched receiver; publishing to none",
				"receiver", receiverName,
				"fingerprint", enrichedAlert.Alert.Fingerprint,
			)
			return []*PublishingResult{}, fmt.Errorf("no targets found for receiver %q", receiverName)
		}

		c.logger.Info("Publishing to receiver-scoped targets",
			"receiver", receiverName,
			"found", len(targets),
			"fingerprint", enrichedAlert.Alert.Fingerprint,
		)
	}

	// Publish concurrently
	results := make([]*PublishingResult, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, target := range targets {
		wg.Add(1)

		go func(idx int, t *core.PublishingTarget) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case c.semaphore <- struct{}{}:
				defer func() { <-c.semaphore }()
			case <-ctx.Done():
				mu.Lock()
				results[idx] = &PublishingResult{
					Target:  t,
					Success: false,
					Error:   ctx.Err(),
				}
				mu.Unlock()
				return
			}

			// Submit to queue
			err := c.queue.Submit(enrichedAlert, t)

			mu.Lock()
			results[idx] = &PublishingResult{
				Target:  t,
				Success: err == nil,
				Error:   err,
			}
			mu.Unlock()
		}(i, target)
	}

	wg.Wait()

	return results, nil
}

// PublishGroupToTargets publishes a resolved batch of alerts belonging to
// ONE alert group as a single logical group notification (task 2.4,
// alertmanager-parity notify-stage chain): target discovery/receiver
// filtering happens exactly ONCE for the whole group, not once per alert as
// a naive loop of PublishToAll/PublishToTargets calls would do.
//
// Wire-level batching (task fwb, alertmanager-parity wave 2): this method
// now submits exactly ONE queue job per matching target — via
// PublishingQueue.SubmitGroup, carrying every alert — instead of the task
// 2.4-era one-job-per-(alert, target) shape. A webhook/alertmanager-format
// target's job is delivered as ONE wire-level POST with an "alerts" array
// (upstream Alertmanager's shape — see GroupAlertFormatter.FormatGroup);
// every other target type is still ONE job, but PublishingQueue.publishJob
// iterates Publish once per alert inside that single job/retry unit (those
// integrations have no array-payload wire shape to batch into).
//
// Delivery confirmation (task rec, alertmanager-parity wave 3): this method
// BLOCKS until every submitted job reports its final outcome, so
// PublishingResult.Success means "this target accepted the notification"
// (the publisher's HTTP call succeeded, after any in-queue retries) rather
// than the pre-rec "a job was enqueued for this target". Each target is
// waited on independently, bounded by
// CoordinatorConfig.DeliveryConfirmationTimeout — a slow target cannot
// prevent a fast sibling's success from being reported, and a target whose
// wait expires is reported as unconfirmed (ErrDeliveryWaitTimeout) so the
// notify chain retries it instead of recording it as sent. Callers therefore
// hold their per-group locks/claims for the duration of an actual delivery;
// see grouping.notifyLogClaimTTL for the TTL that has to cover it.
//
// skipTarget implements task fwb's per-target notification-log dedup (see
// grouping.GroupNotificationPublisher.PublishGroup's doc comment): called
// once per candidate target AFTER receiver-matching resolves it, BEFORE a
// job is submitted for it. A target for which skipTarget returns true is
// excluded entirely — no job, no result — because it already confirmed
// delivery of this exact alert set within repeat_interval; this is what
// makes a retry after a partial failure resend to ONLY the targets that
// failed last cycle.
//
// groupKey is forwarded into the wire payload's "groupKey" field for
// batched targets (see GroupAlertFormatter.FormatGroup); it is passed
// through as a plain string precisely so this package need not import
// infrastructure/grouping (see GroupNotificationPublisher's doc comment on
// that boundary). groupLabels (review finding 1, fwb fix round 1) is
// forwarded the same way into the wire payload's "groupLabels" field — the
// caller resolves it from grouping.GroupMetadata.GroupBy.
//
// Receiver-matching semantics mirror PublishToTargets: empty receiverName,
// or a target with no Receivers list, matches everything. Zero alerts is a
// no-op (nil, nil — the caller, publishGroupAlerts, never calls this with
// an empty slice, but this stays defensive). Zero matching targets returns
// the same "no targets found for receiver" error as PublishToTargets —
// logged by the caller, NOT retried in a loop here (the caller's next
// scheduled group timer will naturally retry with the group's then-current
// state).
func (c *PublishingCoordinator) PublishGroupToTargets(ctx context.Context, alerts []*core.Alert, receiverName string, groupKey string, groupLabels map[string]string, skipTarget func(target string) bool) ([]*PublishingResult, error) {
	if len(alerts) == 0 {
		return nil, nil
	}

	// TN-060: Check mode before publishing (metrics-only mode fallback)
	if c.modeManager != nil && c.modeManager.IsMetricsOnly() {
		c.logger.Info("Group publishing skipped (metrics-only mode)",
			"receiver", receiverName,
			"alert_count", len(alerts),
		)
		return []*PublishingResult{}, nil
	}

	// Resolve targets ONCE for the whole group (not once per alert).
	all := c.discoveryManager.ListTargets()
	targets := make([]*core.PublishingTarget, 0, len(all))
	for _, t := range all {
		if !t.Enabled {
			continue
		}
		if !targetMatchesReceiver(t, receiverName) {
			continue
		}
		// Task fwb per-target nflog dedup: a target already covered this
		// cycle is skipped entirely — no job submitted, no result reported.
		if skipTarget != nil && skipTarget(t.Name) {
			continue
		}
		targets = append(targets, t)
	}

	if len(targets) == 0 {
		c.logger.Warn("No publishing targets matched receiver for group notification; publishing none",
			"receiver", receiverName,
			"alert_count", len(alerts),
		)
		return []*PublishingResult{}, fmt.Errorf("no targets found for receiver %q", receiverName)
	}

	c.logger.Info("Publishing group notification to receiver-scoped targets",
		"receiver", receiverName,
		"alert_count", len(alerts),
		"target_count", len(targets),
	)

	results := make([]*PublishingResult, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, target := range targets {
		wg.Add(1)

		go func(idx int, t *core.PublishingTarget) {
			defer wg.Done()

			handle, err := c.submitGroupJob(ctx, alerts, t, groupKey, receiverName, groupLabels)
			if err == nil {
				// Abandon on every exit (fix round 1, review finding I2): a
				// no-op once the job finished, and the thing that frees the
				// worker when we stop waiting for a job that has not.
				defer handle.Abandon()

				// Task rec: block until this target's job reports its real
				// outcome, so Success below means "the target accepted the
				// notification", not "a job was enqueued".
				err = c.awaitDelivery(ctx, handle, t)
			}

			mu.Lock()
			results[idx] = &PublishingResult{
				Target:  t,
				Success: err == nil,
				Error:   err,
			}
			mu.Unlock()
		}(i, target)
	}

	wg.Wait()

	return results, nil
}

// submitGroupJob enqueues one (group, target) job under the coordinator's
// MaxConcurrent semaphore and returns its delivery-confirmation handle.
//
// The semaphore is held for the ENQUEUE only, never across the confirmation
// wait (task rec). Holding it through the wait would mean that with
// MaxConcurrent=5 the 6th target of a fan-out is not even submitted until an
// earlier target's HTTP delivery finishes — up to
// DeliveryConfirmationTimeout later, by which point the notify chain's
// cross-replica claim may have expired and that target would time out
// without ever having been attempted. Concurrency of the actual delivery
// work is bounded by the queue's own worker pool, not by this semaphore, and
// a goroutine parked on a channel receive costs nothing.
func (c *PublishingCoordinator) submitGroupJob(ctx context.Context, alerts []*core.Alert, target *core.PublishingTarget, groupKey string, receiverName string, groupLabels map[string]string) (*GroupPublishHandle, error) {
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return c.queue.SubmitGroupWithConfirmation(alerts, target, groupKey, receiverName, groupLabels)
}

// awaitDelivery waits for one target's final delivery outcome, bounded by
// deliveryTimeout and by ctx (task rec).
//
// Both bounds report "unconfirmed", never "delivered": the notify chain must
// retry a target it cannot prove was reached rather than record it as sent —
// see ErrDeliveryWaitTimeout. The caller abandons the job on both paths, so
// the queue stops working on an outcome nobody will read.
func (c *PublishingCoordinator) awaitDelivery(ctx context.Context, handle *GroupPublishHandle, target *core.PublishingTarget) error {
	confirm := handle.Done()
	if confirm == nil {
		// Defensive: SubmitGroupWithConfirmation only returns a nil handle
		// together with a non-nil error, which the caller already handled.
		return fmt.Errorf("%w: no confirmation channel for target %q", ErrDeliveryNotAttempted, target.Name)
	}

	timer := time.NewTimer(c.deliveryTimeout)
	defer timer.Stop()

	select {
	case err := <-confirm:
		if err != nil {
			c.logger.Warn("Group notification delivery not confirmed for target",
				"target", target.Name,
				"error", err,
			)
		}
		return err
	case <-timer.C:
		return fmt.Errorf("%w after %s (target %q)", ErrDeliveryWaitTimeout, c.deliveryTimeout, target.Name)
	case <-ctx.Done():
		return fmt.Errorf("%w: delivery confirmation aborted for target %q: %w", ErrDeliveryWaitTimeout, target.Name, ctx.Err())
	}
}
