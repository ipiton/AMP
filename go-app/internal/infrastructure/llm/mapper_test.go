package llm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

func TestCoreAlertToLLMRequest(t *testing.T) {
	t.Run("complete alert", func(t *testing.T) {
		now := time.Now()
		endsAt := now.Add(1 * time.Hour)

		coreAlert := &core.Alert{
			Fingerprint: "abc123",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"severity":  "critical",
				"namespace": "production",
			},
			Annotations: map[string]string{
				"description": "CPU is high",
			},
			StartsAt: now,
			EndsAt:   &endsAt,
		}

		llmReq := CoreAlertToLLMRequest(coreAlert)

		require.NotNil(t, llmReq)
		assert.Equal(t, "abc123", llmReq.Fingerprint)
		assert.Equal(t, "HighCPUUsage", llmReq.AlertName)
		assert.Equal(t, "firing", llmReq.Status)
		assert.Equal(t, coreAlert.Labels, llmReq.Labels)
		assert.Equal(t, coreAlert.Annotations, llmReq.Annotations)
		assert.Equal(t, now.Format(time.RFC3339), llmReq.StartsAt)
		assert.Equal(t, endsAt.Format(time.RFC3339), llmReq.EndsAt)
	})

	t.Run("alert without EndsAt", func(t *testing.T) {
		now := time.Now()

		coreAlert := &core.Alert{
			Fingerprint: "abc123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			StartsAt:    now,
		}

		llmReq := CoreAlertToLLMRequest(coreAlert)

		require.NotNil(t, llmReq)
		assert.Empty(t, llmReq.EndsAt)
	})

	t.Run("nil alert", func(t *testing.T) {
		llmReq := CoreAlertToLLMRequest(nil)
		assert.Nil(t, llmReq)
	})

	t.Run("resolved alert", func(t *testing.T) {
		now := time.Now()
		endsAt := now.Add(1 * time.Hour)

		coreAlert := &core.Alert{
			Fingerprint: "xyz789",
			AlertName:   "TestAlert",
			Status:      core.StatusResolved,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			StartsAt:    now,
			EndsAt:      &endsAt,
		}

		llmReq := CoreAlertToLLMRequest(coreAlert)

		require.NotNil(t, llmReq)
		assert.Equal(t, "resolved", llmReq.Status)
	})
}

func TestLLMResponseToCoreClassification(t *testing.T) {
	t.Run("valid response - critical", func(t *testing.T) {
		llmResp := &LLMClassificationResponse{
			Severity:   4,
			Category:   "infrastructure",
			Summary:    "High CPU",
			Confidence: 0.95,
			Reasoning:  "CPU usage is consistently high",
			Suggestions: []string{
				"Scale horizontally",
				"Check for memory leaks",
			},
		}

		result, err := LLMResponseToCoreClassification(llmResp)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, core.SeverityCritical, result.Severity)
		assert.Equal(t, 0.95, result.Confidence)
		assert.Equal(t, "CPU usage is consistently high", result.Reasoning)
		assert.Equal(t, llmResp.Suggestions, result.Recommendations)
		assert.Equal(t, "infrastructure", result.Metadata["category"])
		assert.Equal(t, "High CPU", result.Metadata["summary"])
	})

	t.Run("valid response - warning", func(t *testing.T) {
		llmResp := &LLMClassificationResponse{
			Severity:    3,
			Category:    "application",
			Summary:     "Warning",
			Confidence:  0.75,
			Reasoning:   "Moderate issue",
			Suggestions: []string{"Monitor"},
		}

		result, err := LLMResponseToCoreClassification(llmResp)

		require.NoError(t, err)
		assert.Equal(t, core.SeverityWarning, result.Severity)
	})

	t.Run("valid response - info", func(t *testing.T) {
		llmResp := &LLMClassificationResponse{
			Severity:    2,
			Confidence:  0.5,
			Reasoning:   "Informational",
			Suggestions: []string{},
		}

		result, err := LLMResponseToCoreClassification(llmResp)

		require.NoError(t, err)
		assert.Equal(t, core.SeverityInfo, result.Severity)
	})

	t.Run("valid response - noise", func(t *testing.T) {
		llmResp := &LLMClassificationResponse{
			Severity:    1,
			Confidence:  0.3,
			Reasoning:   "Likely noise",
			Suggestions: []string{"Ignore"},
		}

		result, err := LLMResponseToCoreClassification(llmResp)

		require.NoError(t, err)
		assert.Equal(t, core.SeverityNoise, result.Severity)
	})

	t.Run("nil response", func(t *testing.T) {
		result, err := LLMResponseToCoreClassification(nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "nil")
	})

	t.Run("invalid severity", func(t *testing.T) {
		llmResp := &LLMClassificationResponse{
			Severity:   999,
			Confidence: 0.5,
			Reasoning:  "Test",
		}

		result, err := LLMResponseToCoreClassification(llmResp)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid severity")
	})
}

