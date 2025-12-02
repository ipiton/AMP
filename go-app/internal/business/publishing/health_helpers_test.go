package publishing

import (
	"log/slog"
	"testing"
	"time"
)

// TestHealthStatus_IsHealthy tests IsHealthy helper method.
func TestHealthStatus_IsHealthy(t *testing.T) {
	status := &TargetHealthStatus{
		TargetName: "test-target",
		Status:     HealthStatusHealthy,
		LastCheck:  time.Now(),
	}

	if !status.IsHealthy() {
		t.Error("Expected status to be healthy")
	}

	status.Status = HealthStatusUnhealthy
	if status.IsHealthy() {
		t.Error("Expected status NOT to be healthy")
	}
}

// TestHealthStatus_IsUnhealthy tests IsUnhealthy helper method.
func TestHealthStatus_IsUnhealthy(t *testing.T) {
	status := &TargetHealthStatus{
		TargetName: "test-target",
		Status:     HealthStatusUnhealthy,
		LastCheck:  time.Now(),
	}

	if !status.IsUnhealthy() {
		t.Error("Expected status to be unhealthy")
	}

	status.Status = HealthStatusHealthy
	if status.IsUnhealthy() {
		t.Error("Expected status NOT to be unhealthy")
	}
}

// TestHealthStatus_IsDegraded tests IsDegraded helper method.
func TestHealthStatus_IsDegraded(t *testing.T) {
	status := &TargetHealthStatus{
		TargetName: "test-target",
		Status:     HealthStatusDegraded,
		LastCheck:  time.Now(),
	}

	if !status.IsDegraded() {
		t.Error("Expected status to be degraded")
	}

	status.Status = HealthStatusHealthy
	if status.IsDegraded() {
		t.Error("Expected status NOT to be degraded")
	}
}

// TestHealthStatus_IsUnknown tests IsUnknown helper method.
func TestHealthStatus_IsUnknown(t *testing.T) {
	status := &TargetHealthStatus{
		TargetName: "test-target",
		Status:     HealthStatusUnknown,
		LastCheck:  time.Now(),
	}

	if !status.IsUnknown() {
		t.Error("Expected status to be unknown")
	}

	status.Status = HealthStatusHealthy
	if status.IsUnknown() {
		t.Error("Expected status NOT to be unknown")
	}
}

// TestHealthStatus_AllStatuses tests all status helper methods together.
func TestHealthStatus_AllStatuses(t *testing.T) {
	statuses := []HealthStatus{
		HealthStatusHealthy,
		HealthStatusUnhealthy,
		HealthStatusDegraded,
		HealthStatusUnknown,
	}

	for _, s := range statuses {
		status := &TargetHealthStatus{
			TargetName: "test-target",
			Status:     s,
			LastCheck:  time.Now(),
		}

		// Only one should be true
		count := 0
		if status.IsHealthy() {
			count++
		}
		if status.IsUnhealthy() {
			count++
		}
		if status.IsDegraded() {
			count++
		}
		if status.IsUnknown() {
			count++
		}

		if count != 1 {
			t.Errorf("Expected exactly 1 status to be true for %s, got %d", s, count)
		}
	}
}

// TestShouldSkipHealthCheck tests shouldSkipHealthCheck function.
func TestShouldSkipHealthCheck(t *testing.T) {
	tests := []struct {
		name          string
		targetName    string
		targetEnabled bool
		targetURL     string
		expectedSkip  bool
	}{
		{
			name:          "Enabled target with URL",
			targetName:    "test-target",
			targetEnabled: true,
			targetURL:     "https://example.com",
			expectedSkip:  false,
		},
		{
			name:          "Disabled target",
			targetName:    "disabled-target",
			targetEnabled: false,
			targetURL:     "https://example.com",
			expectedSkip:  true,
		},
		{
			name:          "Empty URL",
			targetName:    "empty-url",
			targetEnabled: true,
			targetURL:     "",
			expectedSkip:  true,
		},
		{
			name:          "Disabled with empty URL",
			targetName:    "disabled-empty",
			targetEnabled: false,
			targetURL:     "",
			expectedSkip:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skip := shouldSkipHealthCheck(tt.targetName, tt.targetURL, tt.targetEnabled, slog.Default())

			if skip != tt.expectedSkip {
				t.Errorf("Expected skip=%v, got %v", tt.expectedSkip, skip)
			}
		})
	}
}
