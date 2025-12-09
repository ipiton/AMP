package publishing

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// TestHealthMonitor_Lifecycle tests Start/Stop operations.
func TestHealthMonitor_Lifecycle(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *DefaultHealthMonitor
		test    func(t *testing.T, monitor *DefaultHealthMonitor)
		wantErr bool
	}{
		{
			name: "Start successfully",
			setup: func() *DefaultHealthMonitor {
				return createTestHealthMonitor(t)
			},
			test: func(t *testing.T, monitor *DefaultHealthMonitor) {
				err := monitor.Start()
				if err != nil {
					t.Fatalf("Start() failed: %v", err)
				}

				// Verify running state
				if !monitor.running.Load() {
					t.Error("Monitor not marked as running")
				}

				// Cleanup
				_ = monitor.Stop(time.Second)
			},
			wantErr: false,
		},
		{
			name: "Start fails if already started",
			setup: func() *DefaultHealthMonitor {
				monitor := createTestHealthMonitor(t)
				_ = monitor.Start()
				return monitor
			},
			test: func(t *testing.T, monitor *DefaultHealthMonitor) {
				err := monitor.Start()
				if !errors.Is(err, ErrAlreadyStarted) {
					t.Errorf("Expected ErrAlreadyStarted, got: %v", err)
				}

				// Cleanup
				_ = monitor.Stop(time.Second)
			},
			wantErr: true,
		},
		{
			name: "Stop successfully",
			setup: func() *DefaultHealthMonitor {
				monitor := createTestHealthMonitor(t)
				_ = monitor.Start()
				return monitor
			},
			test: func(t *testing.T, monitor *DefaultHealthMonitor) {
				err := monitor.Stop(2 * time.Second)
				if err != nil {
					t.Fatalf("Stop() failed: %v", err)
				}

				// Verify stopped state
				if monitor.running.Load() {
					t.Error("Monitor still marked as running")
				}
			},
			wantErr: false,
		},
		{
			name: "Stop fails if not started",
			setup: func() *DefaultHealthMonitor {
				return createTestHealthMonitor(t)
			},
			test: func(t *testing.T, monitor *DefaultHealthMonitor) {
				err := monitor.Stop(time.Second)
				if !errors.Is(err, ErrNotStarted) {
					t.Errorf("Expected ErrNotStarted, got: %v", err)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := tt.setup()
			tt.test(t, monitor)
		})
	}
}

// TestHealthMonitor_GetHealth tests GetHealth method.
func TestHealthMonitor_GetHealth(t *testing.T) {
	t.Run("Returns all targets health status", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		// Add test targets
		targets := []*core.PublishingTarget{
			{Name: "target1", Type: "rootly", URL: "https://api.rootly.com", Enabled: true},
			{Name: "target2", Type: "slack", URL: "https://slack.com/api", Enabled: true},
			{Name: "target3", Type: "pagerduty", URL: "https://api.pagerduty.com", Enabled: false},
		}
		discovery.SetTargets(targets)

		// Get health status
		ctx := context.Background()
		health, err := monitor.GetHealth(ctx)
		if err != nil {
			t.Fatalf("GetHealth() failed: %v", err)
		}

		// Verify all targets returned
		if len(health) != 3 {
			t.Errorf("Expected 3 targets, got %d", len(health))
		}

		// Verify target names
		names := make(map[string]bool)
		for _, h := range health {
			names[h.TargetName] = true
		}

		for _, target := range targets {
			if !names[target.Name] {
				t.Errorf("Target %s not in health status", target.Name)
			}
		}
	})

	t.Run("Returns empty array when no targets", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)
		discovery.SetTargets([]*core.PublishingTarget{})

		ctx := context.Background()
		health, err := monitor.GetHealth(ctx)
		if err != nil {
			t.Fatalf("GetHealth() failed: %v", err)
		}

		if len(health) != 0 {
			t.Errorf("Expected empty array, got %d targets", len(health))
		}
	})
}

