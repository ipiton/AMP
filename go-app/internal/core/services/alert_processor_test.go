package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes -------------------------------------------------------------

// fakeFilterEngine never blocks: ProcessAlert's mode dispatch reaches the
// publisher deterministically regardless of these tests' route-evaluator
// scenarios.
type fakeFilterEngine struct{}

func (fakeFilterEngine) ShouldBlock(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
	return false, ""
}

// fakePublisher records calls so tests can assert the publish path ran
// unchanged, regardless of whether a routing decision was computed.
type fakePublisher struct {
	publishToAllCalls              int
	publishWithClassificationCalls int
}

func (f *fakePublisher) PublishToAll(ctx context.Context, alert *core.Alert) error {
	f.publishToAllCalls++
	return nil
}

func (f *fakePublisher) PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	f.publishWithClassificationCalls++
	return nil
}

// fakeRouteEvaluator is a scripted services.RouteEvaluator: returns either a
// fixed decision or a fixed error, and records the labels it was called
// with.
type fakeRouteEvaluator struct {
	decision  *RoutingDecision
	err       error
	calls     int
	gotLabels map[string]string
}

func (f *fakeRouteEvaluator) Evaluate(labels map[string]string) (*RoutingDecision, error) {
	f.calls++
	f.gotLabels = labels
	if f.err != nil {
		return nil, f.err
	}
	return f.decision, nil
}

// fakeGroupManager is a scripted GroupManager (task 2.3): records every
// AddAlertToGroup call (fingerprint + resolved key) so tests can assert
// alerts land in groups instead of / never alongside a direct publish.
type fakeGroupManager struct {
	calls []fakeGroupManagerCall
	err   error
}

type fakeGroupManagerCall struct {
	fingerprint string
	key         grouping.GroupKey
}

func (f *fakeGroupManager) AddAlertToGroup(ctx context.Context, alert *core.Alert, groupKey grouping.GroupKey, _ ...grouping.AddAlertOption) (*grouping.AlertGroup, error) {
	f.calls = append(f.calls, fakeGroupManagerCall{fingerprint: alert.Fingerprint, key: groupKey})
	if f.err != nil {
		return nil, f.err
	}
	return &grouping.AlertGroup{Key: groupKey}, nil
}

func newTestProcessorConfig(t *testing.T, routeEvaluator RouteEvaluator, publisher *fakePublisher) AlertProcessorConfig {
	t.Helper()
	return AlertProcessorConfig{
		FilterEngine:   fakeFilterEngine{},
		Publisher:      publisher,
		RouteEvaluator: routeEvaluator,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func testAlert() *core.Alert {
	return &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		StartsAt: time.Now(),
	}
}

// --- tests ---------------------------------------------------------------

func TestAlertProcessor_ProcessAlert_NoRouteEvaluator_NoBehaviorChange(t *testing.T) {
	publisher := &fakePublisher{}
	processor, err := NewAlertProcessor(newTestProcessorConfig(t, nil, publisher))
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())
	require.NoError(t, err)

	// Legacy/lite mode: publish path runs exactly as before this task, and
	// there is no routing decision to observe.
	assert.Equal(t, 1, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"expected exactly one publish call")
	assert.Nil(t, processor.LastRoutingDecision())
}

func TestAlertProcessor_ProcessAlert_ComputesLogsAndStoresRoutingDecision(t *testing.T) {
	publisher := &fakePublisher{}
	wantDecision := &RoutingDecision{
		Receiver:       "critical-pagerduty",
		GroupBy:        []string{"alertname", "namespace"},
		GroupWait:      10 * time.Second,
		GroupInterval:  5 * time.Minute,
		RepeatInterval: 4 * time.Hour,
		MatchedRoute:   "/routes[0]",
	}
	evaluator := &fakeRouteEvaluator{decision: wantDecision}

	processor, err := NewAlertProcessor(newTestProcessorConfig(t, evaluator, publisher))
	require.NoError(t, err)

	alert := testAlert()
	err = processor.ProcessAlert(context.Background(), alert)
	require.NoError(t, err)

	// Evaluator was invoked with the alert's labels.
	require.Equal(t, 1, evaluator.calls)
	assert.Equal(t, alert.Labels, evaluator.gotLabels)

	// Decision is stored (observability) ...
	got := processor.LastRoutingDecision()
	require.NotNil(t, got)
	assert.Equal(t, wantDecision, got)

	// ... and the publish path is completely unaffected: still exactly one
	// call, through the same interface as before this task.
	assert.Equal(t, 1, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"expected exactly one publish call")
}

