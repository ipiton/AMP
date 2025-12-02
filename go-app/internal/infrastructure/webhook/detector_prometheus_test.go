package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetectPrometheusV1Format tests detection of Prometheus v1 format (array)
func TestDetectPrometheusV1Format(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte(`[
		{
			"labels": {
				"alertname": "HighCPU",
				"instance": "server-1",
				"job": "api",
				"severity": "warning"
			},
			"annotations": {
				"summary": "CPU usage high"
			},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"value": "0.85",
			"generatorURL": "http://prometheus:9090/graph",
			"fingerprint": "abc123"
		}
	]`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypePrometheus, webhookType)

	// Verify fine-grained detection
	promDetector := NewPrometheusFormatDetector()
	format, err := promDetector.DetectPrometheusFormat(payload)
	require.NoError(t, err)
	assert.Equal(t, PrometheusFormatV1, format)
}

// TestDetectPrometheusV2Format tests detection of Prometheus v2 format (grouped)
func TestDetectPrometheusV2Format(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte(`{
		"groups": [
			{
				"labels": {"job": "api", "severity": "warning"},
				"alerts": [
					{
						"labels": {"alertname": "HighCPU", "instance": "server-1"},
						"annotations": {"summary": "CPU high"},
						"state": "firing",
						"activeAt": "2025-11-18T10:00:00Z",
						"generatorURL": "http://prometheus:9090/graph"
					}
				]
			}
		]
	}`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypePrometheus, webhookType)

	// Verify fine-grained detection
	promDetector := NewPrometheusFormatDetector()
	format, err := promDetector.DetectPrometheusFormat(payload)
	require.NoError(t, err)
	assert.Equal(t, PrometheusFormatV2, format)
}

// TestDetectAlertmanagerFormat tests regression - Alertmanager still detected
func TestDetectAlertmanagerFormat(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte(`{
		"version": "4",
		"groupKey": "{}:{alertname=\"TestAlert\"}",
		"receiver": "webhook",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "TestAlert"},
				"annotations": {"summary": "Test"},
				"startsAt": "2025-11-18T10:00:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"generatorURL": "http://prometheus:9090/graph",
				"fingerprint": "abc123"
			}
		]
	}`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType, "Alertmanager format should still be detected")
}

// TestDetectUnknownFormat tests unknown format detection
func TestDetectUnknownFormat(t *testing.T) {
	detector := NewWebhookDetector()

	// Generic webhook without Prometheus or Alertmanager indicators
	payload := []byte(`{
		"message": "Something happened",
		"timestamp": "2025-11-18T10:00:00Z",
		"severity": "warning"
	}`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeGeneric, webhookType, "Unknown format should default to generic")
}

