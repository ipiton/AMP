package services

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

// SILENCED-ALERTS-INGEST: a silenced firing alert is ingested but not
// published; once the silence ends it is published exactly once, even when
// its re-sends are unchanged duplicates.

// toggleSilence is a GroupSilenceChecker whose single silence is switched on
// and off by the test.
type toggleSilence struct{ active bool }

func (s *toggleSilence) HasActiveMatch(map[string]string, time.Time) bool { return s.active }

// countingLLM counts classifications; a silenced alert must not reach it.
type countingLLM struct{ calls int }

func (l *countingLLM) ClassifyAlert(context.Context, *core.Alert) (*core.ClassificationResult, error) {
	l.calls++
	return &core.ClassificationResult{Severity: core.SeverityWarning, Confidence: 0.9}, nil
}

func (l *countingLLM) Health(context.Context) error { return nil }

// recordingInhibitionCache records the fingerprints added as inhibition sources.
type recordingInhibitionCache struct {
	mu    sync.Mutex
	added []string
}

func (c *recordingInhibitionCache) GetFiringAlerts(context.Context) ([]*core.Alert, error) {
	return nil, nil
}

func (c *recordingInhibitionCache) AddFiringAlert(_ context.Context, alert *core.Alert) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.added = append(c.added, alert.Fingerprint)
	return nil
}

func (c *recordingInhibitionCache) RemoveAlert(context.Context, string) error { return nil }

// newSilenceTestProcessor wires the real DeduplicationService (in-memory
// storage) and the real, stateful SimpleFilterEngine: its duplicate rule is
// what broke the first publish after a silence in deep-review round 2 (F6).
func newSilenceTestProcessor(t *testing.T, silence *toggleSilence, publisher *fakePublisher, mutate func(*AlertProcessorConfig)) *AlertProcessor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dedup, err := NewDeduplicationService(&DeduplicationConfig{Storage: newMockAlertStorage(), Logger: logger})
	require.NoError(t, err)

	cfg := AlertProcessorConfig{
		FilterEngine:   NewSimpleFilterEngineWithMetrics(logger, nil),
		Publisher:      publisher,
		Deduplication:  dedup,
		SilenceChecker: silence,
		Logger:         logger,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)
	return processor
}

// silenceTestAlert has no EndsAt, so dedup ignores every unchanged re-send.
func silenceTestAlert(status core.AlertStatus) *core.Alert {
	return &core.Alert{
		AlertName: "DiskFull",
		Status:    status,
		Labels:    map[string]string{"alertname": "DiskFull", "instance": "db-1"},
		StartsAt:  time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC),
	}
}

func publishCount(p *fakePublisher) int {
	return p.publishToAllCalls + p.publishWithClassificationCalls
}

func TestAlertProcessor_Silence_DirectMode_NotPublishedButInhibitionSource(t *testing.T) {
	silence := &toggleSilence{active: true}
	publisher := &fakePublisher{}
	cache := &recordingInhibitionCache{}
	processor := newSilenceTestProcessor(t, silence, publisher, func(cfg *AlertProcessorConfig) {
		cfg.InhibitionCache = cache
	})

	require.NoError(t, processor.ProcessAlert(context.Background(), silenceTestAlert(core.StatusFiring)))

	assert.Equal(t, 0, publishCount(publisher), "a silenced firing alert must not be published")
	assert.Len(t, cache.added, 1, "a silenced firing alert is still an inhibition source")
}

func TestAlertProcessor_Silence_UnchangedResendPublishedOnceAfterSilenceEnds(t *testing.T) {
	silence := &toggleSilence{active: true}
	publisher := &fakePublisher{}
	processor := newSilenceTestProcessor(t, silence, publisher, nil)
	ctx := context.Background()

	// Two re-sends under the silence, inside the filter's duplicate window.
	for i := 0; i < 2; i++ {
		require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusFiring)))
	}
	require.Equal(t, 0, publishCount(publisher))

	// Silence ends right away: the next re-send is an unchanged duplicate for
	// dedup and inside the filter's window, yet must be published once.
	silence.active = false
	for i := 0; i < 3; i++ {
		require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusFiring)))
	}
	assert.Equal(t, 1, publishCount(publisher), "published exactly once after the silence ends, no repeats")
}

func TestAlertProcessor_Silence_ResolvedPublished(t *testing.T) {
	silence := &toggleSilence{active: true}
	publisher := &fakePublisher{}
	processor := newSilenceTestProcessor(t, silence, publisher, nil)
	ctx := context.Background()

	require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusFiring)))
	require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusResolved)))

	assert.Equal(t, 1, publishCount(publisher), "resolved is not subject to the silence check, as before")
}

func TestAlertProcessor_Silence_NotSilencedUnchanged(t *testing.T) {
	silence := &toggleSilence{}
	publisher := &fakePublisher{}
	processor := newSilenceTestProcessor(t, silence, publisher, nil)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusFiring)))
	}
	assert.Equal(t, 1, publishCount(publisher), "unchanged re-sends of an unsilenced alert stay duplicates")
}

func newSilenceGroupingConfig(groupMgr *fakeGroupManager, llm *countingLLM) func(*AlertProcessorConfig) {
	return func(cfg *AlertProcessorConfig) {
		cfg.RouteEvaluator = &fakeRouteEvaluator{decision: &RoutingDecision{
			Receiver: "team-db",
			GroupBy:  []string{"alertname"},
		}}
		cfg.GroupingEnabled = true
		cfg.GroupManager = groupMgr
		cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()
		cfg.LLMClient = llm
	}
}

func TestAlertProcessor_Silence_Grouping_EntersGroupWithoutLLM(t *testing.T) {
	silence := &toggleSilence{active: true}
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{}
	llm := &countingLLM{}
	processor := newSilenceTestProcessor(t, silence, publisher, newSilenceGroupingConfig(groupMgr, llm))

	require.NoError(t, processor.ProcessAlert(context.Background(), silenceTestAlert(core.StatusFiring)))

	assert.Len(t, groupMgr.calls, 1, "the group's notify chain applies the silence at send time")
	assert.Equal(t, 0, publishCount(publisher))
	assert.Equal(t, 0, llm.calls, "no classification for a silenced alert")
}

func TestAlertProcessor_Silence_GroupingFailure_NoDirectPublish(t *testing.T) {
	silence := &toggleSilence{active: true}
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{err: assert.AnError}
	llm := &countingLLM{}
	processor := newSilenceTestProcessor(t, silence, publisher, newSilenceGroupingConfig(groupMgr, llm))
	ctx := context.Background()

	require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusFiring)))
	assert.Equal(t, 0, publishCount(publisher), "no fail-open direct publish for a silenced alert")

	// Silence ends and grouping recovers: the unchanged re-send still takes
	// the normal path into the group.
	silence.active = false
	groupMgr.err = nil
	require.NoError(t, processor.ProcessAlert(ctx, silenceTestAlert(core.StatusFiring)))

	assert.Len(t, groupMgr.calls, 2)
	assert.Equal(t, 0, publishCount(publisher))
}
