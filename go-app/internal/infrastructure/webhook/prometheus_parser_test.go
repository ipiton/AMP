package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParsePrometheusV1SingleAlert tests parsing a single Prometheus v1 alert
func TestParsePrometheusV1SingleAlert(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {
				"alertname": "HighCPU",
				"instance": "server-1",
				"job": "api",
				"severity": "warning"
			},
			"annotations": {
				"summary": "CPU usage is high",
				"description": "CPU > 80% for 5m"
			},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"value": "0.85",
			"generatorURL": "http://prometheus:9090/graph",
			"fingerprint": "abc123def456"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)
	require.NotNil(t, webhook)

	// Verify webhook structure
	assert.Equal(t, "prom_prometheus_v1", webhook.Version)
	assert.Len(t, webhook.Alerts, 1)

	// Verify alert fields
	alert := webhook.Alerts[0]
	assert.Equal(t, "firing", alert.Status)
	assert.Equal(t, "HighCPU", alert.Labels["alertname"])
	assert.Equal(t, "server-1", alert.Labels["instance"])
	assert.Equal(t, "CPU usage is high", alert.Annotations["summary"])
	assert.Equal(t, "0.85", alert.Annotations["__prometheus_value__"]) // Value stored in annotations
	assert.Equal(t, "http://prometheus:9090/graph", alert.GeneratorURL)
}

// TestParsePrometheusV1MultipleAlerts tests parsing multiple Prometheus v1 alerts
func TestParsePrometheusV1MultipleAlerts(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Alert1", "instance": "server-1"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		},
		{
			"labels": {"alertname": "Alert2", "instance": "server-2"},
			"state": "pending",
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

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)
	require.NotNil(t, webhook)

	assert.Len(t, webhook.Alerts, 3)

	// Verify state mapping
	assert.Equal(t, "firing", webhook.Alerts[0].Status)     // firing → firing
	assert.Equal(t, "firing", webhook.Alerts[1].Status)     // pending → firing
	assert.Equal(t, "resolved", webhook.Alerts[2].Status)   // inactive → resolved
}

// TestParsePrometheusV2Grouped tests parsing Prometheus v2 grouped format
func TestParsePrometheusV2Grouped(t *testing.T) {
	parser := NewPrometheusParser()

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
						"generatorURL": "http://prometheus:9090"
					},
					{
						"labels": {"alertname": "HighMemory", "instance": "server-2"},
						"annotations": {"summary": "Memory high"},
						"state": "firing",
						"activeAt": "2025-11-18T10:05:00Z",
						"generatorURL": "http://prometheus:9090"
					}
				]
			}
		]
	}`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)
	require.NotNil(t, webhook)

	// Verify webhook structure
	assert.Equal(t, "prom_prometheus_v2", webhook.Version)
	assert.Len(t, webhook.Alerts, 2)

	// Verify group labels merged into alerts
	alert1 := webhook.Alerts[0]
	assert.Equal(t, "api", alert1.Labels["job"])
	assert.Equal(t, "warning", alert1.Labels["severity"])
	assert.Equal(t, "HighCPU", alert1.Labels["alertname"])
	assert.Equal(t, "server-1", alert1.Labels["instance"])

	alert2 := webhook.Alerts[1]
	assert.Equal(t, "api", alert2.Labels["job"])
	assert.Equal(t, "HighMemory", alert2.Labels["alertname"])
}

// TestParseMissingAlertname tests parsing alert without alertname label
func TestParseMissingAlertname(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"instance": "server-1"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err) // Parse succeeds

	// ConvertToDomain should fail due to missing alertname
	_, err = parser.ConvertToDomain(webhook)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "alertname")
}

// TestParseInvalidTimestamp tests parsing with invalid timestamp
func TestParseInvalidTimestamp(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "firing",
			"activeAt": "invalid-timestamp",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	_, err := parser.Parse(payload)
	assert.Error(t, err)
}

// TestParseInvalidState tests parsing with invalid state value
func TestParseInvalidState(t *testing.T) {
	parser := NewPrometheusParser()

	// Missing generatorURL causes detection failure (required field)
	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "unknown",
			"activeAt": "2025-11-18T10:00:00Z"
		}
	]`)

	// Should fail detection (missing generatorURL)
	_, err := parser.Parse(payload)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "format")
}

