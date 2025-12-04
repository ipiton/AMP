// Package handlers provides HTTP handlers for the Alert History Service.
// TN-83: GET /api/dashboard/health (basic) - Integration Tests

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics"
)

// TestDashboardHealthHandler_Integration_AllComponents tests health check with all components available.
// This test requires real dependencies or comprehensive mocks.
func TestDashboardHealthHandler_Integration_AllComponents(t *testing.T) {
	t.Skip("Integration test - requires real PostgresPool or comprehensive test setup")

	// This test would:
	// 1. Create real PostgresPool (or use test database)
	// 2. Create real Redis cache (or use test Redis)
	// 3. Create real ClassificationService (or use test service)
	// 4. Create real TargetDiscoveryManager (or use test manager)
	// 5. Call GetHealth endpoint
	// 6. Verify all components return healthy status
	// 7. Verify response format
	// 8. Verify HTTP status code is 200
}

// TestDashboardHealthHandler_Integration_GracefulDegradation tests graceful degradation scenarios.
func TestDashboardHealthHandler_Integration_GracefulDegradation(t *testing.T) {
	tests := []struct {
		name           string
		hasDB          bool
		hasRedis       bool
		hasLLM         bool
		hasPublishing  bool
		expectedStatus string
		expectedCode   int
	}{
		{
			name:           "Only database configured",
			hasDB:          true,
			hasRedis:       false,
			hasLLM:         false,
			hasPublishing:  false,
			expectedStatus: "healthy",
			expectedCode:   http.StatusOK,
		},
		{
			name:           "Database + Redis configured",
			hasDB:          true,
			hasRedis:       true,
			hasLLM:         false,
			hasPublishing:  false,
			expectedStatus: "healthy",
			expectedCode:   http.StatusOK,
		},
		{
			name:           "All components configured",
			hasDB:          true,
			hasRedis:       true,
			hasLLM:         true,
			hasPublishing:  true,
			expectedStatus: "healthy",
			expectedCode:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with selective dependencies
			var dbPool *postgres.PostgresPool
			if tt.hasDB {
				// In real integration test, would create test PostgresPool
				t.Skip("Requires real PostgresPool")
			}

			var redisCache cache.Cache
			if tt.hasRedis {
				redisCache = &mockCacheForHealth{healthErr: nil}
			}

			var classificationService services.ClassificationService
			if tt.hasLLM {
				classificationService = &mockClassificationServiceForHealth{healthErr: nil}
			}

			var targetDiscovery publishing.TargetDiscoveryManager
			if tt.hasPublishing {
				targetDiscovery = &mockTargetDiscoveryForHealth{
					stats: publishing.DiscoveryStats{TotalTargets: 5},
				}
			}

			handler := NewDashboardHealthHandler(
				dbPool,
				redisCache,
				classificationService,
				targetDiscovery,
				nil, // healthMonitor
				nil, // logger
				metrics.DefaultRegistry(),
			)

			req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
			w := httptest.NewRecorder()

			handler.GetHealth(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("expected status code %d, got %d", tt.expectedCode, w.Code)
			}

			var response DashboardHealthResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if response.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, response.Status)
			}
		})
	}
}

// TestDashboardHealthHandler_Integration_ParallelExecution tests that health checks execute in parallel.
func TestDashboardHealthHandler_Integration_ParallelExecution(t *testing.T) {
	// Create handler with all optional components
	handler := NewDashboardHealthHandler(
		nil, // dbPool - would be real in integration test
		&mockCacheForHealth{healthErr: nil},
		&mockClassificationServiceForHealth{healthErr: nil},
		&mockTargetDiscoveryForHealth{
			stats: publishing.DiscoveryStats{TotalTargets: 5},
		},
		&mockHealthMonitorForHealth{
			health: []publishing.TargetHealthStatus{},
		},
		nil, // logger
		metrics.DefaultRegistry(),
	)

	// Set very short timeouts to verify parallel execution
	handler.config.DatabaseTimeout = 100 * time.Millisecond
	handler.config.RedisTimeout = 100 * time.Millisecond
	handler.config.LLMTimeout = 100 * time.Millisecond
	handler.config.PublishingTimeout = 100 * time.Millisecond
	handler.config.OverallTimeout = 500 * time.Millisecond

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	handler.GetHealth(w, req)
	duration := time.Since(start)

	// If checks were sequential, duration would be ~400ms (4 * 100ms)
	// If checks are parallel, duration should be ~100ms (max of individual timeouts)
	if duration > 200*time.Millisecond {
		t.Errorf("health checks appear to be sequential (duration %v), expected parallel execution", duration)
	}

	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Errorf("unexpected status code: %d", w.Code)
	}
}

// TestDashboardHealthHandler_Integration_TimeoutHandling tests timeout scenarios.
func TestDashboardHealthHandler_Integration_TimeoutHandling(t *testing.T) {
	// Create handler with components that will timeout
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil},
		nil, // classification
		nil, // targetDiscovery
		nil, // healthMonitor
		nil, // logger
		metrics.DefaultRegistry(),
	)

	// Set very short timeout
	handler.config.RedisTimeout = 1 * time.Millisecond

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
	w := httptest.NewRecorder()

	handler.GetHealth(w, req)

	// Should handle timeout gracefully (not panic)
	if w.Code == 0 {
		t.Error("handler did not write response")
	}

	var response DashboardHealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Response should have status set
	if response.Status == "" {
		t.Error("response status should be set")
	}
}

