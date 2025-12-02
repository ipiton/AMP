package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebhookDetector(t *testing.T) {
	detector := NewWebhookDetector()
	require.NotNil(t, detector, "detector should not be nil")
}

func TestDetect_Alertmanager(t *testing.T) {
	detector := NewWebhookDetector()

	// Full Alertmanager webhook payload
	payload := `{
		"version": "4",
		"groupKey": "{}:{alertname=\"TestAlert\"}",
		"receiver": "default",
		"status": "firing",
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
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_AlertmanagerMinimal(t *testing.T) {
	detector := NewWebhookDetector()

	// Minimal Alertmanager payload (only version + alerts)
	payload := `{
		"version": "4",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "Test"}
			}
		]
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_Generic(t *testing.T) {
	detector := NewWebhookDetector()

	// Generic webhook payload (missing Alertmanager-specific fields)
	payload := `{
		"alertname": "TestAlert",
		"status": "firing",
		"severity": "critical",
		"message": "Test alert message"
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeGeneric, webhookType)
}

func TestDetect_GenericWithLabels(t *testing.T) {
	detector := NewWebhookDetector()

	// Generic payload with labels but no Alertmanager fields
	payload := `{
		"alertname": "TestAlert",
		"status": "firing",
		"labels": {
			"severity": "warning",
			"namespace": "production"
		}
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeGeneric, webhookType)
}

func TestDetect_EmptyPayload(t *testing.T) {
	detector := NewWebhookDetector()

	webhookType, err := detector.Detect([]byte{})
	assert.Error(t, err)
	assert.Equal(t, WebhookType(""), webhookType)
	assert.Contains(t, err.Error(), "empty")
}

