package webhook

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test data helpers
func newTestPrometheusAlert(alertname, state string) PrometheusAlert {
	return PrometheusAlert{
		Labels: map[string]string{
			"alertname": alertname,
			"instance":  "server-1",
			"job":       "api",
		},
		Annotations: map[string]string{
			"summary": "Test alert",
		},
		State:        state,
		ActiveAt:     time.Date(2025, 11, 18, 10, 0, 0, 0, time.UTC),
		Value:        "0.85",
		GeneratorURL: "http://prometheus:9090/graph",
		Fingerprint:  "abc123def456",
	}
}

// TestPrometheusAlert_ValidAlert tests a valid Prometheus alert structure
func TestPrometheusAlert_ValidAlert(t *testing.T) {
	alert := newTestPrometheusAlert("HighCPU", "firing")

	assert.Equal(t, "HighCPU", alert.Labels["alertname"])
	assert.Equal(t, "firing", alert.State)
	assert.NotZero(t, alert.ActiveAt)
	assert.NotEmpty(t, alert.GeneratorURL)
}

// TestPrometheusAlert_MissingAlertname tests alert without alertname label
func TestPrometheusAlert_MissingAlertname(t *testing.T) {
	alert := PrometheusAlert{
		Labels: map[string]string{
			"instance": "server-1",
			// alertname missing
		},
		State:        "firing",
		ActiveAt:     time.Now(),
		GeneratorURL: "http://prometheus:9090",
	}

	// Alertname is missing - this should be caught by validation
	_, exists := alert.Labels["alertname"]
	assert.False(t, exists, "alertname should not exist")
}

// TestPrometheusAlert_InvalidState tests alert with invalid state
func TestPrometheusAlert_InvalidState(t *testing.T) {
	alert := PrometheusAlert{
		Labels: map[string]string{
			"alertname": "Test",
		},
		State:        "unknown", // Invalid state
		ActiveAt:     time.Now(),
		GeneratorURL: "http://prometheus:9090",
	}

	// State validation would happen in validator, here we just verify it's set
	assert.Equal(t, "unknown", alert.State)
}

// TestPrometheusWebhook_AlertCount tests AlertCount() method for both v1 and v2
func TestPrometheusWebhook_AlertCount(t *testing.T) {
	tests := []struct {
		name     string
		webhook  PrometheusWebhook
		expected int
	}{
		{
			name: "v1 format with 2 alerts",
			webhook: PrometheusWebhook{
				Alerts: []PrometheusAlert{
					newTestPrometheusAlert("Alert1", "firing"),
					newTestPrometheusAlert("Alert2", "firing"),
				},
			},
			expected: 2,
		},
		{
			name: "v2 format with 1 group, 2 alerts",
			webhook: PrometheusWebhook{
				Groups: []PrometheusAlertGroup{
					{
						Labels: map[string]string{"job": "api"},
						Alerts: []PrometheusAlert{
							newTestPrometheusAlert("Alert1", "firing"),
							newTestPrometheusAlert("Alert2", "firing"),
						},
					},
				},
			},
			expected: 2,
		},
		{
			name: "v2 format with 2 groups, 3 alerts total",
			webhook: PrometheusWebhook{
				Groups: []PrometheusAlertGroup{
					{
						Labels: map[string]string{"job": "api"},
						Alerts: []PrometheusAlert{
							newTestPrometheusAlert("Alert1", "firing"),
						},
					},
					{
						Labels: map[string]string{"job": "worker"},
						Alerts: []PrometheusAlert{
							newTestPrometheusAlert("Alert2", "firing"),
							newTestPrometheusAlert("Alert3", "firing"),
						},
					},
				},
			},
			expected: 3,
		},
		{
			name:     "empty webhook",
			webhook:  PrometheusWebhook{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := tt.webhook.AlertCount()
			assert.Equal(t, tt.expected, count)
		})
	}
}

