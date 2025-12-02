package publishing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/ipiton/AMP/internal/core"
)

// TestDefaultAlertValidator_ValidAlert tests valid alert passes all rules
func TestDefaultAlertValidator_ValidAlert(t *testing.T) {
	validator := NewDefaultAlertValidator()
	alert := createValidAlert()

	errors := validator.Validate(alert)

	assert.Empty(t, errors, "Valid alert should pass all validation rules")
}

// TestNotNilRule tests nil alert detection
func TestNotNilRule(t *testing.T) {
	rule := &NotNilRule{}

	err := rule.Validate(nil)

	assert.NotNil(t, err, "Should return error for nil alert")
	assert.Equal(t, "alert", err.Field)
	assert.Contains(t, err.Message, "nil")
}

// TestAlertNotNilRule tests inner Alert nil detection
func TestAlertNotNilRule(t *testing.T) {
	rule := &AlertNotNilRule{}

	alert := &core.EnrichedAlert{Alert: nil}
	err := rule.Validate(alert)

	assert.NotNil(t, err, "Should return error for nil inner alert")
	assert.Equal(t, "alert.Alert", err.Field)
}

// TestAlertNameRequiredRule tests alert name validation
func TestAlertNameRequiredRule(t *testing.T) {
	rule := &AlertNameRequiredRule{}

	alert := createValidAlert()
	alert.Alert.AlertName = ""

	err := rule.Validate(alert)

	assert.NotNil(t, err, "Should return error for empty alert name")
	assert.Equal(t, "alert.AlertName", err.Field)
	assert.NotEmpty(t, err.Suggestion)
}

// TestFingerprintRequiredRule tests fingerprint validation
func TestFingerprintRequiredRule(t *testing.T) {
	rule := &FingerprintRequiredRule{}

	alert := createValidAlert()
	alert.Alert.Fingerprint = ""

	err := rule.Validate(alert)

	assert.NotNil(t, err)
	assert.Equal(t, "alert.Fingerprint", err.Field)
}

// TestStatusRequiredRule tests status required validation
func TestStatusRequiredRule(t *testing.T) {
	rule := &StatusRequiredRule{}

	alert := createValidAlert()
	alert.Alert.Status = ""

	err := rule.Validate(alert)

	assert.NotNil(t, err)
	assert.Equal(t, "alert.Status", err.Field)
}

// TestStatusValidRule tests status value validation
func TestStatusValidRule(t *testing.T) {
	rule := &StatusValidRule{}

	testCases := []struct {
		name    string
		status  core.AlertStatus
		isValid bool
	}{
		{"firing", core.StatusFiring, true},
		{"resolved", core.StatusResolved, true},
		{"invalid", core.AlertStatus("invalid"), false},
		{"pending", core.AlertStatus("pending"), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.Status = tc.status

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "Status %s should be valid", tc.status)
			} else {
				assert.NotNil(t, err, "Status %s should be invalid", tc.status)
				assert.Contains(t, err.Message, "invalid status")
			}
		})
	}
}

// TestFingerprintFormatRule tests fingerprint format validation
func TestFingerprintFormatRule(t *testing.T) {
	rule := &FingerprintFormatRule{}

	testCases := []struct {
		name        string
		fingerprint string
		isValid     bool
	}{
		{"valid hex", "abc123def456789012345678", true},
		{"valid short", "abc123def4567890", true},
		{"uppercase", "ABC123DEF456", false},
		{"with dash", "abc-123-def", false},
		{"too short", "abc123", false},
		{"special chars", "abc@123", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.Fingerprint = tc.fingerprint

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "Fingerprint %s should be valid", tc.fingerprint)
			} else {
				assert.NotNil(t, err, "Fingerprint %s should be invalid", tc.fingerprint)
			}
		})
	}
}

// TestAlertNameFormatRule tests alert name format validation
func TestAlertNameFormatRule(t *testing.T) {
	rule := &AlertNameFormatRule{}

	testCases := []struct {
		name      string
		alertName string
		isValid   bool
	}{
		{"valid", "HighCPUUsage", true},
		{"with underscore", "High_CPU_Usage", true},
		{"with dash", "High-CPU-Usage", true},
		{"lowercase start", "highCPUUsage", false},
		{"with space", "High CPU", false},
		{"special chars", "High@CPU", false},
		{"starts with number", "1HighCPU", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.AlertName = tc.alertName

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "Alert name %s should be valid", tc.alertName)
			} else {
				assert.NotNil(t, err, "Alert name %s should be invalid", tc.alertName)
			}
		})
	}
}