// TestDetectEmptyPayload tests empty payload handling
func TestDetectEmptyPayload(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte{}

	_, err := detector.Detect(payload)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// TestDetectInvalidJSON tests invalid JSON handling
func TestDetectInvalidJSON(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte(`{invalid json`)

	_, err := detector.Detect(payload)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

// TestDetectPrometheusWithExtraFields tests Prometheus detection with extra fields
func TestDetectPrometheusWithExtraFields(t *testing.T) {
	detector := NewWebhookDetector()

	// Prometheus alert with extra custom fields (should still be detected)
	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090",
			"customField1": "value1",
			"customField2": 123
		}
	]`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypePrometheus, webhookType, "Extra fields should not prevent Prometheus detection")
}

// TestDetectLargePayload tests detection with large payload (performance)
func TestDetectLargePayload(t *testing.T) {
	detector := NewWebhookDetector()

	// Build large payload with 100 alerts
	payload := []byte(`[`)
	for i := 0; i < 100; i++ {
		if i > 0 {
			payload = append(payload, ',')
		}
		alert := `{
			"labels": {"alertname": "Alert` + string(rune('0'+i%10)) + `"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}`
		payload = append(payload, []byte(alert)...)
	}
	payload = append(payload, ']')

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypePrometheus, webhookType)
}

// TestDetectConcurrentDetection tests thread-safety of detector
func TestDetectConcurrentDetection(t *testing.T) {
	detector := NewWebhookDetector()

	payloadProm := []byte(`[{"labels": {"alertname": "Test"}, "state": "firing", "activeAt": "2025-11-18T10:00:00Z", "generatorURL": "http://prometheus:9090"}]`)
	payloadAM := []byte(`{"version": "4", "groupKey": "test", "receiver": "webhook", "alerts": [{"status": "firing", "labels": {"alertname": "Test"}}]}`)

	// Run 100 concurrent detections
	done := make(chan bool, 200)

	for i := 0; i < 100; i++ {
		go func() {
			_, err := detector.Detect(payloadProm)
			assert.NoError(t, err)
			done <- true
		}()
		go func() {
			_, err := detector.Detect(payloadAM)
			assert.NoError(t, err)
			done <- true
		}()
	}

	// Wait for all to complete
	for i := 0; i < 200; i++ {
		<-done
	}
}

// TestDetectPrometheusV1MultipleAlerts tests v1 format with multiple alerts
func TestDetectPrometheusV1MultipleAlerts(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte(`[
		{
			"labels": {"alertname": "Alert1"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		},
		{
			"labels": {"alertname": "Alert2"},
			"state": "pending",
			"activeAt": "2025-11-18T10:05:00Z",
			"generatorURL": "http://prometheus:9090"
		},
		{
			"labels": {"alertname": "Alert3"},
			"state": "inactive",
			"activeAt": "2025-11-18T09:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypePrometheus, webhookType)

	promDetector := NewPrometheusFormatDetector()
	format, err := promDetector.DetectPrometheusFormat(payload)
	require.NoError(t, err)
	assert.Equal(t, PrometheusFormatV1, format)
}

// TestDetectPrometheusV2MultipleGroups tests v2 format with multiple groups
func TestDetectPrometheusV2MultipleGroups(t *testing.T) {
	detector := NewWebhookDetector()

	payload := []byte(`{
		"groups": [
			{
				"labels": {"job": "api", "env": "prod"},
				"alerts": [
					{
						"labels": {"alertname": "HighCPU"},
						"state": "firing",
						"activeAt": "2025-11-18T10:00:00Z",
						"generatorURL": "http://prometheus:9090"
					}
				]
			},
			{
				"labels": {"job": "worker", "env": "staging"},
				"alerts": [
					{
						"labels": {"alertname": "HighMemory"},
						"state": "firing",
						"activeAt": "2025-11-18T10:05:00Z",
						"generatorURL": "http://prometheus:9090"
					}
				]
			}
		]
	}`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	assert.Equal(t, WebhookTypePrometheus, webhookType)

	promDetector := NewPrometheusFormatDetector()
	format, err := promDetector.DetectPrometheusFormat(payload)
	require.NoError(t, err)
	assert.Equal(t, PrometheusFormatV2, format)
}

// TestDetectPrometheusV1InvalidState tests v1 with invalid state (should not detect as Prometheus)
func TestDetectPrometheusV1InvalidState(t *testing.T) {
	detector := NewWebhookDetector()

	// Invalid state "unknown" (not "firing"/"pending"/"inactive")
	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "unknown",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	// Should not detect as Prometheus due to invalid state
	assert.NotEqual(t, WebhookTypePrometheus, webhookType)
	assert.Equal(t, WebhookTypeGeneric, webhookType)
}

// TestDetectPrometheusV1MissingGeneratorURL tests v1 without required generatorURL
func TestDetectPrometheusV1MissingGeneratorURL(t *testing.T) {
	detector := NewWebhookDetector()

	// Missing generatorURL (required in Prometheus)
	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z"
		}
	]`)

	webhookType, err := detector.Detect(payload)
	require.NoError(t, err)
	// Should not detect as Prometheus due to missing generatorURL
	assert.NotEqual(t, WebhookTypePrometheus, webhookType)
	assert.Equal(t, WebhookTypeGeneric, webhookType)
}

// TestDetectPrometheusFormatDetectorErrors tests error cases for fine-grained detector
func TestDetectPrometheusFormatDetectorErrors(t *testing.T) {
	promDetector := NewPrometheusFormatDetector()

	tests := []struct {
		name    string
		payload []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty payload",
			payload: []byte{},
			wantErr: true,
			errMsg:  "empty",
		},
		{
			name:    "invalid JSON",
			payload: []byte(`{invalid`),
			wantErr: true,
			errMsg:  "invalid JSON",
		},
		{
			name: "array but not Prometheus v1",
			payload: []byte(`[
				{"status": "firing", "labels": {"alertname": "Test"}}
			]`),
			wantErr: true,
			errMsg:  "not valid Prometheus v1",
		},
		{
			name: "object without groups field",
			payload: []byte(`{
				"alerts": [{"state": "firing"}]
			}`),
			wantErr: true,
			errMsg:  "no 'groups' field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, err := promDetector.DetectPrometheusFormat(tt.payload)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, format)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, format)
			}
		})
	}
}

// TestHasPrometheusV1Fields tests the hasPrometheusV1Fields helper function
func TestHasPrometheusV1Fields(t *testing.T) {
	tests := []struct {
		name   string
		alert  map[string]interface{}
		expect bool
	}{
		{
			name: "valid Prometheus v1 alert",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"state":        "firing",
				"activeAt":     "2025-11-18T10:00:00Z",
				"generatorURL": "http://prometheus:9090",
			},
			expect: true,
		},
		{
			name: "missing state field",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"activeAt":     "2025-11-18T10:00:00Z",
				"generatorURL": "http://prometheus:9090",
			},
			expect: false,
		},
		{
			name: "missing activeAt field",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"state":        "firing",
				"generatorURL": "http://prometheus:9090",
			},
			expect: false,
		},
		{
			name: "missing generatorURL field",
			alert: map[string]interface{}{
				"labels":   map[string]interface{}{"alertname": "Test"},
				"state":    "firing",
				"activeAt": "2025-11-18T10:00:00Z",
			},
			expect: false,
		},
		{
			name: "invalid state value",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"state":        "unknown",
				"activeAt":     "2025-11-18T10:00:00Z",
				"generatorURL": "http://prometheus:9090",
			},
			expect: false,
		},
		{
			name: "empty generatorURL",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"state":        "firing",
				"activeAt":     "2025-11-18T10:00:00Z",
				"generatorURL": "",
			},
			expect: false,
		},
		{
			name: "valid with pending state",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"state":        "pending",
				"activeAt":     "2025-11-18T10:00:00Z",
				"generatorURL": "http://prometheus:9090",
			},
			expect: true,
		},
		{
			name: "valid with inactive state",
			alert: map[string]interface{}{
				"labels":       map[string]interface{}{"alertname": "Test"},
				"state":        "inactive",
				"activeAt":     "2025-11-18T10:00:00Z",
				"generatorURL": "http://prometheus:9090",
			},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPrometheusV1Fields(tt.alert)
			assert.Equal(t, tt.expect, result)
		})
	}
}