func TestAlertProcessor_ProcessAlert_RouteEvaluatorError_FailsOpen(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{err: errors.New("boom: tree not ready")}

	processor, err := NewAlertProcessor(newTestProcessorConfig(t, evaluator, publisher))
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())

	// A route-evaluation error must not fail alert processing or block
	// publishing (fail-open, same posture as the inhibition check).
	require.NoError(t, err)
	assert.Equal(t, 1, publisher.publishToAllCalls+publisher.publishWithClassificationCalls)
	assert.Nil(t, processor.LastRoutingDecision())
}

// --- task 2.3: grouping.enabled pipeline switch --------------------------

func TestNewAlertProcessor_GroupManagerAndKeyGeneratorMustBePairedTogether(t *testing.T) {
	publisher := &fakePublisher{}

	cfg := newTestProcessorConfig(t, nil, publisher)
	cfg.GroupManager = &fakeGroupManager{}
	cfg.GroupKeyGenerator = nil // deliberately unpaired

	_, err := NewAlertProcessor(cfg)
	require.Error(t, err, "GroupManager without a GroupKeyGenerator must be rejected at construction")
}

func TestAlertProcessor_ProcessAlert_GroupingEnabled_RoutesToGroup_NotPublishedDirectly(t *testing.T) {
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{}
	decision := &RoutingDecision{
		Receiver:      "critical-pagerduty",
		GroupBy:       []string{"alertname", "severity"},
		GroupWait:     10 * time.Second,
		GroupInterval: 5 * time.Minute,
	}
	evaluator := &fakeRouteEvaluator{decision: decision}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.GroupingEnabled = true
	cfg.GroupManager = groupMgr
	cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()

	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	alert := testAlert()
	err = processor.ProcessAlert(context.Background(), alert)
	require.NoError(t, err)

	assert.Equal(t, 0, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"grouping.enabled must route the alert into its group, never publish it directly (mutual exclusion)")

	require.Len(t, groupMgr.calls, 1)
	assert.Equal(t, alert.Fingerprint, groupMgr.calls[0].fingerprint)

	// Finding 2 (review round 1): the storage key is prefixed with the
	// matched route's receiver identity, not just the raw labels+groupBy key.
	baseKey, err := grouping.NewGroupKeyGenerator().GenerateKey(alert.Labels, decision.GroupBy)
	require.NoError(t, err)
	wantKey := grouping.GroupKey("receiver=" + decision.Receiver + "/" + string(baseKey))
	assert.Equal(t, wantKey, groupMgr.calls[0].key)
}

func TestAlertProcessor_ProcessAlert_GroupingDisabled_PublishesDirectly_GroupUntouched(t *testing.T) {
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{}
	decision := &RoutingDecision{GroupBy: []string{"alertname"}}
	evaluator := &fakeRouteEvaluator{decision: decision}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.GroupingEnabled = false // explicit for clarity; also the zero value
	cfg.GroupManager = groupMgr
	cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()

	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())
	require.NoError(t, err)

	assert.Equal(t, 1, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"grouping.enabled=false must publish exactly as before this task")
	assert.Empty(t, groupMgr.calls,
		"grouping.enabled=false must never touch the group manager, even though it is wired")
}

func TestAlertProcessor_ProcessAlert_GroupingEnabledNoRouteTree_FallsBackToDirectPublishWithWarning(t *testing.T) {
	publisher := &fakePublisher{}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Mirrors ServiceRegistry.initializeGrouping's "no route tree configured"
	// skip: cfg.Grouping.Enabled=true but cfg.Routing==nil leaves
	// GroupManager/GroupKeyGenerator (and RouteEvaluator) nil.
	cfg := AlertProcessorConfig{
		FilterEngine:    fakeFilterEngine{},
		Publisher:       publisher,
		RouteEvaluator:  nil,
		GroupingEnabled: true,
		Logger:          logger,
	}
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())
	require.NoError(t, err)

	assert.Equal(t, 1, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"grouping.enabled without a usable routing decision/group manager must fall back to direct publish")
	assert.Contains(t, logBuf.String(), "falling back to direct publish",
		"the fallback must be logged loudly, not silently")
}

