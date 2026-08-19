package application

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// === Task rec (alertmanager-parity wave 3): nflog entries follow CONFIRMED
// delivery ===
//
// End-to-end over the REAL stack — DefaultGroupManager's notify chain ->
// ApplicationPublishingAdapter -> PublishingCoordinator -> PublishingQueue
// worker pool -> httptest webhook — because the bug being fixed only exists
// where those layers meet: every layer in isolation was already "correct"
// while a 500 from the endpoint still produced an nflog entry (RecordSent on
// enqueue), suppressing the group for a whole repeat_interval.

// recordingNotifyLog is an in-memory GroupNotifyLog that also records what
// was written, so a test can assert on nflog state directly.
//
// Dedup semantics mirror the production in-memory notifyDedupLog: an entry
// counts as a duplicate while its sentAt is at/after the caller's ttl
// cutoff. TryClaim always succeeds (single process — DefaultGroupManager's
// own publishLocks serialize same-process fires).
type recordingNotifyLog struct {
	mu      sync.Mutex
	entries map[string]time.Time // "groupKey|target" -> sentAt
	sends   []string             // target names, in RecordSent order
}

func newRecordingNotifyLog() *recordingNotifyLog {
	return &recordingNotifyLog{entries: map[string]time.Time{}}
}

func (l *recordingNotifyLog) key(groupKey grouping.GroupKey, target string) string {
	return string(groupKey) + "|" + target
}

func (l *recordingNotifyLog) IsDuplicate(_ context.Context, groupKey grouping.GroupKey, target string, _ string, ttl time.Time) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sentAt, ok := l.entries[l.key(groupKey, target)]
	return ok && !sentAt.Before(ttl), nil
}

func (l *recordingNotifyLog) RecordSent(_ context.Context, groupKey grouping.GroupKey, target string, _ string, now time.Time, _ time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[l.key(groupKey, target)] = now
	l.sends = append(l.sends, target)
	return nil
}

func (l *recordingNotifyLog) Forget(_ context.Context, groupKey grouping.GroupKey) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.entries {
		if len(k) > len(groupKey) && k[:len(groupKey)] == string(groupKey) {
			delete(l.entries, k)
		}
	}
	return nil
}

func (l *recordingNotifyLog) TryClaim(_ context.Context, _ grouping.GroupKey, _ time.Duration) (bool, func() error, error) {
	return true, func() error { return nil }, nil
}

// recordedSends returns the target names RecordSent was called for.
func (l *recordingNotifyLog) recordedSends() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.sends...)
}

// countingWebhook is an httptest webhook that counts requests and answers
// each one with status.
type countingWebhook struct {
	server *httptest.Server
	hits   atomic.Int64
}

func newCountingWebhook(t *testing.T, status int) *countingWebhook {
	t.Helper()
	w := &countingWebhook{}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		w.hits.Add(1)
		rw.WriteHeader(status)
	}))
	t.Cleanup(w.server.Close)
	return w
}

func (w *countingWebhook) target(name string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:    name,
		Type:    "webhook",
		URL:     w.server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	}
}

// notifyChainStack is the wired-up stack a delivery-confirmation test drives.
type notifyChainStack struct {
	manager   *grouping.DefaultGroupManager
	notifyLog *recordingNotifyLog
	groupKey  grouping.GroupKey
}

// newNotifyChainStack wires the real notify chain onto a real publishing
// queue and coordinator over targets.
//
// Timings are deliberately tiny so a test can watch two consecutive fires:
// group_wait 30ms (first notification), group_interval 120ms (the retry tick
// the fix relies on), repeat_interval as given (the dedup window).
func newNotifyChainStack(t *testing.T, repeatInterval time.Duration, targets ...*core.PublishingTarget) *notifyChainStack {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(&discardWriter{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing

	discovery := infrapublishing.NewStubTargetDiscoveryManager(logger)
	for _, target := range targets {
		discovery.AddTarget(target)
	}

	queue := infrapublishing.NewPublishingQueue(
		infrapublishing.NewPublisherFactory(infrapublishing.NewAlertFormatter(""), logger, metrics, ""),
		nil,
		infrapublishing.NewLRUJobTrackingStore(32),
		infrapublishing.PublishingQueueConfig{
			WorkerCount:             4,
			HighPriorityQueueSize:   32,
			MediumPriorityQueueSize: 32,
			LowPriorityQueueSize:    32,
			MaxRetries:              0, // one HTTP attempt per fire: keeps a failing target fast
			RetryInterval:           time.Millisecond,
			Metrics:                 metrics,
		},
		nil,
		logger,
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	coordinatorConfig := infrapublishing.DefaultCoordinatorConfig()
	coordinatorConfig.DeliveryConfirmationTimeout = 3 * time.Second
	coordinator := infrapublishing.NewPublishingCoordinator(queue, discovery, nil, coordinatorConfig, logger)

	adapter, err := NewApplicationPublishingAdapter(coordinator, logger)
	require.NoError(t, err)

	timerStorage := grouping.NewInMemoryTimerStorage(logger)
	timerManager, err := grouping.NewDefaultTimerManager(grouping.TimerManagerConfig{
		Storage: timerStorage,
		Logger:  logger,
	})
	require.NoError(t, err)

	groupingConfig := &grouping.GroupingConfig{
		Route: &grouping.Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &grouping.Duration{Duration: 30 * time.Millisecond},
			GroupInterval:  &grouping.Duration{Duration: 120 * time.Millisecond},
			RepeatInterval: &grouping.Duration{Duration: repeatInterval},
		},
	}

	notifyLog := newRecordingNotifyLog()

	manager, err := grouping.NewDefaultGroupManager(context.Background(), grouping.DefaultGroupManagerConfig{
		KeyGenerator: grouping.NewGroupKeyGenerator(),
		Config:       groupingConfig,
		Storage:      grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{Logger: logger}),
		TimerManager: timerManager,
		Publisher:    adapter,
		NotifyLog:    notifyLog,
		Logger:       logger,
	})
	require.NoError(t, err)
	require.NoError(t, timerManager.SetGroupManager(manager))
	t.Cleanup(func() { _ = timerManager.Shutdown(context.Background()) })

	return &notifyChainStack{
		manager:   manager,
		notifyLog: notifyLog,
		groupKey:  grouping.GroupKey("receiver=default/alertname=HighCPU"),
	}
}

// addFiringAlert puts one firing alert into the group, which starts its
// group_wait timer and thereby the whole chain.
func (s *notifyChainStack) addFiringAlert(t *testing.T, fingerprint string) {
	t.Helper()
	_, err := s.manager.AddAlertToGroup(context.Background(), &core.Alert{
		Fingerprint: fingerprint,
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "warning"},
		Annotations: map[string]string{"summary": "cpu is high"},
		StartsAt:    time.Now().UTC(),
	}, s.groupKey)
	require.NoError(t, err)
}

