package services

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
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
