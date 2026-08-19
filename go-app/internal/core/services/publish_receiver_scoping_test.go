package services

import (
	"context"
	"errors"
	"testing"
	"time"

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