// discardWriter swallows the stack's log output (these tests exercise real
// failing deliveries, which are legitimately noisy).
type discardWriter struct{}

func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestNotifyChain_FailedDeliveryLeavesNoNflogEntryAndRetriesNextTick is the
// end-to-end regression for FU-RECORDSENT-DELIVERY-CONFIRMATION: a webhook
// answering 500 must leave the notification log untouched, so the group's
// next group_interval fire publishes to it again instead of the group going
// quiet until repeat_interval (hours) elapses.
func TestNotifyChain_FailedDeliveryLeavesNoNflogEntryAndRetriesNextTick(t *testing.T) {
	webhook := newCountingWebhook(t, http.StatusInternalServerError)
	stack := newNotifyChainStack(t, time.Hour, webhook.target("webhook-500"))

	stack.addFiringAlert(t, "fp-1")

	// group_wait fire + at least one group_interval fire.
	require.Eventually(t, func() bool { return webhook.hits.Load() >= 2 }, 5*time.Second, 10*time.Millisecond,
		"a target that failed delivery must be retried on the next scheduled fire")

	assert.Empty(t, stack.notifyLog.recordedSends(),
		"a 500 must never be recorded as sent (that is what used to suppress the group for a whole repeat_interval)")
}

// TestNotifyChain_ConfirmedDeliveryIsRecordedAndDedupedWithinRepeatInterval
// is the other half: a real 200 IS recorded, and the recorded entry then
// suppresses the following fires for the whole repeat_interval — the fix must
// not turn every tick into a duplicate notification.
func TestNotifyChain_ConfirmedDeliveryIsRecordedAndDedupedWithinRepeatInterval(t *testing.T) {
	webhook := newCountingWebhook(t, http.StatusOK)
	stack := newNotifyChainStack(t, time.Hour, webhook.target("webhook-ok"))

	stack.addFiringAlert(t, "fp-1")

	require.Eventually(t, func() bool { return len(stack.notifyLog.recordedSends()) == 1 }, 5*time.Second, 10*time.Millisecond,
		"a confirmed delivery must be recorded in the notification log")
	assert.Equal(t, []string{"webhook-ok"}, stack.notifyLog.recordedSends())

	// Two more group_interval ticks pass (120ms each): the recorded entry is
	// still inside repeat_interval, so skipTarget must suppress them.
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, int64(1), webhook.hits.Load(),
		"an already-confirmed target must stay deduped for repeat_interval")
	assert.Len(t, stack.notifyLog.recordedSends(), 1)
}

// TestNotifyChain_MixedBatchRecordsOnlyTheConfirmedTarget: one target
// confirms, one 500s. The confirmed one gets its entry and is then skipped;
// the failed one keeps being retried. This is the partial-failure path
// per-target nflog keys exist for.
func TestNotifyChain_MixedBatchRecordsOnlyTheConfirmedTarget(t *testing.T) {
	ok := newCountingWebhook(t, http.StatusOK)
	broken := newCountingWebhook(t, http.StatusInternalServerError)

	stack := newNotifyChainStack(t, time.Hour, ok.target("target-ok"), broken.target("target-500"))

	stack.addFiringAlert(t, "fp-1")

	// The failing target is retried on the group_interval fire; the confirmed
	// one is not (its fresh nflog entry makes skipTarget exclude it).
	//
	// Two hits, not more: AMP's timer chain is group_wait -> group_interval ->
	// repeat_interval, so after the group_interval fire the next scheduled
	// fire for this group is a repeat_interval away (one hour here). That
	// cadence is pre-existing and out of scope for task rec — what matters
	// here is that the failed target IS re-published on the next scheduled
	// fire instead of being suppressed by an nflog entry it never earned.
	require.Eventually(t, func() bool { return broken.hits.Load() >= 2 }, 5*time.Second, 10*time.Millisecond,
		"the unconfirmed target must be retried on the next scheduled fire")

	assert.Equal(t, []string{"target-ok"}, stack.notifyLog.recordedSends(),
		"only the target that confirmed delivery may be recorded")
	assert.Equal(t, int64(1), ok.hits.Load(),
		"the confirmed target must not be re-notified while its entry is fresh")
}
