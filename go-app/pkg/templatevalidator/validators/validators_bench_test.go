package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Benchmarks
// ================================================================================

// Benchmark templates for testing
const (
	simpleTemplate = `{{ .Status }}: {{ .Labels.alertname }}`

	complexTemplate = `{{ .Status | toUpper }}: {{ .Labels.alertname | default "unknown" }}
{{ range .Annotations }}
  {{ . }}
{{ end }}`

	securityTemplate = `API_KEY=sk-1234567890abcdef
{{ .Status }}: {{ .Labels.alertname }}`
)

// BenchmarkSyntaxValidator_Simple benchmarks syntax validation of simple template
func BenchmarkSyntaxValidator_Simple(b *testing.B) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, simpleTemplate, opts)
	}
}

// BenchmarkSyntaxValidator_Complex benchmarks syntax validation of complex template
func BenchmarkSyntaxValidator_Complex(b *testing.B) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, complexTemplate, opts)
	}
}

// BenchmarkSemanticValidator_Simple benchmarks semantic validation
func BenchmarkSemanticValidator_Simple(b *testing.B) {
	validator := NewSemanticValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, simpleTemplate, opts)
	}
}

// BenchmarkSecurityValidator_Clean benchmarks security validation of clean template
func BenchmarkSecurityValidator_Clean(b *testing.B) {
	validator := NewSecurityValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, simpleTemplate, opts)
	}
}

// BenchmarkSecurityValidator_WithSecret benchmarks security validation with secret
func BenchmarkSecurityValidator_WithSecret(b *testing.B) {
	validator := NewSecurityValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, securityTemplate, opts)
	}
}

// BenchmarkBestPracticesValidator_Simple benchmarks best practices validation
func BenchmarkBestPracticesValidator_Simple(b *testing.B) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, simpleTemplate, opts)
	}
}

// BenchmarkBestPracticesValidator_Complex benchmarks best practices with complex template
func BenchmarkBestPracticesValidator_Complex(b *testing.B) {
	validator := NewBestPracticesValidator()
	opts := templatevalidator.DefaultValidateOptions()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = validator.Validate(ctx, complexTemplate, opts)
	}
}

// BenchmarkExtractFunctions benchmarks function extraction
func BenchmarkExtractFunctions(b *testing.B) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.extractFunctions(complexTemplate)
	}
}

// BenchmarkExtractVariables benchmarks variable extraction
func BenchmarkExtractVariables(b *testing.B) {
	engine := &MockTemplateEngine{}
	validator := NewSyntaxValidator(engine)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.extractVariables(complexTemplate)
	}
}

// ================================================================================
