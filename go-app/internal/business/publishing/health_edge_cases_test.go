package publishing

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// health_edge_cases_test.go - Edge case tests for Health Monitor
// Target: +8% coverage, comprehensive error handling validation

// TestHealthMonitor_NetworkTimeouts tests various timeout scenarios
func TestHealthMonitor_NetworkTimeouts(t *testing.T) {
	// Create shared metrics instance for all subtests
	metrics, err := NewHealthMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	tests := []struct {
		name            string
		serverDelay     time.Duration
		clientTimeout   time.Duration
		expectTimeout   bool
		expectUnhealthy bool
	}{
		{
			name:            "Fast response within timeout",
			serverDelay:     100 * time.Millisecond,
			clientTimeout:   1 * time.Second,
			expectTimeout:   false,
			expectUnhealthy: false,
		},
		{
			name:            "Slow response exceeds timeout",
			serverDelay:     2 * time.Second,
			clientTimeout:   500 * time.Millisecond,
			expectTimeout:   true,
			expectUnhealthy: true,
		},
		{
			name:            "Edge case: exactly at timeout",
			serverDelay:     1 * time.Second,
			clientTimeout:   1 * time.Second,
			expectTimeout:   false, // Should complete just in time
			expectUnhealthy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create server with configurable delay
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(tt.serverDelay)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			// Create discovery with target
			discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
				"slow-target": {
					Name:    "slow-target",
					Type:    "webhook",
					URL:     server.URL,
					Enabled: true,
				},
			})

			// Create monitor with custom timeout
			config := DefaultHealthConfig()
			config.HTTPTimeout = tt.clientTimeout
			config.FailureThreshold = 1 // Fail immediately
			config.WarmupDelay = 0      // Skip warmup for tests

			monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
			if err != nil {
				t.Fatalf("Failed to create monitor: %v", err)
			}

			// Perform check
			ctx := context.Background()
			health, _ := monitor.CheckNow(ctx, "slow-target")

			if tt.expectTimeout {
				// Health monitor doesn't return error, just marks target as unhealthy
				if health == nil {
					t.Fatal("Expected health status, got nil")
				}
				if tt.expectUnhealthy && !health.IsUnhealthy() {
					t.Errorf("Expected unhealthy status, got: %v", health.Status)
				}
			} else {
				if health == nil {
					t.Fatal("Expected health status, got nil")
				}
				if !health.IsHealthy() && !tt.expectUnhealthy {
					t.Errorf("Expected healthy status, got: %v", health.Status)
				}
			}
		})
	}
}

// TestHealthMonitor_TLSErrors tests TLS certificate validation
func TestHealthMonitor_TLSErrors(t *testing.T) {
	metrics, _ := NewHealthMetrics()

	tests := []struct {
		name        string
		setupServer func() *httptest.Server
		expectError bool
	}{
		{
			name: "Valid HTTPS server",
			setupServer: func() *httptest.Server {
				return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
			},
			expectError: false,
		},
		{
			name: "Self-signed certificate (should be accepted in test)",
			setupServer: func() *httptest.Server {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				server.TLS = &tls.Config{
					InsecureSkipVerify: true,
				}
				server.StartTLS()
				return server
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
				"tls-target": {
					Name:    "tls-target",
					Type:    "webhook",
					URL:     server.URL,
					Enabled: true,
				},
			})

			config := DefaultHealthConfig()
			config.WarmupDelay = 0      // Skip warmup for tests
			config.FailureThreshold = 1 // Mark unhealthy immediately
			monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
			if err != nil {
				t.Fatalf("Failed to create monitor: %v", err)
			}

			ctx := context.Background()
			health, _ := monitor.CheckNow(ctx, "tls-target")

			if health == nil {
				t.Fatal("Expected health status, got nil")
			}

			if tt.expectError {
				if !health.IsUnhealthy() {
					t.Errorf("Expected unhealthy status for TLS error, got: %v", health.Status)
				}
			} else {
				if !health.IsHealthy() {
					t.Errorf("Expected healthy status, got: %v", health.Status)
				}
			}
		})
	}
}

