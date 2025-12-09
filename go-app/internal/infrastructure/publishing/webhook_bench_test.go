package publishing

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

// ==================== Benchmarks ====================

// BenchmarkWebhookRetryConfig_CalculateBackoff benchmarks backoff calculation
func BenchmarkWebhookRetryConfig_CalculateBackoff(b *testing.B) {
	config := DefaultWebhookRetryConfig

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.CalculateBackoff(i % 10)
	}
}

// BenchmarkBearerAuthStrategy_Apply benchmarks bearer token auth
func BenchmarkBearerAuthStrategy_Apply(b *testing.B) {
	strategy := &BearerAuthStrategy{}
	config := AuthConfig{
		Type:  AuthTypeBearer,
		Token: "test_token_1234567890",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchRequest()
		_ = strategy.ApplyAuth(req, config)
	}
}

// BenchmarkBasicAuthStrategy_Apply benchmarks basic auth
func BenchmarkBasicAuthStrategy_Apply(b *testing.B) {
	strategy := &BasicAuthStrategy{}
	config := AuthConfig{
		Type:     AuthTypeBasic,
		Username: "admin",
		Password: "secret123",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchRequest()
		_ = strategy.ApplyAuth(req, config)
	}
}

// BenchmarkAPIKeyAuthStrategy_Apply benchmarks API key auth
func BenchmarkAPIKeyAuthStrategy_Apply(b *testing.B) {
	strategy := &APIKeyAuthStrategy{}
	config := AuthConfig{
		Type:         AuthTypeAPIKey,
		APIKey:       "api_key_xyz",
		APIKeyHeader: "X-API-Key",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newBenchRequest()
		_ = strategy.ApplyAuth(req, config)
	}
}

// BenchmarkWebhookValidator_ValidateURL benchmarks URL validation
func BenchmarkWebhookValidator_ValidateURL(b *testing.B) {
	validator := NewWebhookValidator(nil)
	url := "https://api.example.com/webhook"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateURL(url)
	}
}

// BenchmarkWebhookValidator_ValidatePayloadSize benchmarks payload size validation
func BenchmarkWebhookValidator_ValidatePayloadSize(b *testing.B) {
	validator := NewWebhookValidator(nil)
	payload := make([]byte, 1024) // 1 KB payload

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidatePayloadSize(payload)
	}
}

// BenchmarkWebhookValidator_ValidateHeaders benchmarks header validation
func BenchmarkWebhookValidator_ValidateHeaders(b *testing.B) {
	validator := NewWebhookValidator(nil)
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer token",
		"X-Custom":      "value",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateHeaders(headers)
	}
}

// BenchmarkWebhookMetrics_RecordRequest benchmarks metrics recording
// NOTE: Metrics migrated to pkg/metrics/v2, benchmark kept for reference
func BenchmarkWebhookMetrics_RecordRequest(b *testing.B) {
	// Benchmark is now a no-op (metrics in v2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// No-op
	}
}

// BenchmarkEnhancedWebhookPublisher_Publish benchmarks full publish flow
func BenchmarkEnhancedWebhookPublisher_Publish(b *testing.B) {
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	validator := NewWebhookValidator(nil)
	formatter := NewAlertFormatter()
	logger := slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError}))

	publisher := NewEnhancedWebhookPublisher(client, validator, formatter, nil, logger)

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "bench123",
			AlertName:   "BenchAlert",
			Status:      "firing",
			Labels: map[string]string{
				"env": "prod",
			},
		},
	}

	target := &core.PublishingTarget{
		Name:   "bench-webhook",
		Type:   "webhook",
		URL:    "https://httpbin.org/post",
		Format: core.FormatWebhook,
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Note: This will make real HTTP calls in benchmark
		// In production benchmarks, use mock server
		_ = publisher.Publish(ctx, enrichedAlert, target)
	}
}

// BenchmarkWebhookValidator_ValidateTarget benchmarks full target validation
func BenchmarkWebhookValidator_ValidateTarget(b *testing.B) {
	validator := NewWebhookValidator(nil)
	target := &core.PublishingTarget{
		Name: "bench-webhook",
		Type: "webhook",
		URL:  "https://api.example.com/webhook",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateTarget(target)
	}
}

// BenchmarkAlertFormatter_FormatAlert benchmarks alert formatting
func BenchmarkAlertFormatter_FormatAlert(b *testing.B) {
	formatter := NewAlertFormatter()
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "bench123",
			AlertName:   "BenchAlert",
			Status:      "firing",
			Labels: map[string]string{
				"env":      "prod",
				"severity": "critical",
			},
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = formatter.FormatAlert(ctx, enrichedAlert, core.FormatWebhook)
	}
}

// ==================== Helper Functions ====================

func newBenchRequest() *http.Request {
	req, _ := http.NewRequest("POST", "https://api.example.com/webhook", nil)
	return req
}
