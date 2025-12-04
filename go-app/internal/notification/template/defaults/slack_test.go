package defaults

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// TN-154: Default Templates - Slack Template Tests
// ================================================================================

func TestGetSlackColor(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		expected string
	}{
		{
			name:     "critical severity",
			severity: "critical",
			expected: "danger",
		},
		{
			name:     "error severity",
			severity: "error",
			expected: "danger",
		},
		{
			name:     "warning severity",
			severity: "warning",
			expected: "warning",
		},
		{
			name:     "info severity",
			severity: "info",
			expected: "good",
		},
		{
			name:     "unknown severity",
			severity: "unknown",
			expected: "#439FE0",
		},
		{
			name:     "uppercase CRITICAL",
			severity: "CRITICAL",
			expected: "danger",
		},
		{
			name:     "mixed case Warning",
			severity: "Warning",
			expected: "warning",
		},
		{
			name:     "empty severity",
			severity: "",
			expected: "#439FE0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetSlackColor(tt.severity)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDefaultSlackTemplates(t *testing.T) {
	templates := GetDefaultSlackTemplates()

	require.NotNil(t, templates)
	assert.NotEmpty(t, templates.Title)
	assert.NotEmpty(t, templates.Text)
	assert.NotEmpty(t, templates.Pretext)
	assert.NotEmpty(t, templates.FieldsSingle)
	assert.NotEmpty(t, templates.FieldsMulti)
	assert.NotNil(t, templates.ColorFunc)

	// Test that ColorFunc works
	color := templates.ColorFunc("critical")
	assert.Equal(t, "danger", color)
}

func TestDefaultSlackTitle(t *testing.T) {
	assert.NotEmpty(t, DefaultSlackTitle)
	assert.Contains(t, DefaultSlackTitle, "Status")
	assert.Contains(t, DefaultSlackTitle, "GroupLabels.alertname")
}

func TestDefaultSlackText(t *testing.T) {
	assert.NotEmpty(t, DefaultSlackText)
	assert.Contains(t, DefaultSlackText, "len .Alerts")
	assert.Contains(t, DefaultSlackText, "Annotations.summary")
}

func TestDefaultSlackPretext(t *testing.T) {
	assert.NotEmpty(t, DefaultSlackPretext)
	assert.Contains(t, DefaultSlackPretext, "CommonLabels.environment")
	assert.Contains(t, DefaultSlackPretext, "CommonLabels.cluster")
}

func TestDefaultSlackFieldsSingle(t *testing.T) {
	assert.NotEmpty(t, DefaultSlackFieldsSingle)
	assert.Contains(t, DefaultSlackFieldsSingle, "Severity")
	assert.Contains(t, DefaultSlackFieldsSingle, "Instance")
	assert.Contains(t, DefaultSlackFieldsSingle, "Description")

	// Verify it's valid JSON structure
	assert.Contains(t, DefaultSlackFieldsSingle, "[")
	assert.Contains(t, DefaultSlackFieldsSingle, "]")
	assert.Contains(t, DefaultSlackFieldsSingle, "\"title\"")
	assert.Contains(t, DefaultSlackFieldsSingle, "\"value\"")
	assert.Contains(t, DefaultSlackFieldsSingle, "\"short\"")
}

func TestDefaultSlackFieldsMulti(t *testing.T) {
	assert.NotEmpty(t, DefaultSlackFieldsMulti)
	assert.Contains(t, DefaultSlackFieldsMulti, "Severity")
	assert.Contains(t, DefaultSlackFieldsMulti, "Alert Count")
	assert.Contains(t, DefaultSlackFieldsMulti, "Environment")

	// Verify it's valid JSON structure
	assert.Contains(t, DefaultSlackFieldsMulti, "[")
	assert.Contains(t, DefaultSlackFieldsMulti, "]")
	assert.Contains(t, DefaultSlackFieldsMulti, "\"title\"")
	assert.Contains(t, DefaultSlackFieldsMulti, "\"value\"")
}

func TestValidateSlackMessageSize(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		text     string
		pretext  string
		fields   string
		expected bool
	}{
		{
			name:     "small message",
			title:    "Alert",
			text:     "Test message",
			pretext:  "Context",
			fields:   "[]",
			expected: true,
		},
		{
			name:     "medium message",
			title:    "Alert: HighCPU",
			text:     "CPU usage is above 90% threshold",
			pretext:  "Environment: production | Cluster: us-east-1",
			fields:   `[{"title":"Severity","value":"CRITICAL","short":true}]`,
			expected: true,
		},
		{
			name:     "large but valid message",
			title:    string(make([]byte, 500)),
			text:     string(make([]byte, 1000)),
			pretext:  string(make([]byte, 500)),
			fields:   string(make([]byte, 900)),
			expected: true,
		},
		{
			name:     "too large message",
			title:    string(make([]byte, 1000)),
			text:     string(make([]byte, 1000)),
			pretext:  string(make([]byte, 1000)),
			fields:   string(make([]byte, 1000)),
			expected: false,
		},
		{
			name:     "empty message",
			title:    "",
			text:     "",
			pretext:  "",
			fields:   "",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateSlackMessageSize(tt.title, tt.text, tt.pretext, tt.fields)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSlackTemplatesStructure(t *testing.T) {
	templates := &SlackTemplates{
		Title:        "test-title",
		Text:         "test-text",
		Pretext:      "test-pretext",
		FieldsSingle: "test-fields-single",
		FieldsMulti:  "test-fields-multi",
		ColorFunc:    GetSlackColor,
	}

	assert.Equal(t, "test-title", templates.Title)
	assert.Equal(t, "test-text", templates.Text)
	assert.Equal(t, "test-pretext", templates.Pretext)
	assert.Equal(t, "test-fields-single", templates.FieldsSingle)
	assert.Equal(t, "test-fields-multi", templates.FieldsMulti)
	assert.NotNil(t, templates.ColorFunc)
}

func TestSlackTemplateConstants(t *testing.T) {
	// Verify all constants are defined and non-empty
	constants := map[string]string{
		"DefaultSlackTitle":        DefaultSlackTitle,
		"DefaultSlackText":         DefaultSlackText,
		"DefaultSlackPretext":      DefaultSlackPretext,
		"DefaultSlackFieldsSingle": DefaultSlackFieldsSingle,
		"DefaultSlackFieldsMulti":  DefaultSlackFieldsMulti,
	}

	for name, value := range constants {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, value, "%s should not be empty", name)
			assert.Greater(t, len(value), 10, "%s should have reasonable length", name)
		})
	}
}

func TestSlackColorMapping(t *testing.T) {
	// Test all severity levels map to valid Slack colors
	validColors := map[string]bool{
		"danger":  true,
		"warning": true,
		"good":    true,
		"#439FE0": true,
	}

	severities := []string{"critical", "error", "warning", "info", "unknown", ""}

	for _, severity := range severities {
		t.Run("severity_"+severity, func(t *testing.T) {
			color := GetSlackColor(severity)
			assert.True(t, validColors[color], "Color %s should be valid Slack color", color)
		})
	}
}

// Benchmark tests
func BenchmarkGetSlackColor(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetSlackColor("critical")
	}
}

func BenchmarkGetDefaultSlackTemplates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetDefaultSlackTemplates()
	}
}

func BenchmarkValidateSlackMessageSize(b *testing.B) {
	title := "Alert: HighCPU"
	text := "CPU usage is above 90% threshold"
	pretext := "Environment: production"
	fields := `[{"title":"Severity","value":"CRITICAL"}]`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ValidateSlackMessageSize(title, text, pretext, fields)
	}
}
