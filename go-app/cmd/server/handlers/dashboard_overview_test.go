// Package handlers provides HTTP handlers for the Alert History Service.
// TN-81: Dashboard Overview Handler Tests (150% Quality Target)
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/business/publishing"
)

// mockAlertHistoryRepositoryForOverview is a mock implementation for overview tests.
type mockAlertHistoryRepositoryForOverview struct {
	historyResp *core.HistoryResponse
	err         error
}

func (m *mockAlertHistoryRepositoryForOverview) GetHistory(ctx context.Context, req *core.HistoryRequest) (*core.HistoryResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.historyResp, nil
}

func (m *mockAlertHistoryRepositoryForOverview) GetRecentAlerts(ctx context.Context, limit int) ([]*core.Alert, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepositoryForOverview) GetAlertsByFingerprint(ctx context.Context, fingerprint string, limit int) ([]*core.Alert, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepositoryForOverview) GetAggregatedStats(ctx context.Context, timeRange *core.TimeRange) (*core.AggregatedStats, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepositoryForOverview) GetTopAlerts(ctx context.Context, timeRange *core.TimeRange, limit int) ([]*core.TopAlert, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepositoryForOverview) GetFlappingAlerts(ctx context.Context, timeRange *core.TimeRange, threshold int) ([]*core.FlappingAlert, error) {
	return nil, nil
}

// mockClassificationServiceForOverview is a mock implementation for overview tests.
type mockClassificationServiceForOverview struct {
	stats  services.ClassificationStats
	health error
}

func (m *mockClassificationServiceForOverview) Classify(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForOverview) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForOverview) ClassifyBatch(ctx context.Context, alerts []*core.Alert) ([]*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForOverview) GetCachedClassification(ctx context.Context, fingerprint string) (*core.ClassificationResult, error) {
	return nil, nil
}

func (m *mockClassificationServiceForOverview) InvalidateCache(ctx context.Context, fingerprint string) error {
	return nil
}

func (m *mockClassificationServiceForOverview) WarmCache(ctx context.Context, alerts []*core.Alert) error {
	return nil
}

func (m *mockClassificationServiceForOverview) GetStats() services.ClassificationStats {
	return m.stats
}

func (m *mockClassificationServiceForOverview) Health(ctx context.Context) error {
	return m.health
}

// mockPublishingStatsProvider is a mock implementation of PublishingStatsProvider.
type mockPublishingStatsProvider struct {
	targetCount          int
	mode                 string
	successfulPublishes  int64
	failedPublishes      int64
}

func (m *mockPublishingStatsProvider) GetTargetCount() int {
	return m.targetCount
}

func (m *mockPublishingStatsProvider) GetPublishingMode() string {
	return m.mode
}

func (m *mockPublishingStatsProvider) GetSuccessfulPublishes() int64 {
	return m.successfulPublishes
}

func (m *mockPublishingStatsProvider) GetFailedPublishes() int64 {
	return m.failedPublishes
}

// mockCacheForOverview is a mock implementation of Cache for overview tests.
type mockCacheForOverview struct {
	data map[string]interface{}
}

func (m *mockCacheForOverview) Get(ctx context.Context, key string, dest interface{}) error {
	if val, ok := m.data[key]; ok {
		if resp, ok := val.(*DashboardOverviewResponse); ok {
			*dest.(*DashboardOverviewResponse) = *resp
			return nil
		}
	}
	return cache.ErrNotFound
}

func (m *mockCacheForOverview) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if m.data == nil {
		m.data = make(map[string]interface{})
	}
	m.data[key] = value
	return nil
}

func (m *mockCacheForOverview) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockCacheForOverview) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockCacheForOverview) TTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (m *mockCacheForOverview) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return nil
}

func (m *mockCacheForOverview) HealthCheck(ctx context.Context) error {
	return nil
}

func (m *mockCacheForOverview) Ping(ctx context.Context) error {
	return nil
}

func (m *mockCacheForOverview) Flush(ctx context.Context) error {
	m.data = make(map[string]interface{})
	return nil
}

func (m *mockCacheForOverview) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return nil
}

func (m *mockCacheForOverview) SMembers(ctx context.Context, key string) ([]string, error) {
	return nil, nil
}

func (m *mockCacheForOverview) SRem(ctx context.Context, key string, members ...interface{}) error {
	return nil
}

