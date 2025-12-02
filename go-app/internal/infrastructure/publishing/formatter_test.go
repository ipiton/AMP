package publishing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

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
				"summary":     "Test alert summary",
				"description": "Test alert description",
			},
			StartsAt:     now,
			GeneratorURL: &generatorURL,
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityWarning,
			Confidence: 0.85,
			Reasoning:  "This alert indicates a potential issue that requires attention.",
			Recommendations: []string{
				"Check system logs",
				"Verify resource usage",
				"Contact on-call engineer",
			},
		},
	}
}

func TestNewAlertFormatter(t *testing.T) {
	formatter := NewAlertFormatter()

	assert.NotNil(t, formatter)
}

func TestFormatAlert_Alertmanager(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatAlertmanager)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify structure
	assert.Equal(t, "alert-history-proxy", result["receiver"])
	assert.Equal(t, "firing", result["status"])
	assert.Equal(t, "4", result["version"])

	// Verify alerts array
	alerts, ok := result["alerts"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, alerts, 1)

	alert := alerts[0]
	assert.Equal(t, "test-fingerprint-123", alert["fingerprint"])
	assert.Equal(t, "firing", alert["status"])

	// Verify LLM annotations were added
	annotations, ok := alert["annotations"].(map[string]string)
	require.True(t, ok)
	assert.Contains(t, annotations, "llm_severity")
	assert.Equal(t, "warning", annotations["llm_severity"])
	assert.Contains(t, annotations, "llm_confidence")
	assert.Contains(t, annotations, "llm_reasoning")
	assert.Contains(t, annotations, "llm_recommendations")
}

func TestFormatAlert_Rootly(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatRootly)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify required fields
	assert.Contains(t, result, "title")
	assert.Contains(t, result, "description")
	assert.Contains(t, result, "severity")

	// Verify severity mapping
	assert.Equal(t, "major", result["severity"])

	// Verify title includes AI info
	title, ok := result["title"].(string)
	require.True(t, ok)
	assert.Contains(t, title, "TestAlert")
	assert.Contains(t, title, "production")
	assert.Contains(t, title, "AI:")

	// Verify description includes classification
	description, ok := result["description"].(string)
	require.True(t, ok)
	assert.Contains(t, description, "AI Classification")
	assert.Contains(t, description, "Recommendations")
}

func TestFormatAlert_PagerDuty(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatPagerDuty)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify event action
	assert.Equal(t, "trigger", result["event_action"])
	assert.Equal(t, "test-fingerprint-123", result["dedup_key"])

	// Verify payload
	payload, ok := result["payload"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, payload, "summary")
	assert.Equal(t, "warning", payload["severity"])
	assert.Equal(t, "alert-history-service", payload["source"])

	// Verify custom details include AI classification
	customDetails, ok := payload["custom_details"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, customDetails, "ai_classification")
}

func TestFormatAlert_PagerDuty_Resolved(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()
	enrichedAlert.Alert.Status = core.StatusResolved

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatPagerDuty)

	require.NoError(t, err)
	assert.Equal(t, "resolve", result["event_action"])
}

func TestFormatAlert_Slack(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatSlack)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify blocks exist
	blocks, ok := result["blocks"].([]map[string]any)
	require.True(t, ok)
	assert.Greater(t, len(blocks), 3) // Header, details, AI info, divider, context

	// Verify header block
	headerBlock := blocks[0]
	assert.Equal(t, "header", headerBlock["type"])

	// Verify attachments
	attachments, ok := result["attachments"].([]map[string]any)
	require.True(t, ok)
	assert.Len(t, attachments, 1)

	// Verify color (warning = orange)
	assert.Equal(t, "#FFA500", attachments[0]["color"])
}

func TestFormatAlert_Slack_Critical(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()
	enrichedAlert.Classification.Severity = core.SeverityCritical

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatSlack)

	require.NoError(t, err)

	// Verify color (critical = red)
	attachments, ok := result["attachments"].([]map[string]any)
	require.True(t, ok)
	assert.Equal(t, "#FF0000", attachments[0]["color"])
}

func TestFormatAlert_Webhook(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatWebhook)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify basic fields
	assert.Equal(t, "TestAlert", result["alert_name"])
	assert.Equal(t, "test-fingerprint-123", result["fingerprint"])
	assert.Equal(t, "firing", result["status"])

	// Verify classification is included
	assert.Contains(t, result, "classification")

	classification, ok := result["classification"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, classification, "severity")     // lowercase after JSON marshal/unmarshal
	assert.Contains(t, classification, "confidence")   // lowercase after JSON marshal/unmarshal
	assert.Contains(t, classification, "reasoning")
	assert.Contains(t, classification, "recommendations")
}

func TestFormatAlert_NilAlert(t *testing.T) {
	formatter := NewAlertFormatter()

	result, err := formatter.FormatAlert(context.Background(), nil, core.FormatWebhook)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "nil")
}

func TestFormatAlert_NilClassification(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()
	enrichedAlert.Classification = nil

	// Should still work without classification
	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.FormatRootly)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Should use label-based severity fallback
	assert.Equal(t, "major", result["severity"]) // "warning" label maps to "major"
}

func TestFormatAlert_UnknownFormat(t *testing.T) {
	formatter := NewAlertFormatter()
	enrichedAlert := createTestEnrichedAlert()

	// Use unknown format (should default to webhook)
	result, err := formatter.FormatAlert(context.Background(), enrichedAlert, core.PublishingFormat("unknown"))

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Should have webhook format fields
	assert.Contains(t, result, "alert_name")
	assert.Contains(t, result, "fingerprint")
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"shorter than max", "hello", 10, "hello"},
		{"equal to max", "hello", 5, "hello"},
		{"longer than max", "hello world", 8, "hello..."},
		{"much longer", "this is a very long string that needs truncation", 20, "this is a very lo..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), tt.maxLen)
		})
	}
}

func TestLabelsToTags(t *testing.T) {
	labels := map[string]string{
		"app":         "myapp",
		"environment": "production",
		"team":        "platform",
	}

	tags := labelsToTags(labels)

	assert.Len(t, tags, 3)
	assert.Contains(t, tags, "app:myapp")
	assert.Contains(t, tags, "environment:production")
	assert.Contains(t, tags, "team:platform")
}
