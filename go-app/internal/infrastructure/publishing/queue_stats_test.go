package publishing

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

func TestPublishingQueue_GetStatsTracksCumulativeCounters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), nil, ""),
		nil,
		NewLRUJobTrackingStore(16),
		PublishingQueueConfig{
			WorkerCount:             1,
			HighPriorityQueueSize:   4,
			MediumPriorityQueueSize: 4,
			LowPriorityQueueSize:    4,
			MaxRetries:              0,
			RetryInterval:           time.Millisecond,
		},
		nil,
		slog.Default(),
	)

	alert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "queue-stats-fingerprint",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"severity": "warning",
			},
			StartsAt: time.Now().UTC(),
		},
	}

	successTarget := &core.PublishingTarget{
		Name:    "webhook-success",
		Type:    "webhook",
		URL:     server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	}
	failureTarget := &core.PublishingTarget{
		Name:    "webhook-failure",
		Type:    "webhook",
		URL:     "http://127.0.0.1:1",
		Enabled: true,
		Format:  core.FormatWebhook,
	}

	if err := queue.Submit(alert, successTarget); err != nil {
		t.Fatalf("Submit(success) error = %v", err)
	}
	if err := queue.Submit(alert, failureTarget); err != nil {
		t.Fatalf("Submit(failure) error = %v", err)
	}

	queue.processJob(<-queue.mediumPriorityJobs)
	queue.processJob(<-queue.mediumPriorityJobs)

	stats := queue.GetStats()
	if stats.TotalSubmitted != 2 {
		t.Fatalf("TotalSubmitted = %d, want 2", stats.TotalSubmitted)
	}
	if stats.TotalCompleted != 1 {
		t.Fatalf("TotalCompleted = %d, want 1", stats.TotalCompleted)
	}
	if stats.TotalFailed != 1 {
		t.Fatalf("TotalFailed = %d, want 1", stats.TotalFailed)
	}
}
