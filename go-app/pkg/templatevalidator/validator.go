package templatevalidator

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
)

// ================================================================================
// TN-156: Template Validator - Main Interface
// ================================================================================
// Main validation facade interface and factory.
//
// Features:
// - Single template validation
// - File validation
// - Batch parallel validation
// - TN-153 Template Engine integration
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// Validator is the main facade for template validation
//
// Validator orchestrates validation through multiple phases:
// 1. Syntax validation (Go text/template parsing)
// 2. Semantic validation (Alertmanager data model)
// 3. Security validation (XSS, secrets, injection)
// 4. Best practices validation (performance, readability)
//
// Example usage:
//
//	// Create validator with TN-153 engine
//	engine := template.NewNotificationTemplateEngine(template.DefaultTemplateEngineOptions())
//	validator := templatevalidator.New(engine)
//
//	// Validate template
//	result, err := validator.Validate(ctx, content, templatevalidator.DefaultValidateOptions())
//	if err != nil {
//	    return err
//	}
//
//	if !result.Valid {
//	    for _, err := range result.Errors {
//	        fmt.Printf("Error at line %d: %s\n", err.Line, err.Message)
//	    }
//	}
type Validator interface {
	// Validate validates a single template content
	//
	// Returns ValidationResult with errors, warnings, and suggestions.
	// Returns error only if validation itself fails (not for template errors).
	//
	// Example:
	//   result, err := validator.Validate(ctx, content, opts)
	Validate(ctx context.Context, content string, opts ValidateOptions) (*ValidationResult, error)

	// ValidateFile validates a template file
	//
	// Reads file and validates its content.
	// Returns ValidationResult with errors, warnings, and suggestions.
	//
	// Example:
	//   result, err := validator.ValidateFile(ctx, "slack.tmpl", opts)
	ValidateFile(ctx context.Context, filepath string, opts ValidateOptions) (*ValidationResult, error)

	// ValidateBatch validates multiple templates in parallel
	//
	// Uses ParallelWorkers from opts (or CPU count if 0).
	// Returns slice of results in same order as input.
	//
	// Example:
	//   inputs := []TemplateInput{{Name: "slack", Content: "...", Options: opts}}
	//   results, err := validator.ValidateBatch(ctx, inputs)
	ValidateBatch(ctx context.Context, templates []TemplateInput) ([]ValidationResult, error)
}

// ================================================================================

// TemplateInput represents a template to validate
//
// Used for batch validation with ValidateBatch().
//
// Example:
//
//	input := TemplateInput{
//	    Name:    "slack_critical.tmpl",
//	    Content: "{{ .Status | toUpper }}: {{ .Labels.alertname }}",
//	    Options: templatevalidator.DefaultValidateOptions(),
//	}
type TemplateInput struct {
	// Name is the template identifier (filename or logical name)
	//
	// Used for error reporting and result identification.
	// Example: "slack_critical.tmpl"
	Name string

	// Content is the template content to validate
	Content string

	// Options are validation options for this template
	//
	// Each template can have different validation options.
	Options ValidateOptions
}

// ================================================================================

// TemplateEngine is the interface for TN-153 integration
//
// TemplateEngine provides template parsing and execution capabilities.
// This interface abstracts TN-153 NotificationTemplateEngine for testing.
//
// Implementation: TN-153 NotificationTemplateEngine
type TemplateEngine interface {
	// Parse parses template content
	//
	// Returns error if template syntax is invalid.
	// Used for syntax validation.
	Parse(ctx context.Context, content string) error

	// Execute executes template with mock data
	//
	// Returns rendered output or error.
	// Used for semantic validation (checking variable references).
	Execute(ctx context.Context, content string, data interface{}) (string, error)

	// Functions returns available template functions
	//
	// Returns slice of function names available in template engine.
	// Used for fuzzy matching in error suggestions.
	//
	// Example: ["toUpper", "toLower", "default", "range", "if", ...]
	Functions() []string
}