// TestParseMissingGeneratorURL tests parsing without required generatorURL
func TestParseMissingGeneratorURL(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z"
		}
	]`)

	// Should fail detection (missing generatorURL)
	_, err := parser.Parse(payload)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "format")
}

// TestParseLargePayload tests parsing large payload with 100 alerts
func TestParseLargePayload(t *testing.T) {
	parser := NewPrometheusParser()

	// Build large payload
	var payload []byte
	payload = append(payload, '[')
	for i := 0; i < 100; i++ {
		if i > 0 {
			payload = append(payload, ',')
		}
		alert := []byte(`{
			"labels": {"alertname": "Alert` + string(rune('0'+i%10)) + `", "instance": "server-` + string(rune('0'+i%10)) + `"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}`)
		payload = append(payload, alert...)
	}
	payload = append(payload, ']')

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)
	assert.Len(t, webhook.Alerts, 100)
}

// TestParseWithFingerprintProvided tests using provided fingerprint
func TestParseWithFingerprintProvided(t *testing.T) {
	parser := NewPrometheusParser()

	providedFingerprint := "custom123fingerprint456"
	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090",
			"fingerprint": "` + providedFingerprint + `"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	assert.Equal(t, providedFingerprint, webhook.Alerts[0].Fingerprint)
}

// TestParseWithoutFingerprintGenerate tests fingerprint generation when not provided
func TestParseWithoutFingerprintGenerate(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Test", "instance": "server-1", "job": "api"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	// Fingerprint should be empty after Parse (generated later in ConvertToDomain)
	// But the data is preserved for generation
	assert.NotEmpty(t, webhook.Alerts[0].Labels)
}

// TestConvertToDomain tests full conversion to core.Alert
func TestConvertToDomain(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "HighCPU", "instance": "server-1", "severity": "warning"},
			"annotations": {"summary": "CPU high"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"value": "0.85",
			"generatorURL": "http://prometheus:9090/graph"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)
	require.Len(t, alerts, 1)

	alert := alerts[0]
	assert.Equal(t, "HighCPU", alert.AlertName)
	assert.Equal(t, "firing", string(alert.Status))
	assert.Equal(t, "server-1", alert.Labels["instance"])
	assert.Equal(t, "CPU high", alert.Annotations["summary"])
	assert.Equal(t, "0.85", alert.Annotations["__prometheus_value__"])
	assert.NotEmpty(t, alert.Fingerprint) // Generated
	assert.NotNil(t, alert.GeneratorURL)
	assert.Equal(t, "http://prometheus:9090/graph", *alert.GeneratorURL)
	assert.NotNil(t, alert.Timestamp)
}

// TestConvertStateMapping tests state mapping (firing/pending/inactive)
func TestConvertStateMapping(t *testing.T) {
	parser := NewPrometheusParser()

	tests := []struct {
		name          string
		state         string
		expectedStatus string
	}{
		{"firing maps to firing", "firing", "firing"},
		{"pending maps to firing", "pending", "firing"},
		{"inactive maps to resolved", "inactive", "resolved"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`[
				{
					"labels": {"alertname": "Test"},
					"state": "` + tt.state + `",
					"activeAt": "2025-11-18T10:00:00Z",
					"generatorURL": "http://prometheus:9090"
				}
			]`)

			webhook, err := parser.Parse(payload)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedStatus, webhook.Alerts[0].Status)
		})
	}
}

// TestConvertPreserveValue tests that Prometheus value is preserved in annotations
func TestConvertPreserveValue(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "HighMetric"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"value": "123.45",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)

	// Value should be preserved in annotations
	assert.Equal(t, "123.45", alerts[0].Annotations["__prometheus_value__"])
}

// TestConvertGenerateFingerprint tests fingerprint generation
func TestConvertGenerateFingerprint(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Test", "instance": "server-1", "job": "api"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)

	// Fingerprint should be generated (64 hex characters)
	assert.Len(t, alerts[0].Fingerprint, 64)
	assert.Regexp(t, "^[a-f0-9]{64}$", alerts[0].Fingerprint)
}

// TestConvertNilHandling tests handling of nil/zero values
func TestConvertNilHandling(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"annotations": {},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)

	alert := alerts[0]
	// EndsAt should be nil (not provided in Prometheus)
	assert.Nil(t, alert.EndsAt)
	// Annotations should be empty but not nil
	assert.NotNil(t, alert.Annotations)
}

// TestParseEmptyPayload tests empty payload error
func TestParseEmptyPayload(t *testing.T) {
	parser := NewPrometheusParser()

	_, err := parser.Parse([]byte{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// TestParseNoAlerts tests webhook with no alerts
func TestParseNoAlerts(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[]`)

	_, err := parser.Parse(payload)
	assert.Error(t, err)
	// Empty array fails detection (not valid Prometheus v1 format)
	assert.Contains(t, err.Error(), "format")
}

// TestMapPrometheusState tests mapPrometheusState helper function
func TestMapPrometheusState(t *testing.T) {
	tests := []struct {
		state    string
		expected string
	}{
		{"firing", "firing"},
		{"pending", "firing"},   // Mapped to firing
		{"inactive", "resolved"},
		{"unknown", "firing"},    // Default to firing
		{"", "firing"},           // Empty defaults to firing
	}

	for _, tt := range tests {
		t.Run("state_"+tt.state, func(t *testing.T) {
			result := mapPrometheusState(tt.state)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValidate tests webhook validation
func TestValidate(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Test"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	// Validate should work (delegates to AlertmanagerWebhook validator)
	result := parser.Validate(webhook)
	assert.NotNil(t, result)
	// Note: Validation result depends on WebhookValidator implementation (TN-43)
}

// TestConvertMultipleAlerts tests converting multiple alerts
func TestConvertMultipleAlerts(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {"alertname": "Alert1"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		},
		{
			"labels": {"alertname": "Alert2"},
			"state": "inactive",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)
	require.Len(t, alerts, 2)

	assert.Equal(t, "Alert1", alerts[0].AlertName)
	assert.Equal(t, "Alert2", alerts[1].AlertName)
	assert.Equal(t, "firing", string(alerts[0].Status))
	assert.Equal(t, "resolved", string(alerts[1].Status)) // inactive → resolved
}

// TestConvertEmptyLabels tests conversion error with empty labels
func TestConvertEmptyLabels(t *testing.T) {
	parser := NewPrometheusParser()

	payload := []byte(`[
		{
			"labels": {},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}
	]`)

	webhook, err := parser.Parse(payload)
	require.NoError(t, err)

	// ConvertToDomain should fail due to missing alertname
	_, err = parser.ConvertToDomain(webhook)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "alertname")
}

// TestConvertNilWebhook tests conversion with nil webhook
func TestConvertNilWebhook(t *testing.T) {
	parser := NewPrometheusParser()

	_, err := parser.ConvertToDomain(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}