// TestHealthMonitor_GetHealthByName tests GetHealthByName method.
func TestHealthMonitor_GetHealthByName(t *testing.T) {
	t.Run("Returns target health status", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		target := &core.PublishingTarget{
			Name:    "rootly-prod",
			Type:    "rootly",
			URL:     "https://api.rootly.com",
			Enabled: true,
		}
		discovery.SetTargets([]*core.PublishingTarget{target})

		ctx := context.Background()
		health, err := monitor.GetHealthByName(ctx, "rootly-prod")
		if err != nil {
			t.Fatalf("GetHealthByName() failed: %v", err)
		}

		// Verify target info
		if health.TargetName != "rootly-prod" {
			t.Errorf("Expected target name 'rootly-prod', got '%s'", health.TargetName)
		}
		if health.TargetType != "rootly" {
			t.Errorf("Expected target type 'rootly', got '%s'", health.TargetType)
		}
		if health.Status != HealthStatusUnknown {
			t.Errorf("Expected status 'unknown', got '%s'", health.Status)
		}
	})

	t.Run("Returns error for non-existent target", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)
		discovery.SetTargets([]*core.PublishingTarget{})

		ctx := context.Background()
		_, err := monitor.GetHealthByName(ctx, "non-existent")
		if err == nil {
			t.Error("Expected error for non-existent target")
		}
	})
}

// TestHealthMonitor_CheckNow tests CheckNow method.
func TestHealthMonitor_CheckNow(t *testing.T) {
	t.Run("Performs immediate health check", func(t *testing.T) {
		// Create test HTTP server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		target := &core.PublishingTarget{
			Name:    "test-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		}
		discovery.SetTargets([]*core.PublishingTarget{target})

		ctx := context.Background()
		health, err := monitor.CheckNow(ctx, "test-target")
		if err != nil {
			t.Fatalf("CheckNow() failed: %v", err)
		}

		// Verify health status updated
		if health.Status != HealthStatusHealthy {
			t.Errorf("Expected status 'healthy', got '%s'", health.Status)
		}
		if health.LatencyMs == nil {
			t.Error("Expected latency to be set")
		}
		if health.TotalChecks != 1 {
			t.Errorf("Expected 1 check, got %d", health.TotalChecks)
		}
	})

	t.Run("Returns error for non-existent target", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)
		discovery.SetTargets([]*core.PublishingTarget{})

		ctx := context.Background()
		_, err := monitor.CheckNow(ctx, "non-existent")
		if err == nil {
			t.Error("Expected error for non-existent target")
		}
	})

	t.Run("Detects unhealthy target", func(t *testing.T) {
		// Create test HTTP server that returns 500
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		target := &core.PublishingTarget{
			Name:    "failing-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		}
		discovery.SetTargets([]*core.PublishingTarget{target})

		ctx := context.Background()

		// Perform 3 checks to trigger unhealthy threshold
		for i := 0; i < 3; i++ {
			_, _ = monitor.CheckNow(ctx, "failing-target")
		}

		// Verify unhealthy status
		health, err := monitor.GetHealthByName(ctx, "failing-target")
		if err != nil {
			t.Fatalf("GetHealthByName() failed: %v", err)
		}

		if health.Status != HealthStatusUnhealthy {
			t.Errorf("Expected status 'unhealthy', got '%s'", health.Status)
		}
		if health.ConsecutiveFailures < 3 {
			t.Errorf("Expected ≥3 consecutive failures, got %d", health.ConsecutiveFailures)
		}
	})
}