// TestGeneratorURLFormatRule tests URL format validation
func TestGeneratorURLFormatRule(t *testing.T) {
	rule := &GeneratorURLFormatRule{}

	testCases := []struct {
		name    string
		url     *string
		isValid bool
	}{
		{"nil", nil, true}, // Optional field
		{"valid http", strPtr("http://prometheus:9090/graph"), true},
		{"valid https", strPtr("https://prometheus:9090"), true},
		{"invalid", strPtr("not a url"), false},
		{"empty", strPtr(""), true}, // Empty is OK
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.GeneratorURL = tc.url

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "URL should be valid")
			} else {
				assert.NotNil(t, err, "URL should be invalid")
			}
		})
	}
}

// TestLabelsNotNilRule tests labels nil check
func TestLabelsNotNilRule(t *testing.T) {
	rule := &LabelsNotNilRule{}

	alert := createValidAlert()
	alert.Alert.Labels = nil

	err := rule.Validate(alert)

	assert.NotNil(t, err)
	assert.Equal(t, "alert.Labels", err.Field)
}

// TestAnnotationsNotNilRule tests annotations nil check
func TestAnnotationsNotNilRule(t *testing.T) {
	rule := &AnnotationsNotNilRule{}

	alert := createValidAlert()
	alert.Alert.Annotations = nil

	err := rule.Validate(alert)

	assert.NotNil(t, err)
	assert.Equal(t, "alert.Annotations", err.Field)
}

// TestLabelKeysValidRule tests label key format validation
func TestLabelKeysValidRule(t *testing.T) {
	rule := &LabelKeysValidRule{}

	testCases := []struct {
		name    string
		key     string
		isValid bool
	}{
		{"valid", "severity", true},
		{"with underscore", "alert_name", true},
		{"starts with underscore", "_internal", true},
		{"with number", "label1", true},
		{"starts with number", "1label", false},
		{"with dash", "alert-name", false},
		{"with space", "alert name", false},
		{"special chars", "alert@name", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.Labels = map[string]string{tc.key: "value"}

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "Label key %s should be valid", tc.key)
			} else {
				assert.NotNil(t, err, "Label key %s should be invalid", tc.key)
			}
		})
	}
}

// TestAnnotationKeysValidRule tests annotation key format validation
func TestAnnotationKeysValidRule(t *testing.T) {
	rule := &AnnotationKeysValidRule{}

	testCases := []struct {
		name    string
		key     string
		isValid bool
	}{
		{"valid", "summary", true},
		{"with underscore", "description_long", true},
		{"starts with dash", "-invalid", false},
		{"with space", "desc long", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.Annotations = map[string]string{tc.key: "value"}

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "Annotation key %s should be valid", tc.key)
			} else {
				assert.NotNil(t, err, "Annotation key %s should be invalid", tc.key)
			}
		})
	}
}

// TestStartsAtReasonableRule tests starts_at timestamp validation
func TestStartsAtReasonableRule(t *testing.T) {
	rule := &StartsAtReasonableRule{}

	testCases := []struct {
		name      string
		startsAt  time.Time
		isValid   bool
	}{
		{"now", time.Now(), true},
		{"1 hour ago", time.Now().Add(-1 * time.Hour), true},
		{"1 day ago", time.Now().Add(-24 * time.Hour), true},
		{"2 years ago", time.Now().Add(-2 * 365 * 24 * time.Hour), false},
		{"2 hours future", time.Now().Add(2 * time.Hour), false},
		{"30 min future", time.Now().Add(30 * time.Minute), true}, // Within 1h clock skew
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.StartsAt = tc.startsAt

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "StartsAt %s should be valid", tc.startsAt)
			} else {
				assert.NotNil(t, err, "StartsAt %s should be invalid", tc.startsAt)
			}
		})
	}
}

// TestEndsAtAfterStartsAtRule tests ends_at > starts_at validation
func TestEndsAtAfterStartsAtRule(t *testing.T) {
	rule := &EndsAtAfterStartsAtRule{}

	now := time.Now()

	testCases := []struct {
		name     string
		startsAt time.Time
		endsAt   *time.Time
		isValid  bool
	}{
		{"nil endsAt", now, nil, true}, // Optional
		{"zero endsAt", now, &time.Time{}, true},
		{"endsAt after startsAt", now, timePtr(now.Add(1 * time.Hour)), true},
		{"endsAt before startsAt", now, timePtr(now.Add(-1 * time.Hour)), false},
		{"endsAt equals startsAt", now, timePtr(now), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alert := createValidAlert()
			alert.Alert.StartsAt = tc.startsAt
			alert.Alert.EndsAt = tc.endsAt

			err := rule.Validate(alert)

			if tc.isValid {
				assert.Nil(t, err, "EndsAt should be valid")
			} else {
				assert.NotNil(t, err, "EndsAt should be invalid")
			}
		})
	}
}

