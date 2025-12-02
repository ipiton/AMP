package publishing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// TestHttpConnectivityTest_Success tests successful HTTP connectivity.
func TestHttpConnectivityTest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	success, statusCode, latency, errMsg, errType := httpConnectivityTest(
		context.Background(),
		server.URL,
		client,
		config,
	)

	if !success {
		t.Error("Expected successful connectivity test")
	}
	if statusCode == nil || *statusCode != 200 {
		t.Errorf("Expected status code 200, got %v", statusCode)
	}
	if latency == nil {
		t.Error("Expected latency to be set")
	}
	if errMsg != nil {
		t.Errorf("Expected no error message, got %v", *errMsg)
	}
	if errType != "" {
		t.Errorf("Expected no error type, got %s", errType)
	}
}

// TestHttpConnectivityTest_NonOKStatus tests non-2xx status codes.
func TestHttpConnectivityTest_NonOKStatus(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			config := DefaultHealthConfig()
			client := &http.Client{Timeout: 5 * time.Second}

			success, statusCode, _, errMsg, errType := httpConnectivityTest(
				context.Background(),
				server.URL,
				client,
				config,
			)

			if success {
				t.Error("Expected failed connectivity test")
			}
			if statusCode == nil || *statusCode != tt.statusCode {
				t.Errorf("Expected status code %d, got %v", tt.statusCode, statusCode)
			}
			if errMsg == nil {
				t.Error("Expected error message")
			}
			if errType != ErrorTypeHTTP {
				t.Errorf("Expected ErrorTypeHTTP, got %s", errType)
			}
		})
	}
}

// TestHttpConnectivityTest_InvalidURL tests invalid URL handling.
func TestHttpConnectivityTest_InvalidURL(t *testing.T) {
	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	success, _, _, errMsg, errType := httpConnectivityTest(
		context.Background(),
		"://invalid-url",
		client,
		config,
	)

	if success {
		t.Error("Expected failed connectivity test for invalid URL")
	}
	if errMsg == nil {
		t.Error("Expected error message")
	}
	if errType != ErrorTypeUnknown {
		t.Errorf("Expected ErrorTypeUnknown, got %s", errType)
	}
}

// TestHttpConnectivityTest_Timeout tests timeout handling.
func TestHttpConnectivityTest_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // Slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := DefaultHealthConfig()
	config.HTTPTimeout = 50 * time.Millisecond // Very short timeout
	client := &http.Client{Timeout: config.HTTPTimeout}

	success, _, _, errMsg, _ := httpConnectivityTest(
		context.Background(),
		server.URL,
		client,
		config,
	)

	if success {
		t.Error("Expected timeout failure")
	}
	if errMsg == nil {
		t.Error("Expected error message for timeout")
	}
}

// TestCheckSingleTarget tests single target check.
func TestCheckSingleTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name:    "test-target",
		URL:     server.URL,
		Enabled: true,
	}

	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	result := checkSingleTarget(
		context.Background(),
		target,
		CheckTypePeriodic,
		client,
		config,
	)

	if !result.Success {
		t.Error("Expected successful check")
	}
	if result.TargetName != "test-target" {
		t.Errorf("Expected target name 'test-target', got '%s'", result.TargetName)
	}
	if result.CheckType != CheckTypePeriodic {
		t.Errorf("Expected CheckTypePeriodic, got %s", result.CheckType)
	}
}

// TestCheckSingleTarget_EmptyURL tests empty URL handling.
func TestCheckSingleTarget_EmptyURL(t *testing.T) {
	target := &core.PublishingTarget{
		Name:    "empty-url-target",
		URL:     "",
		Enabled: true,
	}

	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	result := checkSingleTarget(
		context.Background(),
		target,
		CheckTypePeriodic,
		client,
		config,
	)

	if result.Success {
		t.Error("Expected failed check for empty URL")
	}
	if result.ErrorMessage == nil {
		t.Error("Expected error message")
	}
}

// TestCheckTargetWithRetry_Success tests successful check without retry.
func TestCheckTargetWithRetry_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name:    "retry-target",
		URL:     server.URL,
		Enabled: true,
	}

	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	result := checkTargetWithRetry(
		context.Background(),
		target,
		CheckTypePeriodic,
		client,
		config,
	)

	if !result.Success {
		t.Error("Expected successful check")
	}
}

// TestCheckTargetWithRetry_PermanentError tests permanent error (no retry).
func TestCheckTargetWithRetry_PermanentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name:    "permanent-error-target",
		URL:     server.URL,
		Enabled: true,
	}

	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	result := checkTargetWithRetry(
		context.Background(),
		target,
		CheckTypePeriodic,
		client,
		config,
	)

	// Permanent error (HTTP 400), should NOT retry
	if result.Success {
		t.Error("Expected failed check for permanent error")
	}
	if result.ErrorType == nil || *result.ErrorType != ErrorTypeHTTP {
		t.Error("Expected ErrorTypeHTTP")
	}
}

// TestCheckTargetWithRetry_ContextCancelled tests context cancellation.
func TestCheckTargetWithRetry_ContextCancelled(t *testing.T) {
	target := &core.PublishingTarget{
		Name:    "cancelled-target",
		URL:     "http://192.0.2.1:1", // Non-routable IP (will hang)
		Enabled: true,
	}

	config := DefaultHealthConfig()
	client := &http.Client{Timeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := checkTargetWithRetry(ctx, target, CheckTypePeriodic, client, config)

	// Should fail immediately
	if result.Success {
		t.Error("Expected failed check for cancelled context")
	}
}
