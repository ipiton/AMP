// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
	"log/slog"
)

func TestEventPublisher_PublishAlertEvent(t *testing.T) {
	// Use nil metrics to avoid Prometheus registration issues in tests
	eventBus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := eventBus.Start(ctx)
	require.NoError(t, err)
	defer eventBus.Stop(context.Background())

	publisher := NewEventPublisher(eventBus, slog.Default(), nil)

	alert := &core.Alert{
		Fingerprint: "test-fingerprint",
		AlertName:   "TestAlert",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"env": "test", "severity": "critical"},
		StartsAt:    time.Now(),
	}

	err = publisher.PublishAlertEvent(EventTypeAlertCreated, alert)
	assert.NoError(t, err)
}

func TestEventPublisher_PublishStatsEvent(t *testing.T) {
	// Use nil metrics to avoid Prometheus registration issues in tests
	eventBus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := eventBus.Start(ctx)
	require.NoError(t, err)
	defer eventBus.Stop(context.Background())

	publisher := NewEventPublisher(eventBus, slog.Default(), nil)

	stats := &DashboardStats{
		FiringAlerts:    10,
		ResolvedAlerts:  5,
		ActiveSilences:  3,
		InhibitedAlerts: 2,
	}

	err = publisher.PublishStatsEvent(stats)
	assert.NoError(t, err)
}

func TestEventPublisher_PublishHealthEvent(t *testing.T) {
	// Use nil metrics to avoid Prometheus registration issues in tests
	eventBus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := eventBus.Start(ctx)
	require.NoError(t, err)
	defer eventBus.Stop(context.Background())

	publisher := NewEventPublisher(eventBus, slog.Default(), nil)

	err = publisher.PublishHealthEvent("PostgreSQL", "healthy", 10.5, "All checks passed")
	assert.NoError(t, err)
}

func TestEventPublisher_PublishSystemNotification(t *testing.T) {
	// Use nil metrics to avoid Prometheus registration issues in tests
	eventBus := NewEventBus(slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := eventBus.Start(ctx)
	require.NoError(t, err)
	defer eventBus.Stop(context.Background())

	publisher := NewEventPublisher(eventBus, slog.Default(), nil)

	err = publisher.PublishSystemNotification("info", "System maintenance scheduled")
	assert.NoError(t, err)
}

func TestEventPublisher_NilEventBus(t *testing.T) {
	// Publisher should handle nil EventBus gracefully
	publisher := NewEventPublisher(nil, slog.Default(), nil)

	alert := &core.Alert{
		Fingerprint: "test-fingerprint",
		AlertName:   "TestAlert",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"env": "test", "severity": "critical"},
		StartsAt:    time.Now(),
	}

	// Should not panic
	err := publisher.PublishAlertEvent(EventTypeAlertCreated, alert)
	assert.NoError(t, err) // Returns nil when EventBus is nil
}
