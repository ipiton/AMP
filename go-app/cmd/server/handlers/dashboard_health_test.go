// Package handlers provides HTTP handlers for the Alert History Service.
// TN-83: GET /api/dashboard/health (basic) - Unit Tests
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics"
)

// mockPostgresPoolForHealth wraps PostgresPool for testing.
// We'll use a helper function to create a testable pool or use nil for not_configured tests.

// mockCacheForHealth is a mock implementation of Cache for health testing.
type mockCacheForHealth struct {
	healthErr error
}

func (m *mockCacheForHealth) Get(ctx context.Context, key string, dest interface{}) error {
	return nil
}

func (m *mockCacheForHealth) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return nil
}

func (m *mockCacheForHealth) Delete(ctx context.Context, key string) error {
	return nil
}

func (m *mockCacheForHealth) Exists(ctx context.Context, key string) (bool, error) {
	return false, nil
}

func (m *mockCacheForHealth) TTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (m *mockCacheForHealth) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return nil
}

func (m *mockCacheForHealth) HealthCheck(ctx context.Context) error {
	return m.healthErr
}

func (m *mockCacheForHealth) Ping(ctx context.Context) error {
	return m.healthErr
}

func (m *mockCacheForHealth) Flush(ctx context.Context) error {
	return nil
}

func (m *mockCacheForHealth) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return nil
}

func (m *mockCacheForHealth) SMembers(ctx context.Context, key string) ([]string, error) {
	return nil, nil
}

func (m *mockCacheForHealth) SRem(ctx context.Context, key string, members ...interface{}) error {
	return nil
}

func (m *mockCacheForHealth) SCard(ctx context.Context, key string) (int64, error) {
	return 0, nil
}

// mockClassificationServiceForHealth is a mock implementation of ClassificationService for health testing.
type mockClassificationServiceForHealth struct {
	healthErr error
}

func (m *mockClassificationServiceForHealth) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForHealth) ClassifyBatch(ctx context.Context, alerts []*core.Alert) ([]*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForHealth) GetCachedClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForHealth) InvalidateCache(ctx context.Context, fingerprint string) error {
	return nil
}

func (m *mockClassificationServiceForHealth) WarmCache(ctx context.Context, alerts []*core.Alert) error {
	return nil
}

func (m *mockClassificationServiceForHealth) GetStats() services.ClassificationStats {
	return services.ClassificationStats{}
}

func (m *mockClassificationServiceForHealth) Health(ctx context.Context) error {
	return m.healthErr
}

// mockTargetDiscoveryForHealth is a mock implementation of TargetDiscoveryManager for health testing.
type mockTargetDiscoveryForHealth struct {
	stats publishing.DiscoveryStats
}

func (m *mockTargetDiscoveryForHealth) DiscoverTargets(ctx context.Context) error {
	return nil
}

func (m *mockTargetDiscoveryForHealth) GetTarget(name string) (*core.PublishingTarget, error) {
	return nil, nil
}

func (m *mockTargetDiscoveryForHealth) ListTargets() []*core.PublishingTarget {
	return nil
}

func (m *mockTargetDiscoveryForHealth) GetTargetsByType(targetType string) []*core.PublishingTarget {
	return nil
}

func (m *mockTargetDiscoveryForHealth) GetStats() publishing.DiscoveryStats {
	return m.stats
}

func (m *mockTargetDiscoveryForHealth) Health(ctx context.Context) error {
	return nil
}

// mockHealthMonitorForHealth is a mock implementation of HealthMonitor for health testing.
type mockHealthMonitorForHealth struct {
	health []publishing.TargetHealthStatus
	err    error
}

func (m *mockHealthMonitorForHealth) Start() error {
	return nil
}

func (m *mockHealthMonitorForHealth) Stop(timeout time.Duration) error {
	return nil
}

func (m *mockHealthMonitorForHealth) GetHealth(ctx context.Context) ([]publishing.TargetHealthStatus, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.health, nil
}

func (m *mockHealthMonitorForHealth) GetHealthByName(ctx context.Context, targetName string) (*publishing.TargetHealthStatus, error) {
	return nil, nil
}

func (m *mockHealthMonitorForHealth) CheckNow(ctx context.Context, targetName string) (*publishing.TargetHealthStatus, error) {
	return nil, nil
}

func (m *mockHealthMonitorForHealth) GetStats(ctx context.Context) (*publishing.HealthStats, error) {
	return &publishing.HealthStats{}, nil
}

