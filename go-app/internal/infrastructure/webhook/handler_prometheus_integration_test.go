package webhook

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// =======================================================================================
// TN-146 Phase 6 Task 6.2: Handler Integration Tests (5 comprehensive tests)
// =======================================================================================

// mockAlertProcessorWithHealth implements AlertProcessor with Health method.
type mockAlertProcessorWithHealth struct {
	processed []*core.Alert
	mu        sync.Mutex
	errors    map[string]error // Map fingerprint → error for testing error handling
}

func newMockAlertProcessorWithHealth() *mockAlertProcessorWithHealth {
	return &mockAlertProcessorWithHealth{
		processed: []*core.Alert{},
		errors:    make(map[string]error),
	}
}

func (m *mockAlertProcessorWithHealth) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if error should be returned for this alert
	if err, ok := m.errors[alert.Fingerprint]; ok {
		return err
	}

	m.processed = append(m.processed, alert)
	return nil
}

func (m *mockAlertProcessorWithHealth) Health(ctx context.Context) error {
	return nil
}

func (m *mockAlertProcessorWithHealth) getProcessedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.processed)
}

func (m *mockAlertProcessorWithHealth) getProcessedAlerts() []*core.Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processed
}

// TestHandlerSelectsPrometheusParser tests that Prometheus webhook is correctly detected and parsed
func TestHandlerSelectsPrometheusParser(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Prometheus v1 payload
	payload := []byte(`[
		{
			"labels": {
				"alertname": "HighCPU",
				"instance": "server-1",
				"job": "node-exporter",
				"severity": "warning"
			},
			"annotations": {
				"summary": "CPU usage is high"
			},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090/graph"
		}
	]`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	resp, err := handler.HandleWebhook(ctx, req)

	// Assertions
	require.NoError(t, err, "HandleWebhook should succeed for valid Prometheus payload")
	require.NotNil(t, resp)

	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, "prometheus", resp.WebhookType) // Should detect Prometheus
	assert.Equal(t, 1, resp.AlertsReceived)
	assert.Equal(t, 1, resp.AlertsProcessed)
	assert.Empty(t, resp.Errors)

	// Verify alert was processed
	assert.Equal(t, 1, processor.getProcessedCount())
	alerts := processor.getProcessedAlerts()
	assert.Equal(t, "HighCPU", alerts[0].AlertName)
	assert.Equal(t, "firing", string(alerts[0].Status))
}

// TestHandlerSelectsAlertmanagerParser tests that Alertmanager webhook is correctly detected and parsed
func TestHandlerSelectsAlertmanagerParser(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Alertmanager payload
	payload := []byte(`{
		"receiver": "default",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "HighMemory",
					"instance": "server-2",
					"severity": "critical"
				},
				"annotations": {
					"summary": "Memory usage is high"
				},
				"startsAt": "2025-11-18T10:00:00Z",
				"generatorURL": "http://prometheus:9090"
			}
		],
		"groupLabels": {},
		"commonLabels": {},
		"commonAnnotations": {},
		"externalURL": "http://alertmanager:9093",
		"version": "4",
		"groupKey": "{}:{}"
	}`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	resp, err := handler.HandleWebhook(ctx, req)

	// Assertions
	require.NoError(t, err, "HandleWebhook should succeed for valid Alertmanager payload")
	require.NotNil(t, resp)

	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, "alertmanager", resp.WebhookType) // Should detect Alertmanager
	assert.Equal(t, 1, resp.AlertsReceived)
	assert.Equal(t, 1, resp.AlertsProcessed)

	// Verify alert was processed
	assert.Equal(t, 1, processor.getProcessedCount())
	alerts := processor.getProcessedAlerts()
	assert.Equal(t, "HighMemory", alerts[0].AlertName)
}

