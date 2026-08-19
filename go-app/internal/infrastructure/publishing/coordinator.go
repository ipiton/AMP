package publishing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
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

// MaxDeliveryConfirmationTimeout is the largest value
// publishing.queue.delivery_confirmation_timeout may be set to (task rec fix
// round 2, review finding R9). Re-exported from core.MaxDeliveryConfirmationTimeout
// — see that constant's doc comment for the sizing rationale and for why the
// real definition lives there instead of here (wave-3 review finding M-f:
// internal/config must not import infrastructure/publishing for one
// constant).
const MaxDeliveryConfirmationTimeout = core.MaxDeliveryConfirmationTimeout

// PublishingResult represents the result of publishing to a single target
type PublishingResult struct {
	Target  *core.PublishingTarget
	Success bool
	Error   error

	// DeliveredAlerts carries the core.Alert.DeliveryKey of the alerts this
	// target DID accept when Success is false (task fu4, alertmanager-parity
	// wave 4) — the per-alert partial progress of a non-batch group job whose
	// outcome as a whole was not confirmed.
	//
	// Always nil when Success is true: a confirmed target gets a full nflog
	// entry covering the whole alert set, which supersedes any per-alert
	// bookkeeping, so repeating the keys there would be redundant state with
	// its own TTL. Also always nil for a batch-capable target (one POST per
	// group ⇒ nothing partial to report) and for the single-alert
	// PublishToAll/PublishToTargets paths, which have no group set to be
	// partial about.
	//
	// The notify chain turns a non-empty value into the target's delivered set
	// so the group's next fire re-sends only the alerts still owed — see
	// grouping.TargetPublishOutcome.DeliveredAlerts.
	DeliveredAlerts []string
}

// PublishingCoordinator manages concurrent publishing to multiple targets
type PublishingCoordinator struct {
	queue            *PublishingQueue
	discoveryManager TargetDiscoveryManager
	modeManager      ModeManager // TN-060: Mode manager for metrics-only fallback
	semaphore        chan struct{}
	deliveryTimeout  time.Duration // task rec: per-target delivery-confirmation wait
	metrics          *v2.PublishingMetrics
	logger           *slog.Logger

	// knownReceivers is the set of receiver names DECLARED in the loaded
	// config, pushed in by the wiring layer (ServiceRegistry) at startup and on
	// every reload. It exists to tell two zero-target situations apart
	// (FU-RECEIVERS-INTEGRATION slice-1 re-review finding R2):
	//
	//   - the receiver is declared but provisions no targets → upstream
	//     Alertmanager's BLACKHOLE receiver (`- name: 'null'`, or a receiver
	//     whose only integration AMP cannot deliver). Upstream accepts the
	//     notification and drops it, so this must be a SUCCESSFUL no-op.
	//   - the receiver is not declared at all → a real configuration gap
	//     (a route pointing at a receiver that does not exist, or a target set
	//     that never loaded), which must stay a loud error.
	//
	// nil/empty means "the wiring never told us", in which case every
	// zero-target resolution keeps the pre-R2 error behaviour.
	knownReceivers atomic.Pointer[map[string]struct{}]
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

	// Metrics is optional (nil = no metrics). Used for the blackhole-drop
	// counter (re-review finding R2); delivery metrics themselves are recorded
	// by the queue and the publishers.
	Metrics *v2.PublishingMetrics
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
		metrics:          config.Metrics,
		logger:           logger,
	}
}

// SetKnownReceivers publishes the set of receiver names declared in the loaded
// config (re-review finding R2). Called by the wiring layer at startup and on
// every config reload; the swap is atomic, so a reload never leaves the
// coordinator with a half-updated set.
//
// Passing nil/empty restores the pre-R2 behaviour: every zero-target resolution
// is an error, blackhole or not.
func (c *PublishingCoordinator) SetKnownReceivers(names []string) {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	c.knownReceivers.Store(&set)
}

// isBlackholeReceiver reports whether receiverName is a receiver the config
// DECLARES but that resolved to zero publishing targets — upstream
// Alertmanager's blackhole receiver, which accepts a notification and drops it.
//
// An empty receiverName is never a blackhole: that is the legacy
// "all enabled targets" mode, where zero targets means the deployment has no
// targets at all.
func (c *PublishingCoordinator) isBlackholeReceiver(receiverName string) bool {
	if receiverName == "" {
		return false
	}
	set := c.knownReceivers.Load()
	if set == nil {
		return false
	}
	_, ok := (*set)[receiverName]
	return ok
}

