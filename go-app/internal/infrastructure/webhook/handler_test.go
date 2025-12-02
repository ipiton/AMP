package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// Mock AlertProcessor for testing
type mockAlertProcessor struct {
	processAlertFunc func(ctx context.Context, alert *core.Alert) error
	processedAlerts  []*core.Alert
}

func (m *mockAlertProcessor) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	m.processedAlerts = append(m.processedAlerts, alert)
	if m.processAlertFunc != nil {
		return m.processAlertFunc(ctx, alert)
	}
	return nil
}

func (m *mockAlertProcessor) Health(ctx context.Context) error {
	return nil // Mock always healthy
}

func TestNewUniversalWebhookHandler(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, nil)

	require.NotNil(t, handler)
	assert.NotNil(t, handler.detector)
	assert.NotNil(t, handler.parsers) // TN-146: Changed from parser to parsers map
	assert.Contains(t, handler.parsers, WebhookTypeAlertmanager)
	assert.Contains(t, handler.parsers, WebhookTypePrometheus) // TN-146: Verify Prometheus parser registered
	assert.NotNil(t, handler.validator)
	assert.NotNil(t, handler.processor)
	assert.NotNil(t, handler.metrics)
	assert.NotNil(t, handler.logger)
}

func TestHandleWebhook_Success(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "TestAlert",
					"severity": "critical"
				},
				"annotations": {
					"summary": "Test alert"
				},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()
	response, err := handler.HandleWebhook(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "success", response.Status)
	assert.Equal(t, "alertmanager", response.WebhookType)
	assert.Equal(t, 1, response.AlertsReceived)
	assert.Equal(t, 1, response.AlertsProcessed)
	assert.Empty(t, response.Errors)
	assert.Len(t, processor.processedAlerts, 1)
	assert.Equal(t, "TestAlert", processor.processedAlerts[0].AlertName)
}

func TestHandleWebhook_MultipleAlerts(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "Alert1"},
				"startsAt": "2025-01-10T10:00:00Z"
			},
			{
				"status": "resolved",
				"labels": {"alertname": "Alert2"},
				"startsAt": "2025-01-10T10:00:00Z",
				"endsAt": "2025-01-10T11:00:00Z"
			},
			{
				"status": "firing",
				"labels": {"alertname": "Alert3"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()
	response, err := handler.HandleWebhook(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, "success", response.Status)
	assert.Equal(t, 3, response.AlertsReceived)
	assert.Equal(t, 3, response.AlertsProcessed)
	assert.Len(t, processor.processedAlerts, 3)
}

func TestHandleWebhook_InvalidJSON(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{invalid json`)
	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	response, err := handler.HandleWebhook(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "detection failed")
}

func TestHandleWebhook_EmptyPayload(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte{}
	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	response, err := handler.HandleWebhook(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, response)
}

func TestHandleWebhook_ValidationFailure(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	// Payload missing required fields
	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"alerts": []
	}`)

	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	response, err := handler.HandleWebhook(ctx, req)
	assert.Error(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "validation_failed", response.Status)
	assert.NotEmpty(t, response.Errors)
}

func TestHandleWebhook_ParsingFailure(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	// Valid JSON but invalid Alertmanager structure
	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"alerts": "not_an_array"
	}`)

	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	_, err := handler.HandleWebhook(ctx, req)
	assert.Error(t, err)
	// Parser should fail
}

func TestHandleWebhook_ProcessingError(t *testing.T) {
	// Processor that fails for specific alerts
	processor := &mockAlertProcessor{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			if alert.AlertName == "FailingAlert" {
				return fmt.Errorf("simulated processing error")
			}
			return nil
		},
	}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "GoodAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			},
			{
				"status": "firing",
				"labels": {"alertname": "FailingAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			},
			{
				"status": "firing",
				"labels": {"alertname": "AnotherGoodAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	response, err := handler.HandleWebhook(ctx, req)
	require.NoError(t, err) // Handler doesn't return error for partial failures
	require.NotNil(t, response)
	assert.Equal(t, "partial_success", response.Status)
	assert.Equal(t, 3, response.AlertsReceived)
	assert.Equal(t, 2, response.AlertsProcessed) // 2 succeeded, 1 failed
	assert.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0], "FailingAlert")
}

func TestHandleWebhook_AllAlertsFailProcessing(t *testing.T) {
	processor := &mockAlertProcessor{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			return fmt.Errorf("all alerts fail")
		},
	}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "Alert1"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	response, err := handler.HandleWebhook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "failure", response.Status)
	assert.Equal(t, 1, response.AlertsReceived)
	assert.Equal(t, 0, response.AlertsProcessed)
	assert.Len(t, response.Errors, 1)
}

func TestHandleWebhook_ContextCancellation(t *testing.T) {
	processor := &mockAlertProcessor{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			// Check if context is cancelled
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				time.Sleep(100 * time.Millisecond) // Simulate work
				return nil
			}
		},
	}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "SlowAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	req := &HandleWebhookRequest{Payload: payload}

	// Create context with immediate cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	response, err := handler.HandleWebhook(ctx, req)
	require.NoError(t, err) // Handler completes, but processing fails
	require.NotNil(t, response)
	assert.Equal(t, "failure", response.Status)
	assert.Equal(t, 0, response.AlertsProcessed)
}

func TestHandleWebhookSync_Success(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "TestAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	ctx := context.Background()
	jsonResponse, statusCode, err := handler.HandleWebhookSync(ctx, payload)

	require.NoError(t, err)
	assert.Equal(t, 200, statusCode)
	assert.NotEmpty(t, jsonResponse)

	// Verify JSON structure
	assert.Contains(t, string(jsonResponse), `"status":"success"`)
	assert.Contains(t, string(jsonResponse), `"webhook_type":"alertmanager"`)
}

func TestHandleWebhookSync_ValidationError(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	// Invalid payload (empty alerts)
	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"alerts": []
	}`)

	ctx := context.Background()
	jsonResponse, statusCode, err := handler.HandleWebhookSync(ctx, payload)

	assert.Error(t, err)
	assert.Equal(t, 400, statusCode) // Bad Request
	assert.NotEmpty(t, jsonResponse)
	assert.Contains(t, string(jsonResponse), `"status":"validation_failed"`)
}

