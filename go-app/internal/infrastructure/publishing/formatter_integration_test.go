// +build integration

package publishing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// TestIntegration_AlertmanagerFormat tests Alertmanager format against mock server
func TestIntegration_AlertmanagerFormat(t *testing.T) {
	// Create mock Alertmanager server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Parse body
		var payload []map[string]any
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)

		// Verify payload structure
		require.Len(t, payload, 1)
		alert := payload[0]

		// Verify required fields
		assert.NotEmpty(t, alert["fingerprint"])
		assert.NotEmpty(t, alert["status"])
		assert.NotEmpty(t, alert["startsAt"])
		assert.NotNil(t, alert["labels"])
		assert.NotNil(t, alert["annotations"])

		// Verify LLM classification injected
		annotations := alert["annotations"].(map[string]any)
		assert.Contains(t, annotations, "llm_severity")
		assert.Contains(t, annotations, "llm_confidence")

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create formatter
	formatter := NewAlertFormatter()

	// Create enriched alert
	enrichedAlert := createTestEnrichedAlert()

	// Format alert
	ctx := context.Background()
	result, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatAlertmanager)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify result structure
	alerts, ok := result["alerts"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, alerts, 1)

	alert := alerts[0]
	assert.Equal(t, "test-fingerprint-123", alert["fingerprint"])
	assert.Equal(t, "firing", alert["status"])

	// Simulate sending to server (integration test)
	payload, err := json.Marshal(alerts)
	require.NoError(t, err)

	resp, err := http.Post(server.URL+"/api/v2/alerts", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestIntegration_RootlyFormat tests Rootly incident format
func TestIntegration_RootlyFormat(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	ctx := context.Background()
	result, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatRootly)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify Rootly structure
	assert.Contains(t, result, "title")
	assert.Contains(t, result, "severity")
	assert.Contains(t, result, "description")

	// Verify LLM classification used
	assert.Equal(t, "critical", result["severity"])
	assert.Contains(t, result["description"], "High CPU utilization")
}

// TestIntegration_PagerDutyFormat tests PagerDuty event format
func TestIntegration_PagerDutyFormat(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	ctx := context.Background()
	result, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatPagerDuty)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify PagerDuty structure
	assert.Contains(t, result, "routing_key")
	assert.Contains(t, result, "event_action")
	assert.Contains(t, result, "dedup_key")
	assert.Contains(t, result, "payload")

	payload := result["payload"].(map[string]any)
	assert.Contains(t, payload, "summary")
	assert.Contains(t, payload, "severity")
	assert.Contains(t, payload, "source")
}

// TestIntegration_SlackFormat tests Slack blocks format
func TestIntegration_SlackFormat(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	ctx := context.Background()
	result, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatSlack)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify Slack structure
	assert.Contains(t, result, "blocks")
	assert.Contains(t, result, "text")

	blocks := result["blocks"].([]map[string]any)
	assert.NotEmpty(t, blocks)

	// First block should be header
	headerBlock := blocks[0]
	assert.Equal(t, "header", headerBlock["type"])
}

// TestIntegration_MiddlewareStack tests full middleware integration
func TestIntegration_MiddlewareStack(t *testing.T) {
	// Create components
	metrics := NewFormatterMetrics("test", "publishing")
	tracer := NewSimpleTracer(nil)
	validator := NewDefaultAlertValidator()
	cache := NewLRUCache(100, 5*time.Minute)

	// Build middleware stack
	baseFormatter := NewAlertFormatter()
	formatter := NewMiddlewareChain(
		baseFormatter,
		TracingValidationMiddleware(tracer, validator),
		TracingCacheMiddleware(tracer, cache, 5*time.Minute, nil),
		MetricsMiddleware(metrics),
		TracingMiddleware(tracer),
	)

	enrichedAlert := createTestEnrichedAlert()
	ctx := context.Background()

	// First call (cache miss)
	result1, err1 := formatter.FormatAlert(ctx, enrichedAlert, core.FormatAlertmanager)
	require.NoError(t, err1)
	assert.NotNil(t, result1)

	// Second call (cache hit)
	result2, err2 := formatter.FormatAlert(ctx, enrichedAlert, core.FormatAlertmanager)
	require.NoError(t, err2)
	assert.NotNil(t, result2)

	// Results should be identical
	assert.Equal(t, result1, result2)

	// Verify cache stats
	stats := cache.Stats()
	assert.Equal(t, int64(1), stats.Hits, "Second call should be cache hit")
	assert.Equal(t, int64(1), stats.Misses, "First call should be cache miss")
}

// TestIntegration_ValidationFailure tests validation error flow
func TestIntegration_ValidationFailure(t *testing.T) {
	validator := NewDefaultAlertValidator()
	baseFormatter := NewAlertFormatter()
	formatter := NewMiddlewareChain(
		baseFormatter,
		ValidationMiddleware(validator),
	)

	// Create invalid alert (missing required fields)
	invalidAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "", // Invalid: empty
			AlertName:   "", // Invalid: empty
			Status:      core.StatusFiring,
		},
	}

	ctx := context.Background()
	result, err := formatter.FormatAlert(ctx, invalidAlert, core.FormatAlertmanager)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "validation")
}

// TestIntegration_ConcurrentFormatting tests concurrent formatting
func TestIntegration_ConcurrentFormatting(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	const numGoroutines = 100
	const numRequestsPerGoroutine = 10

	ctx := context.Background()
	errors := make(chan error, numGoroutines*numRequestsPerGoroutine)

	// Launch concurrent formatting requests
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numRequestsPerGoroutine; j++ {
				_, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatAlertmanager)
				errors <- err
			}
		}()
	}

	// Collect results
	for i := 0; i < numGoroutines*numRequestsPerGoroutine; i++ {
		err := <-errors
		assert.NoError(t, err, "Concurrent formatting should not error")
	}
}

// TestIntegration_PerformanceBenchmark runs performance validation
func TestIntegration_PerformanceBenchmark(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()
	ctx := context.Background()

	// Target: < 500µs per format
	const targetDuration = 500 * time.Microsecond
	const numSamples = 1000

	var totalDuration time.Duration

	for i := 0; i < numSamples; i++ {
		start := time.Now()
		_, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatAlertmanager)
		duration := time.Since(start)

		require.NoError(t, err)
		totalDuration += duration
	}

	avgDuration := totalDuration / numSamples

	t.Logf("Average formatting duration: %v (target: %v)", avgDuration, targetDuration)
	assert.Less(t, avgDuration, targetDuration, "Average duration should be under target")
}

// Helper: createTestEnrichedAlert for integration tests
func createTestEnrichedAlert() *core.EnrichedAlert {
	now := time.Now()
	generatorURL := "http://prometheus/graph"

	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test-fingerprint-123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"alertname": "TestAlert",
				"severity":  "warning",
				"namespace": "production",
			},
			Annotations: map[string]string{
				"summary":     "Test alert",
				"description": "Test description",
			},
			StartsAt:     now,
			GeneratorURL: &generatorURL,
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityCritical,
			Confidence: 0.98,
			Reasoning:  "High CPU utilization detected",
			Recommendations: []string{
				"Check CPU usage",
				"Scale resources",
			},
		},
	}
}