// recordBlackholeDrop logs and counts an intentional drop.
func (c *PublishingCoordinator) recordBlackholeDrop(receiverName string, alertCount int) {
	// Debug, not Warn: this is the configured outcome, and a blackhole route
	// can carry a large share of the alert volume by design.
	c.logger.Debug("Receiver has no publishing targets; dropping notification as an intentional blackhole",
		"receiver", receiverName,
		"alert_count", alertCount,
	)
	if c.metrics != nil {
		c.metrics.RecordBlackholeDrop(receiverName)
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

// PublishToAll publishes alert to all enabled targets concurrently.
//
// NO RECEIVER FILTERING — deliberately, and that is why it must NOT be used on
// the alert-processing path (slice-1 review finding C1, FU-RECEIVERS-
// INTEGRATION). It used to be the non-grouped publish path, which made every
// alert reach every receiver's targets; since config-provisioned targets exist
// (one per `receivers:` integration), that is a cross-receiver leak rather than
// a wide fan-out. ApplicationPublishingAdapter now calls PublishToTargets with
// the routed receiver instead.
//
// Kept for operator-driven "publish to everything" use (and its existing
// tests): callers that genuinely mean ALL targets, not "the targets of this
// alert's receiver".
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

// FilterConfigSendResolved is the target FilterConfig key carrying upstream's
// per-integration `send_resolved` (FU-RECEIVERS-INTEGRATION slice 2). Absent (or
// any non-false value) means true, upstream's default.
//
// Written by businesspublishing.BuildConfigTargets for config-provisioned
// targets; a K8s Secret can carry the same key in its `filter_config` JSON. This
// is the ONLY consumer, and it deliberately sits in target RESOLUTION rather
// than inside a publisher: the publisher layer is the epic's fixed boundary, and
// filtering here also keeps a suppressed notification out of the queue entirely
// (no job, no retry, no circuit-breaker effect).
const FilterConfigSendResolved = "send_resolved"

// targetAcceptsAlertStatus reports whether target wants a notification about an
// alert in this state.
//
// Only one rule today: `send_resolved: false` drops RESOLVED alerts. Firing
// alerts are always accepted, and a target with no filter_config accepts
// everything — so this is a no-op for every pre-slice-2 target.
func targetAcceptsAlertStatus(target *core.PublishingTarget, status core.AlertStatus) bool {
	if status != core.StatusResolved {
		return true
	}
	if target == nil || target.FilterConfig == nil {
		return true
	}
	sendResolved, ok := target.FilterConfig[FilterConfigSendResolved]
	if !ok {
		return true
	}
	// Tolerant of the shapes a K8s target's filter_config JSON round-trips
	// into. Strings go through strconv.ParseBool, so "false"/"FALSE"/"0"/"f"
	// all mean false — review finding S2-M5: an earlier version special-cased
	// only the literal "false", which made the string "0" mean TRUE while the
	// NUMBER 0 meant false. An unparseable value means true (upstream's
	// default), never a silent suppression.
	switch v := sendResolved.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return true
		}
		return parsed
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return true
	}
}