func TestAlertProcessor_ProcessAlert_GroupingEnabled_GroupKeyGeneration(t *testing.T) {
	const receiver = "team-x"
	cases := []struct {
		name    string
		groupBy []string
		wantKey grouping.GroupKey
	}{
		{
			name:    "groups by present labels",
			groupBy: []string{"alertname", "severity"},
			wantKey: grouping.GroupKey("receiver=" + receiver + "/alertname=HighCPU,severity=critical"),
		},
		{
			name:    "missing label uses the <missing> marker",
			groupBy: []string{"alertname", "namespace"},
			wantKey: grouping.GroupKey("receiver=" + receiver + "/alertname=HighCPU,namespace=<missing>"),
		},
		{
			name:    "empty GroupBy falls back to the global group key",
			groupBy: []string{},
			wantKey: grouping.GroupKey("receiver=" + receiver + "/" + string(grouping.GlobalGroupKey)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publisher := &fakePublisher{}
			groupMgr := &fakeGroupManager{}
			decision := &RoutingDecision{Receiver: receiver, GroupBy: tc.groupBy}
			evaluator := &fakeRouteEvaluator{decision: decision}

			cfg := newTestProcessorConfig(t, evaluator, publisher)
			cfg.GroupingEnabled = true
			cfg.GroupManager = groupMgr
			cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()

			processor, err := NewAlertProcessor(cfg)
			require.NoError(t, err)

			err = processor.ProcessAlert(context.Background(), testAlert())
			require.NoError(t, err)

			require.Len(t, groupMgr.calls, 1)
			assert.Equal(t, tc.wantKey, groupMgr.calls[0].key)
			assert.Zero(t, publisher.publishToAllCalls+publisher.publishWithClassificationCalls)
		})
	}
}

// TestAlertProcessor_ProcessAlert_GroupingEnabled_NewGroupStartsTimer_ExistingGroupDoesNotResetIt
// exercises the real grouping stack (DefaultGroupManager + DefaultTimerManager
// + in-memory storage) end-to-end through AlertProcessor, instead of the
// fakeGroupManager used above, specifically to verify the timer-scheduling
// behavior documented in routeAlertToGroup:
//   - a brand-new group starts its group_wait timer (task 2.2's
//     AddAlertToGroup does this internally), and
//   - a second alert landing in the SAME already-existing group does NOT
//     restart that timer (no ResetTimer call — see routeAlertToGroup's doc
//     comment for why this diverges from the task brief's literal wording
//     to match upstream Alertmanager semantics).
func TestAlertProcessor_ProcessAlert_GroupingEnabled_NewGroupStartsTimer_ExistingGroupDoesNotResetIt(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	timerStorage := grouping.NewInMemoryTimerStorage(logger)
	timerMgr, err := grouping.NewDefaultTimerManager(grouping.TimerManagerConfig{
		Storage: timerStorage,
		Logger:  logger,
	})
	require.NoError(t, err)

	groupWait := 30 * time.Second
	groupingCfg := &grouping.GroupingConfig{
		Route: &grouping.Route{
			Receiver:  "default",
			GroupBy:   []string{"alertname"},
			GroupWait: &grouping.Duration{Duration: groupWait},
		},
	}
	groupingCfg.Route.Defaults()

	keyGen := grouping.NewGroupKeyGenerator()
	groupStorage := grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{Logger: logger})

	groupMgr, err := grouping.NewDefaultGroupManager(ctx, grouping.DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       groupingCfg,
		Storage:      groupStorage,
		TimerManager: timerMgr,
		Logger:       logger,
	})
	require.NoError(t, err)
	require.NoError(t, timerMgr.SetGroupManager(groupMgr))

	publisher := &fakePublisher{}
	decision := &RoutingDecision{Receiver: "default", GroupBy: []string{"alertname"}, GroupWait: groupWait}
	evaluator := &fakeRouteEvaluator{decision: decision}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.GroupingEnabled = true
	cfg.GroupManager = groupMgr
	cfg.GroupKeyGenerator = keyGen

	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	baseKey, err := keyGen.GenerateKey(map[string]string{"alertname": "HighCPU"}, decision.GroupBy)
	require.NoError(t, err)
	key := grouping.GroupKey("receiver=" + decision.Receiver + "/" + string(baseKey))

	// First alert creates the group and starts its group_wait timer.
	alert1 := testAlert()
	require.NoError(t, processor.ProcessAlert(ctx, alert1))

	timer1, err := timerMgr.GetTimer(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, grouping.GroupWaitTimer, timer1.TimerType)
	assert.Equal(t, groupWait, timer1.Duration)
	startedAt1 := timer1.StartedAt

	// Second alert (same group_by label values, different fingerprint) joins
	// the EXISTING group. It must NOT restart the already-running timer.
	alert2 := testAlert()
	alert2.Fingerprint = "fp-2"
	require.NoError(t, processor.ProcessAlert(ctx, alert2))

	timer2, err := timerMgr.GetTimer(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, grouping.GroupWaitTimer, timer2.TimerType,
		"timer type must be unchanged — no reset/switch on an existing-group insert")
	assert.True(t, startedAt1.Equal(timer2.StartedAt),
		"an alert added to an existing group must not restart its timer (would starve notifications)")

	group, err := groupMgr.GetGroup(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, 2, group.Size(), "both alerts must land in the same group")

	assert.Zero(t, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"grouped alerts must never be published directly")
}

