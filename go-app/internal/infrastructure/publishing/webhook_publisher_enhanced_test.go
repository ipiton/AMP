package publishing

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ==================== Test Helpers ====================

func testHandler(statusCode int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(`{"status":"ok"}`))
	}
}

func authCheckHandler(expectedAuth string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != expectedAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func customHeadersHandler(headerName, expectedValue string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get(headerName)
		if value != expectedValue {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ==================== EnhancedWebhookPublisher Tests ====================

func TestEnhancedWebhookPublisher_Name(t *testing.T) {
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	validator := NewWebhookValidator(nil)
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics

	publisher := NewEnhancedWebhookPublisher(client, validator, formatter, metrics, slog.Default())

	if publisher.Name() != "EnhancedWebhook" {
		t.Errorf("Expected name=EnhancedWebhook, got %s", publisher.Name())
	}
}

func TestEnhancedWebhookPublisher_Publish_Success(t *testing.T) {
	// Create test server
	server := httptest.NewServer(testHandler(200))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	validator := NewWebhookValidator(nil)
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics

	publisher := NewEnhancedWebhookPublisher(client, validator, formatter, metrics, slog.Default())

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test123",
			AlertName:   "TestAlert",
			Status:      "firing",
			Labels: map[string]string{
				"env":      "prod",
				"severity": "critical",
			},
		},
	}

	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    server.URL,
		Format: core.FormatWebhook,
	}

	ctx := context.Background()
	err := publisher.Publish(ctx, enrichedAlert, target)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestEnhancedWebhookPublisher_Publish_ValidationError(t *testing.T) {
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	validator := NewWebhookValidator(nil)
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics

	publisher := NewEnhancedWebhookPublisher(client, validator, formatter, metrics, slog.Default())

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test123",
			AlertName:   "TestAlert",
		},
	}

	// Invalid target (HTTP instead of HTTPS)
	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    "http://insecure.example.com/webhook",
		Format: core.FormatWebhook,
	}

	ctx := context.Background()
	err := publisher.Publish(ctx, enrichedAlert, target)

	if err == nil {
		t.Error("Expected validation error for HTTP URL, got nil")
	}
}

func TestEnhancedWebhookPublisher_Publish_WithBearerAuth(t *testing.T) {
	// Create test server that checks auth
	server := httptest.NewServer(authCheckHandler("Bearer test_token"))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	validator := NewWebhookValidator(nil)
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics

	publisher := NewEnhancedWebhookPublisher(client, validator, formatter, metrics, slog.Default())

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test123",
			AlertName:   "TestAlert",
		},
	}

	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    server.URL,
		Format: core.FormatWebhook,
		Headers: map[string]string{
			"Authorization": "Bearer test_token",
		},
	}

	ctx := context.Background()
	err := publisher.Publish(ctx, enrichedAlert, target)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestEnhancedWebhookPublisher_Publish_WithCustomHeaders(t *testing.T) {
	// Create test server that checks custom headers
	server := httptest.NewServer(customHeadersHandler("X-Custom-Auth", "custom_value"))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	validator := NewWebhookValidator(nil)
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics

	publisher := NewEnhancedWebhookPublisher(client, validator, formatter, metrics, slog.Default())

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test123",
			AlertName:   "TestAlert",
		},
	}

	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    server.URL,
		Format: core.FormatWebhook,
		Headers: map[string]string{
			"X-Custom-Auth": "custom_value",
		},
	}

	ctx := context.Background()
	err := publisher.Publish(ctx, enrichedAlert, target)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

// ==================== Factory Methods Tests ====================

func TestNewEnhancedWebhookPublisherWithDefaults(t *testing.T) {
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics
	logger := slog.Default()

	publisher := NewEnhancedWebhookPublisherWithDefaults(formatter, metrics, logger)

	if publisher == nil {
		t.Fatal("Expected publisher, got nil")
	}
	if publisher.Name() != "EnhancedWebhook" {
		t.Errorf("Expected name=EnhancedWebhook, got %s", publisher.Name())
	}
}

func TestNewEnhancedWebhookPublisherWithRetry(t *testing.T) {
	retryConfig := WebhookRetryConfig{
		MaxRetries:  5,
		BaseBackoff: 50,
		MaxBackoff:  10000,
		Multiplier:  3.0,
	}
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics
	logger := slog.Default()

	publisher := NewEnhancedWebhookPublisherWithRetry(retryConfig, formatter, metrics, logger)

	if publisher == nil {
		t.Fatal("Expected publisher, got nil")
	}
}

func TestNewEnhancedWebhookPublisherWithValidation(t *testing.T) {
	validationConfig := ValidationConfig{
		MaxPayloadSize: 512 * 1024, // 512 KB
		MaxHeaders:     50,
		MaxHeaderSize:  2 * 1024,
	}
	formatter := NewAlertFormatter()
	var metrics *v2.PublishingMetrics
	logger := slog.Default()

	publisher := NewEnhancedWebhookPublisherWithValidation(validationConfig, formatter, metrics, logger)

	if publisher == nil {
		t.Fatal("Expected publisher, got nil")
	}
}