// filterAlertsForTarget returns the alerts target actually wants out of alerts
// (send_resolved filtering, FU-RECEIVERS-INTEGRATION slice 2). Returns the input
// slice unchanged when nothing is filtered, so the common path allocates
// nothing.
func filterAlertsForTarget(target *core.PublishingTarget, alerts []*core.Alert) []*core.Alert {
	if targetAcceptsAlertStatus(target, core.StatusResolved) {
		return alerts
	}

	kept := make([]*core.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alert != nil && alert.Status == core.StatusResolved {
			continue
		}
		kept = append(kept, alert)
	}
	return kept
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
		// matchedReceiver counts targets that belong to this receiver BEFORE
		// send_resolved filtering, so "this receiver has no targets at all"
		// (blackhole / config gap) stays distinguishable from "its targets do
		// not want resolved notifications" (slice 2).
		matchedReceiver := 0
		for _, t := range all {
			if !t.Enabled {
				continue
			}
			if !targetMatchesReceiver(t, receiverName) {
				continue
			}
			matchedReceiver++
			if !targetAcceptsAlertStatus(t, enrichedAlert.Alert.Status) {
				continue
			}
			targets = append(targets, t)
		}

		if len(targets) == 0 && matchedReceiver > 0 {
			// Every target for this receiver declined this alert's state —
			// upstream's own outcome for send_resolved: false is "send nothing,
			// record nothing, not an error". No group lifecycle is involved on
			// this path, so there is nothing to settle (cf. S2-I1 below).
			c.recordResolvedSuppressed(receiverName, 1, enrichedAlert.Alert.Fingerprint)
			return []*PublishingResult{}, nil
		}

		if len(targets) == 0 {
			// Blackhole (re-review finding R2): a DECLARED receiver with no
			// targets is upstream's drop-on-purpose receiver, so this is a
			// successful no-op — not an error that would make the ingest batch
			// answer 207/500 for an alert the operator deliberately discards.
			if c.isBlackholeReceiver(receiverName) {
				c.recordBlackholeDrop(receiverName, 1)
				return []*PublishingResult{}, nil
			}

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
// targetAlerts implements task fwb's per-target notification-log dedup and
// task fu4's per-ALERT refinement of it (see
// grouping.GroupNotificationPublisher.PublishGroup's doc comment): called once
// per candidate target AFTER receiver-matching resolves it, BEFORE a job is
// submitted for it, and the job carries exactly the alerts it returns.
//
//   - empty/nil → the target is excluded entirely (no job, no result), because
//     it already confirmed delivery of this exact alert set within
//     repeat_interval. This is what makes a retry after a partial failure
//     resend to ONLY the targets that failed last cycle.
//   - a subset → a non-batch target that already accepted some of these alerts
//     individually (task fu4): its job carries only the remainder, so the
//     alerts that already landed are not sent twice. The subset is what
//     reaches the wire AND what the returned PublishingResult is about.
//   - nil callback → every target gets the full alert set (what every caller
//     outside the notify chain does).
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
func (c *PublishingCoordinator) PublishGroupToTargets(ctx context.Context, alerts []*core.Alert, receiverName string, groupKey string, groupLabels map[string]string, targetAlerts func(target string, alerts []*core.Alert) []*core.Alert) ([]*PublishingResult, error) {
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

	// Resolve targets ONCE for the whole group (not once per alert). Each
	// surviving target carries its OWN alert slice (task fu4): normally the
	// whole group, but only the alerts still owed for a non-batch target that
	// already accepted some of them — see targetAlerts.
	all := c.discoveryManager.ListTargets()
	targets := make([]*core.PublishingTarget, 0, len(all))
	perTargetAlerts := make([][]*core.Alert, 0, len(all))
	// See the single-alert path: counts receiver-matched targets before
	// send_resolved filtering. suppressedByFilter counts the ones that dropped
	// out BECAUSE of send_resolved, which review finding S2-I1 requires telling
	// apart from targets that were merely already covered this cycle.
	matchedReceiver := 0
	suppressedByFilter := 0
	for _, t := range all {
		if !t.Enabled {
			continue
		}
		if !targetMatchesReceiver(t, receiverName) {
			continue
		}

		matchedReceiver++

		// send_resolved filtering (slice 2) runs BEFORE the dedup callback: a
		// target that does not want resolved alerts must not have them counted
		// as owed, and a group that is entirely resolved must not reach it at
		// all.
		wanted := filterAlertsForTarget(t, alerts)
		if len(wanted) == 0 {
			suppressedByFilter++
			continue
		}

		// Task fwb per-target nflog dedup + task fu4 per-alert dedup: a target
		// already fully covered this cycle is skipped entirely — no job
		// submitted, no result reported.
		owed := wanted
		if targetAlerts != nil {
			owed = targetAlerts(t.Name, wanted)
			if len(owed) == 0 {
				continue
			}
		}

		targets = append(targets, t)
		perTargetAlerts = append(perTargetAlerts, owed)
	}

	if len(targets) == 0 && matchedReceiver > 0 {
		// NOT a blackhole: the receiver demonstrably has enabled matching
		// targets. Two very different reasons can land here, and conflating
		// them leaks groups (review finding S2-I1):
		//
		//  1. suppressedByFilter == 0 — every target was already covered this
		//     cycle (the fu4/fwb dedup steady state). Nothing new happened, and
		//     the fire that DID deliver already wrote its nflog entry and pruned
		//     the group. Report no outcomes; the manager logs Debug and stops.
		//
		//  2. suppressedByFilter > 0 — the alerts still owed are resolved ones
		//     that every candidate target declines (send_resolved: false). This
		//     MUST report a successful outcome, not "nothing new": returning
		//     zero outcomes makes publishGroupAlerts skip RecordSent AND
		//     pruneResolvedAlerts, which is the only caller of
		//     RemoveAlertFromGroup — so the group keeps its resolved alerts and
		//     re-arms its repeat_interval timer forever, one silent no-op fire
		//     per interval, one undead group per key. Upstream settles here: its
		//     RetryStage filters the resolved alerts out, SUCCEEDS, records, and
		//     aggrGroup.flush prunes. The synthetic outcome below is the same
		//     device the blackhole branch uses, under its own stable pseudo-
		//     target name so it can never suppress a real target's notification.
		if suppressedByFilter == 0 {
			return []*PublishingResult{}, nil
		}

		suppressedTarget := suppressedTargetName(receiverName)
		if targetAlerts != nil && len(targetAlerts(suppressedTarget, alerts)) == 0 {
			// Already settled this cycle — nothing new to suppress.
			return []*PublishingResult{}, nil
		}

		c.recordResolvedSuppressed(receiverName, len(alerts), "")
		return []*PublishingResult{{
			Target:  &core.PublishingTarget{Name: suppressedTarget, Receivers: []string{receiverName}},
			Success: true,
		}}, nil
	}

	if len(targets) == 0 {
		// Blackhole (re-review finding R2): upstream treats a notification to a
		// blackhole receiver as SENT, so this reports one successful synthetic
		// outcome. That makes the notify chain write its nflog entry and settle
		// the group (prune resolved alerts, no re-fire every group_interval),
		// instead of logging a publish error on every single fire for the life
		// of the alerts.
		//
		// The synthetic target name is stable across fires, which is what lets
		// the chain's own per-target dedup (targetAlerts, consulted just below)
		// recognise it next time.
		if c.isBlackholeReceiver(receiverName) {
			blackholeTarget := blackholeTargetName(receiverName)
			if targetAlerts != nil && len(targetAlerts(blackholeTarget, alerts)) == 0 {
				// Already recorded as sent this cycle — nothing new to drop.
				return []*PublishingResult{}, nil
			}

			c.recordBlackholeDrop(receiverName, len(alerts))
			return []*PublishingResult{{
				Target:  &core.PublishingTarget{Name: blackholeTarget, Receivers: []string{receiverName}},
				Success: true,
			}}, nil
		}

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

		go func(idx int, t *core.PublishingTarget, owed []*core.Alert) {
			defer wg.Done()

			var delivered []string

			handle, err := c.submitGroupJob(ctx, owed, t, groupKey, receiverName, groupLabels)
			if err == nil {
				// Abandon on every exit (fix round 1, review finding I2): a
				// no-op once the job finished, and the thing that frees the
				// worker when we stop waiting for a job that has not.
				//
				// The reason is what lets the queue tell "this target did not
				// answer in time" (breaker failure) from "we already have its
				// outcome" (nothing to judge) — fix round 2, finding R3. The
				// default is the pessimistic one, so an unforeseen early exit
				// still counts as an unanswered target rather than silently
				// exonerating it.
				reason := AbandonReasonUnconfirmed
				defer func() { handle.Abandon(reason) }()

				// Task rec: block until this target's job reports its real
				// outcome, so Success below means "the target accepted the
				// notification", not "a job was enqueued".
				var settled bool
				settled, err = c.awaitDelivery(ctx, handle, t)
				switch {
				case settled:
					reason = AbandonReasonSettled
				case errors.Is(err, context.Canceled):
					// ctx was explicitly cancelled — shutdown, or a caller
					// giving up for a reason that says nothing about this
					// target's health — as opposed to ctx.Done() firing
					// because the CALLER's own bounded deadline ran out
					// (context.DeadlineExceeded), which is still evidence the
					// target didn't answer in time and stays Unconfirmed.
					//
					// Review finding M-b: on a real SIGTERM the grouping
					// context dies too, so without this every in-flight
					// target's abandonment defaulted to Unconfirmed and
					// tripped one spurious circuit-breaker failure per target
					// on every shutdown.
					reason = AbandonReasonShutdown
				}

				if err != nil {
					// Task fu4: this target did not confirm the notification,
					// but a non-batch job may still have got SOME of the
					// alerts through. Harvest that progress so the notify
					// chain's retry fire carries only the alerts still owed.
					// Read before the deferred Abandon so a job that is about
					// to be cancelled still reports what it managed to send.
					delivered = handle.DeliveredAlerts()
				}
			}

			mu.Lock()
			results[idx] = &PublishingResult{
				Target:          t,
				Success:         err == nil,
				Error:           err,
				DeliveredAlerts: delivered,
			}
			mu.Unlock()
		}(i, target, perTargetAlerts[i])
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
// settled reports whether the job actually reported an outcome (true) or the
// wait was given up on (false) — the caller turns that into the
// AbandonReason, which decides whether the target's circuit breaker hears
// about it (fix round 2, review finding R3).
//
// Returns (settled, err) rather than (err, settled) — error-last per Go
// convention (ST1008); the original (err, settled) order was a style nit
// flagged in review finding M-f.
func (c *PublishingCoordinator) awaitDelivery(ctx context.Context, handle *GroupPublishHandle, target *core.PublishingTarget) (settled bool, err error) {
	confirm := handle.Done()
	if confirm == nil {
		// Defensive: SubmitGroupWithConfirmation only returns a nil handle
		// together with a non-nil error, which the caller already handled.
		// "settled": there is no job in flight to judge a target by.
		return true, fmt.Errorf("%w: no confirmation channel for target %q", ErrDeliveryNotAttempted, target.Name)
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
		return true, err
	case <-timer.C:
		return false, fmt.Errorf("%w after %s (target %q)", ErrDeliveryWaitTimeout, c.deliveryTimeout, target.Name)
	case <-ctx.Done():
		return false, fmt.Errorf("%w: delivery confirmation aborted for target %q: %w", ErrDeliveryWaitTimeout, target.Name, ctx.Err())
	}
}

// BlackholeTargetPrefix namespaces the synthetic "target" a blackhole receiver
// reports its (intentionally dropped) notification under — see
// PublishGroupToTargets and re-review finding R2.
//
// It can never collide with a real target: K8s-sourced names are DNS-1123
// (no ':'), and config-provisioned ones are namespaced `cfg:`.
const BlackholeTargetPrefix = "blackhole:"

// blackholeTargetName builds the stable synthetic target name for a receiver.
// Stability matters: the notify chain's per-target dedup (nflog) keys on it, so
// a name that changed between fires would re-record on every fire.
func blackholeTargetName(receiverName string) string {
	return BlackholeTargetPrefix + receiverName
}

// SuppressedTargetPrefix namespaces the synthetic "target" a receiver reports a
// send_resolved-suppressed notification under (review finding S2-I1).
//
// Like BlackholeTargetPrefix it cannot collide with a real target name (K8s
// names are DNS-1123, config-provisioned ones are namespaced `cfg:`), and it is
// STABLE across fires so the notify chain's per-target dedup recognises it.
const SuppressedTargetPrefix = "suppressed:"

func suppressedTargetName(receiverName string) string {
	return SuppressedTargetPrefix + receiverName
}

// recordResolvedSuppressed logs and counts a suppressed resolved notification.
// fingerprint is empty on the group path (the suppression covers a whole group).
func (c *PublishingCoordinator) recordResolvedSuppressed(receiverName string, alertCount int, fingerprint string) {
	// Debug, not Warn: the operator asked for this by setting
	// send_resolved: false. resolved_suppressed_total is the dashboard signal.
	c.logger.Debug("Resolved notification suppressed for every target of receiver (send_resolved: false)",
		"receiver", receiverName,
		"alert_count", alertCount,
		"fingerprint", fingerprint,
	)
	if c.metrics != nil {
		c.metrics.RecordResolvedSuppressed(receiverName)
	}
}
