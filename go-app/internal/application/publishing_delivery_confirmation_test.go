package application

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
//
// CONTEXT-AWARE ON PURPOSE (fix round 1, review finding I4): every method
// honours its context the way RedisNotifyLog does, and every ctx failure is
// counted in ctxErrors. That is what makes finding C1 testable — before the
// fix, a fire whose callback deadline expired during a slow delivery called
// RecordSent / the claim release / pruning with a dead context, so a target
// that HAD received the notification was never recorded and got re-paged.
// A test-double that ignored ctx could never see it.
type recordingNotifyLog struct {
	mu        sync.Mutex
	entries   map[string]time.Time // "groupKey|target" -> sentAt
	sends     []string             // target names, in RecordSent order
	releases  int                  // successful claim releases
	ctxErrors int                  // calls rejected because their ctx was already done

	// delivered is task fu4's per-alert delivered state:
	// "groupKey|target" -> {alert fingerprint -> delivered status}.
	//
	// Keyed by FINGERPRINT, exactly like both production implementations
	// (review round 1, finding C1): a set of composite "fingerprint:status"
	// members would accumulate both statuses of a flapping alert and suppress
	// its re-fire. A double that kept the looser shape would let that bug back
	// in through the end-to-end tests, which is how C1 survived round 1.
	// partials counts RecordPartialDelivery calls.
	delivered map[string]map[string]string
	partials  int
}

func newRecordingNotifyLog() *recordingNotifyLog {
	return &recordingNotifyLog{
		entries:   map[string]time.Time{},
		delivered: map[string]map[string]string{},
	}
}

func (l *recordingNotifyLog) key(groupKey grouping.GroupKey, target string) string {
	return string(groupKey) + "|" + target
}

// checkCtx mimics a Redis client's behaviour on an expired context: fail the
// operation and remember that it happened.
func (l *recordingNotifyLog) checkCtx(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		l.mu.Lock()
		l.ctxErrors++
		l.mu.Unlock()
		return fmt.Errorf("recordingNotifyLog: context already done: %w", err)
	}
	return nil
}

