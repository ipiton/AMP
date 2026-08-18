package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

type fakePublishingCoordinator struct {
	results []*infrapublishing.PublishingResult
	err     error
	alert   *core.EnrichedAlert

	// task 2.4: PublishGroupToTargets call recording
	groupResults  []*infrapublishing.PublishingResult
	groupErr      error
	groupAlerts   []*core.Alert
	groupReceiver string
}

func (f *fakePublishingCoordinator) PublishToAll(_ context.Context, alert *core.EnrichedAlert) ([]*infrapublishing.PublishingResult, error) {
	f.alert = alert
	return f.results, f.err
}

func (f *fakePublishingCoordinator) PublishGroupToTargets(_ context.Context, alerts []*core.Alert, receiver string) ([]*infrapublishing.PublishingResult, error) {
	f.groupAlerts = alerts
	f.groupReceiver = receiver
	return f.groupResults, f.groupErr
}

type fakeBusinessDiscoveryManager struct {
	targets []*core.PublishingTarget
}

func (f *fakeBusinessDiscoveryManager) DiscoverTargets(context.Context) error { return nil }

func (f *fakeBusinessDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	for _, target := range f.targets {
		if target.Name == name {
			return target, nil
		}
	}
	return nil, errors.New("target not found")
}

func (f *fakeBusinessDiscoveryManager) ListTargets() []*core.PublishingTarget {
	return f.targets
}

func (f *fakeBusinessDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	filtered := make([]*core.PublishingTarget, 0, len(f.targets))
	for _, target := range f.targets {
		if target.Type == targetType {
			filtered = append(filtered, target)
		}
	}
	return filtered
}

func (f *fakeBusinessDiscoveryManager) GetStats() businesspublishing.DiscoveryStats {
	return businesspublishing.DiscoveryStats{ValidTargets: len(f.targets)}
}

func (f *fakeBusinessDiscoveryManager) Health(context.Context) error { return nil }

func TestApplicationPublishingAdapter_BuildsEnrichedAlert(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{
				Target:  &core.PublishingTarget{Name: "ops"},
				Success: true,
			},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alert := &core.Alert{Fingerprint: "abc123", AlertName: "HighCPU"}
	classification := &core.ClassificationResult{Severity: core.SeverityWarning}

	if err := adapter.PublishWithClassification(context.Background(), alert, classification); err != nil {
		t.Fatalf("PublishWithClassification() error = %v", err)
	}

	if coordinator.alert == nil {
		t.Fatalf("coordinator did not receive enriched alert")
	}
	if coordinator.alert.Alert != alert {
		t.Fatalf("expected original alert to be passed through")
	}
	if coordinator.alert.Classification != classification {
		t.Fatalf("expected classification to be passed through")
	}
	if coordinator.alert.ProcessingTimestamp == nil {
		t.Fatalf("expected processing timestamp to be set")
	}
	if coordinator.alert.ProcessingTimestamp.After(time.Now().UTC().Add(1 * time.Second)) {
		t.Fatalf("unexpected future processing timestamp: %v", coordinator.alert.ProcessingTimestamp)
	}
}

