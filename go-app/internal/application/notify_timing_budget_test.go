package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// === Task rec fix round 1 (review finding I3): startup guard ===
//
// The wait/callback/claim-TTL triple spans two packages that must not import
// each other, so nothing enforces it at compile time. validateNotifyTimingBudget
// is the runtime enforcement — these tests pin that it actually rejects the
// inconsistent combinations, since a violation is invisible at runtime
// (silently truncated deliveries, or a claim that expires mid-publish).

func newBudgetTestRegistry(t *testing.T, deliveryTimeout, callbackTimeout, claimTTL time.Duration) *ServiceRegistry {
	t.Helper()
	// Reconciliation disabled (interval 0) unless a test opts in — see
	// newBudgetTestRegistryWithGrace.
	return newBudgetTestRegistryWithGrace(t, deliveryTimeout, callbackTimeout, claimTTL, 0, 0)
}

func newBudgetTestRegistryWithGrace(t *testing.T, deliveryTimeout, callbackTimeout, claimTTL, reconciliationInterval, reconciliationGrace time.Duration) *ServiceRegistry {
	t.Helper()

	logger := slog.Default()
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing

	queue := infrapublishing.NewPublishingQueue(
		infrapublishing.NewPublisherFactory(infrapublishing.NewAlertFormatter(""), logger, metrics, ""),
		nil,
		infrapublishing.NewLRUJobTrackingStore(8),
		infrapublishing.PublishingQueueConfig{WorkerCount: 0, HighPriorityQueueSize: 4, MediumPriorityQueueSize: 4, LowPriorityQueueSize: 4, Metrics: metrics},
		nil,
		logger,
	)

	coordinatorConfig := infrapublishing.DefaultCoordinatorConfig()
	coordinatorConfig.DeliveryConfirmationTimeout = deliveryTimeout

	timerManager, err := grouping.NewDefaultTimerManager(grouping.TimerManagerConfig{
		Storage:                grouping.NewInMemoryTimerStorage(logger),
		Logger:                 logger,
		CallbackTimeout:        callbackTimeout,
		ReconciliationInterval: reconciliationInterval,
		ReconciliationGrace:    reconciliationGrace,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = timerManager.Shutdown(context.Background()) })

	groupManager, err := grouping.NewDefaultGroupManager(context.Background(), grouping.DefaultGroupManagerConfig{
		KeyGenerator: grouping.NewGroupKeyGenerator(),
		Config: &grouping.GroupingConfig{
			Route: &grouping.Route{Receiver: "default", GroupBy: []string{"alertname"}},
		},
		Storage:           grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{Logger: logger}),
		TimerManager:      timerManager,
		NotifyLogClaimTTL: claimTTL,
		Logger:            logger,
	})
	require.NoError(t, err)

	return &ServiceRegistry{
		logger:                logger,
		publishingCoordinator: infrapublishing.NewPublishingCoordinator(queue, infrapublishing.NewStubTargetDiscoveryManager(logger), nil, coordinatorConfig, logger),
		groupManager:          groupManager,
		groupTimerManager:     timerManager,
	}
}

// TestValidateNotifyTimingBudget_DerivedValuesPass: what ServiceRegistry
// actually wires (everything derived from one knob) must always validate.
func TestValidateNotifyTimingBudget_DerivedValuesPass(t *testing.T) {
	for _, wait := range []time.Duration{5 * time.Second, 45 * time.Second, 90 * time.Second} {
		r := newBudgetTestRegistry(t, wait, grouping.TimerCallbackTimeoutFor(wait), grouping.NotifyLogClaimTTLFor(wait))
		assert.NoError(t, r.validateNotifyTimingBudget(), "derived budget must validate for wait=%s", wait)
	}
}

// TestValidateNotifyTimingBudget_RejectsShortCallbackDeadline is finding C1's
// exact shape: a 30s callback deadline under a 45s wait.
func TestValidateNotifyTimingBudget_RejectsShortCallbackDeadline(t *testing.T) {
	r := newBudgetTestRegistry(t, 45*time.Second, 30*time.Second, 60*time.Second)

	err := r.validateNotifyTimingBudget()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timer callback timeout")
}