func (l *recordingNotifyLog) IsDuplicate(ctx context.Context, groupKey grouping.GroupKey, target string, _ string, ttl time.Time) (bool, error) {
	if err := l.checkCtx(ctx); err != nil {
		return false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	sentAt, ok := l.entries[l.key(groupKey, target)]
	return ok && !sentAt.Before(ttl), nil
}

func (l *recordingNotifyLog) RecordSent(ctx context.Context, groupKey grouping.GroupKey, target string, _ string, now time.Time, _ time.Duration) error {
	if err := l.checkCtx(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[l.key(groupKey, target)] = now
	l.sends = append(l.sends, target)
	// A full entry supersedes any per-alert progress toward it (task fu4) —
	// the production implementations drop it, so the double must too, or the
	// "delivered-set cleaned on success" invariant is untested.
	delete(l.delivered, l.key(groupKey, target))
	return nil
}

// DeliveredAlerts implements task fu4's per-alert delivered-set read.
func (l *recordingNotifyLog) DeliveredAlerts(ctx context.Context, groupKey grouping.GroupKey, target string) ([]string, error) {
	if err := l.checkCtx(ctx); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	statuses := l.delivered[l.key(groupKey, target)]
	if len(statuses) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(statuses))
	for fingerprint, status := range statuses {
		out = append(out, fingerprint+":"+status)
	}
	return out, nil
}

// RecordPartialDelivery implements task fu4's additive per-alert record.
func (l *recordingNotifyLog) RecordPartialDelivery(ctx context.Context, groupKey grouping.GroupKey, target string, deliveryKeys []string, _ time.Duration) error {
	if err := l.checkCtx(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.partials++
	key := l.key(groupKey, target)
	statuses := l.delivered[key]
	if statuses == nil {
		statuses = map[string]string{}
		l.delivered[key] = statuses
	}
	for _, deliveryKey := range deliveryKeys {
		idx := strings.LastIndex(deliveryKey, ":")
		if idx <= 0 || idx == len(deliveryKey)-1 {
			continue
		}
		// One status per fingerprint: the new status REPLACES the old one.
		statuses[deliveryKey[:idx]] = deliveryKey[idx+1:]
	}
	return nil
}

// deliveredAlerts returns the recorded per-alert delivered set for one
// (group, target) pair.
func (l *recordingNotifyLog) deliveredAlerts(groupKey grouping.GroupKey, target string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	statuses := l.delivered[l.key(groupKey, target)]
	out := make([]string, 0, len(statuses))
	for fingerprint, status := range statuses {
		out = append(out, fingerprint+":"+status)
	}
	return out
}

// partialRecords returns how many times RecordPartialDelivery was called.
func (l *recordingNotifyLog) partialRecords() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.partials
}

func (l *recordingNotifyLog) Forget(ctx context.Context, groupKey grouping.GroupKey) error {
	if err := l.checkCtx(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.entries {
		if len(k) > len(groupKey) && k[:len(groupKey)] == string(groupKey) {
			delete(l.entries, k)
		}
	}
	for k := range l.delivered {
		if len(k) > len(groupKey) && k[:len(groupKey)] == string(groupKey) {
			delete(l.delivered, k)
		}
	}
	return nil
}

// TryClaim always wins the claim (single process).
//
// The release closure detaches from the acquiring context, mirroring the
// production RedisNotifyLog (fix round 1, finding C1.3): release takes no
// context of its own, so the only place that decision can live is the
// implementation, and a release bound to the fire's context would silently
// skip its CAS-delete on exactly the long fires that need it — leaving the
// claim to linger to its TTL and making the group's NEXT fire skip itself.
// Counting releases here is what proves the chain releases the claim even
// when the fire's own context is already dead.
func (l *recordingNotifyLog) TryClaim(ctx context.Context, _ grouping.GroupKey, _ time.Duration) (bool, func() error, error) {
	if err := l.checkCtx(ctx); err != nil {
		return false, func() error { return nil }, err
	}
	releaseCtx := context.WithoutCancel(ctx)
	return true, func() error {
		if err := l.checkCtx(releaseCtx); err != nil {
			return err
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		l.releases++
		return nil
	}, nil
}

// recordedSends returns the target names RecordSent was called for.
func (l *recordingNotifyLog) recordedSends() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.sends...)
}

// contextErrors returns how many notify-log operations were rejected because
// their context was already done.
func (l *recordingNotifyLog) contextErrors() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ctxErrors
}

// claimReleases returns how many claim releases completed successfully.
func (l *recordingNotifyLog) claimReleases() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.releases
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

// notifyChainOptions tunes the harness where a test needs the time budget to
// bite (fix round 1, review finding I4). Zero values mean "comfortable
// defaults": a 3s confirmation wait and a callback deadline derived from it,
// i.e. nothing expires during a healthy fire.
type notifyChainOptions struct {
	repeatInterval time.Duration

	// confirmationTimeout is the coordinator's per-target delivery wait.
	confirmationTimeout time.Duration

	// callbackTimeout is the timer manager's per-callback deadline — the
	// context publishGroupAlerts actually receives in production. Setting it
	// SHORTER than confirmationTimeout reproduces the production shape review
	// finding C1 describes: the fire's own context dies while a slow target is
	// still being waited on.
	callbackTimeout time.Duration
}

// newNotifyChainStack wires the real notify chain onto a real publishing
// queue and coordinator over targets.
//
// Timings are deliberately tiny so a test can watch two consecutive fires:
// group_wait 30ms (first notification), group_interval 120ms (the retry tick
// the fix relies on), repeat_interval as given (the dedup window).
func newNotifyChainStack(t *testing.T, repeatInterval time.Duration, targets ...*core.PublishingTarget) *notifyChainStack {
	t.Helper()
	return newNotifyChainStackWithOptions(t, notifyChainOptions{repeatInterval: repeatInterval}, targets...)
}

