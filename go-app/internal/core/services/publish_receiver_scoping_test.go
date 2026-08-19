package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

// Slice-1 review finding C1: the NON-GROUPED publish path used to call
// PublishToAll, which applied no receiver filtering at all. With
// config-provisioned targets (one per `receivers:` integration) that means
// every alert reaches every receiver's integrations. These tests pin the
// contract at its source: whatever route the alert matched, its receiver is
// what the publisher is told.

func scopingDecision(receiver string) *RoutingDecision {
	return &RoutingDecision{
		Receiver:       receiver,
		GroupBy:        []string{"alertname"},
		GroupWait:      time.Second,
		GroupInterval:  time.Minute,
		RepeatInterval: time.Hour,
	}
}

// Path 1: grouping.enabled is false — the DEFAULT — so every alert takes the
// direct publish path.
func TestProcessAlert_GroupingDisabled_PublishesToRoutedReceiverOnly(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{decision: scopingDecision("team-x")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.GroupingEnabled = false
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))

	require.Equal(t, 1, publisher.publishToAllCalls)
	assert.Equal(t, []string{"team-x"}, publisher.gotReceivers,
		"the direct publish path must carry the routed receiver, not fan out to every target")
}

// Path 2: grouping is enabled but AddAlertToGroup fails, which falls open to a
// direct publish by design. That fallback must stay receiver-scoped too.
func TestProcessAlert_GroupingFailOpen_PublishesToRoutedReceiverOnly(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{decision: scopingDecision("team-y")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.GroupingEnabled = true
	cfg.GroupManager = &fakeGroupManager{err: errors.New("storage down")}
	cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))

	require.Equal(t, 1, publisher.publishToAllCalls, "grouping failure must fall open to a direct publish")
	assert.Equal(t, []string{"team-y"}, publisher.gotReceivers)
}

// Enriched mode takes the classification-carrying method; it must be scoped
// identically.
func TestProcessAlert_EnrichedDirectPublish_CarriesReceiver(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{decision: scopingDecision("team-z")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	// EnrichmentModeManager's zero value reports "enriched", so wiring an LLM
	// client is all it takes to reach processEnriched's publish call.
	cfg.LLMClient = fakeClassifyingLLM{}
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))

	require.Equal(t, 1, publisher.publishWithClassificationCalls)
	assert.Equal(t, []string{"team-z"}, publisher.gotReceivers)
}

// No route tree (legacy/lite single-receiver mode): receiver is "", which the
// publisher reads as "every enabled target" — the pre-routing behaviour, kept
// deliberately.
func TestProcessAlert_NoRouteTree_PublishesUnscoped(t *testing.T) {
	publisher := &fakePublisher{}
	processor, err := NewAlertProcessor(newTestProcessorConfig(t, nil, publisher))
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))

	assert.Equal(t, []string{""}, publisher.gotReceivers,
		"without a route tree the direct path must stay unscoped (legacy behaviour)")
}

// fakeClassifyingLLM returns a fixed classification so processEnriched reaches
// its publish call.
type fakeClassifyingLLM struct{}

func (fakeClassifyingLLM) ClassifyAlert(context.Context, *core.Alert) (*core.ClassificationResult, error) {
	return &core.ClassificationResult{
		Severity:   core.AlertSeverity("critical"),
		Confidence: 0.9,
		Reasoning:  "test",
	}, nil
}

func (fakeClassifyingLLM) Health(context.Context) error { return nil }

// ============================================================================
// Re-review finding R1: a CONFIGURED route tree that produces no decision must
// never publish unscoped (which targetMatchesReceiver reads as "every target").
// ============================================================================

// Path: the route tree failed to BUILD, so no evaluator is wired at all —
// initializeRouting's error is non-fatal, so the process runs on with config
// targets provisioned. The alert must go to the root receiver, not everywhere.
func TestProcessAlert_RouteTreeConfiguredButNotBuilt_UsesRootReceiver(t *testing.T) {
	publisher := &fakePublisher{}

	cfg := newTestProcessorConfig(t, nil, publisher) // nil evaluator = tree never built
	cfg.RouteTreeConfigured = true
	cfg.DefaultReceiver = "root-default"
	cfg.RoutingFallbackMetrics = NewRoutingFallbackMetricsWithRegisterer(prometheus.NewRegistry())
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))

	assert.Equal(t, []string{"root-default"}, publisher.gotReceivers,
		"a configured-but-unavailable route tree must fall back to the root receiver, never to unscoped fan-out")
}

// Path: the tree is wired but Evaluate errors for this alert.
func TestProcessAlert_EvaluateError_UsesRootReceiver(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{err: errors.New("matcher blew up")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.RouteTreeConfigured = true
	cfg.DefaultReceiver = "root-default"
	cfg.RoutingFallbackMetrics = NewRoutingFallbackMetricsWithRegisterer(prometheus.NewRegistry())
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))

	require.Equal(t, 1, evaluator.calls)
	assert.Equal(t, []string{"root-default"}, publisher.gotReceivers)
}

// Path: no decision AND no root receiver resolvable → fail the alert loudly
// rather than fan it out. Config validation rejects a root route without a
// receiver, so this is the hand-built/degraded case.
func TestProcessAlert_RouteTreeUnavailableWithoutRootReceiver_FailsLoudly(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{err: errors.New("boom")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.RouteTreeConfigured = true
	cfg.DefaultReceiver = ""
	cfg.RoutingFallbackMetrics = NewRoutingFallbackMetricsWithRegisterer(prometheus.NewRegistry())
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRoutingUnavailable)
	assert.Empty(t, publisher.gotReceivers, "nothing may be published on this path")
}

// Enriched mode takes the other publish method; same rule applies.
func TestProcessAlert_EnrichedRouteTreeUnavailable_FailsLoudly(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{err: errors.New("boom")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.LLMClient = fakeClassifyingLLM{}
	cfg.RouteTreeConfigured = true
	cfg.RoutingFallbackMetrics = NewRoutingFallbackMetricsWithRegisterer(prometheus.NewRegistry())
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	err = processor.ProcessAlert(context.Background(), testAlert())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRoutingUnavailable)
	assert.Zero(t, publisher.publishWithClassificationCalls)
}

// The legacy path must be untouched: no `route:` section at all keeps the
// unscoped behaviour, which is what a pre-routing deployment relies on.
func TestProcessAlert_NoRouteTreeConfigured_StaysUnscoped(t *testing.T) {
	publisher := &fakePublisher{}

	cfg := newTestProcessorConfig(t, nil, publisher)
	cfg.RouteTreeConfigured = false
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))
	assert.Equal(t, []string{""}, publisher.gotReceivers)
}

// A successful decision still wins over both fallbacks.
func TestProcessAlert_DecisionWinsOverRootReceiver(t *testing.T) {
	publisher := &fakePublisher{}
	evaluator := &fakeRouteEvaluator{decision: scopingDecision("team-x")}

	cfg := newTestProcessorConfig(t, evaluator, publisher)
	cfg.RouteTreeConfigured = true
	cfg.DefaultReceiver = "root-default"
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	require.NoError(t, processor.ProcessAlert(context.Background(), testAlert()))
	assert.Equal(t, []string{"team-x"}, publisher.gotReceivers)
}