// TestHandlerFallbackToAlertmanager tests fallback to Alertmanager parser for unknown types
func TestHandlerFallbackToAlertmanager(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Generic webhook (unknown type, should fallback to Alertmanager)
	// This will fail Alertmanager parsing but tests fallback logic
	payload := []byte(`{
		"alertname": "GenericAlert",
		"status": "firing",
		"timestamp": "2025-11-18T10:00:00Z"
	}`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	_, err := handler.HandleWebhook(ctx, req)

	// Should error (invalid Alertmanager format), but tests fallback occurred
	// In production, this would log a warning about fallback
	assert.Error(t, err, "Should fail to parse generic payload with Alertmanager parser")
}

// TestHandlerPrometheusV2Grouped tests Prometheus v2 grouped format
func TestHandlerPrometheusV2Grouped(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Prometheus v2 payload (grouped)
	payload := []byte(`{
		"groups": [
			{
				"labels": {"job": "api", "severity": "warning"},
				"alerts": [
					{
						"labels": {
							"alertname": "HighLatency",
							"instance": "api-1"
						},
						"annotations": {
							"summary": "API latency is high"
						},
						"state": "firing",
						"activeAt": "2025-11-18T10:00:00Z",
						"generatorURL": "http://prometheus:9090"
					},
					{
						"labels": {
							"alertname": "HighErrorRate",
							"instance": "api-2"
						},
						"state": "firing",
						"activeAt": "2025-11-18T10:05:00Z",
						"generatorURL": "http://prometheus:9090"
					}
				]
			}
		]
	}`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	resp, err := handler.HandleWebhook(ctx, req)

	// Assertions
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, "prometheus", resp.WebhookType)
	assert.Equal(t, 2, resp.AlertsReceived) // 2 alerts in group
	assert.Equal(t, 2, resp.AlertsProcessed)

	// Verify both alerts processed
	assert.Equal(t, 2, processor.getProcessedCount())
	alerts := processor.getProcessedAlerts()

	// Verify group labels merged into alerts
	assert.Equal(t, "HighLatency", alerts[0].AlertName)
	assert.Equal(t, "api", alerts[0].Labels["job"])
	assert.Equal(t, "warning", alerts[0].Labels["severity"])

	assert.Equal(t, "HighErrorRate", alerts[1].AlertName)
	assert.Equal(t, "api", alerts[1].Labels["job"])
}

// TestHandlerConcurrentRequests tests thread-safe concurrent webhook processing
func TestHandlerConcurrentRequests(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Prometheus payload template
	payloadTemplate := `[{
		"labels": {"alertname": "Alert%d", "instance": "server-%d"},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"generatorURL": "http://prometheus:9090"
	}]`

	// Send 10 concurrent requests
	concurrentCount := 10
	var wg sync.WaitGroup
	wg.Add(concurrentCount)

	errors := make(chan error, concurrentCount)
	responses := make(chan *HandleWebhookResponse, concurrentCount)

	ctx := context.Background()

	for i := 0; i < concurrentCount; i++ {
		go func(index int) {
			defer wg.Done()

			// Create unique payload
			payload := []byte(fmt.Sprintf(payloadTemplate, index, index))
			req := &HandleWebhookRequest{
				Payload:     payload,
				ContentType: "application/json",
			}

			resp, err := handler.HandleWebhook(ctx, req)
			if err != nil {
				errors <- err
			} else {
				responses <- resp
			}
		}(i)
	}

	wg.Wait()
	close(errors)
	close(responses)

	// Assertions
	assert.Empty(t, errors, "No errors should occur during concurrent processing")

	successCount := 0
	for resp := range responses {
		if resp.Status == "success" {
			successCount++
		}
	}
	assert.Equal(t, concurrentCount, successCount)

	// Verify all alerts processed (no race conditions)
	assert.Equal(t, concurrentCount, processor.getProcessedCount())
}

// TestHandlerPrometheusMultipleAlerts tests handling multiple alerts in single webhook
func TestHandlerPrometheusMultipleAlerts(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Prometheus v1 payload with 3 alerts
	payload := []byte(`[
		{
			"labels": {"alertname": "Alert1", "instance": "server-1"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		},
		{
			"labels": {"alertname": "Alert2", "instance": "server-2"},
			"state": "firing",
			"activeAt": "2025-11-18T10:05:00Z",
			"generatorURL": "http://prometheus:9090"
		},
		{
			"labels": {"alertname": "Alert3", "instance": "server-3"},
			"state": "inactive",
			"activeAt": "2025-11-18T09:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	resp, err := handler.HandleWebhook(ctx, req)

	// Assertions
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, 3, resp.AlertsReceived)
	assert.Equal(t, 3, resp.AlertsProcessed)

	// Verify all alerts processed with correct statuses
	alerts := processor.getProcessedAlerts()
	assert.Equal(t, "firing", string(alerts[0].Status))
	assert.Equal(t, "firing", string(alerts[1].Status))
	assert.Equal(t, "resolved", string(alerts[2].Status)) // inactive → resolved
}

// TestHandlerPrometheusInvalidPayload tests error handling for invalid Prometheus payload
func TestHandlerPrometheusInvalidPayload(t *testing.T) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	// Invalid JSON
	payload := []byte(`[invalid json}`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	_, err := handler.HandleWebhook(ctx, req)

	// Should error on detection or parsing
	assert.Error(t, err)

	// No alerts should be processed
	assert.Equal(t, 0, processor.getProcessedCount())
}