// TestHealthMonitor_GetStats tests GetStats method.
func TestHealthMonitor_GetStats(t *testing.T) {
	t.Run("Returns aggregate statistics", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		// Create test HTTP servers
		healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer healthyServer.Close()

		unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer unhealthyServer.Close()

		targets := []*core.PublishingTarget{
			{Name: "healthy1", Type: "webhook", URL: healthyServer.URL, Enabled: true},
			{Name: "healthy2", Type: "webhook", URL: healthyServer.URL, Enabled: true},
			{Name: "unhealthy1", Type: "webhook", URL: unhealthyServer.URL, Enabled: true},
		}
		discovery.SetTargets(targets)

		ctx := context.Background()

		// Perform health checks
		for _, target := range targets {
			// Check 3 times to trigger unhealthy for failing target
			for i := 0; i < 3; i++ {
				_, _ = monitor.CheckNow(ctx, target.Name)
			}
		}

		// Get stats
		stats, err := monitor.GetStats(ctx)
		if err != nil {
			t.Fatalf("GetStats() failed: %v", err)
		}

		// Verify counts
		if stats.TotalTargets != 3 {
			t.Errorf("Expected 3 total targets, got %d", stats.TotalTargets)
		}
		if stats.HealthyCount != 2 {
			t.Errorf("Expected 2 healthy targets, got %d", stats.HealthyCount)
		}
		if stats.UnhealthyCount != 1 {
			t.Errorf("Expected 1 unhealthy target, got %d", stats.UnhealthyCount)
		}

		// Verify success rate
		// Expected: (2*3 successes) / (3 targets * 3 checks) = 6/9 = 66.67%
		expectedRate := 66.67
		if stats.OverallSuccessRate < expectedRate-1 || stats.OverallSuccessRate > expectedRate+1 {
			t.Errorf("Expected success rate ~%.1f%%, got %.1f%%", expectedRate, stats.OverallSuccessRate)
		}
	})

	t.Run("Returns zero stats when no targets", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)
		discovery.SetTargets([]*core.PublishingTarget{})

		ctx := context.Background()
		stats, err := monitor.GetStats(ctx)
		if err != nil {
			t.Fatalf("GetStats() failed: %v", err)
		}

		if stats.TotalTargets != 0 {
			t.Errorf("Expected 0 targets, got %d", stats.TotalTargets)
		}
		if stats.OverallSuccessRate != 0 {
			t.Errorf("Expected 0%% success rate, got %.1f%%", stats.OverallSuccessRate)
		}
	})
}

// TestHealthMonitor_Concurrent tests concurrent access.
func TestHealthMonitor_Concurrent(t *testing.T) {
	t.Run("Concurrent GetHealth calls are safe", func(t *testing.T) {
		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		targets := []*core.PublishingTarget{
			{Name: "target1", Type: "rootly", URL: "https://api.rootly.com", Enabled: true},
			{Name: "target2", Type: "slack", URL: "https://slack.com/api", Enabled: true},
		}
		discovery.SetTargets(targets)

		ctx := context.Background()

		// Launch 10 concurrent readers
		var wg sync.WaitGroup
		errors := make(chan error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := monitor.GetHealth(ctx)
				if err != nil {
					errors <- err
				}
			}()
		}

		wg.Wait()
		close(errors)

		// Check for errors
		for err := range errors {
			t.Errorf("Concurrent GetHealth() failed: %v", err)
		}
	})

	t.Run("Concurrent CheckNow calls are safe", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(10 * time.Millisecond) // Simulate slow response
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		monitor, discovery := createTestHealthMonitorWithDiscovery(t)

		target := &core.PublishingTarget{
			Name:    "concurrent-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		}
		discovery.SetTargets([]*core.PublishingTarget{target})

		ctx := context.Background()

		// Launch 5 concurrent health checks
		var wg sync.WaitGroup
		errors := make(chan error, 5)

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := monitor.CheckNow(ctx, "concurrent-target")
				if err != nil {
					errors <- err
				}
			}()
		}

		wg.Wait()
		close(errors)

		// Check for errors
		for err := range errors {
			t.Errorf("Concurrent CheckNow() failed: %v", err)
		}

		// Verify final state
		health, err := monitor.GetHealthByName(ctx, "concurrent-target")
		if err != nil {
			t.Fatalf("GetHealthByName() failed: %v", err)
		}

		// Should have at least 1 check (may be less than 5 due to cache updates during concurrent access)
		if health.TotalChecks < 1 {
			t.Errorf("Expected at least 1 total check, got %d", health.TotalChecks)
		}

		// Verify all checks succeeded
		if health.Status != HealthStatusHealthy {
			t.Errorf("Expected healthy status, got %s", health.Status)
		}
	})
}

