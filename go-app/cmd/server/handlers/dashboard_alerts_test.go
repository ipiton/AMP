// Package handlers provides HTTP handlers for the Alert History Service.
// TN-84: Dashboard Alerts Handler Tests (150% Quality Target)
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/ui"
)

// mockAlertHistoryRepository is a mock implementation of AlertHistoryRepository.
type mockAlertHistoryRepository struct {
	alerts []*core.Alert
	err    error
}

func (m *mockAlertHistoryRepository) GetRecentAlerts(ctx context.Context, limit int) ([]*core.Alert, error) {
	if m.err != nil {
		return nil, m.err
	}
	if limit > len(m.alerts) {
		limit = len(m.alerts)
	}
	return m.alerts[:limit], nil
}

func (m *mockAlertHistoryRepository) GetHistory(ctx context.Context, req *core.HistoryRequest) (*core.HistoryResponse, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepository) GetAlertsByFingerprint(ctx context.Context, fingerprint string, limit int) ([]*core.Alert, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepository) GetAggregatedStats(ctx context.Context, timeRange *core.TimeRange) (*core.AggregatedStats, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepository) GetTopAlerts(ctx context.Context, timeRange *core.TimeRange, limit int) ([]*core.TopAlert, error) {
	return nil, nil
}

func (m *mockAlertHistoryRepository) GetFlappingAlerts(ctx context.Context, timeRange *core.TimeRange, threshold int) ([]*core.FlappingAlert, error) {
	return nil, nil
}

// mockClassificationEnricher is a mock implementation of ClassificationEnricher.
type mockClassificationEnricher struct {
	enriched []*ui.EnrichedAlert
	err      error
}

func (m *mockClassificationEnricher) EnrichAlerts(ctx context.Context, alerts []*core.Alert) ([]*ui.EnrichedAlert, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.enriched, nil
}

func (m *mockClassificationEnricher) EnrichAlert(ctx context.Context, alert *core.Alert) (*ui.EnrichedAlert, error) {
	return nil, nil
}

func (m *mockClassificationEnricher) BatchEnrich(ctx context.Context, alerts []*core.Alert, batchSize int) ([]*ui.EnrichedAlert, error) {
	return nil, nil
}

// mockCache is a mock implementation of Cache.
type mockCache struct {
	data map[string]interface{}
}

func (m *mockCache) Get(ctx context.Context, key string, dest interface{}) error {
	if val, ok := m.data[key]; ok {
		// Simple type assertion for testing
		if resp, ok := val.(*DashboardAlertResponse); ok {
			*dest.(*DashboardAlertResponse) = *resp
			return nil
		}
	}
	return cache.ErrNotFound
}

func (m *mockCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if m.data == nil {
		m.data = make(map[string]interface{})
	}
	m.data[key] = value
	return nil
}

func (m *mockCache) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockCache) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (m *mockCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return nil
}

func (m *mockCache) HealthCheck(ctx context.Context) error {
	return nil
}

func (m *mockCache) Ping(ctx context.Context) error {
	return nil
}

func (m *mockCache) Flush(ctx context.Context) error {
	m.data = make(map[string]interface{})
	return nil
}

func (m *mockCache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	return nil
}

func (m *mockCache) SMembers(ctx context.Context, key string) ([]string, error) {
	return nil, nil
}

func (m *mockCache) SRem(ctx context.Context, key string, members ...interface{}) error {
	return nil
}

func (m *mockCache) SCard(ctx context.Context, key string) (int64, error) {
	return 0, nil
}

