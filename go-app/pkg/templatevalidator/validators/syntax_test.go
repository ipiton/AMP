package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Syntax Validator Tests
// ================================================================================

// MockTemplateEngine for testing
type MockTemplateEngine struct {
	parseError error
}

func (m *MockTemplateEngine) Parse(ctx context.Context, content string) error {
	return m.parseError
}

func (m *MockTemplateEngine) Execute(ctx context.Context, content string, data interface{}) (string, error) {
	return "", nil
}

func (m *MockTemplateEngine) Functions() []string {
	return []string{"toUpper", "toLower", "default", "range", "if"}
}

func TestSyntaxValidator_ValidTemplate(t *testing.T) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)

	opts := templatevalidator.DefaultValidateOptions()
	errors, warnings, suggestions, err := validator.Validate(context.Background(), "{{ .Status }}", opts)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(errors) > 0 {
		t.Errorf("Expected no errors, got %d", len(errors))
	}

	// Valid template may have warnings or suggestions
	_ = warnings
	_ = suggestions
}

func TestSyntaxValidator_InvalidSyntax(t *testing.T) {
	engine := &MockTemplateEngine{
		parseError: &templatevalidator.ValidationError{
			Phase:    "syntax",
			Severity: "critical",
			Message:  "template: test:1:10: function \"invalid\" not defined",
			Code:     "syntax-error",
		},
	}
	validator := NewSyntaxValidator(engine)

	opts := templatevalidator.DefaultValidateOptions()
	errors, _, _, err := validator.Validate(context.Background(), "{{ .Status | invalid }}", opts)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(errors) == 0 {
		t.Error("Expected at least 1 error for invalid syntax")
	}
}

func TestExtractFunctions(t *testing.T) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)

	content := "{{ .Status | toUpper | default \"unknown\" }}"
	functions := validator.extractFunctions(content)

	expectedFuncs := map[string]bool{
		"toUpper": true,
		"default": true,
	}

	for _, fn := range functions {
		if !expectedFuncs[fn] {
			t.Errorf("Unexpected function: %s", fn)
		}
	}
}

func TestExtractVariables(t *testing.T) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)

	content := "{{ .Status }}: {{ .Labels.alertname }}"
	variables := validator.extractVariables(content)

	expectedVars := map[string]bool{
		".Status":          true,
		".Labels.alertname": true,
	}

	for _, v := range variables {
		if !expectedVars[v] {
			t.Errorf("Unexpected variable: %s", v)
		}
	}
}

// ================================================================================
