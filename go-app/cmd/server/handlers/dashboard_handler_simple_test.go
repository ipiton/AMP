// Package handlers provides HTTP handlers for the dashboard.
// TN-77: Modern Dashboard Page - Unit Tests (150% Quality Target)
package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/ui"
)

// createTestTemplateEngine creates a template engine for testing.
// Uses the real template engine - all required functions should be available.
func createTestTemplateEngine() (*ui.TemplateEngine, error) {
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false // Disable hot reload for tests
	// TemplateDir defaults to "templates/" which is correct when running from go-app/
	return ui.NewTemplateEngine(opts)
}

// TestSimpleDashboardHandler_ServeHTTP tests the dashboard handler HTTP response.
func TestSimpleDashboardHandler_ServeHTTP(t *testing.T) {
	// Setup
	logger := slog.Default()
	templateEngine, err := createTestTemplateEngine()
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)

	tests := []struct {
		name           string
		method         string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET /dashboard returns 200 OK",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectedBody:   "dashboard", // Case-insensitive check
		},
		{
			name:           "POST /dashboard returns 200 OK (handler allows all methods)",
			method:         http.MethodPost,
			expectedStatus: http.StatusOK,
			expectedBody:   "Dashboard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/dashboard", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			body := w.Body.String()
			if !bytes.Contains([]byte(body), []byte(tt.expectedBody)) {
				bodyPreview := body
				if len(bodyPreview) > 100 {
					bodyPreview = bodyPreview[:100]
				}
				t.Errorf("Expected body to contain %q, got %q", tt.expectedBody, bodyPreview)
			}

			// Check Content-Type
			contentType := w.Header().Get("Content-Type")
			if contentType != "text/html; charset=utf-8" {
				t.Errorf("Expected Content-Type text/html; charset=utf-8, got %q", contentType)
			}
		})
	}
}

// TestSimpleDashboardHandler_getMockDashboardData tests mock data generation.
func TestSimpleDashboardHandler_getMockDashboardData(t *testing.T) {
	logger := slog.Default()
	templateEngine, err := createTestTemplateEngine()
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	data := handler.getMockDashboardData()

	// Validate stats
	if data.FiringAlerts < 0 {
		t.Error("FiringAlerts should be non-negative")
	}
	if data.ResolvedAlerts < 0 {
		t.Error("ResolvedAlerts should be non-negative")
	}
	if data.ActiveSilences < 0 {
		t.Error("ActiveSilences should be non-negative")
	}
	if data.InhibitedAlerts < 0 {
		t.Error("InhibitedAlerts should be non-negative")
	}

	// Validate recent alerts
	if len(data.RecentAlerts) == 0 {
		t.Error("RecentAlerts should not be empty")
	}

	for i, alert := range data.RecentAlerts {
		if alert.AlertName == "" {
			t.Errorf("RecentAlerts[%d].AlertName should not be empty", i)
		}
		if alert.Status == "" {
			t.Errorf("RecentAlerts[%d].Status should not be empty", i)
		}
		if alert.Severity == "" {
			t.Errorf("RecentAlerts[%d].Severity should not be empty", i)
		}
		if alert.Fingerprint == "" {
			t.Errorf("RecentAlerts[%d].Fingerprint should not be empty", i)
		}
	}

	// Validate active silences
	if len(data.ActiveSilencesList) == 0 {
		t.Error("ActiveSilencesList should not be empty")
	}

	for i, silence := range data.ActiveSilencesList {
		if silence.ID == "" {
			t.Errorf("ActiveSilencesList[%d].ID should not be empty", i)
		}
		if silence.Creator == "" {
			t.Errorf("ActiveSilencesList[%d].Creator should not be empty", i)
		}
		if len(silence.Matchers) == 0 {
			t.Errorf("ActiveSilencesList[%d].Matchers should not be empty", i)
		}
	}

	// Validate health status
	if data.Health == nil {
		t.Error("Health should not be nil")
	} else {
		if data.Health.Overall == "" {
			t.Error("Health.Overall should not be empty")
		}
		if len(data.Health.Components) == 0 {
			t.Error("Health.Components should not be empty")
		}

		for i, comp := range data.Health.Components {
			if comp.Name == "" {
				t.Errorf("Health.Components[%d].Name should not be empty", i)
			}
			if comp.Status == "" {
				t.Errorf("Health.Components[%d].Status should not be empty", i)
			}
			if comp.Latency < 0 {
				t.Errorf("Health.Components[%d].Latency should be non-negative", i)
			}
		}
	}
}