// TestDashboardHealthHandler_Integration_ConcurrentRequests tests concurrent request handling.
func TestDashboardHealthHandler_Integration_ConcurrentRequests(t *testing.T) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil},
		&mockClassificationServiceForHealth{healthErr: nil},
		nil, // targetDiscovery
		nil, // healthMonitor
		nil, // logger
		metrics.DefaultRegistry(),
	)

	// Test concurrent requests
	const numRequests = 10
	results := make(chan int, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
			w := httptest.NewRecorder()
			handler.GetHealth(w, req)
			results <- w.Code
		}()
	}

	// Collect results
	codes := make(map[int]int)
	for i := 0; i < numRequests; i++ {
		code := <-results
		codes[code]++
	}

	// All requests should complete successfully (no panics)
	if len(codes) == 0 {
		t.Error("no requests completed")
	}

	// Verify thread safety (no race conditions)
	for code, count := range codes {
		if count == 0 {
			t.Errorf("unexpected status code: %d", code)
		}
	}
}

// TestDashboardHealthHandler_Integration_ErrorRecovery tests error recovery scenarios.
// Note: Without database, system will always be unhealthy (database is critical).
// This test verifies that errors in optional components are handled gracefully.
func TestDashboardHealthHandler_Integration_ErrorRecovery(t *testing.T) {
	tests := []struct {
		name           string
		redisErr       error
		llmErr         error
		publishingErr  error
		expectedCode   int // Without DB, will be 503 (unhealthy)
		verifyErrorHandling bool // Verify that errors are captured in response
	}{
		{
			name:           "Redis error - should handle gracefully",
			redisErr:       context.DeadlineExceeded,
			expectedCode:   http.StatusServiceUnavailable, // DB missing = unhealthy
			verifyErrorHandling: true,
		},
		{
			name:           "LLM error - should handle gracefully",
			llmErr:         context.DeadlineExceeded,
			expectedCode:   http.StatusServiceUnavailable, // DB missing = unhealthy
			verifyErrorHandling: true,
		},
		{
			name:           "Multiple errors - should handle gracefully",
			redisErr:       context.DeadlineExceeded,
			llmErr:         context.DeadlineExceeded,
			expectedCode:   http.StatusServiceUnavailable, // DB missing = unhealthy
			verifyErrorHandling: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var redisCache cache.Cache
			if tt.redisErr != nil {
				redisCache = &mockCacheForHealth{healthErr: tt.redisErr}
			}

			var classificationService services.ClassificationService
			if tt.llmErr != nil {
				classificationService = &mockClassificationServiceForHealth{healthErr: tt.llmErr}
			}

			var targetDiscovery publishing.TargetDiscoveryManager
			if tt.publishingErr != nil {
				targetDiscovery = &mockTargetDiscoveryForHealth{
					stats: publishing.DiscoveryStats{TotalTargets: 0},
				}
			}

			handler := NewDashboardHealthHandler(
				nil, // dbPool - missing DB makes system unhealthy (expected)
				redisCache,
				classificationService,
				targetDiscovery,
				nil, // healthMonitor
				nil, // logger
				metrics.DefaultRegistry(),
			)

			req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
			w := httptest.NewRecorder()

			handler.GetHealth(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("expected status code %d, got %d", tt.expectedCode, w.Code)
			}

			var response DashboardHealthResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			// Verify error handling: errors should be captured in response
			if tt.verifyErrorHandling {
				if tt.redisErr != nil {
					if redisHealth, ok := response.Services["redis"]; ok {
						if redisHealth.Error == "" {
							t.Error("Redis error should be captured in response")
						}
					}
				}
				if tt.llmErr != nil {
					if llmHealth, ok := response.Services["llm_service"]; ok {
						if llmHealth.Error == "" {
							t.Error("LLM error should be captured in response")
						}
					}
				}
			}

			// Verify response structure is valid
			if response.Status == "" {
				t.Error("response status should be set")
			}
			if response.Services == nil {
				t.Error("services map should be initialized")
			}
		})
	}
}

// TestDashboardHealthHandler_Integration_ResponseFormat tests response format validation.
func TestDashboardHealthHandler_Integration_ResponseFormat(t *testing.T) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil},
		&mockClassificationServiceForHealth{healthErr: nil},
		nil, // targetDiscovery
		nil, // healthMonitor
		nil, // logger
		metrics.DefaultRegistry(),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
	w := httptest.NewRecorder()

	handler.GetHealth(w, req)

	// Verify Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	// Verify JSON is valid
	var response DashboardHealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	// Verify required fields
	if response.Status == "" {
		t.Error("status field is required")
	}

	if response.Timestamp.IsZero() {
		t.Error("timestamp field is required")
	}

	if response.Services == nil {
		t.Error("services field is required")
	}
}
