package validators

import (
	"context"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - SubValidator Interface
// ================================================================================
// SubValidator interface for phase validators.
//
// Each validation phase (syntax, semantic, security, best_practices) implements
// this interface to provide phase-specific validation logic.
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// SubValidator is the interface for phase validators
//
// SubValidator defines the contract for validation phases.
// Each phase validator must implement this interface.
//
// Validators:
// - SyntaxValidator: validates Go text/template syntax
// - SemanticValidator: validates Alertmanager data model compatibility
// - SecurityValidator: validates security (XSS, secrets, injection)
// - BestPracticesValidator: validates best practices (performance, readability)
//
// Example implementation:
//
//	type SyntaxValidator struct {
//	    engine TemplateEngine
//	}
//
//	func (v *SyntaxValidator) Name() string {
//	    return "Syntax Validator"
//	}
//
//	func (v *SyntaxValidator) Phase() templatevalidator.ValidationPhase {
//	    return templatevalidator.PhaseSyntax
//	}
//
//	func (v *SyntaxValidator) Validate(ctx context.Context, content string, opts templatevalidator.ValidateOptions) (
//	    []templatevalidator.ValidationError,
//	    []templatevalidator.ValidationWarning,
//	    []templatevalidator.ValidationSuggestion,
//	    error,
//	) {
//	    // Validation logic here
//	}
//
//	func (v *SyntaxValidator) Enabled(opts templatevalidator.ValidateOptions) bool {
//	    return opts.HasPhase(templatevalidator.PhaseSyntax)
//	}
type SubValidator interface {
	// Name returns the validator name
	//
	// Human-readable validator name for logging and error reporting.
	// Example: "Syntax Validator", "Security Validator"
	Name() string

	// Phase returns the validation phase
	//
	// Returns the phase this validator belongs to.
	// Used for phase filtering and result organization.
	Phase() templatevalidator.ValidationPhase

	// Validate validates template content
	//
	// Returns:
	// - errors: blocking validation errors
	// - warnings: non-blocking warnings
	// - suggestions: improvement suggestions
	// - error: validator error (not template error)
	//
	// Validator should:
	// - Check context cancellation (ctx.Done())
	// - Return quickly if context cancelled
	// - Return validator errors only for internal failures
	// - Return template errors as ValidationError structs
	Validate(
		ctx context.Context,
		content string,
		opts templatevalidator.ValidateOptions,
	) (
		errors []templatevalidator.ValidationError,
		warnings []templatevalidator.ValidationWarning,
		suggestions []templatevalidator.ValidationSuggestion,
		err error,
	)

	// Enabled returns true if validator should run for given options
	//
	// Checks if this validator is enabled in options.Phases.
	// Pipeline skips disabled validators.
	//
	// Default implementation:
	//   return opts.HasPhase(v.Phase())
	Enabled(opts templatevalidator.ValidateOptions) bool
}

// ================================================================================
