package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ipiton/AMP/internal/core"
)

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
	logger           *slog.Logger
}

// CoordinatorConfig holds configuration for publishing coordinator
type CoordinatorConfig struct {
	MaxConcurrent int // Maximum concurrent publishing operations
}

// DefaultCoordinatorConfig returns default configuration
func DefaultCoordinatorConfig() CoordinatorConfig {
	return CoordinatorConfig{
		MaxConcurrent: 5, // Publish to max 5 targets concurrently
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

	return &PublishingCoordinator{
		queue:            queue,
		discoveryManager: discoveryManager,
		modeManager:      modeManager,
		semaphore:        make(chan struct{}, config.MaxConcurrent),
		logger:           logger,
	}
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
// that boundary).
//
// Receiver-matching semantics mirror PublishToTargets: empty receiverName,
// or a target with no Receivers list, matches everything. Zero alerts is a
// no-op (nil, nil — the caller, publishGroupAlerts, never calls this with
// an empty slice, but this stays defensive). Zero matching targets returns
// the same "no targets found for receiver" error as PublishToTargets —
// logged by the caller, NOT retried in a loop here (the caller's next
// scheduled group timer will naturally retry with the group's then-current
// state).
func (c *PublishingCoordinator) PublishGroupToTargets(ctx context.Context, alerts []*core.Alert, receiverName string, groupKey string, skipTarget func(target string) bool) ([]*PublishingResult, error) {
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

			err := c.queue.SubmitGroup(alerts, t, groupKey, receiverName)

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