// TestClassificationValidRule tests classification validation
func TestClassificationValidRule(t *testing.T) {
	rule := &ClassificationValidRule{}

	t.Run("nil classification", func(t *testing.T) {
		alert := createValidAlert()
		alert.Classification = nil

		err := rule.Validate(alert)
		assert.Nil(t, err, "Nil classification should be valid (optional)")
	})

	t.Run("valid classification", func(t *testing.T) {
		alert := createValidAlert()

		err := rule.Validate(alert)
		assert.Nil(t, err, "Valid classification should pass")
	})

	t.Run("invalid severity", func(t *testing.T) {
		alert := createValidAlert()
		alert.Classification.Severity = core.AlertSeverity("invalid")

		err := rule.Validate(alert)
		assert.NotNil(t, err)
		assert.Equal(t, "classification.Severity", err.Field)
	})

	t.Run("confidence out of range negative", func(t *testing.T) {
		alert := createValidAlert()
		alert.Classification.Confidence = -0.5

		err := rule.Validate(alert)
		assert.NotNil(t, err)
		assert.Equal(t, "classification.Confidence", err.Field)
	})

	t.Run("confidence out of range positive", func(t *testing.T) {
		alert := createValidAlert()
		alert.Classification.Confidence = 1.5

		err := rule.Validate(alert)
		assert.NotNil(t, err)
	})

	t.Run("empty reasoning with confidence", func(t *testing.T) {
		alert := createValidAlert()
		alert.Classification.Confidence = 0.8
		alert.Classification.Reasoning = ""

		err := rule.Validate(alert)
		assert.NotNil(t, err)
		assert.Equal(t, "classification.Reasoning", err.Field)
	})
}

// TestFormatValidationErrors tests error formatting
func TestFormatValidationErrors(t *testing.T) {
	t.Run("empty errors", func(t *testing.T) {
		formatted := FormatValidationErrors([]ValidationError{})
		assert.Empty(t, formatted)
	})

	t.Run("single error", func(t *testing.T) {
		errors := []ValidationError{
			{Field: "test", Message: "test error", Suggestion: "fix it"},
		}

		formatted := FormatValidationErrors(errors)
		assert.Contains(t, formatted, "1 error")
		assert.Contains(t, formatted, "test error")
		assert.Contains(t, formatted, "fix it")
	})

	t.Run("multiple errors", func(t *testing.T) {
		errors := []ValidationError{
			{Field: "field1", Message: "error1"},
			{Field: "field2", Message: "error2"},
		}

		formatted := FormatValidationErrors(errors)
		assert.Contains(t, formatted, "2 error")
		assert.Contains(t, formatted, "error1")
		assert.Contains(t, formatted, "error2")
	})
}

// TestDefaultAlertValidator_MultipleErrors tests multiple validation errors
func TestDefaultAlertValidator_MultipleErrors(t *testing.T) {
	validator := NewDefaultAlertValidator()

	// Create alert with multiple errors
	alert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "", // Empty fingerprint
			AlertName:   "", // Empty alert name
			Status:      core.AlertStatus("invalid"), // Invalid status
			StartsAt:    time.Time{}, // Zero time
			Labels:      nil, // Nil labels
			Annotations: nil, // Nil annotations
		},
	}

	errors := validator.Validate(alert)

	// Should have multiple errors
	assert.GreaterOrEqual(t, len(errors), 5, "Should have at least 5 validation errors")

	// Verify specific errors present
	fields := make(map[string]bool)
	for _, err := range errors {
		fields[err.Field] = true
	}

	assert.True(t, fields["alert.Fingerprint"], "Should have fingerprint error")
	assert.True(t, fields["alert.AlertName"], "Should have alert name error")
	assert.True(t, fields["alert.Status"], "Should have status error")
}

// Helper functions

func createValidAlert() *core.EnrichedAlert {
	now := time.Now()
	generatorURL := "http://prometheus:9090/graph"

	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "abc123def456789012345678",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"alertname": "HighCPUUsage",
				"severity":  "warning",
			},
			Annotations: map[string]string{
				"summary":     "High CPU usage",
				"description": "CPU > 80%",
			},
			StartsAt:     now,
			GeneratorURL: &generatorURL,
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityWarning,
			Confidence: 0.85,
			Reasoning:  "CPU usage is high",
			Recommendations: []string{
				"Check processes",
			},
		},
	}
}

func strPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}
