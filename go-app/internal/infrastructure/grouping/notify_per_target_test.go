package grouping

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/require"
)

// Tests for the per-target notify decision (PROD-GROUPING-DEFAULT): what a
// target is sent on a flush, when resolved alerts leave the group, and what
// happens while the notification log cannot be read.

// perTargetPublisher mimics PublishingCoordinator's call order for one or
// more targets: filter by send_resolved, then ask targetAlerts.
type perTargetPublisher struct {
	mu sync.Mutex
	// targets in consult order; nil means the single target "t".
	targets []string
	// noResolve lists targets configured with send_resolved: false.
	noResolve map[string]bool
	// failing lists targets that report an unconfirmed delivery.
	failing map[string]bool
	// silent makes the publisher consult nobody, as metrics-only mode does.
	silent bool
	// sent is the signature of every set delivered, per target.
	sent map[string][]string
	// offered is the signature of every set targetAlerts was asked about.
	offered map[string][]string
}

func (p *perTargetPublisher) PublishGroup(_ context.Context, _ string, alerts []*core.Alert, _ string, _ map[string]string, targetAlerts func(string, []*core.Alert) []*core.Alert) ([]TargetPublishOutcome, error) {
	if p.silent {
		return nil, nil
	}
	targets := p.targets
	if targets == nil {
		targets = []string{"t"}
	}

	var outcomes []TargetPublishOutcome
	for _, target := range targets {
		wanted := alerts
		if p.noResolve[target] {
			wanted = nil
			for _, a := range alerts {
				if a.Status != core.StatusResolved {
					wanted = append(wanted, a)
				}
			}
			if len(wanted) == 0 {
				continue
			}
		}

		p.mu.Lock()
		if p.offered == nil {
			p.offered = map[string][]string{}
		}
		p.offered[target] = append(p.offered[target], alertSetSignature(wanted))
		p.mu.Unlock()

		owed := targetAlerts(target, wanted)
		if len(owed) == 0 {
			continue
		}
		if p.failing[target] {
			outcomes = append(outcomes, TargetPublishOutcome{Target: target, Success: false})
			continue
		}

		p.mu.Lock()
		if p.sent == nil {
			p.sent = map[string][]string{}
		}
		p.sent[target] = append(p.sent[target], alertSetSignature(owed))
		p.mu.Unlock()
		outcomes = append(outcomes, TargetPublishOutcome{Target: target, Success: true})
	}
	return outcomes, nil
}

func (p *perTargetPublisher) sentTo(target string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sent[target]...)
}

// unreadableNotifyLog is a notification log whose duplicate check can be made
// to fail, as RedisNotifyLog's does while Redis is unreachable.
type unreadableNotifyLog struct {
	*notifyDedupLog
	fail atomic.Bool
}

var errNotifyLogDown = errors.New("redis: i/o timeout")

func (l *unreadableNotifyLog) IsDuplicate(ctx context.Context, groupKey GroupKey, target, signature string, ttl time.Time) (bool, error) {
	if l.fail.Load() {
		return false, errNotifyLogDown
	}
	return l.notifyDedupLog.IsDuplicate(ctx, groupKey, target, signature, ttl)
}

var (
	perTargetGroupKey = GroupKey("receiver=default/alertname=X")
	perTargetLabels   = map[string]string{"alertname": "X"}
)

// newPerTargetManager returns a manager without timers: tests flush by hand.
func newPerTargetManager(t *testing.T, publisher GroupNotificationPublisher, notifyLog GroupNotifyLog) *DefaultGroupManager {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hour := &Duration{Duration: time.Hour}
	m, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: NewGroupKeyGenerator(),
		Config: &GroupingConfig{Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      hour,
			GroupInterval:  hour,
			RepeatInterval: hour,
		}},
		Logger:    logger,
		Storage:   NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: logger}),
		Publisher: publisher,
		NotifyLog: notifyLog,
	})
	require.NoError(t, err)
	return m
}