func TestMapIntToSeverity(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected core.AlertSeverity
		wantErr  bool
	}{
		{"noise", 1, core.SeverityNoise, false},
		{"info", 2, core.SeverityInfo, false},
		{"warning", 3, core.SeverityWarning, false},
		{"critical", 4, core.SeverityCritical, false},
		{"invalid 0", 0, "", true},
		{"invalid 5", 5, "", true},
		{"invalid negative", -1, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := mapIntToSeverity(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestMapSeverityToInt(t *testing.T) {
	tests := []struct {
		name     string
		input    core.AlertSeverity
		expected int
	}{
		{"noise", core.SeverityNoise, 1},
		{"info", core.SeverityInfo, 2},
		{"warning", core.SeverityWarning, 3},
		{"critical", core.SeverityCritical, 4},
		{"invalid", "invalid", 2}, // defaults to info
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapSeverityToInt(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCoreClassificationToLLMResponse(t *testing.T) {
	t.Run("complete classification", func(t *testing.T) {
		coreResult := &core.ClassificationResult{
			Severity:        core.SeverityCritical,
			Confidence:      0.95,
			Reasoning:       "High priority",
			Recommendations: []string{"Act now"},
			Metadata: map[string]any{
				"category": "infrastructure",
				"summary":  "Critical issue",
			},
		}

		llmResp := CoreClassificationToLLMResponse(coreResult)

		require.NotNil(t, llmResp)
		assert.Equal(t, 4, llmResp.Severity)
		assert.Equal(t, 0.95, llmResp.Confidence)
		assert.Equal(t, "High priority", llmResp.Reasoning)
		assert.Equal(t, coreResult.Recommendations, llmResp.Suggestions)
		assert.Equal(t, "infrastructure", llmResp.Category)
		assert.Equal(t, "Critical issue", llmResp.Summary)
	})

	t.Run("nil classification", func(t *testing.T) {
		llmResp := CoreClassificationToLLMResponse(nil)
		assert.Nil(t, llmResp)
	})

	t.Run("classification without metadata", func(t *testing.T) {
		coreResult := &core.ClassificationResult{
			Severity:        core.SeverityWarning,
			Confidence:      0.8,
			Reasoning:       "Test",
			Recommendations: []string{},
		}

		llmResp := CoreClassificationToLLMResponse(coreResult)

		require.NotNil(t, llmResp)
		assert.Empty(t, llmResp.Category)
		assert.Empty(t, llmResp.Summary)
	})
}

func TestParseProcessingTime(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		wantErr  bool
	}{
		{"milliseconds", "234ms", 0.234, false},
		{"seconds", "1.5s", 1.5, false},
		{"float", "0.234", 0.234, false},
		{"integer", "2", 2.0, false},
		{"empty", "", 0, false},
		{"invalid", "invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseProcessingTime(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.InDelta(t, tt.expected, result, 0.001)
			}
		})
	}
}

func TestFormatProcessingTime(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected string
	}{
		{"sub-second", 0.234, "0.234s"},
		{"one second", 1.0, "1.000s"},
		{"multiple seconds", 2.567, "2.567s"},
		{"zero", 0.0, "0.000s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatProcessingTime(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoundTripConversion(t *testing.T) {
	t.Run("Alert round trip", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)

		original := &core.Alert{
			Fingerprint: "test123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"key": "value"},
			Annotations: map[string]string{"desc": "test"},
			StartsAt:    now,
		}

		// Core -> LLM Request
		llmReq := CoreAlertToLLMRequest(original)
		require.NotNil(t, llmReq)

		// Verify key fields preserved
		assert.Equal(t, original.Fingerprint, llmReq.Fingerprint)
		assert.Equal(t, original.AlertName, llmReq.AlertName)
		assert.Equal(t, string(original.Status), llmReq.Status)
	})

	t.Run("Classification round trip", func(t *testing.T) {
		original := &core.ClassificationResult{
			Severity:        core.SeverityCritical,
			Confidence:      0.95,
			Reasoning:       "Test",
			Recommendations: []string{"action1"},
			Metadata: map[string]any{
				"category": "test",
				"summary":  "summary",
			},
		}

		// Core -> LLM Response
		llmResp := CoreClassificationToLLMResponse(original)
		require.NotNil(t, llmResp)

		// LLM Response -> Core
		converted, err := LLMResponseToCoreClassification(llmResp)
		require.NoError(t, err)

		// Verify round trip
		assert.Equal(t, original.Severity, converted.Severity)
		assert.Equal(t, original.Confidence, converted.Confidence)
		assert.Equal(t, original.Reasoning, converted.Reasoning)
		assert.Equal(t, original.Recommendations, converted.Recommendations)
	})
}