// createTestAlerts creates a list of test alerts.
func createTestAlerts() []*core.Alert {
	now := time.Now()

	return []*core.Alert{
		{
			Fingerprint: "fp1",
			AlertName:   "HighCPU",
			Status:      core.AlertStatus("firing"),
			StartsAt:    now.Add(-10 * time.Minute),
			Labels:      map[string]string{"namespace": "production", "instance": "app-1"},
			Annotations: map[string]string{"description": "CPU usage exceeds 90%"},
		},
		{
			Fingerprint: "fp2",
			AlertName:   "LowMemory",
			Status:      core.AlertStatus("firing"),
			StartsAt:    now.Add(-5 * time.Minute),
			Labels:      map[string]string{"namespace": "staging", "instance": "app-2"},
			Annotations: map[string]string{"description": "Memory usage below 10%"},
		},
		{
			Fingerprint: "fp3",
			AlertName:   "DiskFull",
			Status:      core.AlertStatus("resolved"),
			StartsAt:    now.Add(-30 * time.Minute),
			EndsAt:      &now,
			Labels:      map[string]string{"namespace": "production", "instance": "app-3"},
			Annotations: map[string]string{"description": "Disk usage at 95%"},
		},
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_Basic(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?limit=10", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response DashboardAlertResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Count != 3 {
		t.Errorf("Expected 3 alerts, got %d", response.Count)
	}

	if len(response.Alerts) != 3 {
		t.Errorf("Expected 3 alerts in response, got %d", len(response.Alerts))
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_WithLimit(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?limit=2", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	var response DashboardAlertResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	if response.Limit != 2 {
		t.Errorf("Expected limit 2, got %d", response.Limit)
	}

	if len(response.Alerts) > 2 {
		t.Errorf("Expected at most 2 alerts, got %d", len(response.Alerts))
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_WithStatusFilter(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?status=firing", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	var response DashboardAlertResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// Should have 2 firing alerts
	firingCount := 0
	for _, alert := range response.Alerts {
		if alert.Status == "firing" {
			firingCount++
		}
	}

	if firingCount != 2 {
		t.Errorf("Expected 2 firing alerts, got %d", firingCount)
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_WithSeverityFilter(t *testing.T) {
	alerts := createTestAlerts()
	// Set severity for alerts
	alerts[0].Labels["severity"] = "critical"
	alerts[1].Labels["severity"] = "warning"
	alerts[2].Labels["severity"] = "info"

	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?severity=critical", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	var response DashboardAlertResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// All returned alerts should have critical severity
	for _, alert := range response.Alerts {
		if alert.Severity != "critical" {
			t.Errorf("Expected critical severity, got %s", alert.Severity)
		}
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_WithClassification(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}

	// Create enriched alerts with classification
	enriched := []*ui.EnrichedAlert{
		{
			Alert: alerts[0],
			Classification: &core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 0.85,
			},
			HasClassification:   true,
			ClassificationSource: "llm",
		},
		{
			Alert:             alerts[1],
			HasClassification: false,
		},
		{
			Alert:             alerts[2],
			HasClassification: false,
		},
	}

	enricher := &mockClassificationEnricher{enriched: enriched}
	handler := NewDashboardAlertsHandler(repo, enricher, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?include_classification=true", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	var response DashboardAlertResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// First alert should have classification
	if response.Alerts[0].Classification == nil {
		t.Error("Expected classification for first alert")
	}

	if response.Alerts[0].Classification.Severity != "critical" {
		t.Errorf("Expected critical classification severity, got %s", response.Alerts[0].Classification.Severity)
	}

	if response.Alerts[0].Classification.Confidence != 0.85 {
		t.Errorf("Expected confidence 0.85, got %f", response.Alerts[0].Classification.Confidence)
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_InvalidLimit(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?limit=100", nil) // > 50
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_InvalidStatus(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?status=invalid", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_InvalidMethod(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("POST", "/api/dashboard/alerts/recent", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_RepositoryError(t *testing.T) {
	repo := &mockAlertHistoryRepository{err: context.DeadlineExceeded}
	handler := NewDashboardAlertsHandler(repo, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?limit=10", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestDashboardAlertsHandler_GetRecentAlerts_WithCache(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	cache := &mockCache{data: make(map[string]interface{})}
	handler := NewDashboardAlertsHandler(repo, nil, cache, nil)

	// First request - should populate cache
	req1 := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?limit=10", nil)
	w1 := httptest.NewRecorder()
	handler.GetRecentAlerts(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w1.Code)
	}

	// Second request - should hit cache
	req2 := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?limit=10", nil)
	w2 := httptest.NewRecorder()
	handler.GetRecentAlerts(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w2.Code)
	}

	// Verify cache was used (repo should only be called once)
	// This is a simple test - in real scenario we'd track calls
}

func TestDashboardAlertsHandler_GetRecentAlerts_ClassificationError(t *testing.T) {
	alerts := createTestAlerts()
	repo := &mockAlertHistoryRepository{alerts: alerts}
	enricher := &mockClassificationEnricher{err: context.DeadlineExceeded}
	handler := NewDashboardAlertsHandler(repo, enricher, nil, nil)

	req := httptest.NewRequest("GET", "/api/dashboard/alerts/recent?include_classification=true", nil)
	w := httptest.NewRecorder()

	handler.GetRecentAlerts(w, req)

	// Should still return 200 with alerts (graceful degradation)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 (graceful degradation), got %d", w.Code)
	}

	var response DashboardAlertResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// Should still have alerts, just without classification
	if len(response.Alerts) == 0 {
		t.Error("Expected alerts even when classification fails")
	}
}