// TestDashboardHealthHandler_GetHealth tests the main GetHealth handler method.
func TestDashboardHealthHandler_GetHealth(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		dbPool         *postgres.PostgresPool // nil for not_configured
		cache          *mockCacheForHealth
		classification *mockClassificationServiceForHealth
		targetDiscovery *mockTargetDiscoveryForHealth
		healthMonitor  *mockHealthMonitorForHealth
		expectedStatus int
		expectedOverallStatus string
		skipTest       bool // Skip tests that require real PostgresPool
	}{
		{
			name:   "POST request - method not allowed",
			method: http.MethodPost,
			dbPool: nil,
			expectedStatus: http.StatusMethodNotAllowed,
			skipTest: false,
		},
		{
			name:   "All components not configured - database required",
			method: http.MethodGet,
			dbPool: nil,
			expectedStatus:        http.StatusServiceUnavailable,
			expectedOverallStatus: "unhealthy",
			skipTest: false,
		},
	}

	for _, tt := range tests {
		if tt.skipTest {
			t.Skip("Skipping test that requires real PostgresPool")
			continue
		}

		t.Run(tt.name, func(t *testing.T) {
			// Convert mocks to interfaces
			var cache cache.Cache
			if tt.cache != nil {
				cache = tt.cache
			}

			var classification services.ClassificationService
			if tt.classification != nil {
				classification = tt.classification
			}

			var targetDiscovery publishing.TargetDiscoveryManager
			if tt.targetDiscovery != nil {
				targetDiscovery = tt.targetDiscovery
			}

			var healthMonitor publishing.HealthMonitor
			if tt.healthMonitor != nil {
				healthMonitor = tt.healthMonitor
			}

			// Create handler with mocks
			handler := NewDashboardHealthHandler(
				tt.dbPool,
				cache,
				classification,
				targetDiscovery,
				healthMonitor,
				nil, // logger
				metrics.DefaultRegistry(),
			)

			// Create request
			req := httptest.NewRequest(tt.method, "/api/dashboard/health", nil)
			w := httptest.NewRecorder()

			// Call handler
			handler.GetHealth(w, req)

			// Check status code
			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			// Parse response if successful
			if tt.expectedStatus == http.StatusOK || tt.expectedStatus == http.StatusServiceUnavailable {
				var response DashboardHealthResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				if response.Status != tt.expectedOverallStatus {
					t.Errorf("expected overall status %s, got %s", tt.expectedOverallStatus, response.Status)
				}

				// Check timestamp is set
				if response.Timestamp.IsZero() {
					t.Error("timestamp should be set")
				}
			}
		})
	}
}

// TestDashboardHealthHandler_checkDatabaseHealth tests database health check.
func TestDashboardHealthHandler_checkDatabaseHealth(t *testing.T) {
	tests := []struct {
		name         string
		dbPool       *postgres.PostgresPool // nil for not_configured
		expectedStatus string
		expectError bool
		skipTest     bool
	}{
		{
			name:           "Database not configured",
			dbPool:         nil,
			expectedStatus: "not_configured",
			expectError:    false,
			skipTest:       false,
		},
	}

	for _, tt := range tests {
		if tt.skipTest {
			t.Skip("Skipping test that requires real PostgresPool")
			continue
		}

		t.Run(tt.name, func(t *testing.T) {
			handler := NewDashboardHealthHandler(
				tt.dbPool,
				nil,
				nil,
				nil,
				nil,
				nil,
				metrics.DefaultRegistry(),
			)

			ctx := context.Background()
			health := handler.checkDatabaseHealth(ctx)

			if health.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, health.Status)
			}

			if tt.expectError && health.Error == "" {
				t.Error("expected error message, got empty")
			}
		})
	}
}

// TestDashboardHealthHandler_checkRedisHealth tests Redis health check.
func TestDashboardHealthHandler_checkRedisHealth(t *testing.T) {
	tests := []struct {
		name         string
		cache        *mockCacheForHealth
		expectedStatus string
		expectError bool
	}{
		{
			name:          "Redis healthy",
			cache:         &mockCacheForHealth{healthErr: nil},
			expectedStatus: "healthy",
			expectError:    false,
		},
		{
			name:          "Redis unhealthy",
			cache:         &mockCacheForHealth{healthErr: errors.New("connection timeout")},
			expectedStatus: "unhealthy",
			expectError:    true,
		},
		{
			name:          "Redis not configured",
			cache:         nil,
			expectedStatus: "not_configured",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cache cache.Cache
			if tt.cache != nil {
				cache = tt.cache
			}

			handler := NewDashboardHealthHandler(
				nil, // dbPool not needed for Redis tests
				cache,
				nil,
				nil,
				nil,
				nil,
				metrics.DefaultRegistry(),
			)

			ctx := context.Background()
			health := handler.checkRedisHealth(ctx)

			if health.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, health.Status)
			}

			if tt.expectError && health.Error == "" {
				t.Error("expected error message, got empty")
			}
		})
	}
}

