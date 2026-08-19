package publishing

import (
	"log/slog"
	"testing"
)

// TestNewPublishingQueue_NilMetricsFallbackDoesNotPanicOnSecondCall is the
// regression test for item 1 (FU-WAVE3-RELIABILITY): NewPublishingQueue's
// nil-Metrics fallback used to call v2.NewRegistry(), which defaults to
// prometheus.DefaultRegisterer and registers every PublishingMetrics
// collector from scratch on every call. A second PublishingQueue built the
// same way in the same process — multiple instances, or simply two tests in
// one binary both omitting Metrics — hit prometheus's duplicate-collector
// panic ("duplicate metrics collector registration attempted").
//
// The fallback now reuses v2.Global() (a sync.Once-guarded singleton), so
// constructing the queue any number of times in one process must not panic.
func TestNewPublishingQueue_NilMetricsFallbackDoesNotPanicOnSecondCall(t *testing.T) {
	build := func() *PublishingQueue {
		return NewPublishingQueue(
			NewPublisherFactory(NewAlertFormatter(""), slog.Default(), nil, ""),
			nil,
			NewLRUJobTrackingStore(16),
			PublishingQueueConfig{
				WorkerCount:             0,
				HighPriorityQueueSize:   1,
				MediumPriorityQueueSize: 1,
				LowPriorityQueueSize:    1,
				// Metrics intentionally left nil to exercise the fallback.
			},
			nil,
			slog.Default(),
		)
	}

	assertNoPanic := func(n int) *PublishingQueue {
		var q *PublishingQueue
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("NewPublishingQueue panicked on call #%d: %v", n, r)
				}
			}()
			q = build()
		}()
		return q
	}

	first := assertNoPanic(1)
	second := assertNoPanic(2)
	third := assertNoPanic(3)

	if first == nil || second == nil || third == nil {
		t.Fatal("expected all three queues to be constructed")
	}
	if first.metrics == nil || second.metrics == nil || third.metrics == nil {
		t.Fatal("expected fallback metrics to be populated")
	}
	// All three share the same process-wide singleton.
	if first.metrics != second.metrics || second.metrics != third.metrics {
		t.Error("expected nil-Metrics fallback to reuse the same singleton across instances")
	}
}