// ================================================================================

// New creates a new Validator with TN-153 engine integration
//
// The engine parameter should be TN-153 NotificationTemplateEngine.
// Returns a Validator ready for use.
//
// Example:
//
//	engine := template.NewNotificationTemplateEngine(template.DefaultTemplateEngineOptions())
//	validator := templatevalidator.New(engine)
func New(engine TemplateEngine) Validator {
	return &defaultValidator{
		engine:     engine,
		validators: createValidators(engine),
	}
}

// ================================================================================

// defaultValidator implements Validator interface
type defaultValidator struct {
	// engine is the TN-153 template engine
	engine TemplateEngine

	// validators are the phase validators (syntax, semantic, security, best_practices)
	validators []SubValidator
}

// SubValidator is the interface for phase validators
//
// Each validation phase (syntax, semantic, security, best_practices) implements this interface.
type SubValidator interface {
	// Name returns the validator name
	Name() string

	// Phase returns the validation phase
	Phase() ValidationPhase

	// Validate validates template content
	Validate(ctx context.Context, content string, opts ValidateOptions) ([]ValidationError, []ValidationWarning, []ValidationSuggestion, error)

	// Enabled returns true if validator should run for given options
	Enabled(opts ValidateOptions) bool
}

// ================================================================================

// Validate validates a single template content
func (v *defaultValidator) Validate(
	ctx context.Context,
	content string,
	opts ValidateOptions,
) (*ValidationResult, error) {
	// Validate options
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	// Create validation pipeline
	pipeline := &validationPipeline{
		validators: v.validators,
	}

	// Run validation
	result, err := pipeline.Run(ctx, content, opts)
	if err != nil {
		return nil, fmt.Errorf("validation pipeline failed: %w", err)
	}

	return result, nil
}

// ValidateFile validates a template file
func (v *defaultValidator) ValidateFile(
	ctx context.Context,
	filepath string,
	opts ValidateOptions,
) (*ValidationResult, error) {
	// Read file content
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filepath, err)
	}

	// Validate content
	result, err := v.Validate(ctx, string(content), opts)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ValidateBatch validates multiple templates in parallel
func (v *defaultValidator) ValidateBatch(
	ctx context.Context,
	templates []TemplateInput,
) ([]ValidationResult, error) {
	// Determine number of workers
	workers := templates[0].Options.ParallelWorkers
	if workers == 0 {
		workers = runtime.NumCPU()
	}

	// Create results slice
	results := make([]ValidationResult, len(templates))

	// Create job channel
	jobs := make(chan int, len(templates))

	// Create error channel
	errs := make(chan error, workers)

	// Worker pool
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				// Check context cancellation
				select {
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				default:
				}

				// Validate template
				result, err := v.Validate(ctx, templates[idx].Content, templates[idx].Options)
				if err != nil {
					errs <- fmt.Errorf("template %s validation failed: %w", templates[idx].Name, err)
					return
				}

				// Store result
				results[idx] = *result
			}
		}()
	}

	// Submit jobs
	for i := range templates {
		jobs <- i
	}
	close(jobs)

	// Wait for completion
	wg.Wait()
	close(errs)

	// Check for errors
	if err := <-errs; err != nil {
		return nil, err
	}

	return results, nil
}

// ================================================================================

// createValidators creates phase validators
//
// Creates validators for all phases:
// - SyntaxValidator
// - SemanticValidator
// - SecurityValidator
// - BestPracticesValidator
//
// Note: Validator implementations will be created in Phase 2-5.
func createValidators(engine TemplateEngine) []SubValidator {
	// TODO: Phase 2-5 - Implement validators
	// For now, return empty slice
	return []SubValidator{
		// &SyntaxValidator{engine: engine},         // Phase 2
		// &SemanticValidator{},                      // Phase 3
		// &SecurityValidator{},                      // Phase 4
		// &BestPracticesValidator{},                 // Phase 5
	}
}

// ================================================================================
