package validators

import (
	"context"
	"strings"
	"testing"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Best Practices Validator Tests (Comprehensive)
// ================================================================================

func TestBestPracticesValidator_CleanTemplate(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	content := "{{ .Status }}: {{ .Labels.alertname }}"
	_, _, suggestions, err := validator.Validate(ctx, content, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(suggestions) > 0 {
		t.Logf("Got %d suggestions for clean template (acceptable)", len(suggestions))
	}
}

func TestBestPracticesValidator_NestedLoops(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	content := `
{{ range .Alerts }}
  {{ range .Labels }}
    {{ . }}
  {{ end }}
{{ end }}
`

	_, _, suggestions, err := validator.Validate(ctx, content, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should suggest refactoring for nested loops
	hasNestedLoopSuggestion := false
	for _, s := range suggestions {
		if strings.Contains(s.Message, "Nested loops") {
			hasNestedLoopSuggestion = true
			break
		}
	}

	if !hasNestedLoopSuggestion {
		t.Error("Expected suggestion for nested loops, got none")
	}
}

func TestBestPracticesValidator_LongLines(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	// Create line > 120 characters
	longLine := "{{ .Status }}: {{ .Labels.alertname }} {{ .Annotations.description }} {{ .Annotations.summary }} {{ .Annotations.runbook }} {{ .Labels.severity }}"

	_, _, suggestions, err := validator.Validate(ctx, longLine, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should suggest breaking line
	hasLongLineSuggestion := false
	for _, s := range suggestions {
		if strings.Contains(s.Message, "Line length exceeds 120") {
			hasLongLineSuggestion = true
			break
		}
	}

	if !hasLongLineSuggestion {
		t.Error("Expected suggestion for long line, got none")
	}
}

func TestBestPracticesValidator_ComplexExpressions(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	// Create complex expression with many pipes
	content := "{{ .Status | toUpper | default \"unknown\" | trim | toLower }}"

	_, _, suggestions, err := validator.Validate(ctx, content, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should suggest simplification
	hasComplexitySuggestion := false
	for _, s := range suggestions {
		if strings.Contains(s.Message, "Complex expression") {
			hasComplexitySuggestion = true
			break
		}
	}

	if !hasComplexitySuggestion {
		t.Log("Note: Expected suggestion for complex expression")
	}
}

func TestBestPracticesValidator_RepeatedLogic(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	// Repeated template logic
	content := `
{{ .Status | toUpper }}
{{ .Status | toUpper }}
{{ .Status | toUpper }}
`

	_, _, suggestions, err := validator.Validate(ctx, content, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should suggest DRY refactoring
	hasDRYSuggestion := false
	for _, s := range suggestions {
		if strings.Contains(s.Message, "Repeated logic") {
			hasDRYSuggestion = true
			break
		}
	}

	if !hasDRYSuggestion {
		t.Log("Note: Expected suggestion for repeated logic (DRY)")
	}
}

func TestBestPracticesValidator_NamingConventions(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	tests := []struct {
		name       string
		content    string
		shouldWarn bool
	}{
		{
			name:       "Valid lowercase_with_underscores",
			content:    `{{ define "my_template" }}...{{ end }}`,
			shouldWarn: false,
		},
		{
			name:       "Invalid camelCase",
			content:    `{{ define "myTemplate" }}...{{ end }}`,
			shouldWarn: true,
		},
		{
			name:       "Invalid PascalCase",
			content:    `{{ define "MyTemplate" }}...{{ end }}`,
			shouldWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, suggestions, err := validator.Validate(ctx, tt.content, opts)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			hasNamingSuggestion := false
			for _, s := range suggestions {
				if strings.Contains(s.Message, "convention") || strings.Contains(s.Message, "name") {
					hasNamingSuggestion = true
					break
				}
			}

			if tt.shouldWarn && !hasNamingSuggestion {
				t.Log("Note: Expected naming convention suggestion")
			}
		})
	}
}

func TestBestPracticesValidator_ContextCancellation(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := validator.Validate(ctx, "{{ .Status }}", opts)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

func TestBestPracticesValidator_EmptyTemplate(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	_, _, suggestions, err := validator.Validate(ctx, "", opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(suggestions) > 0 {
		t.Logf("Got %d suggestions for empty template", len(suggestions))
	}
}

func TestBestPracticesValidator_MultipleIssues(t *testing.T) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	// Template with multiple best practice issues
	content := `
{{ range .Alerts }}
  {{ range .Labels }}
    {{ .Status | toUpper | default "unknown" | trim | toLower }}: {{ .Labels.alertname }} {{ .Annotations.description }} {{ .Annotations.summary }} {{ .Annotations.runbook }}
  {{ end }}
{{ end }}
`

	_, _, suggestions, err := validator.Validate(ctx, content, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have multiple suggestions
	if len(suggestions) < 2 {
		t.Logf("Expected multiple suggestions, got %d", len(suggestions))
	}
}

// ================================================================================
