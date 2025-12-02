package webhook

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

func TestNewAlertmanagerParser(t *testing.T) {
	parser := NewAlertmanagerParser()
	require.NotNil(t, parser, "parser should not be nil")
}

func TestParse_ValidPayload(t *testing.T) {
	parser := NewAlertmanagerParser()

	payload := `{
		"version": "4",
		"groupKey": "{}:{alertname=\"TestAlert\"}",
		"truncatedAlerts": 0,
		"status": "firing",
		"receiver": "default",
		"groupLabels": {
			"alertname": "TestAlert"
		},
		"commonLabels": {
			"alertname": "TestAlert",
			"severity": "critical"
		},
		"commonAnnotations": {},
		"externalURL": "http://alertmanager.example.com",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "TestAlert",
					"severity": "critical",
					"namespace": "production"
				},
				"annotations": {
					"summary": "Test alert summary",
					"description": "Test alert description"
				},
				"startsAt": "2025-01-10T10:00:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"generatorURL": "http://prometheus.example.com/graph",
				"fingerprint": "abc123"
			}
		]
	}`

	webhook, err := parser.Parse([]byte(payload))
	require.NoError(t, err, "parse should succeed")
	require.NotNil(t, webhook, "webhook should not be nil")

	assert.Equal(t, "4", webhook.Version)
	assert.Equal(t, "firing", webhook.Status)
	assert.Equal(t, 1, len(webhook.Alerts))
	assert.Equal(t, "TestAlert", webhook.Alerts[0].Labels["alertname"])
}

func TestParse_EmptyPayload(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook, err := parser.Parse([]byte{})
	assert.Error(t, err, "should fail on empty payload")
	assert.Nil(t, webhook, "webhook should be nil")
	assert.Contains(t, err.Error(), "empty")
}

func TestParse_InvalidJSON(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook, err := parser.Parse([]byte(`{invalid json`))
	assert.Error(t, err, "should fail on invalid JSON")
	assert.Nil(t, webhook, "webhook should be nil")
	assert.Contains(t, err.Error(), "parse webhook JSON")
}