// --- task 2.3 review round 1 fixes ---------------------------------------

// TestAlertProcessor_ProcessAlert_GroupingEnabled_AddAlertToGroupError_FailsOpenToDirectPublish
// covers Finding 1: AddAlertToGroup failing must not drop the alert. It must
// fall back to direct publish exactly once — never zero (dropped) and never
// twice (double notification).
func TestAlertProcessor_ProcessAlert_GroupingEnabled_AddAlertToGroupError_FailsOpenToDirectPublish(t *testing.T) {
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{err: errors.New("boom: group storage unavailable")}
	decision := &RoutingDecision{Receiver: "team-x", GroupBy: []string{"alertname"}}
	evaluator := &fakeRouteEvaluator{decision: decision}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.GroupingEnabled = true
	cfg.GroupManager = groupMgr
	cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()

	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())
	require.NoError(t, err)

	// The grouping attempt happened (and failed) ...
	require.Len(t, groupMgr.calls, 1)
	// ... but the alert is still published, exactly once (fail-open, no drop,
	// no double-publish).
	assert.Equal(t, 1, publisher.publishToAllCalls+publisher.publishWithClassificationCalls,
		"AddAlertToGroup failure must fall back to direct publish exactly once, never drop the alert")
}

// TestAlertProcessor_ProcessAlert_GroupingEnabled_SameLabelsDifferentReceivers_DistinctGroups
// covers Finding 2: two alerts with identical labels/groupBy but different
// matched receivers must land in two distinct groups, not collide into one
// (which would risk misdelivery once task 2.4 delivers per-receiver).
func TestAlertProcessor_ProcessAlert_GroupingEnabled_SameLabelsDifferentReceivers_DistinctGroups(t *testing.T) {
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{}
	groupBy := []string{"alertname", "severity"}

	decisionA := &RoutingDecision{Receiver: "team-a-pagerduty", GroupBy: groupBy}
	evaluatorA := &fakeRouteEvaluator{decision: decisionA}
	cfgA := newTestProcessorConfig(t, evaluatorA, publisher)
	cfgA.GroupingEnabled = true
	cfgA.GroupManager = groupMgr
	cfgA.GroupKeyGenerator = grouping.NewGroupKeyGenerator()
	processorA, err := NewAlertProcessor(cfgA)
	require.NoError(t, err)

	decisionB := &RoutingDecision{Receiver: "team-b-slack", GroupBy: groupBy}
	evaluatorB := &fakeRouteEvaluator{decision: decisionB}
	cfgB := newTestProcessorConfig(t, evaluatorB, publisher)
	cfgB.GroupingEnabled = true
	cfgB.GroupManager = groupMgr
	cfgB.GroupKeyGenerator = grouping.NewGroupKeyGenerator()
	processorB, err := NewAlertProcessor(cfgB)
	require.NoError(t, err)

	// Same labels for both alerts — only the matched route/receiver differs.
	require.NoError(t, processorA.ProcessAlert(context.Background(), testAlert()))
	require.NoError(t, processorB.ProcessAlert(context.Background(), testAlert()))

	require.Len(t, groupMgr.calls, 2)
	assert.NotEqual(t, groupMgr.calls[0].key, groupMgr.calls[1].key,
		"identical labels/groupBy under different receivers must produce distinct group keys")
	assert.Contains(t, string(groupMgr.calls[0].key), "receiver=team-a-pagerduty/")
	assert.Contains(t, string(groupMgr.calls[1].key), "receiver=team-b-slack/")

	assert.Zero(t, publisher.publishToAllCalls+publisher.publishWithClassificationCalls)
}