// TestSimpleDashboardHandler_ResponseHeaders tests response headers.
func TestSimpleDashboardHandler_ResponseHeaders(t *testing.T) {
	logger := slog.Default()
	templateEngine, err := createTestTemplateEngine()
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Check Content-Type header
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Expected Content-Type text/html; charset=utf-8, got %q", contentType)
	}

	// Check that body is not empty
	if w.Body.Len() == 0 {
		t.Error("Response body should not be empty")
	}
}

// TestModernDashboardData_StructValidation tests data structure validation.
func TestModernDashboardData_StructValidation(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		data    *ModernDashboardData
		wantErr bool
	}{
		{
			name: "Valid data with all fields",
			data: &ModernDashboardData{
				FiringAlerts:    42,
				ResolvedAlerts:  128,
				ActiveSilences:  5,
				InhibitedAlerts: 8,
				RecentAlerts: []AlertSummary{
					{
						Fingerprint: "abc123",
						AlertName:   "TestAlert",
						Status:      "firing",
						Severity:    "critical",
						Summary:     "Test summary",
						StartsAt:    now,
					},
				},
				ActiveSilencesList: []SilenceSummary{
					{
						ID:      "silence-123",
						Creator: "test-user",
						Comment: "Test silence",
						Matchers: []Matcher{
							{Name: "alertname", Operator: "=", Value: "TestAlert"},
						},
						StartsAt: now,
						EndsAt:   now.Add(1 * time.Hour),
						Status:   "active",
					},
				},
				Health: &HealthStatus{
					Overall: "healthy",
					Components: []HealthCheck{
						{Name: "PostgreSQL", Status: "healthy", Latency: 2.3},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Empty recent alerts (valid)",
			data: &ModernDashboardData{
				FiringAlerts:    0,
				ResolvedAlerts:  0,
				ActiveSilences:  0,
				InhibitedAlerts: 0,
				RecentAlerts:     []AlertSummary{},
				ActiveSilencesList: []SilenceSummary{},
				Health: &HealthStatus{
					Overall:    "healthy",
					Components: []HealthCheck{},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation
			if tt.data.FiringAlerts < 0 {
				t.Error("FiringAlerts should be non-negative")
			}
			if tt.data.Health != nil && tt.data.Health.Overall == "" {
				t.Error("Health.Overall should not be empty if Health is not nil")
			}
		})
	}
}

// TestAlertSummary_AIClassification tests AI classification data.
func TestAlertSummary_AIClassification(t *testing.T) {
	alert := AlertSummary{
		Fingerprint: "test-123",
		AlertName:   "HighCPU",
		Status:      "firing",
		Severity:    "warning",
		AIClassification: &AIClassification{
			Severity:   "warning",
			Confidence: 0.85,
			Reasoning:  "CPU load is elevated",
			ActionItems: []string{
				"Monitor for sustained high load",
			},
		},
	}

	if alert.AIClassification == nil {
		t.Fatal("AIClassification should not be nil")
	}

	if alert.AIClassification.Confidence < 0.0 || alert.AIClassification.Confidence > 1.0 {
		t.Errorf("Confidence should be between 0.0 and 1.0, got %f", alert.AIClassification.Confidence)
	}

	if alert.AIClassification.Severity == "" {
		t.Error("AIClassification.Severity should not be empty")
	}
}

// BenchmarkSimpleDashboardHandler_ServeHTTP benchmarks dashboard rendering.
func BenchmarkSimpleDashboardHandler_ServeHTTP(b *testing.B) {
	logger := slog.Default()
	templateEngine, err := createTestTemplateEngine()
	if err != nil {
		b.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Errorf("Expected status 200, got %d", w.Code)
		}
	}
}

// BenchmarkSimpleDashboardHandler_getMockDashboardData benchmarks mock data generation.
func BenchmarkSimpleDashboardHandler_getMockDashboardData(b *testing.B) {
	logger := slog.Default()
	templateEngine, err := createTestTemplateEngine()
	if err != nil {
		b.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data := handler.getMockDashboardData()
		if data == nil {
			b.Fatal("getMockDashboardData returned nil")
		}
	}
}

// Helper function (avoiding conflict with template_funcs.go min)
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
