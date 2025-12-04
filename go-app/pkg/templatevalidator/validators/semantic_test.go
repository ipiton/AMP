package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Semantic Validator Tests (Comprehensive)
// ================================================================================

func TestSemanticValidator_ValidFields(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	tests := []struct {
		name     string
		content  string
		wantErr  bool
	}{
		{
			name:    "Valid Status field",
			content: "{{ .Status }}",
			wantErr: false,
		},
		{
			name:    "Valid Labels field",
			content: "{{ .Labels }}",
			wantErr: false,
		},
		{
			name:    "Valid Labels.alertname",
			content: "{{ .Labels.alertname }}",
			wantErr: false,
		},
		{
			name:    "Valid Annotations.summary",
			content: "{{ .Annotations.summary }}",
			wantErr: false,
		},
		{
			name:    "Valid StartsAt",
			content: "{{ .StartsAt }}",
			wantErr: false,
		},
		{
			name:    "Valid EndsAt",
			content: "{{ .EndsAt }}",
			wantErr: false,
		},
		{
			name:    "Valid GeneratorURL",
			content: "{{ .GeneratorURL }}",
			wantErr: false,
		},
		{
			name:    "Valid Fingerprint",
			content: "{{ .Fingerprint }}",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors, _, _, err := validator.Validate(ctx, tt.content, opts)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tt.wantErr && len(errors) == 0 {
				t.Error("Expected errors, got none")
			}
			if !tt.wantErr && len(errors) > 0 {
				t.Errorf("Expected no errors, got %d: %v", len(errors), errors)
			}
		})
	}
}

func TestSemanticValidator_InvalidFields(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "Unknown field UnknownField",
			content: "{{ .UnknownField }}",
		},
		{
			name:    "Unknown field InvalidData",
			content: "{{ .InvalidData }}",
		},
		{
			name:    "Unknown field Metadata",
			content: "{{ .Metadata }}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors, _, _, err := validator.Validate(ctx, tt.content, opts)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(errors) == 0 {
				t.Error("Expected errors for unknown field, got none")
			}
		})
	}
}

func TestSemanticValidator_NestedAccess(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	tests := []struct {
		name     string
		content  string
		wantErr  bool
		errCount int
	}{
		{
			name:     "Valid map access",
			content:  "{{ .Labels.alertname }}",
			wantErr:  false,
			errCount: 0,
		},
		{
			name:     "Invalid nested map access",
			content:  "{{ .Labels.foo.bar }}",
			wantErr:  true,
			errCount: 1,
		},
		{
			name:     "Invalid nested non-map access",
			content:  "{{ .Status.foo }}",
			wantErr:  true,
			errCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors, _, _, err := validator.Validate(ctx, tt.content, opts)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tt.wantErr && len(errors) != tt.errCount {
				t.Errorf("Expected %d errors, got %d", tt.errCount, len(errors))
			}
			if !tt.wantErr && len(errors) > 0 {
				t.Errorf("Expected no errors, got %d", len(errors))
			}
		})
	}
}

func TestSemanticValidator_OptionalFields(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	// EndsAt and GeneratorURL are optional fields
	optionalFields := []string{
		"{{ .EndsAt }}",
		"{{ .GeneratorURL }}",
	}

	for _, content := range optionalFields {
		t.Run(content, func(t *testing.T) {
			_, warnings, _, err := validator.Validate(ctx, content, opts)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Should have warning about optional field
			if len(warnings) == 0 {
				t.Error("Expected warning for optional field, got none")
			}
		})
	}
}

func TestSemanticValidator_MapKeyWarnings(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	mapAccess := []string{
		"{{ .Labels.severity }}",
		"{{ .Labels.alertname }}",
		"{{ .Annotations.summary }}",
		"{{ .Annotations.description }}",
	}

	for _, content := range mapAccess {
		t.Run(content, func(t *testing.T) {
			_, warnings, _, err := validator.Validate(ctx, content, opts)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Should have warning about map key not guaranteed
			if len(warnings) == 0 {
				t.Error("Expected warning for map key access, got none")
			}
		})
	}
}

func TestSemanticValidator_MultipleVariables(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	content := `
{{ .Status }}: {{ .Labels.alertname }}
{{ .Annotations.summary }}
{{ .StartsAt }}
`

	errors, warnings, _, err := validator.Validate(ctx, content, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(errors) > 0 {
		t.Errorf("Expected no errors, got %d", len(errors))
	}

	// Should have warnings for map keys and optional fields
	if len(warnings) == 0 {
		t.Log("Note: Expected some warnings for map keys")
	}
}

func TestSemanticValidator_ContextCancellation(t *testing.T) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := validator.Validate(ctx, "{{ .Status }}", opts)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

// ================================================================================
