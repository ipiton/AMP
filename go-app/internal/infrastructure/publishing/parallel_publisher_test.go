package publishing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Mock implementations for testing

// mockPublisherFactory is a mock publisher factory for testing
type mockPublisherFactory struct {
	createErr     error
	publishErr    error
	publishDelay  time.Duration
	callCount     int
}

func (m *mockPublisherFactory) CreatePublisherForTarget(target *core.PublishingTarget) (AlertPublisher, error) {
	m.callCount++
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &mockAlertPublisher{
		publishErr:   m.publishErr,
		publishDelay: m.publishDelay,
	}, nil
}

// mockAlertPublisher is a mock alert publisher for testing
type mockAlertPublisher struct {
	publishErr   error
	publishDelay time.Duration
}

func (m *mockAlertPublisher) Publish(ctx context.Context, alert *core.EnrichedAlert, target *core.PublishingTarget) error {
	if m.publishDelay > 0 {
		select {
		case <-time.After(m.publishDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.publishErr
}

func (m *mockAlertPublisher) Name() string {
	return "MockPublisher"
}

// mockTargetDiscoveryManager is a mock target discovery manager for testing
type mockTargetDiscoveryManager struct {
	targets []*core.PublishingTarget
}

func (m *mockTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	return nil
}

func (m *mockTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	for _, t := range m.targets {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, errors.New("target not found")
}

func (m *mockTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	return m.targets
}

func (m *mockTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	var result []*core.PublishingTarget
	for _, t := range m.targets {
		if t.Type == targetType {
			result = append(result, t)
		}
	}
	return result
}

func (m *mockTargetDiscoveryManager) GetHealthyTargets() []*core.PublishingTarget {
	return m.targets
}

func (m *mockTargetDiscoveryManager) RefreshTargets(ctx context.Context) error {
	return nil
}

func (m *mockTargetDiscoveryManager) GetTargetCount() int {
	return len(m.targets)
}

// mockHealthMonitor is a mock health monitor for testing
type mockHealthMonitor struct {
	healthStatus map[string]TargetHealth
}

func (m *mockHealthMonitor) GetHealthByName(ctx context.Context, targetName string) (TargetHealth, error) {
	if health, ok := m.healthStatus[targetName]; ok {
		return health, nil
	}
	return &mockTargetHealth{healthy: true}, nil // Default: healthy
}

// mockTargetHealth implements TargetHealth interface
type mockTargetHealth struct {
	healthy  bool
	degraded bool
	unknown  bool
}

func (m *mockTargetHealth) IsHealthy() bool {
	return m.healthy
}

func (m *mockTargetHealth) IsUnhealthy() bool {
	return !m.healthy && !m.degraded && !m.unknown
}

func (m *mockTargetHealth) IsDegraded() bool {
	return m.degraded
}

func (m *mockTargetHealth) IsUnknown() bool {
	return m.unknown
}

// Helper functions

func createTestAlert() *core.EnrichedAlert {
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test-fingerprint-123",
			Labels: map[string]string{
				"alertname": "TestAlert",
				"severity":  "critical",
			},
			Annotations: map[string]string{
				"summary": "Test alert for parallel publishing",
			},
			StartsAt: time.Now(),
			Status:   "firing",
		},
	}
}

func createTestTargets(count int) []*core.PublishingTarget {
	targets := make([]*core.PublishingTarget, count)
	for i := 0; i < count; i++ {
		targets[i] = &core.PublishingTarget{
			Name:    fmt.Sprintf("target-%d", i+1),
			Type:    "webhook",
			URL:     fmt.Sprintf("https://example.com/webhook-%d", i+1),
			Enabled: true,
		}
	}
	return targets
}

// Test: PublishToMultiple - skipped (requires full integration)
func TestPublishToMultiple_Integration(t *testing.T) {
	t.Skip("Integration test - requires full publisher factory setup")
}

// Test: ParallelPublishResult helper methods
func TestParallelPublishResult_Helpers(t *testing.T) {
	tests := []struct {
		name          string
		result        *ParallelPublishResult
		wantSuccess   bool
		wantAllOK     bool
		wantAllFailed bool
		wantRate      float64
	}{
		{
			name: "all succeeded",
			result: &ParallelPublishResult{
				TotalTargets: 3,
				SuccessCount: 3,
				FailureCount: 0,
				SkippedCount: 0,
			},
			wantSuccess:   true,
			wantAllOK:     true,
			wantAllFailed: false,
			wantRate:      100.0,
		},
		{
			name: "partial success",
			result: &ParallelPublishResult{
				TotalTargets:     3,
				SuccessCount:     2,
				FailureCount:     1,
				SkippedCount:     0,
				IsPartialSuccess: true,
			},
			wantSuccess:   true,
			wantAllOK:     false,
			wantAllFailed: false,
			wantRate:      66.67,
		},
		{
			name: "all failed",
			result: &ParallelPublishResult{
				TotalTargets: 3,
				SuccessCount: 0,
				FailureCount: 3,
				SkippedCount: 0,
			},
			wantSuccess:   false,
			wantAllOK:     false,
			wantAllFailed: true,
			wantRate:      0.0,
		},
		{
			name: "all skipped",
			result: &ParallelPublishResult{
				TotalTargets: 3,
				SuccessCount: 0,
				FailureCount: 0,
				SkippedCount: 3,
			},
			wantSuccess:   false,
			wantAllOK:     false,
			wantAllFailed: true,
			wantRate:      0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.Success(); got != tt.wantSuccess {
				t.Errorf("Success() = %v, want %v", got, tt.wantSuccess)
			}
			if got := tt.result.AllSucceeded(); got != tt.wantAllOK {
				t.Errorf("AllSucceeded() = %v, want %v", got, tt.wantAllOK)
			}
			if got := tt.result.AllFailed(); got != tt.wantAllFailed {
				t.Errorf("AllFailed() = %v, want %v", got, tt.wantAllFailed)
			}
			// Check success rate with tolerance for floating point precision
			gotRate := tt.result.SuccessRate()
			diff := gotRate - tt.wantRate
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.01 { // Tolerance: 0.01%
				t.Errorf("SuccessRate() = %.2f, want %.2f (diff: %.2f)", gotRate, tt.wantRate, diff)
			}
		})
	}
}

// Test: ParallelPublishOptions validation
func TestParallelPublishOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		options ParallelPublishOptions
		wantErr bool
	}{
		{
			name:    "valid default options",
			options: DefaultParallelPublishOptions(),
			wantErr: false,
		},
		{
			name: "invalid timeout",
			options: ParallelPublishOptions{
				Timeout:       0,
				MaxConcurrent: 10,
				HealthStrategy: SkipUnhealthy,
			},
			wantErr: true,
		},
		{
			name: "invalid max concurrent",
			options: ParallelPublishOptions{
				Timeout:       30 * time.Second,
				MaxConcurrent: 0,
				HealthStrategy: SkipUnhealthy,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.options.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test: HealthCheckStrategy String()
func TestHealthCheckStrategy_String(t *testing.T) {
	tests := []struct {
		strategy HealthCheckStrategy
		want     string
	}{
		{SkipUnhealthy, "skip_unhealthy"},
		{PublishToAll, "publish_to_all"},
		{SkipUnhealthyAndDegraded, "skip_unhealthy_and_degraded"},
		{HealthCheckStrategy(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.strategy.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}