func TestParse_MalformedJSON(t *testing.T) {
	parser := NewAlertmanagerParser()

	tests := []struct {
		name    string
		payload string
	}{
		{"missing closing brace", `{"version": "4", "status": "firing"`},
		{"extra comma", `{"version": "4",, "status": "firing"}`},
		{"unquoted keys", `{version: "4", status: "firing"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webhook, err := parser.Parse([]byte(tt.payload))
			assert.Error(t, err, "should fail on malformed JSON")
			assert.Nil(t, webhook)
		})
	}
}

func TestValidate_Integration(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "test"},
			},
		},
	}

	result := parser.Validate(webhook)
	assert.True(t, result.Valid, "valid webhook should pass validation")
	assert.Empty(t, result.Errors, "should have no errors")
}

func TestConvertToDomain_Success(t *testing.T) {
	parser := NewAlertmanagerParser()

	startsAt := time.Now().Add(-5 * time.Minute)
	endsAt := time.Now()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "resolved",
				Labels: map[string]string{
					"alertname": "HighCPU",
					"severity":  "critical",
					"namespace": "production",
				},
				Annotations: map[string]string{
					"summary": "CPU usage is high",
				},
				StartsAt:     startsAt,
				EndsAt:       endsAt,
				GeneratorURL: "http://prometheus.example.com",
				Fingerprint:  "existing-fingerprint",
			},
		},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err, "conversion should succeed")
	require.Len(t, alerts, 1, "should have 1 alert")

	alert := alerts[0]
	assert.Equal(t, "existing-fingerprint", alert.Fingerprint)
	assert.Equal(t, "HighCPU", alert.AlertName)
	assert.Equal(t, core.StatusResolved, alert.Status)
	assert.Equal(t, "critical", alert.Labels["severity"])
	assert.Equal(t, "production", alert.Labels["namespace"])
	assert.Equal(t, "CPU usage is high", alert.Annotations["summary"])
	assert.Equal(t, startsAt.Unix(), alert.StartsAt.Unix()) // Compare Unix timestamps
	require.NotNil(t, alert.EndsAt)
	assert.Equal(t, endsAt.Unix(), alert.EndsAt.Unix())
	require.NotNil(t, alert.GeneratorURL)
	assert.Equal(t, "http://prometheus.example.com", *alert.GeneratorURL)
	require.NotNil(t, alert.Timestamp)
}

func TestConvertToDomain_GeneratesFingerprint(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "warning",
				},
				StartsAt:    time.Now(),
				Fingerprint: "", // Empty fingerprint should be generated
			},
		},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)
	require.Len(t, alerts, 1)

	assert.NotEmpty(t, alerts[0].Fingerprint, "fingerprint should be generated")
	assert.Len(t, alerts[0].Fingerprint, 64, "SHA256 fingerprint should be 64 hex chars")
}

func TestConvertToDomain_DeterministicFingerprint(t *testing.T) {
	parser := NewAlertmanagerParser()

	// Create two identical webhooks
	createWebhook := func() *AlertmanagerWebhook {
		return &AlertmanagerWebhook{
			Version:  "4",
			GroupKey: "test",
			Status:   "firing",
			Alerts: []AlertmanagerAlert{
				{
					Status: "firing",
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "warning",
						"instance": "server1",
					},
					StartsAt: time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC),
				},
			},
		}
	}

	webhook1 := createWebhook()
	webhook2 := createWebhook()

	alerts1, err1 := parser.ConvertToDomain(webhook1)
	alerts2, err2 := parser.ConvertToDomain(webhook2)

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Len(t, alerts1, 1)
	require.Len(t, alerts2, 1)

	assert.Equal(t, alerts1[0].Fingerprint, alerts2[0].Fingerprint,
		"identical alerts should generate identical fingerprints")
}

func TestConvertToDomain_NilWebhook(t *testing.T) {
	parser := NewAlertmanagerParser()

	alerts, err := parser.ConvertToDomain(nil)
	assert.Error(t, err)
	assert.Nil(t, alerts)
	assert.Contains(t, err.Error(), "webhook is nil")
}

func TestConvertToDomain_EmptyAlerts(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts:   []AlertmanagerAlert{},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)
	assert.Empty(t, alerts, "should return empty array")
}

func TestConvertToDomain_MissingAlertname(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status:   "firing",
				Labels:   map[string]string{}, // Missing alertname
				StartsAt: time.Now(),
			},
		},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	assert.Error(t, err, "should fail without alertname")
	assert.Nil(t, alerts)
	assert.Contains(t, err.Error(), "alertname")
}

func TestConvertToDomain_InvalidStatus(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status: "invalid_status",
				Labels: map[string]string{"alertname": "test"},
				StartsAt: time.Now(),
			},
		},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	assert.Error(t, err)
	assert.Nil(t, alerts)
	assert.Contains(t, err.Error(), "invalid alert status")
}

func TestConvertToDomain_MissingStartsAt(t *testing.T) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status:   "firing",
				Labels:   map[string]string{"alertname": "test"},
				StartsAt: time.Time{}, // Zero time
			},
		},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	assert.Error(t, err)
	assert.Nil(t, alerts)
	assert.Contains(t, err.Error(), "startsAt is required")
}

func TestConvertToDomain_MultipleAlerts(t *testing.T) {
	parser := NewAlertmanagerParser()

	startsAt := time.Now().Add(-10 * time.Minute)

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status:   "firing",
				Labels:   map[string]string{"alertname": "Alert1", "severity": "critical"},
				StartsAt: startsAt,
			},
			{
				Status:   "resolved",
				Labels:   map[string]string{"alertname": "Alert2", "severity": "warning"},
				StartsAt: startsAt,
				EndsAt:   time.Now(),
			},
			{
				Status:   "firing",
				Labels:   map[string]string{"alertname": "Alert3", "severity": "info"},
				StartsAt: startsAt,
			},
		},
	}

	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)
	require.Len(t, alerts, 3)

	assert.Equal(t, "Alert1", alerts[0].AlertName)
	assert.Equal(t, core.StatusFiring, alerts[0].Status)

	assert.Equal(t, "Alert2", alerts[1].AlertName)
	assert.Equal(t, core.StatusResolved, alerts[1].Status)
	assert.NotNil(t, alerts[1].EndsAt, "resolved alert should have EndsAt")

	assert.Equal(t, "Alert3", alerts[2].AlertName)
	assert.Equal(t, core.StatusFiring, alerts[2].Status)
}

func TestMapAlertStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected core.AlertStatus
		wantErr  bool
	}{
		{"firing", core.StatusFiring, false},
		{"resolved", core.StatusResolved, false},
		{"pending", "", true},
		{"FIRING", "", true}, // Case sensitive
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			status, err := mapAlertStatus(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, status)
			}
		})
	}
}

func TestGenerateFingerprint_Deterministic(t *testing.T) {
	labels := map[string]string{
		"severity":  "critical",
		"instance": "server1",
		"job":      "api",
	}

	fp1 := generateFingerprint("TestAlert", labels)
	fp2 := generateFingerprint("TestAlert", labels)

	assert.Equal(t, fp1, fp2, "fingerprints should be identical for same input")
	assert.Len(t, fp1, 64, "SHA256 fingerprint should be 64 hex chars")
}

func TestGenerateFingerprint_OrderIndependent(t *testing.T) {
	// Labels in different order should generate same fingerprint
	labels1 := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	}

	labels2 := map[string]string{
		"c": "3",
		"a": "1",
		"b": "2",
	}

	fp1 := generateFingerprint("TestAlert", labels1)
	fp2 := generateFingerprint("TestAlert", labels2)

	assert.Equal(t, fp1, fp2, "label order should not affect fingerprint")
}

func TestGenerateFingerprint_DifferentForDifferentInputs(t *testing.T) {
	labels := map[string]string{"severity": "critical"}

	fp1 := generateFingerprint("Alert1", labels)
	fp2 := generateFingerprint("Alert2", labels)
	fp3 := generateFingerprint("Alert1", map[string]string{"severity": "warning"})

	assert.NotEqual(t, fp1, fp2, "different alertnames should have different fingerprints")
	assert.NotEqual(t, fp1, fp3, "different labels should have different fingerprints")
}

// TestParse_RealAlertmanagerPayload tests parsing with real Prometheus Alertmanager webhook payload
func TestParse_RealAlertmanagerPayload(t *testing.T) {
	parser := NewAlertmanagerParser()

	// Real payload from Prometheus Alertmanager v0.25
	payload := `{
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
				"generatorURL": "http://prometheus.example.com:9090/graph?g0.expr=up+%3D%3D+0&g0.tab=1",
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
	}`

	webhook, err := parser.Parse([]byte(payload))
	require.NoError(t, err)
	require.NotNil(t, webhook)

	// Validate parsed structure
	assert.Equal(t, "4", webhook.Version)
	assert.Equal(t, "firing", webhook.Status)
	assert.Equal(t, "default", webhook.Receiver)
	assert.Equal(t, "{}:{alertname=\"InstanceDown\"}", webhook.GroupKey)
	assert.Equal(t, 0, webhook.TruncatedAlerts)
	require.Len(t, webhook.Alerts, 1)

	// Convert to domain
	alerts, err := parser.ConvertToDomain(webhook)
	require.NoError(t, err)
	require.Len(t, alerts, 1)

	alert := alerts[0]
	assert.Equal(t, "5ef77f1f8a3ecf8d", alert.Fingerprint)
	assert.Equal(t, "InstanceDown", alert.AlertName)
	assert.Equal(t, core.StatusFiring, alert.Status)
	assert.Equal(t, "critical", alert.Labels["severity"])
	assert.Equal(t, "Instance localhost:9090 down", alert.Annotations["summary"])
}

func TestParseAndValidate_Success(t *testing.T) {
	parser := NewAlertmanagerParser().(*alertmanagerParser)

	payload := `{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {"alertname": "test"},
				"startsAt": "2025-01-10T10:00:00Z"
			}
		]
	}`

	webhook, result, err := parser.ParseAndValidate([]byte(payload))
	require.NoError(t, err)
	require.NotNil(t, webhook)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestParseAndValidate_ValidationFails(t *testing.T) {
	parser := NewAlertmanagerParser().(*alertmanagerParser)

	// Missing required fields
	payload := `{
		"version": "4",
		"groupKey": "test",
		"status": "firing",
		"alerts": []
	}`

	webhook, result, err := parser.ParseAndValidate([]byte(payload))
	require.NoError(t, err, "parse should succeed")
	require.NotNil(t, webhook)
	assert.False(t, result.Valid, "validation should fail")
	assert.NotEmpty(t, result.Errors, "should have validation errors")
}

func TestParseAndValidate_ParseFails(t *testing.T) {
	parser := NewAlertmanagerParser().(*alertmanagerParser)

	webhook, result, err := parser.ParseAndValidate([]byte(`{invalid`))
	assert.Error(t, err, "parse should fail")
	assert.Nil(t, webhook)
	assert.Nil(t, result)
}

// Benchmark tests
func BenchmarkParse(b *testing.B) {
	parser := NewAlertmanagerParser()
	payload := []byte(`{"version":"4","groupKey":"test","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"test"},"startsAt":"2025-01-10T10:00:00Z"}]}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(payload)
	}
}

func BenchmarkConvertToDomain(b *testing.B) {
	parser := NewAlertmanagerParser()

	webhook := &AlertmanagerWebhook{
		Version:  "4",
		GroupKey: "test",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status:   "firing",
				Labels:   map[string]string{"alertname": "test", "severity": "critical"},
				StartsAt: time.Now(),
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.ConvertToDomain(webhook)
	}
}

func BenchmarkGenerateFingerprint(b *testing.B) {
	labels := map[string]string{
		"alertname": "TestAlert",
		"severity":  "critical",
		"instance": "server1",
		"job":      "api",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generateFingerprint("TestAlert", labels)
	}
}