func (m *mockCacheForOverview) SCard(ctx context.Context, key string) (int64, error) {
	return 0, nil
}

// createTestHistoryResponse creates a test history response.
func createTestHistoryResponse() *core.HistoryResponse {
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	alerts := []*core.Alert{
		{
			Fingerprint: "fp1",
			AlertName:   "HighCPU",
			Status:      core.AlertStatus("firing"),
			StartsAt:    now.Add(-10 * time.Minute),
		},
		{
			Fingerprint: "fp2",
			AlertName:   "LowMemory",
			Status:      core.AlertStatus("firing"),
			StartsAt:    now.Add(-5 * time.Minute),
		},
		{
			Fingerprint: "fp3",
			AlertName:   "DiskFull",
			Status:      core.AlertStatus("resolved"),
			StartsAt:    yesterday.Add(-1 * time.Hour),
		},
		{
			Fingerprint: "fp4",
			AlertName:   "NetworkDown",
			Status:      core.AlertStatus("resolved"),
			StartsAt:    now.Add(-2 * time.Hour), // Last 24h
		},
	}

	return &core.HistoryResponse{
		Alerts:     alerts,
		Total:      4,
		Page:       1,
		PerPage:    10000,
		TotalPages: 1,
		HasNext:    false,
		HasPrev:    false,
	}
}

