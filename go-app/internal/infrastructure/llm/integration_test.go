//go:build integration
// +build integration

package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// MockLLMServer creates a mock LLM server for integration testing.
type MockLLMServer struct {
	server *httptest.Server
	config Config
}

// NewMockLLMServer creates a new mock LLM server.
func NewMockLLMServer() *MockLLMServer {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"version": "1.0.0",
		})
	})

	// Classification endpoint
	mux.HandleFunc("/classify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var request ClassificationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Invalid JSON payload",
			})
			return
		}

		// Simulate processing delay
		time.Sleep(50 * time.Millisecond)

		// Generate mock classification based on alert
		classification := generateMockClassification(request.Alert)

		response := ClassificationResponse{
			Classification: classification,
			RequestID:      "mock-" + request.Alert.AlertName,
			ProcessingTime: "50ms",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Error simulation endpoint
	mux.HandleFunc("/classify-error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Internal server error",
		})
	})

	// Slow endpoint for timeout testing
	mux.HandleFunc("/classify-slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)

	config := DefaultConfig()
	config.BaseURL = server.URL

	return &MockLLMServer{
		server: server,
		config: config,
	}
}

// Close shuts down the mock server.
func (m *MockLLMServer) Close() {
	m.server.Close()
}

// URL returns the server URL.
func (m *MockLLMServer) URL() string {
	return m.server.URL
}

// Config returns the server configuration.
func (m *MockLLMServer) Config() Config {
	return m.config
}

// generateMockClassification generates a mock classification based on alert properties.
func generateMockClassification(alert Alert) Classification {
	severity := 3 // Default severity
	category := "infrastructure"

	// Adjust severity based on alert name
	switch alert.AlertName {
	case "HighCPUUsage":
		severity = 4
		category = "performance"
	case "DiskSpaceLow":
		severity = 3
		category = "storage"
	case "ServiceDown":
		severity = 5
		category = "availability"
	case "SecurityAlert":
		severity = 5
		category = "security"
	}

	// Adjust severity based on status
	if alert.Status == "resolved" {
		severity = max(1, severity-2)
	}

	confidence := 0.85
	if alert.Labels != nil {
		if _, ok := alert.Labels["severity"]; ok {
			confidence = 0.95 // Higher confidence if severity label exists
		}
	}

	return Classification{
		Severity:   severity,
		Category:   category,
		Summary:    "Mock classification for " + alert.AlertName,
		Confidence: confidence,
		Reasoning:  "Generated based on alert name and properties",
		Suggestions: []string{
			"Check system resources",
			"Review recent changes",
			"Monitor trends",
		},
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Integration tests
func TestLLMIntegration_FullWorkflow(t *testing.T) {
	// Start mock server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	// Create client
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	client := NewHTTPLLMClient(mockServer.Config(), logger)

	// Test health check
	ctx := context.Background()
	err := client.Health(ctx)
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}

	// Test alert classification
	alert := &Alert{
		AlertName: "HighCPUUsage",
		Status:    "firing",
		Labels: map[string]string{
			"instance": "server-01",
			"severity": "warning",
		},
		Annotations: map[string]string{
			"summary":     "High CPU usage detected",
			"description": "CPU usage is above 80% for more than 5 minutes",
		},
		StartsAt:    "2024-01-01T10:00:00Z",
		Fingerprint: "abc123def456",
	}

	classification, err := client.ClassifyAlert(ctx, alert)
	if err != nil {
		t.Fatalf("Classification failed: %v", err)
	}

	// Validate classification
	if classification.Severity < 1 || classification.Severity > 5 {
		t.Errorf("Invalid severity: %d", classification.Severity)
	}

	if classification.Category == "" {
		t.Error("Category should not be empty")
	}

	if classification.Confidence < 0.0 || classification.Confidence > 1.0 {
		t.Errorf("Invalid confidence: %f", classification.Confidence)
	}

	if len(classification.Suggestions) == 0 {
		t.Error("Suggestions should not be empty")
	}

	t.Logf("Classification result: %+v", classification)
}

func TestLLMIntegration_MultipleAlerts(t *testing.T) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // Reduce log noise
	}))

	client := NewHTTPLLMClient(mockServer.Config(), logger)

	alerts := []*Alert{
		{
			AlertName: "HighCPUUsage",
			Status:    "firing",
			Labels:    map[string]string{"severity": "warning"},
		},
		{
			AlertName: "DiskSpaceLow",
			Status:    "firing",
			Labels:    map[string]string{"severity": "critical"},
		},
		{
			AlertName: "ServiceDown",
			Status:    "firing",
			Labels:    map[string]string{"severity": "critical"},
		},
		{
			AlertName: "SecurityAlert",
			Status:    "resolved",
			Labels:    map[string]string{"severity": "high"},
		},
	}

	ctx := context.Background()

	for _, alert := range alerts {
		classification, err := client.ClassifyAlert(ctx, alert)
		if err != nil {
			t.Errorf("Classification failed for %s: %v", alert.AlertName, err)
			continue
		}

		t.Logf("Alert: %s, Severity: %d, Category: %s, Confidence: %.2f",
			alert.AlertName, classification.Severity, classification.Category, classification.Confidence)

		// Validate basic properties
		if classification.Severity < 1 || classification.Severity > 5 {
			t.Errorf("Invalid severity for %s: %d", alert.AlertName, classification.Severity)
		}
	}
}

