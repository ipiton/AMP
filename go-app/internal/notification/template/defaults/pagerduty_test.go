package defaults

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// TN-154: Default Templates - PagerDuty Template Tests
// ================================================================================

func TestGetPagerDutySeverity(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		expected string
	}{
		{
			name:     "critical severity",
			severity: "critical",
			expected: "critical",
		},
		{
			name:     "error severity",
			severity: "error",
			expected: "error",
		},
		{
			name:     "warning severity",
			severity: "warning",
			expected: "warning",
		},
		{
			name:     "info severity",
			severity: "info",
			expected: "info",
		},
		{
			name:     "unknown severity defaults to info",
			severity: "unknown",
			expected: "info",
		},
		{
			name:     "uppercase CRITICAL",
			severity: "CRITICAL",
			expected: "critical",
		},
		{
			name:     "mixed case Warning",
			severity: "Warning",
			expected: "warning",
		},
		{
			name:     "empty severity defaults to info",
			severity: "",
			expected: "info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetPagerDutySeverity(tt.severity)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDefaultPagerDutyTemplates(t *testing.T) {
	templates := GetDefaultPagerDutyTemplates()

	require.NotNil(t, templates)
	assert.NotEmpty(t, templates.Description)
	assert.NotEmpty(t, templates.DetailsSingle)
	assert.NotEmpty(t, templates.DetailsMulti)
	assert.NotNil(t, templates.SeverityFunc)

	// Test that SeverityFunc works
	severity := templates.SeverityFunc("critical")
	assert.Equal(t, "critical", severity)
}

func TestDefaultPagerDutyDescription(t *testing.T) {
	assert.NotEmpty(t, DefaultPagerDutyDescription)
	assert.Contains(t, DefaultPagerDutyDescription, "Status")
	assert.Contains(t, DefaultPagerDutyDescription, "GroupLabels.alertname")
}

func TestDefaultPagerDutyDetailsSingle(t *testing.T) {
	assert.NotEmpty(t, DefaultPagerDutyDetailsSingle)
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "severity")
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "environment")
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "instance")
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "description")
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "runbook_url")
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "generator_url")

	// Verify it's valid JSON structure
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "{")
	assert.Contains(t, DefaultPagerDutyDetailsSingle, "}")
}

func TestDefaultPagerDutyDetailsMulti(t *testing.T) {
	assert.NotEmpty(t, DefaultPagerDutyDetailsMulti)
	assert.Contains(t, DefaultPagerDutyDetailsMulti, "alert_count")
	assert.Contains(t, DefaultPagerDutyDetailsMulti, "severity")
	assert.Contains(t, DefaultPagerDutyDetailsMulti, "environment")
	assert.Contains(t, DefaultPagerDutyDetailsMulti, "status")

	// Verify it's valid JSON structure
	assert.Contains(t, DefaultPagerDutyDetailsMulti, "{")
	assert.Contains(t, DefaultPagerDutyDetailsMulti, "}")
}

func TestValidatePagerDutyDescriptionSize(t *testing.T) {
	tests := []struct {
		name        string
		description string
		expected    bool
	}{
		{
			name:        "short description",
			description: "Alert",
			expected:    true,
		},
		{
			name:        "medium description",
			description: "HighCPU: CPU usage above 90% threshold",
			expected:    true,
		},
		{
			name:        "large but valid description",
			description: string(make([]byte, 1000)),
			expected:    true,
		},
		{
			name:        "exactly 1023 chars (valid)",
			description: string(make([]byte, 1023)),
			expected:    true,
		},
		{
			name:        "exactly 1024 chars (invalid)",
			description: string(make([]byte, 1024)),
			expected:    false,
		},
		{
			name:        "too large description",
			description: string(make([]byte, 2000)),
			expected:    false,
		},
		{
			name:        "empty description",
			description: "",
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidatePagerDutyDescriptionSize(tt.description)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPagerDutyTemplatesStructure(t *testing.T) {
	templates := &PagerDutyTemplates{
		Description:   "test-description",
		DetailsSingle: "test-details-single",
		DetailsMulti:  "test-details-multi",
		SeverityFunc:  GetPagerDutySeverity,
	}

	assert.Equal(t, "test-description", templates.Description)
	assert.Equal(t, "test-details-single", templates.DetailsSingle)
	assert.Equal(t, "test-details-multi", templates.DetailsMulti)
	assert.NotNil(t, templates.SeverityFunc)
}

func TestPagerDutyTemplateConstants(t *testing.T) {
	// Verify all constants are defined and non-empty
	constants := map[string]string{
		"DefaultPagerDutyDescription":   DefaultPagerDutyDescription,
		"DefaultPagerDutyDetailsSingle": DefaultPagerDutyDetailsSingle,
		"DefaultPagerDutyDetailsMulti":  DefaultPagerDutyDetailsMulti,
	}

	for name, value := range constants {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, value, "%s should not be empty", name)
			assert.Greater(t, len(value), 10, "%s should have reasonable length", name)
		})
	}
}

func TestPagerDutySeverityMapping(t *testing.T) {
	// Test all severity levels map to valid PagerDuty severities
	validSeverities := map[string]bool{
		"critical": true,
		"error":    true,
		"warning":  true,
		"info":     true,
	}

	severities := []string{"critical", "error", "warning", "info", "unknown", ""}

	for _, severity := range severities {
		t.Run("severity_"+severity, func(t *testing.T) {
			result := GetPagerDutySeverity(severity)
			assert.True(t, validSeverities[result], "Severity %s should be valid PagerDuty severity", result)
		})
	}
}

func TestPagerDutyDescriptionSizeLimit(t *testing.T) {
	// Verify description template is reasonable size
	assert.Less(t, len(DefaultPagerDutyDescription), 500,
		"Description template should be < 500 chars to leave room for data")
}

func TestPagerDutyDetailsJSONStructure(t *testing.T) {
	// Verify details templates have valid JSON structure
	templates := []struct {
		name     string
		template string
	}{
		{"DetailsSingle", DefaultPagerDutyDetailsSingle},
		{"DetailsMulti", DefaultPagerDutyDetailsMulti},
	}

	for _, tt := range templates {
		t.Run(tt.name, func(t *testing.T) {
			// Check for JSON object markers
			assert.Contains(t, tt.template, "{")
			assert.Contains(t, tt.template, "}")

			// Check for key-value structure
			assert.Contains(t, tt.template, "\"")
			assert.Contains(t, tt.template, ":")

			// Verify no trailing commas (common JSON error)
			assert.NotContains(t, tt.template, ",\n}")
		})
	}
}

// Benchmark tests
func BenchmarkGetPagerDutySeverity(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetPagerDutySeverity("critical")
	}
}

func BenchmarkGetDefaultPagerDutyTemplates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetDefaultPagerDutyTemplates()
	}
}

func BenchmarkValidatePagerDutyDescriptionSize(b *testing.B) {
	description := "HighCPU: CPU usage above 90% threshold"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ValidatePagerDutyDescriptionSize(description)
	}
}
