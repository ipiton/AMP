package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// These tests run ProcessAlert with the real SimpleFilterEngine instead of
// fakeFilterEngine (PROD-HARDCODED-FILTER): a filter rule that drops an alert
// returns nil from ProcessAlert, so only the publisher shows whether the alert
// reached a receiver.

// fakeLLMClient classifies every alert the same way, so the enriched path runs
// without a real provider.
type fakeLLMClient struct {
	result *core.ClassificationResult
}

func (f fakeLLMClient) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	return f.result, nil
}

func (f fakeLLMClient) Health(ctx context.Context) error { return nil }

func newFilteringProcessor(t *testing.T, publisher *fakePublisher, llm LLMClient) *AlertProcessor {
	t.Helper()
	cfg := newTestProcessorConfig(t, nil, publisher)
	cfg.FilterEngine = NewSimpleFilterEngineWithMetrics(nil, nil)
	cfg.LLMClient = llm
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)
	return processor
}

func testLookingAlert(status core.AlertStatus) *core.Alert {
	return &core.Alert{
		Fingerprint: "fp-test-runner",
		AlertName:   "TestRunnerDown",
		Status:      status,
		Labels: map[string]string{
			"alertname":   "TestRunnerDown",
			"environment": "testing",
			"namespace":   "tmp",
		},
		StartsAt: time.Now(),
	}
}

func publishCalls(p *fakePublisher) int {
	return p.publishToAllCalls + p.publishWithClassificationCalls
}

// processorModes covers both paths that call the filter: without an LLM client
// processEnriched falls back to processTransparent; with one, the filter sees
// the classification.
var processorModes = []struct {
	name string
	llm  LLMClient
}{
	{name: "transparent", llm: nil},
	{name: "enriched", llm: fakeLLMClient{result: &core.ClassificationResult{
		Severity:   core.SeverityWarning,
		Confidence: 0.9,
	}}},
}

func TestAlertProcessor_Filter_DeliversTestLookingAlerts(t *testing.T) {
	for _, mode := range processorModes {
		t.Run(mode.name, func(t *testing.T) {
			publisher := &fakePublisher{}
			processor := newFilteringProcessor(t, publisher, mode.llm)

			require.NoError(t, processor.ProcessAlert(context.Background(), testLookingAlert(core.StatusFiring)))

			assert.Equal(t, 1, publishCalls(publisher),
				"an alert named Test*, environment=testing, namespace=tmp must reach the publisher")
		})
	}
}

// Rule 7 stays the only notification dedup on the direct-publish path (no
// route: tree): the same status repeated within the window is not published
// twice.
func TestAlertProcessor_Filter_RepeatWithinWindowPublishedOnce(t *testing.T) {
	for _, mode := range processorModes {
		t.Run(mode.name, func(t *testing.T) {
			publisher := &fakePublisher{}
			processor := newFilteringProcessor(t, publisher, mode.llm)

			require.NoError(t, processor.ProcessAlert(context.Background(), testLookingAlert(core.StatusFiring)))
			require.NoError(t, processor.ProcessAlert(context.Background(), testLookingAlert(core.StatusFiring)))

			assert.Equal(t, 1, publishCalls(publisher))
		})
	}
}

func TestAlertProcessor_Filter_FlapPublishedOnEveryStatusChange(t *testing.T) {
	for _, mode := range processorModes {
		t.Run(mode.name, func(t *testing.T) {
			publisher := &fakePublisher{}
			processor := newFilteringProcessor(t, publisher, mode.llm)

			for _, status := range []core.AlertStatus{core.StatusFiring, core.StatusResolved, core.StatusFiring} {
				require.NoError(t, processor.ProcessAlert(context.Background(), testLookingAlert(status)))
			}

			assert.Equal(t, 3, publishCalls(publisher),
				"firing -> resolved -> firing within the window must publish all three")
		})
	}
}