func TestDetect_InvalidJSON(t *testing.T) {
	detector := NewWebhookDetector()

	webhookType, err := detector.Detect([]byte(`{invalid json`))
	assert.Error(t, err)
	assert.Equal(t, WebhookType(""), webhookType)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestDetect_AlertmanagerWithAllFields(t *testing.T) {
	detector := NewWebhookDetector()

	// Complete Alertmanager payload with all optional fields
	payload := `{
		"version": "4",
		"groupKey": "{}:{alertname=\"InstanceDown\"}",
		"truncatedAlerts": 0,
		"status": "firing",
		"receiver": "default",
		"groupLabels": {
			"alertname": "InstanceDown"
		},
		"commonLabels": {
			"alertname": "InstanceDown",
			"job": "prometheus",
			"severity": "critical"
		},
		"commonAnnotations": {
			"description": "Instance is down"
		},
		"externalURL": "http://alertmanager.example.com",
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
					"description": "localhost:9090 has been down for more than 5 minutes.",
					"summary": "Instance localhost:9090 down"
				},
				"startsAt": "2025-01-10T10:30:00.000Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"generatorURL": "http://prometheus.example.com:9090/graph",
				"fingerprint": "5ef77f1f8a3ecf8d"
			}
		]
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_AlertmanagerMultipleAlerts(t *testing.T) {
	detector := NewWebhookDetector()

	payload := `{
		"version": "4",
		"groupKey": "test",
		"receiver": "default",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "Alert1"}
			},
			{
				"status": "firing",
				"labels": {"alertname": "Alert2"}
			},
			{
				"status": "resolved",
				"labels": {"alertname": "Alert3"}
			}
		]
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_GenericArray(t *testing.T) {
	detector := NewWebhookDetector()

	// Generic payload with array structure but no Alertmanager fields
	payload := `{
		"events": [
			{
				"name": "alert1",
				"level": "critical"
			}
		]
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeGeneric, webhookType)
}

func TestDetect_AlertmanagerWithoutVersion(t *testing.T) {
	detector := NewWebhookDetector()

	// Alertmanager payload missing "version" but has other fields
	payload := `{
		"groupKey": "{}:{alertname=\"Test\"}",
		"receiver": "default",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "Test"}
			}
		]
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	// Should still detect as Alertmanager (groupKey + receiver + alerts)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_AlertmanagerResolved(t *testing.T) {
	detector := NewWebhookDetector()

	payload := `{
		"version": "4",
		"groupKey": "test",
		"status": "resolved",
		"alerts": [
			{
				"status": "resolved",
				"labels": {"alertname": "Test"},
				"startsAt": "2025-01-10T10:00:00Z",
				"endsAt": "2025-01-10T11:00:00Z"
			}
		]
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_EmptyAlerts(t *testing.T) {
	detector := NewWebhookDetector()

	// Payload with empty alerts array
	payload := `{
		"version": "4",
		"groupKey": "test",
		"receiver": "default",
		"alerts": []
	}`

	webhookType, err := detector.Detect([]byte(payload))
	require.NoError(t, err)
	// Should detect as Alertmanager based on version + groupKey + receiver
	assert.Equal(t, WebhookTypeAlertmanager, webhookType)
}

func TestDetect_PartialAlertmanagerFields(t *testing.T) {
	detector := NewWebhookDetector()

	tests := []struct {
		name     string
		payload  string
		expected WebhookType
	}{
		{
			name: "only version",
			payload: `{
				"version": "4",
				"data": {"alert": "test"}
			}`,
			expected: WebhookTypeGeneric, // Not enough Alertmanager fields
		},
		{
			name: "version + groupKey",
			payload: `{
				"version": "4",
				"groupKey": "test",
				"data": {}
			}`,
			expected: WebhookTypeAlertmanager, // 2 fields = Alertmanager
		},
		{
			name: "receiver + alerts without version",
			payload: `{
				"receiver": "default",
				"alerts": [{"status": "firing", "labels": {"alertname": "test"}}]
			}`,
			expected: WebhookTypeAlertmanager, // 2 fields = Alertmanager
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhookType, err := detector.Detect([]byte(tt.payload))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, webhookType)
		})
	}
}

func TestIsAlertmanagerWebhook(t *testing.T) {
	detector := &webhookDetector{}

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected bool
	}{
		{
			name: "all alertmanager fields",
			data: map[string]interface{}{
				"version":  "4",
				"groupKey": "test",
				"receiver": "default",
				"alerts": []interface{}{
					map[string]interface{}{
						"status": "firing",
						"labels": map[string]interface{}{"alertname": "test"},
					},
				},
			},
			expected: true,
		},
		{
			name: "only version and groupKey",
			data: map[string]interface{}{
				"version":  "4",
				"groupKey": "test",
			},
			expected: true,
		},
		{
			name: "only version",
			data: map[string]interface{}{
				"version": "4",
			},
			expected: false,
		},
		{
			name:     "empty data",
			data:     map[string]interface{}{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.isAlertmanagerWebhook(tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasAlertmanagerAlertStructure(t *testing.T) {
	detector := &webhookDetector{}

	tests := []struct {
		name     string
		alerts   []interface{}
		expected bool
	}{
		{
			name: "valid alertmanager alert",
			alerts: []interface{}{
				map[string]interface{}{
					"status":      "firing",
					"labels":      map[string]interface{}{"alertname": "test"},
					"annotations": map[string]interface{}{"summary": "test"},
				},
			},
			expected: true,
		},
		{
			name: "missing annotations (optional)",
			alerts: []interface{}{
				map[string]interface{}{
					"status": "firing",
					"labels": map[string]interface{}{"alertname": "test"},
				},
			},
			expected: true,
		},
		{
			name: "missing labels",
			alerts: []interface{}{
				map[string]interface{}{
					"status": "firing",
				},
			},
			expected: false,
		},
		{
			name: "invalid status",
			alerts: []interface{}{
				map[string]interface{}{
					"status": "invalid",
					"labels": map[string]interface{}{"alertname": "test"},
				},
			},
			expected: false,
		},
		{
			name:     "empty alerts",
			alerts:   []interface{}{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.hasAlertmanagerAlertStructure(tt.alerts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Benchmark tests
func BenchmarkDetect_Alertmanager(b *testing.B) {
	detector := NewWebhookDetector()
	payload := []byte(`{"version":"4","groupKey":"test","receiver":"default","alerts":[{"status":"firing","labels":{"alertname":"test"}}]}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = detector.Detect(payload)
	}
}

func BenchmarkDetect_Generic(b *testing.B) {
	detector := NewWebhookDetector()
	payload := []byte(`{"alertname":"test","status":"firing","severity":"critical"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = detector.Detect(payload)
	}
}
