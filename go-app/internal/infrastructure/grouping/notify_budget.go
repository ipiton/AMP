package grouping

import "time"

// Notify-fire time budget (task rec fix round 1, review finding C1).
//
// THE PROBLEM THIS SOLVES. Since task rec, publishGroupAlerts blocks until
// the publishing stack confirms delivery per target. That turned three
// previously-unrelated durations into one dependent chain:
//
//	delivery-confirmation wait  (publishing.CoordinatorConfig.DeliveryConfirmationTimeout)
//	  <  timer-callback deadline (TimerManagerConfig.CallbackTimeout)
//	  <= cross-replica claim TTL (DefaultGroupManagerConfig.NotifyLogClaimTTL)
//
// Round 1 shipped them as three independent literals — a 30s callback
// deadline (hardcoded in onTimerExpired), a 45s wait and a 60s claim TTL —
// so the callback context silently capped the wait at 30s, and every
// document explaining the "45s wait / 60s claim / 15s margin" sizing
// described a number that could never be reached. Worse, the expired
// callback context was then reused for RecordSent / claim release /
// pruneResolvedAlerts, so a fire that ran long lost the bookkeeping for
// targets that HAD delivered.
//
// The helpers below make the two grouping-side durations derived values of
// the one knob an operator actually sets (publishing.queue.
// delivery_confirmation_timeout), and ServiceRegistry re-checks the
// relationship at startup (validateNotifyTimingBudget) so a hand-wired
// combination fails fast instead of silently truncating deliveries.
const (
	// notifyChainOverheadBudget is the slack a single notify fire needs ON
	// TOP OF the delivery-confirmation wait: the chain's own preamble
	// (Inhibit/Silence/TimeMute filtering, TryClaim, one IsDuplicate per
	// candidate target — Redis round-trips, milliseconds each), the
	// post-delivery bookkeeping, and goroutine scheduling jitter under load.
	// 15s is deliberately generous: overshooting costs nothing (the wait
	// itself is what bounds a fire in practice), while undershooting
	// reintroduces exactly the truncation bug this exists to prevent.
	notifyChainOverheadBudget = 15 * time.Second

	// notifyBookkeepingTimeout bounds the post-delivery bookkeeping —
	// RecordSent per confirmed target, claim release, resolved-alert
	// pruning. It is applied to a context DETACHED from the fire's own
	// context (see publishGroupAlerts) precisely so this work still runs
	// when the fire's context is already dead: an nflog entry that is not
	// written for a target that DID receive the notification causes a
	// duplicate page, and an unreleased claim stalls the group until the
	// claim TTL expires.
	notifyBookkeepingTimeout = 5 * time.Second

	// defaultDeliveryConfirmationBudget mirrors
	// publishing.DefaultDeliveryConfirmationTimeout and exists only so the
	// package-level defaults below (notifyLogClaimTTL,
	// defaultTimerCallbackTimeout) have a value without importing
	// infrastructure/publishing — grouping must not depend on it (see
	// GroupNotificationPublisher's doc comment on that boundary). Wiring
	// code passes the REAL configured value in; this constant only sets what
	// a hand-constructed manager/timer manager gets by default.
	defaultDeliveryConfirmationBudget = 45 * time.Second
)

// TimerCallbackTimeoutFor returns the timer-callback deadline that can
// accommodate a notify fire whose delivery-confirmation wait is
// deliveryTimeout. A non-positive deliveryTimeout falls back to the package
// default.
//
// Used by wiring code (ServiceRegistry.initializeGrouping) to size
// TimerManagerConfig.CallbackTimeout from the publishing stack's configured
// wait, instead of the two drifting apart.
func TimerCallbackTimeoutFor(deliveryTimeout time.Duration) time.Duration {
	if deliveryTimeout <= 0 {
		deliveryTimeout = defaultDeliveryConfirmationBudget
	}
	return deliveryTimeout + notifyChainOverheadBudget
}

// NotifyLogClaimTTLFor returns the cross-replica publish-claim TTL that
// covers a whole notify fire whose delivery-confirmation wait is
// deliveryTimeout — i.e. it must outlive the callback deadline, since the
// claim is taken early in the fire and released at the very end.
//
// Kept equal to TimerCallbackTimeoutFor rather than larger: the claim is
// acquired a few milliseconds into the fire, so its expiry lands just after
// the callback deadline, and every extra second here is a second longer that
// a CRASHED replica blocks this group's retries on the surviving ones.
func NotifyLogClaimTTLFor(deliveryTimeout time.Duration) time.Duration {
	return TimerCallbackTimeoutFor(deliveryTimeout)
}