// TestPrometheusWebhook_FlattenAlerts tests FlattenAlerts() method
func TestPrometheusWebhook_FlattenAlerts(t *testing.T) {
	t.Run("v1 format - already flat", func(t *testing.T) {
		webhook := PrometheusWebhook{
			Alerts: []PrometheusAlert{
				newTestPrometheusAlert("Alert1", "firing"),
				newTestPrometheusAlert("Alert2", "firing"),
			},
		}

		flat := webhook.FlattenAlerts()
		assert.Len(t, flat, 2)
		assert.Equal(t, "Alert1", flat[0].Labels["alertname"])
		assert.Equal(t, "Alert2", flat[1].Labels["alertname"])
	})

	t.Run("v2 format - merge group labels", func(t *testing.T) {
		webhook := PrometheusWebhook{
			Groups: []PrometheusAlertGroup{
				{
					Labels: map[string]string{
						"job":      "api",
						"severity": "warning",
					},
					Alerts: []PrometheusAlert{
						{
							Labels: map[string]string{
								"alertname": "HighCPU",
								"instance":  "server-1",
							},
							State:        "firing",
							ActiveAt:     time.Now(),
							GeneratorURL: "http://prometheus:9090",
						},
					},
				},
			},
		}

		flat := webhook.FlattenAlerts()
		require.Len(t, flat, 1)

		// Verify group labels merged
		assert.Equal(t, "api", flat[0].Labels["job"])
		assert.Equal(t, "warning", flat[0].Labels["severity"])

		// Verify alert labels preserved
		assert.Equal(t, "HighCPU", flat[0].Labels["alertname"])
		assert.Equal(t, "server-1", flat[0].Labels["instance"])

		// Total labels: 2 group + 2 alert = 4
		assert.Len(t, flat[0].Labels, 4)
	})

	t.Run("v2 format - alert labels override group labels", func(t *testing.T) {
		webhook := PrometheusWebhook{
			Groups: []PrometheusAlertGroup{
				{
					Labels: map[string]string{
						"job":      "api",
						"severity": "warning", // Will be overridden
					},
					Alerts: []PrometheusAlert{
						{
							Labels: map[string]string{
								"alertname": "HighCPU",
								"severity":  "critical", // Override group severity
							},
							State:        "firing",
							ActiveAt:     time.Now(),
							GeneratorURL: "http://prometheus:9090",
						},
					},
				},
			},
		}

		flat := webhook.FlattenAlerts()
		require.Len(t, flat, 1)

		// Alert severity should override group severity
		assert.Equal(t, "critical", flat[0].Labels["severity"])
		assert.Equal(t, "api", flat[0].Labels["job"])
	})

	t.Run("empty groups", func(t *testing.T) {
		webhook := PrometheusWebhook{
			Groups: []PrometheusAlertGroup{},
		}

		flat := webhook.FlattenAlerts()
		assert.Empty(t, flat)
	})
}

// TestPrometheusAlert_JSONMarshal tests JSON serialization
func TestPrometheusAlert_JSONMarshal(t *testing.T) {
	alert := newTestPrometheusAlert("HighCPU", "firing")

	data, err := json.Marshal(alert)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Verify JSON contains expected fields
	var decoded map[string]interface{}
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "firing", decoded["state"])
	assert.NotNil(t, decoded["labels"])
	assert.NotNil(t, decoded["annotations"])
	assert.Equal(t, "http://prometheus:9090/graph", decoded["generatorURL"])
}

// TestPrometheusAlert_JSONUnmarshal tests JSON deserialization
func TestPrometheusAlert_JSONUnmarshal(t *testing.T) {
	jsonData := `{
		"labels": {
			"alertname": "HighCPU",
			"instance": "server-1",
			"job": "api",
			"severity": "warning"
		},
		"annotations": {
			"summary": "CPU usage high",
			"description": "CPU > 80% for 5m"
		},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"value": "0.85",
		"generatorURL": "http://prometheus:9090/graph",
		"fingerprint": "a3f8b2c1d4e5f6789"
	}`

	var alert PrometheusAlert
	err := json.Unmarshal([]byte(jsonData), &alert)
	require.NoError(t, err)

	assert.Equal(t, "HighCPU", alert.Labels["alertname"])
	assert.Equal(t, "firing", alert.State)
	assert.Equal(t, "0.85", alert.Value)
	assert.Equal(t, "http://prometheus:9090/graph", alert.GeneratorURL)
	assert.Equal(t, "a3f8b2c1d4e5f6789", alert.Fingerprint)
	assert.False(t, alert.ActiveAt.IsZero())
}

