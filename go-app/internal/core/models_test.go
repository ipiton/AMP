package core_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// TestAlertValidation tests validation rules for Alert struct
func TestAlertValidation(t *testing.T) {
	validate := validator.New()

	tests := []struct {
		name    string
		alert   core.Alert
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid alert with all required fields",
			alert: core.Alert{
				Fingerprint: "abc123",
				AlertName:   "HighCPUUsage",
				Status:      core.StatusFiring,
				Labels:      map[string]string{"severity": "critical"},
				Annotations: map[string]string{"description": "CPU is high"},
				StartsAt:    time.Now(),
			},
			wantErr: false,
		},
		{
			name: "missing fingerprint",
			alert: core.Alert{
				AlertName: "TestAlert",
				Status:    core.StatusFiring,
				StartsAt:  time.Now(),
			},
			wantErr: true,
			errMsg:  "Fingerprint is required",
		},
		{
			name: "missing alert name",
			alert: core.Alert{
				Fingerprint: "abc123",
				Status:      core.StatusFiring,
				StartsAt:    time.Now(),
			},
			wantErr: true,
			errMsg:  "AlertName is required",
		},
		{
			name: "missing status",
			alert: core.Alert{
				Fingerprint: "abc123",
				AlertName:   "TestAlert",
				StartsAt:    time.Now(),
			},
			wantErr: true,
			errMsg:  "Status is required",
		},
		{
			name: "invalid status",
			alert: core.Alert{
				Fingerprint: "abc123",
				AlertName:   "TestAlert",
				Status:      "invalid",
				StartsAt:    time.Now(),
			},
			wantErr: true,
			errMsg:  "Status must be firing or resolved",
		},
		{
			name: "invalid generator URL",
			alert: core.Alert{
				Fingerprint:  "abc123",
				AlertName:    "TestAlert",
				Status:       core.StatusFiring,
				StartsAt:     time.Now(),
				GeneratorURL: stringPtr("not-a-url"),
			},
			wantErr: true,
			errMsg:  "GeneratorURL must be valid URL",
		},
		{
			name: "valid generator URL",
			alert: core.Alert{
				Fingerprint:  "abc123",
				AlertName:    "TestAlert",
				Status:       core.StatusFiring,
				StartsAt:     time.Now(),
				GeneratorURL: stringPtr("https://prometheus.example.com/graph"),
			},
			wantErr: false,
		},
		{
			name: "valid with optional fields",
			alert: core.Alert{
				Fingerprint:  "abc123",
				AlertName:    "TestAlert",
				Status:       core.StatusResolved,
				Labels:       map[string]string{"namespace": "production"},
				Annotations:  map[string]string{},
				StartsAt:     time.Now().Add(-1 * time.Hour),
				EndsAt:       timePtr(time.Now()),
				GeneratorURL: stringPtr("https://prometheus.example.com"),
				Timestamp:    timePtr(time.Now()),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.alert)
			if tt.wantErr {
				assert.Error(t, err, tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestClassificationResultValidation tests validation rules for ClassificationResult
func TestClassificationResultValidation(t *testing.T) {
	validate := validator.New()

	tests := []struct {
		name           string
		classification core.ClassificationResult
		wantErr        bool
		errMsg         string
	}{
		{
			name: "valid classification",
			classification: core.ClassificationResult{
				Severity:        core.SeverityCritical,
				Confidence:      0.95,
				Reasoning:       "High CPU usage detected",
				Recommendations: []string{"Scale up", "Check for memory leaks"},
				ProcessingTime:  0.123,
			},
			wantErr: false,
		},
		{
			name: "missing severity",
			classification: core.ClassificationResult{
				Confidence: 0.95,
				Reasoning:  "Test",
			},
			wantErr: true,
			errMsg:  "Severity is required",
		},
		{
			name: "invalid severity",
			classification: core.ClassificationResult{
				Severity:   "invalid",
				Confidence: 0.95,
				Reasoning:  "Test",
			},
			wantErr: true,
			errMsg:  "Severity must be critical, warning, info, or noise",
		},
		{
			name: "confidence below 0",
			classification: core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: -0.1,
				Reasoning:  "Test",
			},
			wantErr: true,
			errMsg:  "Confidence must be between 0 and 1",
		},
		{
			name: "confidence above 1",
			classification: core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 1.5,
				Reasoning:  "Test",
			},
			wantErr: true,
			errMsg:  "Confidence must be between 0 and 1",
		},
		{
			name: "confidence exactly 0",
			classification: core.ClassificationResult{
				Severity:   core.SeverityInfo,
				Confidence: 0.0,
				Reasoning:  "Test",
			},
			wantErr: false,
		},
		{
			name: "confidence exactly 1",
			classification: core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 1.0,
				Reasoning:  "Test",
			},
			wantErr: false,
		},
		{
			name: "missing reasoning",
			classification: core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 0.95,
			},
			wantErr: true,
			errMsg:  "Reasoning is required",
		},
		{
			name: "negative processing time",
			classification: core.ClassificationResult{
				Severity:       core.SeverityCritical,
				Confidence:     0.95,
				Reasoning:      "Test",
				ProcessingTime: -0.1,
			},
			wantErr: true,
			errMsg:  "ProcessingTime must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.classification)
			if tt.wantErr {
				assert.Error(t, err, tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestPublishingTargetValidation tests validation rules for PublishingTarget
func TestPublishingTargetValidation(t *testing.T) {
	validate := validator.New()

	tests := []struct {
		name    string
		target  core.PublishingTarget
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid target",
			target: core.PublishingTarget{
				Name:    "rootly-prod",
				Type:    "rootly",
				URL:     "https://api.rootly.com/v1",
				Enabled: true,
				Format:  core.FormatRootly,
			},
			wantErr: false,
		},
		{
			name: "missing name",
			target: core.PublishingTarget{
				Type:   "slack",
				URL:    "https://hooks.slack.com/services/xxx",
				Format: core.FormatSlack,
			},
			wantErr: true,
			errMsg:  "Name is required",
		},
		{
			name: "missing type",
			target: core.PublishingTarget{
				Name:   "test",
				URL:    "https://example.com",
				Format: core.FormatWebhook,
			},
			wantErr: true,
			errMsg:  "Type is required",
		},
		{
			name: "missing URL",
			target: core.PublishingTarget{
				Name:   "test",
				Type:   "webhook",
				Format: core.FormatWebhook,
			},
			wantErr: true,
			errMsg:  "URL is required",
		},
		{
			name: "invalid URL",
			target: core.PublishingTarget{
				Name:   "test",
				Type:   "webhook",
				URL:    "not-a-url",
				Format: core.FormatWebhook,
			},
			wantErr: true,
			errMsg:  "URL must be valid",
		},
		{
			name: "invalid format",
			target: core.PublishingTarget{
				Name:   "test",
				Type:   "custom",
				URL:    "https://example.com",
				Format: "invalid",
			},
			wantErr: true,
			errMsg:  "Format must be one of: alertmanager, rootly, pagerduty, slack, webhook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.target)
			if tt.wantErr {
				assert.Error(t, err, tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestAlertJSONSerialization tests JSON marshaling and unmarshaling for Alert
func TestAlertJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second) // Truncate for comparison
	endsAt := now.Add(1 * time.Hour)
	genURL := "https://prometheus.example.com"

	original := core.Alert{
		Fingerprint: "test123",
		AlertName:   "HighCPUUsage",
		Status:      core.StatusFiring,
		Labels: map[string]string{
			"severity":  "critical",
			"namespace": "production",
		},
		Annotations: map[string]string{
			"description": "CPU usage is above 90%",
			"summary":     "High CPU on prod-server-01",
		},
		StartsAt:     now,
		EndsAt:       &endsAt,
		GeneratorURL: &genURL,
		Timestamp:    &now,
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Unmarshal back
	var decoded core.Alert
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Compare fields
	assert.Equal(t, original.Fingerprint, decoded.Fingerprint)
	assert.Equal(t, original.AlertName, decoded.AlertName)
	assert.Equal(t, original.Status, decoded.Status)
	assert.Equal(t, original.Labels, decoded.Labels)
	assert.Equal(t, original.Annotations, decoded.Annotations)
	assert.True(t, original.StartsAt.Equal(decoded.StartsAt))

	require.NotNil(t, decoded.EndsAt)
	assert.True(t, original.EndsAt.Equal(*decoded.EndsAt))

	require.NotNil(t, decoded.GeneratorURL)
	assert.Equal(t, *original.GeneratorURL, *decoded.GeneratorURL)

	require.NotNil(t, decoded.Timestamp)
	assert.True(t, original.Timestamp.Equal(*decoded.Timestamp))
}

// TestClassificationResultJSONSerialization tests JSON for ClassificationResult
func TestClassificationResultJSONSerialization(t *testing.T) {
	original := core.ClassificationResult{
		Severity:   core.SeverityCritical,
		Confidence: 0.95,
		Reasoning:  "CPU usage consistently above 90%",
		Recommendations: []string{
			"Scale horizontally",
			"Investigate memory leaks",
			"Review application logs",
		},
		ProcessingTime: 0.234,
		Metadata: map[string]any{
			"model":   "gpt-4",
			"version": "2024-03",
		},
	}

	// Marshal
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Unmarshal
	var decoded core.ClassificationResult
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Compare
	assert.Equal(t, original.Severity, decoded.Severity)
	assert.Equal(t, original.Confidence, decoded.Confidence)
	assert.Equal(t, original.Reasoning, decoded.Reasoning)
	assert.Equal(t, original.Recommendations, decoded.Recommendations)
	assert.Equal(t, original.ProcessingTime, decoded.ProcessingTime)
	assert.Equal(t, original.Metadata, decoded.Metadata)
}

// TestAlertMethods tests Alert helper methods
func TestAlertMethods(t *testing.T) {
	t.Run("Namespace - present", func(t *testing.T) {
		alert := core.Alert{
			Labels: map[string]string{
				"namespace": "production",
				"severity":  "critical",
			},
		}

		ns := alert.Namespace()
		require.NotNil(t, ns)
		assert.Equal(t, "production", *ns)
	})

	t.Run("Namespace - absent", func(t *testing.T) {
		alert := core.Alert{
			Labels: map[string]string{
				"severity": "critical",
			},
		}

		ns := alert.Namespace()
		assert.Nil(t, ns)
	})

	t.Run("Namespace - empty labels", func(t *testing.T) {
		alert := core.Alert{
			Labels: map[string]string{},
		}

		ns := alert.Namespace()
		assert.Nil(t, ns)
	})

	t.Run("Severity - present", func(t *testing.T) {
		alert := core.Alert{
			Labels: map[string]string{
				"namespace": "production",
				"severity":  "critical",
			},
		}

		sev := alert.Severity()
		require.NotNil(t, sev)
		assert.Equal(t, "critical", *sev)
	})

	t.Run("Severity - absent", func(t *testing.T) {
		alert := core.Alert{
			Labels: map[string]string{
				"namespace": "production",
			},
		}

		sev := alert.Severity()
		assert.Nil(t, sev)
	})
}

// TestAlertStatusEnum tests AlertStatus enum values
func TestAlertStatusEnum(t *testing.T) {
	assert.Equal(t, core.AlertStatus("firing"), core.StatusFiring)
	assert.Equal(t, core.AlertStatus("resolved"), core.StatusResolved)
}

// TestAlertSeverityEnum tests AlertSeverity enum values
func TestAlertSeverityEnum(t *testing.T) {
	assert.Equal(t, core.AlertSeverity("critical"), core.SeverityCritical)
	assert.Equal(t, core.AlertSeverity("warning"), core.SeverityWarning)
	assert.Equal(t, core.AlertSeverity("info"), core.SeverityInfo)
	assert.Equal(t, core.AlertSeverity("noise"), core.SeverityNoise)
}

// TestPublishingFormatEnum tests PublishingFormat enum values
func TestPublishingFormatEnum(t *testing.T) {
	assert.Equal(t, core.PublishingFormat("alertmanager"), core.FormatAlertmanager)
	assert.Equal(t, core.PublishingFormat("rootly"), core.FormatRootly)
	assert.Equal(t, core.PublishingFormat("pagerduty"), core.FormatPagerDuty)
	assert.Equal(t, core.PublishingFormat("slack"), core.FormatSlack)
	assert.Equal(t, core.PublishingFormat("webhook"), core.FormatWebhook)
}

// TestEnrichedAlertJSONSerialization tests EnrichedAlert serialization
func TestEnrichedAlertJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	enriched := core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"severity": "critical"},
			Annotations: map[string]string{},
			StartsAt:    now,
		},
		Classification: &core.ClassificationResult{
			Severity:        core.SeverityCritical,
			Confidence:      0.95,
			Reasoning:       "High priority alert",
			Recommendations: []string{"Check logs"},
			ProcessingTime:  0.1,
		},
		EnrichmentMetadata: map[string]any{
			"enriched_at": now.Format(time.RFC3339),
		},
		ProcessingTimestamp: &now,
	}

	// Marshal
	data, err := json.Marshal(enriched)
	require.NoError(t, err)

	// Unmarshal
	var decoded core.EnrichedAlert
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify
	require.NotNil(t, decoded.Alert)
	assert.Equal(t, enriched.Alert.Fingerprint, decoded.Alert.Fingerprint)

	require.NotNil(t, decoded.Classification)
	assert.Equal(t, enriched.Classification.Severity, decoded.Classification.Severity)
}

// Helper functions

func stringPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}