func TestApplicationPublishingAdapter_ReturnsErrorWhenAllTargetsFail(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		results: []*infrapublishing.PublishingResult{
			{
				Target:  &core.PublishingTarget{Name: "ops"},
				Success: false,
				Error:   errors.New("queue full"),
			},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishToAll(context.Background(), &core.Alert{Fingerprint: "abc", AlertName: "Test"}); err == nil {
		t.Fatalf("expected publish error when all targets fail")
	}
}

func TestMetricsOnlyPublisher_Noops(t *testing.T) {
	publisher := NewMetricsOnlyPublisher("test_reason", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := publisher.PublishToAll(context.Background(), &core.Alert{Fingerprint: "abc", AlertName: "Test"}); err != nil {
		t.Fatalf("PublishToAll() error = %v", err)
	}
}

func TestDiscoveryAdapter_DelegatesToBusinessDiscovery(t *testing.T) {
	manager := &fakeBusinessDiscoveryManager{
		targets: []*core.PublishingTarget{
			{Name: "ops", Type: "webhook"},
			{Name: "paging", Type: "pagerduty"},
		},
	}

	adapter, err := NewDiscoveryAdapter(manager)
	if err != nil {
		t.Fatalf("NewDiscoveryAdapter() error = %v", err)
	}

	if got := adapter.GetTargetCount(); got != 2 {
		t.Fatalf("GetTargetCount() = %d, want 2", got)
	}

	target, err := adapter.GetTarget("paging")
	if err != nil {
		t.Fatalf("GetTarget() error = %v", err)
	}
	if target.Name != "paging" {
		t.Fatalf("GetTarget() returned %q, want paging", target.Name)
	}
}

func TestInitializeBusinessServices_LiteProfileUsesMetricsOnlyPublisher(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{
			Profile: appconfig.ProfileLite,
			Publishing: appconfig.PublishingConfig{
				Enabled: true,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := registry.initializeBusinessServices(context.Background()); err != nil {
		t.Fatalf("initializeBusinessServices() error = %v", err)
	}

	if _, ok := registry.publisher.(*MetricsOnlyPublisher); !ok {
		t.Fatalf("expected MetricsOnlyPublisher, got %T", registry.publisher)
	}
}

// --- task 2.4: PublishGroup (notify-stage chain, batch publish) ----------

func TestApplicationPublishingAdapter_PublishGroup_PropagatesAlertsAndReceiver(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops"}, Success: true},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{
		{Fingerprint: "a1", AlertName: "HighCPU"},
		{Fingerprint: "a2", AlertName: "HighCPU"},
	}

	if err := adapter.PublishGroup(context.Background(), alerts, "critical-pagerduty"); err != nil {
		t.Fatalf("PublishGroup() error = %v", err)
	}

	if len(coordinator.groupAlerts) != 2 {
		t.Fatalf("expected coordinator to receive 2 alerts, got %d", len(coordinator.groupAlerts))
	}
	if coordinator.groupReceiver != "critical-pagerduty" {
		t.Fatalf("expected receiver %q, got %q", "critical-pagerduty", coordinator.groupReceiver)
	}
}

func TestApplicationPublishingAdapter_PublishGroup_EmptyAlertsIsNoop(t *testing.T) {
	coordinator := &fakePublishingCoordinator{}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	if err := adapter.PublishGroup(context.Background(), nil, "any"); err != nil {
		t.Fatalf("PublishGroup() with no alerts should be a no-op, got error = %v", err)
	}
	if coordinator.groupAlerts != nil {
		t.Fatalf("coordinator should not have been called for an empty alert set")
	}
}

func TestApplicationPublishingAdapter_PublishGroup_NoTargetsForReceiverReturnsError(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{},
		groupErr:     errors.New(`no targets found for receiver "unknown-receiver"`),
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	err = adapter.PublishGroup(context.Background(), alerts, "unknown-receiver")
	if err == nil {
		t.Fatal("expected an error when no targets match the receiver")
	}
}

// --- task 2.4 fix round 1, Findings 1+2: PublishGroup must not report
// success unless every resolved target confirmed the enqueue, or the
// caller's Dedup step records a send that never happened. -------------

// TestApplicationPublishingAdapter_PublishGroup_EmptyResultsReturnsError
// covers Finding 1: PublishingCoordinator.PublishGroupToTargets's
// metrics-only-mode fallback returns (empty results, nil error) — this
// must NOT read as success here, or the notify-chain's Dedup log records
// "sent" for a notification that was actually skipped, and the group goes
// silent for a full repeat_interval once metrics-only mode ends.
func TestApplicationPublishingAdapter_PublishGroup_EmptyResultsReturnsError(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{}, // e.g. metrics-only mode
		groupErr:     nil,
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	if err := adapter.PublishGroup(context.Background(), alerts, "any-receiver"); err == nil {
		t.Fatal("expected an error for empty results with a nil coordinator error (no confirmed delivery)")
	}
}

// TestApplicationPublishingAdapter_PublishGroup_PartialFailureReturnsError
// covers Finding 2: previously, as long as at least one of N targets
// succeeded, PublishGroup returned nil — so Dedup recorded "sent" and the
// failed target(s) would not be retried until repeat_interval elapsed
// (every other publish path in this codebase retries on the very next
// tick). Now a partial failure must return a non-nil error too.
func TestApplicationPublishingAdapter_PublishGroup_PartialFailureReturnsError(t *testing.T) {
	coordinator := &fakePublishingCoordinator{
		groupResults: []*infrapublishing.PublishingResult{
			{Target: &core.PublishingTarget{Name: "ops-a"}, Success: true},
			{Target: &core.PublishingTarget{Name: "ops-b"}, Success: false, Error: errors.New("ops-b unreachable")},
		},
	}

	adapter, err := NewApplicationPublishingAdapter(coordinator, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApplicationPublishingAdapter() error = %v", err)
	}

	alerts := []*core.Alert{{Fingerprint: "a1", AlertName: "HighCPU"}}
	if err := adapter.PublishGroup(context.Background(), alerts, "multi-target-receiver"); err == nil {
		t.Fatal("expected an error when 1 of 2 targets fails, so the caller does not record this as fully delivered")
	}
}