func addPerTargetAlert(t *testing.T, m *DefaultGroupManager, name string, status core.AlertStatus) {
	t.Helper()
	_, err := m.AddAlertToGroup(context.Background(), createTestAlert(name, status, perTargetLabels), perTargetGroupKey)
	require.NoError(t, err)
}

func flushPerTargetGroup(t *testing.T, m *DefaultGroupManager) {
	t.Helper()
	group, err := m.GetGroup(context.Background(), perTargetGroupKey)
	require.NoError(t, err)
	m.publishGroupAlerts(context.Background(), group)
}

func perTargetGroupSize(t *testing.T, m *DefaultGroupManager) int {
	t.Helper()
	group, err := m.GetGroup(context.Background(), perTargetGroupKey)
	require.NoError(t, err)
	return alertCount(group)
}

// currentSignature is the signature of everything in the group right now.
func currentSignature(t *testing.T, m *DefaultGroupManager) string {
	t.Helper()
	group, err := m.GetGroup(context.Background(), perTargetGroupKey)
	require.NoError(t, err)
	var alerts []*core.Alert
	for _, a := range group.Alerts {
		alerts = append(alerts, a)
	}
	return alertSetSignature(alerts)
}

// A target with send_resolved: false is not told again about the alerts still
// firing when another alert of the group resolves (review finding H1).
func TestPerTargetDedup_NoResolveTargetNotRenotifiedOnPartialResolve(t *testing.T) {
	pub := &perTargetPublisher{noResolve: map[string]bool{"t": true}}
	m := newPerTargetManager(t, pub, nil)

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 1)

	addPerTargetAlert(t, m, "a", core.StatusResolved)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 1, "the remaining firing alert was already notified")
	require.Equal(t, 1, perTargetGroupSize(t, m), "the resolved alert leaves the group although nothing was sent")

	// The target never heard of the resolve, so the alert firing again is
	// nothing new to it. Upstream behaves the same.
	addPerTargetAlert(t, m, "a", core.StatusFiring)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 1)

	addPerTargetAlert(t, m, "c", core.StatusFiring)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 2, "a new alert is notified on the next flush")
}

func TestPerTargetDedup_ResolveIsNotifiedOnceAndPruned(t *testing.T) {
	pub := &perTargetPublisher{}
	m := newPerTargetManager(t, pub, nil)

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	flushPerTargetGroup(t, m)

	addPerTargetAlert(t, m, "a", core.StatusResolved)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 2)
	require.Equal(t, 1, perTargetGroupSize(t, m))

	// The group only shrank: no notification until repeat_interval.
	flushPerTargetGroup(t, m)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 2)
}

// The log entry is written under the signature of the set the target was
// offered, not of the whole group — otherwise the next flush could not
// recognize the target's own set as already sent.
func TestPerTargetDedup_RecordsTheSetTheTargetWasOffered(t *testing.T) {
	pub := &perTargetPublisher{noResolve: map[string]bool{"t": true}}
	notifyLog := newNotifyDedupLog()
	m := newPerTargetManager(t, pub, notifyLog)

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	addPerTargetAlert(t, m, "b", core.StatusResolved)
	flushPerTargetGroup(t, m)

	firingOnly := alertSetSignature([]*core.Alert{createTestAlert("a", core.StatusFiring, perTargetLabels)})
	require.Equal(t, []string{firingOnly}, pub.sentTo("t"))

	since := time.Now().Add(-time.Hour)
	dup, err := notifyLog.IsDuplicate(context.Background(), perTargetGroupKey, "t", firingOnly, since)
	require.NoError(t, err)
	require.True(t, dup, "the firing-only set must be on record for the target")
}

