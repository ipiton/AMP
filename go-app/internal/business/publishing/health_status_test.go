package publishing

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

// Shared test metrics to avoid duplicate registration
var (
	statusTestMetrics     *HealthMetrics
	statusTestMetricsOnce sync.Once
)

func getStatusTestMetrics() *HealthMetrics {
	statusTestMetricsOnce.Do(func() {
		var err error
		statusTestMetrics, err = NewHealthMetrics()
		if err != nil {
			panic(err)
		}
	})
	return statusTestMetrics
}

// TestProcessHealthCheckResult_Success tests processing successful health check.
func TestProcessHealthCheckResult_Success(t *testing.T) {
	cache := newHealthStatusCache()
	metrics := getStatusTestMetrics()
	logger := slog.Default()
	config := DefaultHealthConfig()

	result := HealthCheckResult{
		TargetName:   "test-target",
		TargetURL:    "https://example.com",
		Success:      true,
		LatencyMs:    ptr(int64(100)),
		StatusCode:   ptr(200),
		ErrorMessage: nil,
		CheckedAt:    time.Now(),
		CheckType:    CheckTypePeriodic,
	}

	processHealthCheckResult(cache, metrics, logger, config, result)

	// Verify status updated
	status, ok := cache.Get("test-target")
	if !ok {
		t.Fatal("Status not found in cache")
	}

	if status.Status != HealthStatusHealthy {
		t.Errorf("Expected status 'healthy', got '%s'", status.Status)
	}
	if status.TotalChecks != 1 {
		t.Errorf("Expected 1 total check, got %d", status.TotalChecks)
	}
	if status.TotalSuccesses != 1 {
		t.Errorf("Expected 1 success, got %d", status.TotalSuccesses)
	}
	if status.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", status.ConsecutiveFailures)
	}
}

// TestProcessHealthCheckResult_Failure tests processing failed health check.
func TestProcessHealthCheckResult_Failure(t *testing.T) {
	cache := newHealthStatusCache()
	metrics := getStatusTestMetrics()
	logger := slog.Default()
	config := DefaultHealthConfig()

	result := HealthCheckResult{
		TargetName:   "failing-target",
		TargetURL:    "https://example.com",
		Success:      false,
		LatencyMs:    nil,
		StatusCode:   ptr(500),
		ErrorMessage: ptr("HTTP 500: Internal Server Error"),
		ErrorType:    ptr(ErrorTypeHTTP),
		CheckedAt:    time.Now(),
		CheckType:    CheckTypePeriodic,
	}

	processHealthCheckResult(cache, metrics, logger, config, result)

	// Verify status updated
	status, ok := cache.Get("failing-target")
	if !ok {
		t.Fatal("Status not found in cache")
	}

	if status.TotalChecks != 1 {
		t.Errorf("Expected 1 total check, got %d", status.TotalChecks)
	}
	if status.TotalFailures != 1 {
		t.Errorf("Expected 1 failure, got %d", status.TotalFailures)
	}
	if status.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 consecutive failure, got %d", status.ConsecutiveFailures)
	}
	if status.ErrorMessage == nil || *status.ErrorMessage != "HTTP 500: Internal Server Error" {
		t.Error("Error message not set correctly")
	}
}

// TestProcessHealthCheckResult_FailureThreshold tests unhealthy threshold.
func TestProcessHealthCheckResult_FailureThreshold(t *testing.T) {
	cache := newHealthStatusCache()
	metrics := getStatusTestMetrics()
	logger := slog.Default()
	config := DefaultHealthConfig()
	config.FailureThreshold = 3

	// Process 3 consecutive failures
	for i := 0; i < 3; i++ {
		result := HealthCheckResult{
			TargetName:   "unhealthy-target",
			TargetURL:    "https://example.com",
			Success:      false,
			LatencyMs:    nil,
			StatusCode:   ptr(503),
			ErrorMessage: ptr("Service Unavailable"),
			ErrorType:    ptr(ErrorTypeHTTP),
			CheckedAt:    time.Now(),
			CheckType:    CheckTypePeriodic,
		}

		processHealthCheckResult(cache, metrics, logger, config, result)
	}

	// Verify unhealthy status
	status, ok := cache.Get("unhealthy-target")
	if !ok {
		t.Fatal("Status not found in cache")
	}

	if status.Status != HealthStatusUnhealthy {
		t.Errorf("Expected status 'unhealthy', got '%s'", status.Status)
	}
	if status.ConsecutiveFailures != 3 {
		t.Errorf("Expected 3 consecutive failures, got %d", status.ConsecutiveFailures)
	}
}

