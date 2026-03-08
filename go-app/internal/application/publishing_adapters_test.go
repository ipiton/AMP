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
}

func (f *fakePublishingCoordinator) PublishToAll(_ context.Context, alert *core.EnrichedAlert) ([]*infrapublishing.PublishingResult, error) {
	f.alert = alert
	return f.results, f.err
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