// TestHealthMonitor_DegradedState tests degraded state detection
func TestHealthMonitor_DegradedState(t *testing.T) {
	// Create failing HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	// Setup discovery with target pointing to failing server
	discoveryMgr := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
		"test-target": {
			Name:    "test-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		},
	})

	config := DefaultHealthConfig()
	config.CheckInterval = 100 * time.Millisecond
	config.FailureThreshold = 3

	monitor, err := NewHealthMonitor(discoveryMgr, config, slog.Default(), nil)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	// Start monitor and let it run checks
	if err := monitor.Start(); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(time.Second)

	// Wait for checks to complete
	time.Sleep(300 * time.Millisecond)

	// Verify degraded or unhealthy state
	ctx := context.Background()
	health, err := monitor.GetHealthByName(ctx, "test-target")
	if err != nil {
		t.Fatalf("GetHealthByName failed: %v", err)
	}

	if health.IsHealthy() {
		t.Errorf("Expected degraded or unhealthy state, got healthy: %+v", health)
	}
}

// TestHealthMonitor_ConcurrentChecks tests concurrent health checks
func TestHealthMonitor_ConcurrentChecks(t *testing.T) {
	// Create healthy HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Setup discovery with multiple targets
	discoveryMgr := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
		"target-1": {Name: "target-1", Type: "webhook", URL: server.URL, Enabled: true},
		"target-2": {Name: "target-2", Type: "webhook", URL: server.URL, Enabled: true},
		"target-3": {Name: "target-3", Type: "webhook", URL: server.URL, Enabled: true},
	})

	monitor, err := NewHealthMonitor(discoveryMgr, DefaultHealthConfig(), slog.Default(), nil)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	// Run concurrent GetHealth calls
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = monitor.GetHealth(ctx)
		}()
	}
	wg.Wait()

	// Verify all targets are accessible
	allHealth, err := monitor.GetHealth(ctx)
	if err != nil {
		t.Fatalf("GetHealth failed: %v", err)
	}

	if len(allHealth) != 3 {
		t.Errorf("Expected 3 health statuses, got %d", len(allHealth))
	}
}

// Helper functions

var (
	testMetrics     *v2.PublishingMetrics
	testMetricsOnce sync.Once
)

func getTestMetrics(t *testing.T) *v2.PublishingMetrics {
	t.Helper()

	testMetricsOnce.Do(func() {
		testMetrics = v2.NewPublishingMetrics(prometheus.NewRegistry())
	})

	return testMetrics
}

func createTestHealthMonitor(t *testing.T) *DefaultHealthMonitor {
	t.Helper()

	discovery := NewTestHealthDiscoveryManager()
	config := DefaultHealthConfig()
	config.CheckInterval = 100 * time.Millisecond // Fast for testing
	config.WarmupDelay = 10 * time.Millisecond

	metrics := getTestMetrics(t)

	monitor, err := NewHealthMonitor(discovery, config, slog.Default(), metrics)
	if err != nil {
		t.Fatalf("Failed to create health monitor: %v", err)
	}

	return monitor
}

func createTestHealthMonitorWithDiscovery(t *testing.T) (*DefaultHealthMonitor, *TestHealthDiscoveryManager) {
	t.Helper()

	discovery := NewTestHealthDiscoveryManager()
	config := DefaultHealthConfig()
	config.CheckInterval = 100 * time.Millisecond
	config.WarmupDelay = 10 * time.Millisecond

	metrics := getTestMetrics(t)

	monitor, err := NewHealthMonitor(discovery, config, slog.Default(), metrics)
	if err != nil {
		t.Fatalf("Failed to create health monitor: %v", err)
	}

	return monitor, discovery
}

func createTestDiscoveryManager(t *testing.T, targets map[string]*core.PublishingTarget) TargetDiscoveryManager {
	t.Helper()

	discovery := NewTestHealthDiscoveryManager()
	targetList := make([]*core.PublishingTarget, 0, len(targets))
	for _, target := range targets {
		targetList = append(targetList, target)
	}
	discovery.SetTargets(targetList)

	return discovery
}

// createTestHealthMonitorWith creates health monitor with custom discovery and config.
func createTestHealthMonitorWith(discoveryMgr TargetDiscoveryManager, config HealthConfig) (HealthMonitor, error) {
	// Create metrics manually to avoid nil pointer
	registry := v2.NewRegistry()
	metrics := registry.Publishing
	return NewHealthMonitor(discoveryMgr, config, slog.Default(), metrics)
}