// A send that was recorded but not followed by pruning (a crash in between)
// leaves a resolved alert behind. The next flush sends nothing and must still
// remove it (review finding H6).
func TestPerTargetDedup_CoveredResolvedAlertIsPrunedWithoutSending(t *testing.T) {
	pub := &perTargetPublisher{}
	notifyLog := newNotifyDedupLog()
	m := newPerTargetManager(t, pub, notifyLog)

	addPerTargetAlert(t, m, "a", core.StatusResolved)
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	require.NoError(t, notifyLog.RecordSent(context.Background(), perTargetGroupKey, "t", currentSignature(t, m), time.Now(), time.Hour))

	flushPerTargetGroup(t, m)

	require.Empty(t, pub.sentTo("t"))
	require.Equal(t, 1, perTargetGroupSize(t, m))
}

// A publisher that asks about no target (metrics-only mode) has not shown
// anyone the resolved alert, so it stays.
func TestPerTargetDedup_NothingPrunedWhenNoTargetWasConsulted(t *testing.T) {
	m := newPerTargetManager(t, &perTargetPublisher{silent: true}, nil)

	addPerTargetAlert(t, m, "a", core.StatusResolved)
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	flushPerTargetGroup(t, m)

	require.Equal(t, 2, perTargetGroupSize(t, m))
}

// While the shared log cannot be read, a replica does not repeat what it sent
// itself, and still sends what it has no record of.
func TestNotifyLogOutage_OwnSendNotRepeatedNewAlertStillSent(t *testing.T) {
	pub := &perTargetPublisher{}
	shared := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	m := newPerTargetManager(t, pub, NewResilientNotifyLog(shared))

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	flushPerTargetGroup(t, m)

	shared.fail.Store(true)
	flushPerTargetGroup(t, m)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 1, "the replica's own send is not repeated on every flush")

	addPerTargetAlert(t, m, "b", core.StatusFiring)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 2, "a new alert goes out although the log is unreadable")
}

// A replica with no record of its own (another replica, or this one after a
// restart) fails open: a duplicate is preferred to a lost notification.
func TestNotifyLogOutage_ReplicaWithoutOwnRecordFailsOpen(t *testing.T) {
	pub := &perTargetPublisher{}
	shared := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	shared.fail.Store(true)
	m := newPerTargetManager(t, pub, NewResilientNotifyLog(shared))

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	flushPerTargetGroup(t, m)

	require.Len(t, pub.sentTo("t"), 1)
}

// The replica's own record can be older than what another replica sent, so an
// answer from it is not proof the target has seen the resolved alert: the
// alert must stay until the shared log can be read (review finding K1).
func TestNotifyLogOutage_LocalAnswerDoesNotPruneResolved(t *testing.T) {
	pub := &perTargetPublisher{}
	shared := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	m := newPerTargetManager(t, pub, NewResilientNotifyLog(shared))

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	addPerTargetAlert(t, m, "b", core.StatusResolved)
	flushPerTargetGroup(t, m) // this replica sends a:firing|b:resolved
	require.Len(t, pub.sentTo("t"), 1)
	require.Equal(t, 1, perTargetGroupSize(t, m))

	// Another replica announces b firing again: only the shared log knows.
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	require.NoError(t, shared.RecordSent(context.Background(), perTargetGroupKey, "t", currentSignature(t, m), time.Now(), time.Hour))

	addPerTargetAlert(t, m, "b", core.StatusResolved)
	shared.fail.Store(true)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 1, "held back on the replica's own record")
	require.Equal(t, 2, perTargetGroupSize(t, m), "the resolved alert must stay until the target is told")

	shared.fail.Store(false)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("t"), 2, "the resolve goes out once the log is readable")
	require.Equal(t, 1, perTargetGroupSize(t, m))
}