// TestDashboardHealthHandler_checkLLMHealth tests LLM service health check.
func TestDashboardHealthHandler_checkLLMHealth(t *testing.T) {
		tests := []struct {
		name         string
		classification *mockClassificationServiceForHealth
		expectedStatus string
		expectError bool
	}{
		{
			name:          "LLM available",
			classification: &mockClassificationServiceForHealth{healthErr: nil},
			expectedStatus: "available",
			expectError:    false,
		},
		{
			name:          "LLM unavailable",
			classification: &mockClassificationServiceForHealth{healthErr: errors.New("service unavailable")},
			expectedStatus: "unavailable",
			expectError:    true,
		},
		{
			name:          "LLM not configured",
			classification: nil,
			expectedStatus: "not_configured",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var classification services.ClassificationService
			if tt.classification != nil {
				classification = tt.classification
			}

			handler := NewDashboardHealthHandler(
				nil, // dbPool not needed for LLM tests
				nil,
				classification,
				nil,
				nil,
				nil,
				metrics.DefaultRegistry(),
			)

			ctx := context.Background()
			health := handler.checkLLMHealth(ctx)

			if health.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, health.Status)
			}

			if tt.expectError && health.Error == "" {
				t.Error("expected error message, got empty")
			}
		})
	}
}

// TestDashboardHealthHandler_checkPublishingHealth tests publishing system health check.
func TestDashboardHealthHandler_checkPublishingHealth(t *testing.T) {
		tests := []struct {
		name          string
		targetDiscovery *mockTargetDiscoveryForHealth
		healthMonitor *mockHealthMonitorForHealth
		expectedStatus string
		expectError   bool
	}{
		{
			name: "Publishing healthy",
			targetDiscovery: &mockTargetDiscoveryForHealth{
				stats: publishing.DiscoveryStats{TotalTargets: 5},
			},
			healthMonitor: &mockHealthMonitorForHealth{
				health: []publishing.TargetHealthStatus{},
			},
			expectedStatus: "healthy",
			expectError:    false,
		},
		{
			name: "Publishing degraded - unhealthy targets",
			targetDiscovery: &mockTargetDiscoveryForHealth{
				stats: publishing.DiscoveryStats{TotalTargets: 5},
			},
			healthMonitor: &mockHealthMonitorForHealth{
				health: []publishing.TargetHealthStatus{
					{Status: publishing.HealthStatusUnhealthy},
				},
			},
			expectedStatus: "degraded",
			expectError:    false,
		},
		{
			name: "Publishing not configured",
			targetDiscovery: nil,
			expectedStatus: "not_configured",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var targetDiscovery publishing.TargetDiscoveryManager
			if tt.targetDiscovery != nil {
				targetDiscovery = tt.targetDiscovery
			}

			var healthMonitor publishing.HealthMonitor
			if tt.healthMonitor != nil {
				healthMonitor = tt.healthMonitor
			}

			handler := NewDashboardHealthHandler(
				nil, // dbPool not needed for publishing tests
				nil,
				nil,
				targetDiscovery,
				healthMonitor,
				nil,
				metrics.DefaultRegistry(),
			)

			ctx := context.Background()
			health := handler.checkPublishingHealth(ctx)

			if health.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, health.Status)
			}
		})
	}
}

// TestDashboardHealthHandler_aggregateStatus tests status aggregation logic.
func TestDashboardHealthHandler_aggregateStatus(t *testing.T) {
	tests := []struct {
		name           string
		services       map[string]ServiceHealth
		expectedStatus string
		expectedCode   int
	}{
		{
			name: "All healthy",
			services: map[string]ServiceHealth{
				"database": {Status: "healthy"},
				"redis":    {Status: "healthy"},
			},
			expectedStatus: "healthy",
			expectedCode:   http.StatusOK,
		},
		{
			name: "Database unhealthy - should be unhealthy",
			services: map[string]ServiceHealth{
				"database": {Status: "unhealthy"},
				"redis":    {Status: "healthy"},
			},
			expectedStatus: "unhealthy",
			expectedCode:   http.StatusServiceUnavailable,
		},
		{
			name: "Redis unhealthy - should be degraded",
			services: map[string]ServiceHealth{
				"database": {Status: "healthy"},
				"redis":    {Status: "unhealthy"},
			},
			expectedStatus: "degraded",
			expectedCode:   http.StatusOK,
		},
		{
			name: "LLM unavailable - should be degraded",
			services: map[string]ServiceHealth{
				"database":    {Status: "healthy"},
				"llm_service": {Status: "unavailable"},
			},
			expectedStatus: "degraded",
			expectedCode:   http.StatusOK,
		},
		{
			name: "Publishing degraded - should be degraded",
			services: map[string]ServiceHealth{
				"database":   {Status: "healthy"},
				"publishing": {Status: "degraded"},
			},
			expectedStatus: "degraded",
			expectedCode:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewDashboardHealthHandler(
				nil, // dbPool not needed for aggregation tests
				nil,
				nil,
				nil,
				nil,
				nil,
				metrics.DefaultRegistry(),
			)

			status, code := handler.aggregateStatus(tt.services)

			if status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, status)
			}

			if code != tt.expectedCode {
				t.Errorf("expected code %d, got %d", tt.expectedCode, code)
			}
		})
	}
}

// TestDashboardHealthHandler_TimeoutHandling tests timeout handling.
func TestDashboardHealthHandler_TimeoutHandling(t *testing.T) {
	t.Skip("Skipping timeout test - requires real PostgresPool or more complex mocking")
}
