package grouping

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Task rec fix round 1 (review finding C1): the derived time budget ===

// TestNotifyBudget_CallbackDeadlineExceedsDeliveryWait is the inequality whose
// violation caused C1: a callback deadline at or below the delivery wait makes
// the wait unreachable and truncates every fire.
func TestNotifyBudget_CallbackDeadlineExceedsDeliveryWait(t *testing.T) {
	for _, wait := range []time.Duration{time.Second, 15 * time.Second, 45 * time.Second, 2 * time.Minute} {
		callback := TimerCallbackTimeoutFor(wait)
		assert.Greater(t, callback, wait,
			"callback deadline must leave room for the wait plus the chain's own overhead")
	}
}

// TestNotifyBudget_ClaimTTLCoversTheWholeFire: the claim is taken early in a
// fire and released at the very end, so it must cover the callback deadline —
// otherwise it expires mid-publish and a second replica can notify the same
// group.
func TestNotifyBudget_ClaimTTLCoversTheWholeFire(t *testing.T) {
	for _, wait := range []time.Duration{time.Second, 45 * time.Second, 5 * time.Minute} {
		claim := NotifyLogClaimTTLFor(wait)
		assert.GreaterOrEqual(t, claim, TimerCallbackTimeoutFor(wait))
		assert.Greater(t, claim, wait)
	}
}

// TestNotifyBudget_NonPositiveWaitFallsBackToDefaults: a hand-built manager or
// timer manager (tests, embedders) must still get a self-consistent budget.
func TestNotifyBudget_NonPositiveWaitFallsBackToDefaults(t *testing.T) {
	assert.Equal(t, defaultDeliveryConfirmationBudget+notifyChainOverheadBudget, TimerCallbackTimeoutFor(0))
	assert.Equal(t, TimerCallbackTimeoutFor(0), TimerCallbackTimeoutFor(-time.Second))
	assert.Equal(t, defaultNotifyLogClaimTTL, NotifyLogClaimTTLFor(0))
}

// TestNotifyBudget_DefaultsAreWiredIntoTheManagers pins that the zero-value
// path of both constructors lands on the derived defaults rather than on a
// literal that could drift again.
func TestNotifyBudget_DefaultsAreWiredIntoTheManagers(t *testing.T) {
	timerManager, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage: NewInMemoryTimerStorage(slog.Default()),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = timerManager.Shutdown(context.Background()) })
	assert.Equal(t, TimerCallbackTimeoutFor(0), timerManager.CallbackTimeout())

	manager := createTestManager(t)
	assert.Equal(t, defaultNotifyLogClaimTTL, manager.NotifyLogClaimTTL())
}

// TestNotifyBudget_ExplicitOverridesWin: wiring code passes the values derived
// from the configured delivery wait, so the overrides must actually take.
func TestNotifyBudget_ExplicitOverridesWin(t *testing.T) {
	timerManager, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:         NewInMemoryTimerStorage(slog.Default()),
		CallbackTimeout: 7 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = timerManager.Shutdown(context.Background()) })
	assert.Equal(t, 7*time.Second, timerManager.CallbackTimeout())
}
