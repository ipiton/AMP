package publishing

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// ==================== WebhookMetrics Tests ====================

func TestNewWebhookMetrics_Creation(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewWebhookMetrics(registry)

	if metrics == nil {
		t.Fatal("Expected metrics, got nil")
	}
	if metrics.RequestsTotal == nil {
		t.Error("Expected RequestsTotal metric, got nil")
	}
	if metrics.RequestDuration == nil {
		t.Error("Expected RequestDuration metric, got nil")
	}
	if metrics.ErrorsTotal == nil {
		t.Error("Expected ErrorsTotal metric, got nil")
	}
	if metrics.RetriesTotal == nil {
		t.Error("Expected RetriesTotal metric, got nil")
	}
	if metrics.PayloadSize == nil {
		t.Error("Expected PayloadSize metric, got nil")
	}
	if metrics.AuthFailures == nil {
		t.Error("Expected AuthFailures metric, got nil")
	}
	if metrics.ValidationErrors == nil {
		t.Error("Expected ValidationErrors metric, got nil")
	}
	if metrics.TimeoutErrors == nil {
		t.Error("Expected TimeoutErrors metric, got nil")
	}
}

func TestWebhookMetrics_RecordRequest(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic with nil metrics
	metrics.RecordRequest("test-target", "success", "POST")
	metrics.RecordRequest("test-target", "error", "POST")
}

func TestWebhookMetrics_RecordDuration(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordDuration("test-target", "success", 0.123)
	metrics.RecordDuration("test-target", "error", 1.456)
}

func TestWebhookMetrics_RecordError(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordError("test-target", "validation")
	metrics.RecordError("test-target", "network")
	metrics.RecordError("test-target", "timeout")
}

func TestWebhookMetrics_RecordRetry(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordRetry("test-target", 1)
	metrics.RecordRetry("test-target", 2)
	metrics.RecordRetry("test-target", 3)
}

func TestWebhookMetrics_RecordPayloadSize(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordPayloadSize("test-target", 1024)
	metrics.RecordPayloadSize("test-target", 1024*1024)
}

func TestWebhookMetrics_RecordAuthFailure(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordAuthFailure("test-target", "bearer")
	metrics.RecordAuthFailure("test-target", "basic")
	metrics.RecordAuthFailure("test-target", "apikey")
}

func TestWebhookMetrics_RecordValidationError(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordValidationError("test-target", "url")
	metrics.RecordValidationError("test-target", "payload_size")
	metrics.RecordValidationError("test-target", "headers")
}

func TestWebhookMetrics_RecordTimeoutError(t *testing.T) {
	metrics := NewWebhookMetrics(nil)

	// Should not panic
	metrics.RecordTimeoutError("test-target")
}

func TestWebhookMetrics_NilSafety(t *testing.T) {
	// Create metrics without registry
	metrics := NewWebhookMetrics(nil)

	// All methods should be nil-safe
	metrics.RecordRequest("target", "status", "method")
	metrics.RecordDuration("target", "status", 0.5)
	metrics.RecordError("target", "type")
	metrics.RecordRetry("target", 1)
	metrics.RecordPayloadSize("target", 1000)
	metrics.RecordAuthFailure("target", "auth_type")
	metrics.RecordValidationError("target", "validation_type")
	metrics.RecordTimeoutError("target")

	// If we reach here, all methods are nil-safe
}

func TestWebhookMetrics_WithRegistry(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewWebhookMetrics(registry)

	// Record some metrics
	metrics.RecordRequest("test-webhook", "success", "POST")
	metrics.RecordDuration("test-webhook", "success", 0.123)
	metrics.RecordPayloadSize("test-webhook", 2048)

	// Gather metrics
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Check that metrics were registered
	if len(metricFamilies) == 0 {
		t.Error("Expected metrics to be registered, got 0")
	}

	// Check for specific metrics
	foundRequests := false
	foundDuration := false
	foundPayload := false

	for _, mf := range metricFamilies {
		switch *mf.Name {
		case "webhook_requests_total":
			foundRequests = true
		case "webhook_request_duration_seconds":
			foundDuration = true
		case "webhook_payload_size_bytes":
			foundPayload = true
		}
	}

	if !foundRequests {
		t.Error("webhook_requests_total metric not found")
	}
	if !foundDuration {
		t.Error("webhook_request_duration_seconds metric not found")
	}
	if !foundPayload {
		t.Error("webhook_payload_size_bytes metric not found")
	}
}