func TestLLMIntegration_ConcurrentRequests(t *testing.T) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	client := NewHTTPLLMClient(mockServer.Config(), logger)

	const numRequests = 10
	results := make(chan error, numRequests)

	ctx := context.Background()

	// Send concurrent requests
	for i := 0; i < numRequests; i++ {
		go func(id int) {
			alert := &Alert{
				AlertName: "ConcurrentTest",
				Status:    "firing",
				Labels:    map[string]string{"id": string(rune(id))},
			}

			_, err := client.ClassifyAlert(ctx, alert)
			results <- err
		}(i)
	}

	// Collect results
	var errors []error
	for i := 0; i < numRequests; i++ {
		if err := <-results; err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		t.Errorf("Got %d errors out of %d requests: %v", len(errors), numRequests, errors[0])
	}
}

func TestLLMIntegration_ErrorHandling(t *testing.T) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	// Create client with error endpoint
	config := mockServer.Config()
	config.BaseURL = mockServer.URL() + "/classify-error"
	config.MaxRetries = 2
	config.RetryDelay = 10 * time.Millisecond

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	client := NewHTTPLLMClient(config, logger)

	alert := &Alert{
		AlertName: "ErrorTest",
		Status:    "firing",
	}

	ctx := context.Background()
	_, err := client.ClassifyAlert(ctx, alert)

	if err == nil {
		t.Error("Expected error from error endpoint, got nil")
	}
}

func TestLLMIntegration_Timeout(t *testing.T) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	// Create client with short timeout
	config := mockServer.Config()
	config.BaseURL = mockServer.URL() + "/classify-slow"
	config.Timeout = 100 * time.Millisecond
	config.MaxRetries = 0

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	client := NewHTTPLLMClient(config, logger)

	alert := &Alert{
		AlertName: "TimeoutTest",
		Status:    "firing",
	}

	ctx := context.Background()
	_, err := client.ClassifyAlert(ctx, alert)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// Benchmark integration test
func BenchmarkLLMIntegration_ClassifyAlert(b *testing.B) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	client := NewHTTPLLMClient(mockServer.Config(), logger)

	alert := &Alert{
		AlertName: "BenchmarkAlert",
		Status:    "firing",
		Labels:    map[string]string{"severity": "warning"},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.ClassifyAlert(ctx, alert)
		if err != nil {
			b.Fatalf("ClassifyAlert() error = %v", err)
		}
	}
}