// Same as above with two targets: one is held back on the local record, the
// other is sent to. The successful send must not prune either (review round
// 5, finding M1).
func TestNotifyLogOutage_SendToOneTargetDoesNotPruneForHeldBackTarget(t *testing.T) {
	pub := &perTargetPublisher{targets: []string{"A", "B"}, failing: map[string]bool{"B": true}}
	shared := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	m := newPerTargetManager(t, pub, NewResilientNotifyLog(shared))

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	addPerTargetAlert(t, m, "b", core.StatusResolved)
	flushPerTargetGroup(t, m) // A confirmed (on local record), B did not
	require.Equal(t, 2, perTargetGroupSize(t, m))
	pub.failing = nil

	// Another replica announces b firing again to both targets.
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	refired := currentSignature(t, m)
	for _, target := range []string{"A", "B"} {
		require.NoError(t, shared.RecordSent(context.Background(), perTargetGroupKey, target, refired, time.Now(), time.Hour))
	}

	addPerTargetAlert(t, m, "b", core.StatusResolved)
	shared.fail.Store(true)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("A"), 1, "A is held back on the replica's own record")
	require.Len(t, pub.sentTo("B"), 1, "B has no local record and is sent to")
	require.Equal(t, 2, perTargetGroupSize(t, m), "A has not been told about the resolve")

	shared.fail.Store(false)
	flushPerTargetGroup(t, m)
	require.Len(t, pub.sentTo("A"), 2, "A gets the resolve once the log is readable")
	flushPerTargetGroup(t, m)
	require.Equal(t, 1, perTargetGroupSize(t, m))
}

// Pruning works from the alert set the flush read. An alert that fired again
// while the notification was in flight must not be removed (review finding K5).
func TestPruneResolvedAlerts_KeepsAlertThatFiredAgain(t *testing.T) {
	m := newPerTargetManager(t, &perTargetPublisher{}, nil)
	addPerTargetAlert(t, m, "a", core.StatusResolved)
	addPerTargetAlert(t, m, "b", core.StatusFiring)
	flushed := []*core.Alert{
		createTestAlert("a", core.StatusResolved, perTargetLabels),
		createTestAlert("b", core.StatusFiring, perTargetLabels),
	}

	addPerTargetAlert(t, m, "a", core.StatusFiring)
	m.pruneResolvedAlerts(context.Background(), perTargetGroupKey, flushed)
	require.Equal(t, 2, perTargetGroupSize(t, m), "the alert is firing again")

	addPerTargetAlert(t, m, "a", core.StatusResolved)
	m.pruneResolvedAlerts(context.Background(), perTargetGroupKey, flushed)
	require.Equal(t, 1, perTargetGroupSize(t, m))
}

// The explicit API still removes an alert whatever its status.
func TestRemoveAlertFromGroup_RemovesFiringAlert(t *testing.T) {
	m := newPerTargetManager(t, &perTargetPublisher{}, nil)
	addPerTargetAlert(t, m, "a", core.StatusFiring)
	addPerTargetAlert(t, m, "b", core.StatusFiring)

	removed, err := m.RemoveAlertFromGroup(context.Background(), createTestAlert("a", core.StatusFiring, perTargetLabels).Fingerprint, perTargetGroupKey)
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 1, perTargetGroupSize(t, m))
}

// Run with -race. Alerts joining a group while it is stored, flushed and
// pruned used to race on the group's alert map.
func TestGroupManager_ConcurrentIngestFlushAndPrune(t *testing.T) {
	m := newPerTargetManager(t, &perTargetPublisher{}, nil)
	addPerTargetAlert(t, m, "keep", core.StatusFiring)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); addPerTargetAlert(t, m, "x", core.StatusFiring) }()
		go func() { defer wg.Done(); addPerTargetAlert(t, m, "y", core.StatusResolved) }()
		go func() { defer wg.Done(); flushPerTargetGroup(t, m) }()
		go func() {
			defer wg.Done()
			m.pruneResolvedAlerts(context.Background(), perTargetGroupKey,
				[]*core.Alert{createTestAlert("y", core.StatusResolved, perTargetLabels)})
		}()
	}
	wg.Wait()

	require.GreaterOrEqual(t, perTargetGroupSize(t, m), 2, "firing alerts must survive")
}