// TestPrometheusWebhook_V2JSONUnmarshal tests v2 format deserialization
func TestPrometheusWebhook_V2JSONUnmarshal(t *testing.T) {
	jsonData := `{
		"groups": [
			{
				"labels": {"job": "api", "severity": "warning"},
				"alerts": [
					{
						"labels": {"alertname": "HighCPU", "instance": "server-1"},
						"state": "firing",
						"activeAt": "2025-11-18T10:00:00Z",
						"generatorURL": "http://prometheus:9090/graph"
					},
					{
						"labels": {"alertname": "HighMemory", "instance": "server-2"},
						"state": "pending",
						"activeAt": "2025-11-18T10:05:00Z",
						"generatorURL": "http://prometheus:9090/graph"
					}
				]
			}
		]
	}`

	var webhook PrometheusWebhook
	err := json.Unmarshal([]byte(jsonData), &webhook)
	require.NoError(t, err)

	assert.Len(t, webhook.Groups, 1)
	assert.Len(t, webhook.Groups[0].Alerts, 2)
	assert.Equal(t, "api", webhook.Groups[0].Labels["job"])
	assert.Equal(t, "HighCPU", webhook.Groups[0].Alerts[0].Labels["alertname"])
	assert.Equal(t, "HighMemory", webhook.Groups[0].Alerts[1].Labels["alertname"])
}

// TestPrometheusWebhook_FlattenAlertsEmptyGroups tests edge case with empty groups
func TestPrometheusWebhook_FlattenAlertsEmptyGroups(t *testing.T) {
	webhook := PrometheusWebhook{
		Groups: []PrometheusAlertGroup{
			{
				Labels: map[string]string{"job": "api"},
				Alerts: []PrometheusAlert{}, // Empty alerts
			},
		},
	}

	flat := webhook.FlattenAlerts()
	assert.Empty(t, flat)
}

// TestPrometheusWebhook_FlattenAlertsMultipleGroups tests multiple groups flattening
func TestPrometheusWebhook_FlattenAlertsMultipleGroups(t *testing.T) {
	webhook := PrometheusWebhook{
		Groups: []PrometheusAlertGroup{
			{
				Labels: map[string]string{"job": "api", "env": "prod"},
				Alerts: []PrometheusAlert{
					{
						Labels:       map[string]string{"alertname": "Alert1"},
						State:        "firing",
						ActiveAt:     time.Now(),
						GeneratorURL: "http://prometheus:9090",
					},
					{
						Labels:       map[string]string{"alertname": "Alert2"},
						State:        "firing",
						ActiveAt:     time.Now(),
						GeneratorURL: "http://prometheus:9090",
					},
				},
			},
			{
				Labels: map[string]string{"job": "worker", "env": "staging"},
				Alerts: []PrometheusAlert{
					{
						Labels:       map[string]string{"alertname": "Alert3"},
						State:        "pending",
						ActiveAt:     time.Now(),
						GeneratorURL: "http://prometheus:9090",
					},
				},
			},
		},
	}

	flat := webhook.FlattenAlerts()
	require.Len(t, flat, 3)

	// Verify first alert has group labels from first group
	assert.Equal(t, "Alert1", flat[0].Labels["alertname"])
	assert.Equal(t, "api", flat[0].Labels["job"])
	assert.Equal(t, "prod", flat[0].Labels["env"])

	// Verify second alert has group labels from first group
	assert.Equal(t, "Alert2", flat[1].Labels["alertname"])
	assert.Equal(t, "api", flat[1].Labels["job"])

	// Verify third alert has group labels from second group
	assert.Equal(t, "Alert3", flat[2].Labels["alertname"])
	assert.Equal(t, "worker", flat[2].Labels["job"])
	assert.Equal(t, "staging", flat[2].Labels["env"])
}
