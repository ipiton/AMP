package publishing

import (
	"testing"
	"time"
)

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
				Timeout:        0,
				MaxConcurrent:  10,
				HealthStrategy: SkipUnhealthy,
			},
			wantErr: true,
		},
		{
			name: "invalid max concurrent",
			options: ParallelPublishOptions{
				Timeout:        30 * time.Second,
				MaxConcurrent:  0,
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