// TestProcessHealthCheckResult_Recovery tests recovery from unhealthy.
func TestProcessHealthCheckResult_Recovery(t *testing.T) {
	cache := newHealthStatusCache()
	metrics := getStatusTestMetrics()
	logger := slog.Default()
	config := DefaultHealthConfig()

	// First, make target unhealthy (3 failures)
	for i := 0; i < 3; i++ {
		result := HealthCheckResult{
			TargetName:   "recovery-target",
			Success:      false,
			StatusCode:   ptr(500),
			ErrorMessage: ptr("Error"),
			CheckedAt:    time.Now(),
		}
		processHealthCheckResult(cache, metrics, logger, config, result)
	}

	// Verify unhealthy
	status, _ := cache.Get("recovery-target")
	if status.Status != HealthStatusUnhealthy {
		t.Fatalf("Expected unhealthy status, got '%s'", status.Status)
	}

	// Now send successful check
	recovery := HealthCheckResult{
		TargetName: "recovery-target",
		Success:    true,
		LatencyMs:  ptr(int64(150)),
		StatusCode: ptr(200),
		CheckedAt:  time.Now(),
	}
	processHealthCheckResult(cache, metrics, logger, config, recovery)

	// Verify recovered
	status, _ = cache.Get("recovery-target")
	if status.Status != HealthStatusHealthy {
		t.Errorf("Expected status 'healthy', got '%s'", status.Status)
	}
	if status.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", status.ConsecutiveFailures)
	}
}

// TestProcessHealthCheckResult_Degraded tests degraded detection.
func TestProcessHealthCheckResult_Degraded(t *testing.T) {
	cache := newHealthStatusCache()
	metrics := getStatusTestMetrics()
	logger := slog.Default()
	config := DefaultHealthConfig()
	config.DegradedThreshold = 5 * time.Second

	// Process slow but successful check (6 seconds)
	result := HealthCheckResult{
		TargetName: "degraded-target",
		Success:    true,
		LatencyMs:  ptr(int64(6000)), // 6 seconds
		StatusCode: ptr(200),
		CheckedAt:  time.Now(),
	}

	processHealthCheckResult(cache, metrics, logger, config, result)

	// Verify degraded status
	status, ok := cache.Get("degraded-target")
	if !ok {
		t.Fatal("Status not found in cache")
	}

	if status.Status != HealthStatusDegraded {
		t.Errorf("Expected status 'degraded', got '%s'", status.Status)
	}
}

// TestProcessHealthCheckResult_SuccessRate tests success rate calculation.
func TestProcessHealthCheckResult_SuccessRate(t *testing.T) {
	cache := newHealthStatusCache()
	metrics := getStatusTestMetrics()
	logger := slog.Default()
	config := DefaultHealthConfig()

	// Process mix of successes and failures
	checks := []bool{true, true, false, true, false, true, true, true, false, true}

	for _, success := range checks {
		result := HealthCheckResult{
			TargetName: "rate-target",
			Success:    success,
			CheckedAt:  time.Now(),
		}

		if success {
			result.LatencyMs = ptr(int64(100))
			result.StatusCode = ptr(200)
		} else {
			result.StatusCode = ptr(500)
			result.ErrorMessage = ptr("Error")
		}

		processHealthCheckResult(cache, metrics, logger, config, result)
	}

	// Verify success rate
	// Expected: 7 successes / 10 total = 70%
	status, ok := cache.Get("rate-target")
	if !ok {
		t.Fatal("Status not found in cache")
	}

	expectedRate := 70.0
	if status.SuccessRate < expectedRate-0.1 || status.SuccessRate > expectedRate+0.1 {
		t.Errorf("Expected success rate %.1f%%, got %.1f%%", expectedRate, status.SuccessRate)
	}

	if status.TotalChecks != 10 {
		t.Errorf("Expected 10 total checks, got %d", status.TotalChecks)
	}
	if status.TotalSuccesses != 7 {
		t.Errorf("Expected 7 successes, got %d", status.TotalSuccesses)
	}
	if status.TotalFailures != 3 {
		t.Errorf("Expected 3 failures, got %d", status.TotalFailures)
	}
}