func TestDashboardOverviewHandler_GetOverview_Basic(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	handler := NewDashboardOverviewHandler(repo, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response DashboardOverviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.TotalAlerts != 4 {
		t.Errorf("Expected 4 total alerts, got %d", response.TotalAlerts)
	}

	if response.ActiveAlerts != 2 {
		t.Errorf("Expected 2 active alerts, got %d", response.ActiveAlerts)
	}

	if response.ResolvedAlerts != 2 {
		t.Errorf("Expected 2 resolved alerts, got %d", response.ResolvedAlerts)
	}
}

func TestDashboardOverviewHandler_GetOverview_WithClassification(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	classificationService := &mockClassificationServiceForOverview{
		stats: services.ClassificationStats{
			TotalRequests:   100,
			CacheHitRate:    0.85,
			LLMSuccessRate: 0.95,
		},
		health: nil, // LLM available
	}
	handler := NewDashboardOverviewHandler(repo, classificationService, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	var response DashboardOverviewResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	if !response.ClassificationEnabled {
		t.Error("Expected classification enabled")
	}

	if response.ClassifiedAlerts != 100 {
		t.Errorf("Expected 100 classified alerts, got %d", response.ClassifiedAlerts)
	}

	if response.ClassificationCacheHitRate != 0.85 {
		t.Errorf("Expected cache hit rate 0.85, got %f", response.ClassificationCacheHitRate)
	}

	if !response.LLMServiceAvailable {
		t.Error("Expected LLM service available")
	}
}

func TestDashboardOverviewHandler_GetOverview_WithPublishing(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	publishingStats := &mockPublishingStatsProvider{
		targetCount:         3,
		mode:                "intelligent",
		successfulPublishes: 12500,
		failedPublishes:     25,
	}
	handler := NewDashboardOverviewHandler(repo, nil, publishingStats, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	var response DashboardOverviewResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	if response.PublishingTargets != 3 {
		t.Errorf("Expected 3 publishing targets, got %d", response.PublishingTargets)
	}

	if response.PublishingMode != "intelligent" {
		t.Errorf("Expected publishing mode 'intelligent', got %s", response.PublishingMode)
	}

	if response.SuccessfulPublishes != 12500 {
		t.Errorf("Expected 12500 successful publishes, got %d", response.SuccessfulPublishes)
	}

	if response.FailedPublishes != 25 {
		t.Errorf("Expected 25 failed publishes, got %d", response.FailedPublishes)
	}
}

func TestDashboardOverviewHandler_GetOverview_WithCache(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	cache := &mockCacheForOverview{data: make(map[string]interface{})}
	handler := NewDashboardOverviewHandler(repo, nil, nil, cache, nil)

	// First request - should populate cache
	req1 := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w1 := httptest.NewRecorder()
	handler.GetOverview(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w1.Code)
	}

	// Second request - should hit cache
	req2 := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w2 := httptest.NewRecorder()
	handler.GetOverview(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w2.Code)
	}
}

func TestDashboardOverviewHandler_GetOverview_InvalidMethod(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	handler := NewDashboardOverviewHandler(repo, nil, nil, nil, nil)

	req := httptest.NewRequest("POST", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestDashboardOverviewHandler_GetOverview_RepositoryError(t *testing.T) {
	repo := &mockAlertHistoryRepositoryForOverview{err: context.DeadlineExceeded}
	handler := NewDashboardOverviewHandler(repo, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	// Should still return 200 with defaults (graceful degradation)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 (graceful degradation), got %d", w.Code)
	}

	var response DashboardOverviewResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// Should have defaults
	if response.TotalAlerts != 0 {
		t.Error("Expected 0 total alerts on error")
	}
}

func TestDashboardOverviewHandler_GetOverview_GracefulDegradation(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	// No classification service, no publishing stats, no cache
	handler := NewDashboardOverviewHandler(repo, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 (graceful degradation), got %d", w.Code)
	}

	var response DashboardOverviewResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// Should have alert stats
	if response.TotalAlerts == 0 {
		t.Error("Expected alert stats even without other components")
	}

	// Should have defaults for classification
	if response.ClassificationEnabled {
		t.Error("Expected classification disabled when service unavailable")
	}

	// Should have defaults for publishing
	if response.PublishingMode != "unknown" {
		t.Errorf("Expected publishing mode 'unknown', got %s", response.PublishingMode)
	}
}

func TestDashboardOverviewHandler_GetOverview_AllComponents(t *testing.T) {
	historyResp := createTestHistoryResponse()
	repo := &mockAlertHistoryRepositoryForOverview{historyResp: historyResp}
	classificationService := &mockClassificationServiceForOverview{
		stats: services.ClassificationStats{
			TotalRequests: 100,
			CacheHitRate:  0.85,
		},
		health: nil,
	}
	publishingStats := &mockPublishingStatsProvider{
		targetCount:         3,
		mode:                "intelligent",
		successfulPublishes: 12500,
		failedPublishes:     25,
	}
	cache := &mockCacheForOverview{data: make(map[string]interface{})}
	handler := NewDashboardOverviewHandler(repo, classificationService, publishingStats, cache, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/overview", nil)
	w := httptest.NewRecorder()

	handler.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response DashboardOverviewResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// Verify all components are present
	if response.TotalAlerts == 0 {
		t.Error("Expected alert stats")
	}

	if !response.ClassificationEnabled {
		t.Error("Expected classification enabled")
	}

	if response.PublishingTargets == 0 {
		t.Error("Expected publishing stats")
	}
}

func TestPublishingStatsProvider_GetTargetCount(t *testing.T) {
	// Create a mock collector
	mockCollector := &MockCollectorForHandler{
		CollectAllFunc: func(ctx context.Context) *publishing.MetricsSnapshot {
			return &publishing.MetricsSnapshot{
				Timestamp: time.Now(),
				Metrics: map[string]float64{
					"discovery.total_targets": 3,
				},
			}
		},
	}

	provider := NewPublishingStatsProviderWithCollector(mockCollector, nil)

	if count := provider.GetTargetCount(); count != 3 {
		t.Errorf("Expected 3 targets, got %d", count)
	}
}

func TestPublishingStatsProvider_GetPublishingMode(t *testing.T) {
	// Test intelligent mode
	mockCollector1 := &MockCollectorForHandler{
		CollectAllFunc: func(ctx context.Context) *publishing.MetricsSnapshot {
			return &publishing.MetricsSnapshot{
				Timestamp: time.Now(),
				Metrics: map[string]float64{
					"discovery.total_targets": 3,
				},
			}
		},
	}
	provider1 := NewPublishingStatsProviderWithCollector(mockCollector1, nil)
	if mode := provider1.GetPublishingMode(); mode != "intelligent" {
		t.Errorf("Expected 'intelligent' mode, got %s", mode)
	}

	// Test metrics-only mode
	mockCollector2 := &MockCollectorForHandler{
		CollectAllFunc: func(ctx context.Context) *publishing.MetricsSnapshot {
			return &publishing.MetricsSnapshot{
				Timestamp: time.Now(),
				Metrics: map[string]float64{
					"discovery.total_targets": 0,
				},
			}
		},
	}
	provider2 := NewPublishingStatsProviderWithCollector(mockCollector2, nil)
	if mode := provider2.GetPublishingMode(); mode != "metrics-only" {
		t.Errorf("Expected 'metrics-only' mode, got %s", mode)
	}
}