// TestHealthMonitor_DNSFailures tests DNS resolution errors
func TestHealthMonitor_DNSFailures(t *testing.T) {
	metrics, _ := NewHealthMetrics()

	tests := []struct {
		name        string
		targetURL   string
		expectError bool
	}{
		{
			name:        "Invalid hostname",
			targetURL:   "http://this-domain-does-not-exist-12345.com",
			expectError: true,
		},
		{
			name:        "Malformed URL",
			targetURL:   "not-a-valid-url",
			expectError: true,
		},
		{
			name:        "Empty hostname",
			targetURL:   "http://",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
				"dns-target": {
					Name:    "dns-target",
					Type:    "webhook",
					URL:     tt.targetURL,
					Enabled: true,
				},
			})

			config := DefaultHealthConfig()
			config.HTTPTimeout = 2 * time.Second // Short timeout for DNS failures
			config.WarmupDelay = 0               // Skip warmup for tests
			config.FailureThreshold = 1          // Mark unhealthy immediately

			monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
			if err != nil {
				t.Fatalf("Failed to create monitor: %v", err)
			}

			ctx := context.Background()
			health, _ := monitor.CheckNow(ctx, "dns-target")

			if health == nil {
				t.Fatal("Expected health status, got nil")
			}

			if tt.expectError {
				if !health.IsUnhealthy() {
					t.Errorf("Expected unhealthy status for DNS/URL error, got: %v", health.Status)
				}
			}
		})
	}
}

// TestHealthMonitor_StateTransitions tests degraded → unhealthy transitions
func TestHealthMonitor_StateTransitions(t *testing.T) {
	metrics, _ := NewHealthMetrics()
	failureCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failureCount++
		if failureCount <= 2 {
			// First 2 requests fail (degraded)
			w.WriteHeader(http.StatusServiceUnavailable)
		} else if failureCount == 3 {
			// 3rd request fails (unhealthy)
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			// Subsequent requests succeed (recovery)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
		"transition-target": {
			Name:    "transition-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		},
	})

	config := DefaultHealthConfig()
	config.FailureThreshold = 3
	config.WarmupDelay = 0 // Skip warmup for tests

	monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	ctx := context.Background()

	// Check 1: Should be degraded (1 failure)
	health1, _ := monitor.CheckNow(ctx, "transition-target")
	if health1 != nil && health1.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 consecutive failure, got %d", health1.ConsecutiveFailures)
	}

	// Check 2: Should be more degraded (2 failures)
	health2, _ := monitor.CheckNow(ctx, "transition-target")
	if health2 != nil && health2.ConsecutiveFailures != 2 {
		t.Errorf("Expected 2 consecutive failures, got %d", health2.ConsecutiveFailures)
	}

	// Check 3: Should be unhealthy (3 failures)
	health3, _ := monitor.CheckNow(ctx, "transition-target")
	if health3 != nil {
		if health3.ConsecutiveFailures != 3 {
			t.Errorf("Expected 3 consecutive failures, got %d", health3.ConsecutiveFailures)
		}
		if !health3.IsUnhealthy() {
			t.Errorf("Expected unhealthy status, got: %v", health3.Status)
		}
	}

	// Check 4: Should recover (1 success)
	health4, _ := monitor.CheckNow(ctx, "transition-target")
	if health4 != nil {
		if health4.ConsecutiveFailures != 0 {
			t.Errorf("Expected 0 consecutive failures after recovery, got %d", health4.ConsecutiveFailures)
		}
		if !health4.IsHealthy() {
			t.Errorf("Expected healthy status after recovery, got: %v", health4.Status)
		}
	}
}