func TestHandleWebhookSync_PartialSuccess(t *testing.T) {
	processor := &mockAlertProcessor{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			if alert.AlertName == "FailAlert" {
				return fmt.Errorf("fail")
			}
			return nil
		},
	}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"receiver": "default",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "GoodAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			},
			{
				"status": "firing",
				"labels": {"alertname": "FailAlert"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`)

	ctx := context.Background()
	jsonResponse, statusCode, err := handler.HandleWebhookSync(ctx, payload)

	require.NoError(t, err)
	assert.Equal(t, 207, statusCode) // Multi-Status
	assert.Contains(t, string(jsonResponse), `"status":"partial_success"`)
}

func TestGetMetrics(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	metrics := handler.GetMetrics()
	require.NotNil(t, metrics)
	assert.NotNil(t, metrics.RequestsTotal)
	assert.NotNil(t, metrics.DurationSeconds)
}

// Integration test with real Alertmanager payload
func TestHandleWebhook_RealAlertmanagerPayload(t *testing.T) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	// Real Prometheus Alertmanager v0.25 payload
	payload := []byte(`{
		"receiver": "default",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "InstanceDown",
					"instance": "localhost:9090",
					"job": "prometheus",
					"severity": "critical"
				},
				"annotations": {
					"description": "localhost:9090 of job prometheus has been down for more than 5 minutes.",
					"summary": "Instance localhost:9090 down"
				},
				"startsAt": "2025-01-10T10:30:00.000Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"generatorURL": "http://prometheus.example.com:9090/graph",
				"fingerprint": "5ef77f1f8a3ecf8d"
			}
		],
		"groupLabels": {
			"alertname": "InstanceDown"
		},
		"commonLabels": {
			"alertname": "InstanceDown",
			"instance": "localhost:9090",
			"job": "prometheus",
			"severity": "critical"
		},
		"commonAnnotations": {
			"description": "localhost:9090 of job prometheus has been down for more than 5 minutes.",
			"summary": "Instance localhost:9090 down"
		},
		"externalURL": "http://alertmanager.example.com:9093",
		"version": "4",
		"groupKey": "{}:{alertname=\"InstanceDown\"}",
		"truncatedAlerts": 0
	}`)

	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	response, err := handler.HandleWebhook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "success", response.Status)
	assert.Equal(t, "alertmanager", response.WebhookType)
	assert.Equal(t, 1, response.AlertsReceived)
	assert.Equal(t, 1, response.AlertsProcessed)

	// Verify processed alert
	require.Len(t, processor.processedAlerts, 1)
	alert := processor.processedAlerts[0]
	assert.Equal(t, "InstanceDown", alert.AlertName)
	assert.Equal(t, core.StatusFiring, alert.Status)
	assert.Equal(t, "5ef77f1f8a3ecf8d", alert.Fingerprint)
	assert.Equal(t, "critical", alert.Labels["severity"])
}

// Benchmark tests
func BenchmarkHandleWebhook_SingleAlert(b *testing.B) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{"version":"4","groupKey":"test","status":"firing","receiver":"default","alerts":[{"status":"firing","labels":{"alertname":"test"},"startsAt":"2025-01-10T10:00:00Z"}]}`)
	req := &HandleWebhookRequest{Payload: payload}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.HandleWebhook(ctx, req)
	}
}

func BenchmarkHandleWebhookSync(b *testing.B) {
	processor := &mockAlertProcessor{}
	handler := NewUniversalWebhookHandler(processor, slog.Default())

	payload := []byte(`{"version":"4","groupKey":"test","status":"firing","receiver":"default","alerts":[{"status":"firing","labels":{"alertname":"test"},"startsAt":"2025-01-10T10:00:00Z"}]}`)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = handler.HandleWebhookSync(ctx, payload)
	}
}