// TestInitializeHealthStatus tests initialization.
func TestInitializeHealthStatus(t *testing.T) {
	status := initializeHealthStatus("test-target", "rootly", true)

	if status.TargetName != "test-target" {
		t.Errorf("Expected target name 'test-target', got '%s'", status.TargetName)
	}
	if status.TargetType != "rootly" {
		t.Errorf("Expected target type 'rootly', got '%s'", status.TargetType)
	}
	if !status.Enabled {
		t.Error("Expected enabled to be true")
	}
	if status.Status != HealthStatusUnknown {
		t.Errorf("Expected status 'unknown', got '%s'", status.Status)
	}
	if status.TotalChecks != 0 {
		t.Errorf("Expected 0 total checks, got %d", status.TotalChecks)
	}
	if status.SuccessRate != 0 {
		t.Errorf("Expected 0%% success rate, got %.1f%%", status.SuccessRate)
	}
}

// TestCalculateAggregateStats tests aggregate statistics calculation.
func TestCalculateAggregateStats(t *testing.T) {
	statuses := []TargetHealthStatus{
		{
			TargetName:     "healthy1",
			Status:         HealthStatusHealthy,
			LastCheck:      time.Now().Add(-1 * time.Minute),
			TotalChecks:    100,
			TotalSuccesses: 100,
		},
		{
			TargetName:     "healthy2",
			Status:         HealthStatusHealthy,
			LastCheck:      time.Now(),
			TotalChecks:    50,
			TotalSuccesses: 50,
		},
		{
			TargetName:     "unhealthy1",
			Status:         HealthStatusUnhealthy,
			LastCheck:      time.Now().Add(-2 * time.Minute),
			TotalChecks:    20,
			TotalSuccesses: 10,
		},
		{
			TargetName:     "degraded1",
			Status:         HealthStatusDegraded,
			LastCheck:      time.Now().Add(-30 * time.Second),
			TotalChecks:    10,
			TotalSuccesses: 9,
		},
		{
			TargetName:  "unknown1",
			Status:      HealthStatusUnknown,
			LastCheck:   time.Time{},
			TotalChecks: 0,
		},
	}

	stats := calculateAggregateStats(statuses)

	// Verify counts
	if stats.TotalTargets != 5 {
		t.Errorf("Expected 5 total targets, got %d", stats.TotalTargets)
	}
	if stats.HealthyCount != 2 {
		t.Errorf("Expected 2 healthy targets, got %d", stats.HealthyCount)
	}
	if stats.UnhealthyCount != 1 {
		t.Errorf("Expected 1 unhealthy target, got %d", stats.UnhealthyCount)
	}
	if stats.DegradedCount != 1 {
		t.Errorf("Expected 1 degraded target, got %d", stats.DegradedCount)
	}
	if stats.UnknownCount != 1 {
		t.Errorf("Expected 1 unknown target, got %d", stats.UnknownCount)
	}

	// Verify success rate
	// Total: 100+50+10+9+0 = 169 successes / 180 total = 93.89%
	expectedRate := 93.89
	if stats.OverallSuccessRate < expectedRate-0.5 || stats.OverallSuccessRate > expectedRate+0.5 {
		t.Errorf("Expected success rate ~%.1f%%, got %.1f%%", expectedRate, stats.OverallSuccessRate)
	}

	// Verify most recent check
	if stats.LastCheckTime == nil {
		t.Error("Expected LastCheckTime to be set")
	}
}

// TestCalculateAggregateStats_Empty tests empty statuses.
func TestCalculateAggregateStats_Empty(t *testing.T) {
	stats := calculateAggregateStats([]TargetHealthStatus{})

	if stats.TotalTargets != 0 {
		t.Errorf("Expected 0 targets, got %d", stats.TotalTargets)
	}
	if stats.OverallSuccessRate != 0 {
		t.Errorf("Expected 0%% success rate, got %.1f%%", stats.OverallSuccessRate)
	}
	if stats.LastCheckTime != nil {
		t.Error("Expected LastCheckTime to be nil for empty stats")
	}
}