// TestHealthMonitor_ConcurrentStarts tests concurrent Start() calls
func TestHealthMonitor_ConcurrentStarts(t *testing.T) {
	metrics, _ := NewHealthMetrics()
	discovery := NewTestHealthDiscoveryManager()
	monitor, err := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	// Try to start multiple times concurrently
	startErrors := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			startErrors <- monitor.Start()
		}()
	}

	// Collect results
	successCount := 0
	alreadyStartedCount := 0
	for i := 0; i < 10; i++ {
		err := <-startErrors
		if err == nil {
			successCount++
		} else if errors.Is(err, ErrAlreadyStarted) {
			alreadyStartedCount++
		}
	}

	// Should have exactly 1 success, rest should be ErrAlreadyStarted
	if successCount != 1 {
		t.Errorf("Expected 1 successful start, got %d", successCount)
	}
	if alreadyStartedCount != 9 {
		t.Errorf("Expected 9 ErrAlreadyStarted, got %d", alreadyStartedCount)
	}

	// Cleanup
	monitor.Stop(time.Second)
}

// TestHealthMonitor_StopDuringCheck tests Stop() during active checks
func TestHealthMonitor_StopDuringCheck(t *testing.T) {
	metrics, _ := NewHealthMetrics()
	// Create slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // Long delay
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
		"slow-target": {
			Name:    "slow-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		},
	})

	config := DefaultHealthConfig()
	config.CheckInterval = 100 * time.Millisecond
	config.WarmupDelay = 0 // Skip warmup for tests

	monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	// Start monitor
	if err := monitor.Start(); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}

	// Let it start checking
	time.Sleep(200 * time.Millisecond)

	// Stop while check is in progress
	stopStart := time.Now()
	err = monitor.Stop(2 * time.Second)
	stopDuration := time.Since(stopStart)

	// Should stop within timeout
	if err != nil {
		t.Errorf("Stop failed: %v", err)
	}
	if stopDuration > 3*time.Second {
		t.Errorf("Stop took too long: %v", stopDuration)
	}
}

// TestHealthMonitor_ConnectionRefused tests connection refused errors
func TestHealthMonitor_ConnectionRefused(t *testing.T) {
	metrics, _ := NewHealthMetrics()
	// Use a port that's not listening
	discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
		"refused-target": {
			Name:    "refused-target",
			Type:    "webhook",
			URL:     "http://localhost:9999", // Unlikely to be listening
			Enabled: true,
		},
	})

	config := DefaultHealthConfig()
	config.WarmupDelay = 0 // Skip warmup for tests
	monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
	config.FailureThreshold = 1 // Mark unhealthy immediately
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	ctx := context.Background()
	health, _ := monitor.CheckNow(ctx, "refused-target")

	// Should mark target as unhealthy
	if health == nil {
		t.Fatal("Expected health status, got nil")
	}

	if !health.IsUnhealthy() {
		t.Errorf("Expected unhealthy status for connection refused, got: %v", health.Status)
	}
}

// TestHealthMonitor_ContextCancellation tests context cancellation during check
func TestHealthMonitor_ContextCancellation(t *testing.T) {
	metrics, _ := NewHealthMetrics()
	// Create slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	discovery := createTestDiscoveryManager(t, map[string]*core.PublishingTarget{
		"cancel-target": {
			Name:    "cancel-target",
			Type:    "webhook",
			URL:     server.URL,
			Enabled: true,
		},
	})

	config := DefaultHealthConfig()
	config.WarmupDelay = 0 // Skip warmup for tests
	config.FailureThreshold = 1 // Mark unhealthy immediately
	monitor, err := NewHealthMonitor(discovery, config, nil, metrics)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should mark target as unhealthy due to context timeout
	health, _ := monitor.CheckNow(ctx, "cancel-target")
	if health == nil {
		t.Fatal("Expected health status, got nil")
	}
	if !health.IsUnhealthy() {
		t.Errorf("Expected unhealthy status for context cancellation, got: %v", health.Status)
	}
}