// TestValidateNotifyTimingBudget_RejectsShortClaimTTL: a claim that cannot
// outlive the delivery wait reopens the double-publish window.
func TestValidateNotifyTimingBudget_RejectsShortClaimTTL(t *testing.T) {
	r := newBudgetTestRegistry(t, 45*time.Second, 60*time.Second, 30*time.Second)

	err := r.validateNotifyTimingBudget()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publish-claim TTL")
}

// TestValidateNotifyTimingBudget_RejectsClaimNotOutlastingCallback: the claim
// must cover the whole fire including its post-delivery bookkeeping, not just
// the wait — strict since fix round 2 (review finding R8).
func TestValidateNotifyTimingBudget_RejectsClaimNotOutlastingCallback(t *testing.T) {
	r := newBudgetTestRegistry(t, 10*time.Second, 60*time.Second, 30*time.Second)

	err := r.validateNotifyTimingBudget()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must exceed the timer callback timeout")

	// Equality is rejected too: it leaves zero margin for the bookkeeping.
	equal := newBudgetTestRegistry(t, 10*time.Second, 60*time.Second, 60*time.Second)
	require.Error(t, equal.validateNotifyTimingBudget())
}

// TestValidateNotifyTimingBudget_RejectsGraceShorterThanTheFire is review
// finding R4: with reconciliation enabled, a grace period that a live fire can
// outlast makes the fire adoptable, and the adopting replica deletes the shared
// timer record while the publisher is still using it.
func TestValidateNotifyTimingBudget_RejectsGraceShorterThanTheFire(t *testing.T) {
	wait := 45 * time.Second
	r := newBudgetTestRegistryWithGrace(t, wait,
		grouping.TimerCallbackTimeoutFor(wait),
		grouping.NotifyLogClaimTTLFor(wait),
		45*time.Second,
		20*time.Second, // the pre-round-2 default: shorter than one fire
	)

	err := r.validateNotifyTimingBudget()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation_grace")
	assert.Contains(t, err.Error(), grouping.ReconciliationGraceFor(wait).String(),
		"the error must name the grace value that would be safe")
}

// TestValidateNotifyTimingBudget_DerivedGracePasses: what ServiceRegistry
// actually wires (config default grace, derived callback/claim) validates.
func TestValidateNotifyTimingBudget_DerivedGracePasses(t *testing.T) {
	wait := 45 * time.Second
	r := newBudgetTestRegistryWithGrace(t, wait,
		grouping.TimerCallbackTimeoutFor(wait),
		grouping.NotifyLogClaimTTLFor(wait),
		45*time.Second,
		grouping.ReconciliationGraceFor(wait),
	)
	assert.NoError(t, r.validateNotifyTimingBudget())
}

// TestValidateNotifyTimingBudget_DisabledReconciliationSkipsGraceCheck: with no
// reconciliation loop there is nothing to adopt, so the grace period is not
// consulted (lite profile / non-Redis timer storage).
func TestValidateNotifyTimingBudget_DisabledReconciliationSkipsGraceCheck(t *testing.T) {
	wait := 45 * time.Second
	r := newBudgetTestRegistryWithGrace(t, wait,
		grouping.TimerCallbackTimeoutFor(wait),
		grouping.NotifyLogClaimTTLFor(wait),
		0,           // loop disabled
		time.Second, // absurdly short grace, never consulted
	)
	assert.NoError(t, r.validateNotifyTimingBudget())
}

// TestValidateNotifyTimingBudget_NoopWithoutBothSides: publishing or grouping
// disabled means there is no blocking publish to budget for.
func TestValidateNotifyTimingBudget_NoopWithoutBothSides(t *testing.T) {
	r := &ServiceRegistry{logger: slog.Default()}
	assert.NoError(t, r.validateNotifyTimingBudget())
}