func newNotifyChainStackWithOptions(t *testing.T, opts notifyChainOptions, targets ...*core.PublishingTarget) *notifyChainStack {
	t.Helper()

	repeatInterval := opts.repeatInterval
	if repeatInterval <= 0 {
		repeatInterval = time.Hour
	}
	confirmationTimeout := opts.confirmationTimeout
	if confirmationTimeout <= 0 {
		confirmationTimeout = 3 * time.Second
	}
	callbackTimeout := opts.callbackTimeout
	if callbackTimeout <= 0 {
		callbackTimeout = grouping.TimerCallbackTimeoutFor(confirmationTimeout)
	}

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
	coordinatorConfig.DeliveryConfirmationTimeout = confirmationTimeout
	coordinator := infrapublishing.NewPublishingCoordinator(queue, discovery, nil, coordinatorConfig, logger)

	adapter, err := NewApplicationPublishingAdapter(coordinator, logger)
	require.NoError(t, err)

	timerStorage := grouping.NewInMemoryTimerStorage(logger)
	timerManager, err := grouping.NewDefaultTimerManager(grouping.TimerManagerConfig{
		Storage:         timerStorage,
		Logger:          logger,
		CallbackTimeout: callbackTimeout,
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
		KeyGenerator:      grouping.NewGroupKeyGenerator(),
		Config:            groupingConfig,
		Storage:           grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{Logger: logger}),
		TimerManager:      timerManager,
		Publisher:         adapter,
		NotifyLog:         notifyLog,
		NotifyLogClaimTTL: grouping.NotifyLogClaimTTLFor(confirmationTimeout),
		Logger:            logger,
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

// addResolvedAlert re-adds an alert under the same fingerprint as RESOLVED,
// which is how a real flap reaches a group: AddAlertToGroup replaces the alert
// in place, so the group's signature and the alert's DeliveryKey both change.
func (s *notifyChainStack) addResolvedAlert(t *testing.T, fingerprint string) {
	t.Helper()
	endsAt := time.Now().UTC()
	_, err := s.manager.AddAlertToGroup(context.Background(), &core.Alert{
		Fingerprint: fingerprint,
		AlertName:   "HighCPU",
		Status:      core.StatusResolved,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "warning"},
		Annotations: map[string]string{"summary": "cpu is high"},
		StartsAt:    time.Now().UTC().Add(-time.Minute),
		EndsAt:      &endsAt,
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

// === Fix round 1 (review findings C1 / I4): the fire's own context expires ===

// newHangingWebhook serves requests that never answer until the test tears
// down: it is how these tests burn a fire's callback budget on one target
// without failing its delivery.
//
// Parks on an explicit release channel (closed before Server.Close, so
// teardown never waits on a stuck handler) rather than on r.Context(), which
// net/http does not cancel promptly for a handler that has not written yet.
func newHangingWebhook(t *testing.T) *countingWebhook {
	t.Helper()
	w := &countingWebhook{}
	release := make(chan struct{})
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.hits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		w.server.Close()
	})
	return w
}

// TestNotifyChain_ConfirmedTargetIsRecordedEvenWhenTheFireContextExpires is
// the regression net for review finding C1, in the production shape: the
// notify chain runs on the timer manager's per-callback context, and that
// deadline is SHORTER here than one target's delivery takes.
//
// Round 1 shipped RecordSent / claim release / pruning on that same context,
// so the fast target — which really did receive the notification — got no
// nflog entry (and the claim was never released), meaning it would be re-paged
// on the next fire. The fix runs post-delivery bookkeeping on a detached,
// bounded context, so the confirmed target IS recorded even though the fire's
// own context is already dead by then.
func TestNotifyChain_ConfirmedTargetIsRecordedEvenWhenTheFireContextExpires(t *testing.T) {
	fast := newCountingWebhook(t, http.StatusOK)
	slow := newHangingWebhook(t)

	stack := newNotifyChainStackWithOptions(t, notifyChainOptions{
		repeatInterval: time.Hour,
		// Confirmation wait far longer than the callback deadline: the fire's
		// context is what gives up first, exactly as in production before this
		// fix (30s callback ctx vs. 45s wait).
		confirmationTimeout: 10 * time.Second,
		callbackTimeout:     300 * time.Millisecond,
	}, fast.target("target-fast"), slow.target("target-slow"))

	stack.addFiringAlert(t, "fp-1")

	// The fast target must end up recorded — bookkeeping survives the dead
	// fire context.
	require.Eventually(t, func() bool {
		for _, target := range stack.notifyLog.recordedSends() {
			if target == "target-fast" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"a target that confirmed delivery must be recorded even when the fire's context expired waiting on a slower target")

	assert.NotContains(t, stack.notifyLog.recordedSends(), "target-slow",
		"the target that never confirmed must NOT be recorded")
	assert.Zero(t, stack.notifyLog.contextErrors(),
		"no notify-log operation may run on an already-expired context")
	assert.Positive(t, stack.notifyLog.claimReleases(),
		"the cross-replica publish claim must be released even on a fire whose context expired")
}

// TestNotifyChain_CallerContextShorterThanConfirmationWaitStillReleasesClaim
// is the single-target version of the same class: nothing confirms at all
// (the only target is slower than the fire), so nothing may be recorded — but
// the claim must still be released and no bookkeeping call may hit a dead
// context, or the group's next fire would skip itself with "claim held by
// another replica".
func TestNotifyChain_CallerContextShorterThanConfirmationWaitStillReleasesClaim(t *testing.T) {
	slow := newHangingWebhook(t)

	stack := newNotifyChainStackWithOptions(t, notifyChainOptions{
		repeatInterval:      time.Hour,
		confirmationTimeout: 10 * time.Second,
		callbackTimeout:     250 * time.Millisecond,
	}, slow.target("target-slow"))

	stack.addFiringAlert(t, "fp-1")

	require.Eventually(t, func() bool { return stack.notifyLog.claimReleases() >= 1 }, 5*time.Second, 10*time.Millisecond,
		"the publish claim must be released once the fire gives up on its unconfirmed target")

	assert.Empty(t, stack.notifyLog.recordedSends(),
		"an unconfirmed target must never be recorded")
	assert.Zero(t, stack.notifyLog.contextErrors(),
		"no notify-log operation may run on an already-expired context")
}

// TestNotifyChain_ConfirmedTargetIsRecordedWhenTheFireOutlastsTheBookkeepingWindow
// is the fix-round-2 regression net (review findings R1/R2), and the one that
// runs at PRODUCTION proportions: the fire's delivery wait (6s) is longer than
// grouping's bookkeeping window (notifyBookkeepingTimeout, 5s), which is the
// relationship production has at the defaults (45s wait vs. 5s window).
//
// Fix round 1 detached the bookkeeping context but built it BEFORE the blocking
// publish, so context.WithTimeout's absolute deadline was already spent by the
// time RecordSent ran — the "detached" context was expired and a target that
// really delivered still got no nflog entry. The earlier
// callback-deadline-shorter-than-the-wait test cannot see this, because its
// whole fire ends well inside the 5s window.
//
// Deliberately slow (~6s): the timing relationship IS the assertion, and
// notifyBookkeepingTimeout is an internal constant rather than an injectable
// knob. Kept to one test.
func TestNotifyChain_ConfirmedTargetIsRecordedWhenTheFireOutlastsTheBookkeepingWindow(t *testing.T) {
	fast := newCountingWebhook(t, http.StatusOK)
	hanging := newHangingWebhook(t)

	stack := newNotifyChainStackWithOptions(t, notifyChainOptions{
		repeatInterval: time.Hour,
		// 6s > notifyBookkeepingTimeout (5s): the wait alone would exhaust a
		// bookkeeping deadline stamped before the publish.
		confirmationTimeout: 6 * time.Second,
		// Comfortably longer than the wait, so the fire's own context is NOT
		// what expires — isolating the bookkeeping-deadline bug from the
		// callback-deadline bug the sibling test covers.
		callbackTimeout: 30 * time.Second,
	}, fast.target("target-fast"), hanging.target("target-hanging"))

	stack.addFiringAlert(t, "fp-1")

	require.Eventually(t, func() bool {
		for _, target := range stack.notifyLog.recordedSends() {
			if target == "target-fast" {
				return true
			}
		}
		return false
	}, 20*time.Second, 50*time.Millisecond,
		"a target that confirmed delivery must be recorded even when the fire spent longer than the bookkeeping window waiting on a slower target")

	assert.NotContains(t, stack.notifyLog.recordedSends(), "target-hanging")
	assert.Zero(t, stack.notifyLog.contextErrors(),
		"bookkeeping must never run on an expired context, however long the delivery wait took")
	assert.Positive(t, stack.notifyLog.claimReleases())
}
