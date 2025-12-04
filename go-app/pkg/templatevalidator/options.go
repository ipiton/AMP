package templatevalidator

import "time"

// ================================================================================
// TN-156: Template Validator - Options & Configuration
// ================================================================================
// Validation options and configuration enums.
//
// Features:
// - Validation modes (strict, lenient, permissive)
// - Validation phases (syntax, semantic, security, best_practices)
// - Configurable behavior (max_errors, fail_fast, parallel_workers)
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// ValidationMode controls validation strictness
type ValidationMode string

const (
	// ModeStrict fails on warnings (no warnings allowed)
	// Use for production templates that must be perfect.
	ModeStrict ValidationMode = "strict"

	// ModeLenient allows warnings (default)
	// Warnings don't fail validation, only errors do.
	// Use for most validation scenarios.
	ModeLenient ValidationMode = "lenient"

	// ModePermissive allows warnings and some minor errors
	// Only critical errors fail validation.
	// Use for legacy templates during migration.
	ModePermissive ValidationMode = "permissive"
)

// String returns the string representation of ValidationMode
func (m ValidationMode) String() string {
	return string(m)
}

// Valid returns true if the validation mode is valid
func (m ValidationMode) Valid() bool {
	switch m {
	case ModeStrict, ModeLenient, ModePermissive:
		return true
	default:
		return false
	}
}

// ================================================================================

// ValidationPhase represents a validation phase
type ValidationPhase string

const (
	// PhaseSyntax validates Go text/template syntax
	PhaseSyntax ValidationPhase = "syntax"

	// PhaseSemantic validates Alertmanager data model compatibility
	PhaseSemantic ValidationPhase = "semantic"

	// PhaseSecurity validates security (XSS, secrets, injection)
	PhaseSecurity ValidationPhase = "security"

	// PhaseBestPractices validates best practices (performance, readability)
	PhaseBestPractices ValidationPhase = "best_practices"
)

// String returns the string representation of ValidationPhase
func (p ValidationPhase) String() string {
	return string(p)
}

// Valid returns true if the validation phase is valid
func (p ValidationPhase) Valid() bool {
	switch p {
	case PhaseSyntax, PhaseSemantic, PhaseSecurity, PhaseBestPractices:
		return true
	default:
		return false
	}
}

// AllPhases returns all validation phases
func AllPhases() []ValidationPhase {
	return []ValidationPhase{
		PhaseSyntax,
		PhaseSemantic,
		PhaseSecurity,
		PhaseBestPractices,
	}
}

// ================================================================================

// ValidateOptions controls validation behavior
type ValidateOptions struct {
	// Mode controls validation strictness (default: ModeLenient)
	//
	// - ModeStrict: fail on warnings
	// - ModeLenient: allow warnings (default)
	// - ModePermissive: allow warnings and some errors
	Mode ValidationMode

	// Phases controls which validators run (default: all phases)
	//
	// Empty slice = all phases
	// Specify phases to run only specific validators
	//
	// Example: []ValidationPhase{PhaseSyntax, PhaseSecurity}
	Phases []ValidationPhase

	// TemplateType for type-specific validation
	//
	// Values: slack, pagerduty, email, webhook, generic
	// Default: generic
	//
	// Type-specific validators check for type-specific issues:
	// - slack: check for Block Kit structure
	// - email: check for Annotations usage
	// - pagerduty: check for Events API fields
	TemplateType string

	// MaxErrors limits error collection (0 = collect all, default: 0)
	//
	// Stop validation after MaxErrors errors found.
	// Useful for large templates with many errors.
	//
	// 0 = collect all errors (default)
	// N = stop after N errors
	MaxErrors int

	// FailFast stops validation on first error (default: false)
	//
	// true = stop on first error (fast feedback)
	// false = collect all errors (comprehensive feedback)
	FailFast bool

	// ParallelWorkers for batch validation (0 = CPU count, default: 0)
	//
	// Number of parallel workers for ValidateBatch().
	// 0 = use runtime.NumCPU() (default)
	// N = use N workers
	ParallelWorkers int

	// Timeout for validation (default: 30s)
	//
	// Maximum time allowed for single template validation.
	// Prevents infinite loops or slow validators.
	Timeout time.Duration
}

// DefaultValidateOptions returns default validation options
//
// Defaults:
// - Mode: ModeLenient (allow warnings)
// - Phases: All phases (syntax, semantic, security, best_practices)
// - TemplateType: "generic"
// - MaxErrors: 0 (collect all)
// - FailFast: false (collect all errors)
// - ParallelWorkers: 0 (CPU count)
// - Timeout: 30s
func DefaultValidateOptions() ValidateOptions {
	return ValidateOptions{
		Mode:            ModeLenient,
		Phases:          AllPhases(),
		TemplateType:    "generic",
		MaxErrors:       0,
		FailFast:        false,
		ParallelWorkers: 0,
		Timeout:         30 * time.Second,
	}
}

// WithMode returns a copy of options with the specified mode
func (o ValidateOptions) WithMode(mode ValidationMode) ValidateOptions {
	o.Mode = mode
	return o
}

// WithPhases returns a copy of options with the specified phases
func (o ValidateOptions) WithPhases(phases ...ValidationPhase) ValidateOptions {
	o.Phases = phases
	return o
}

// WithTemplateType returns a copy of options with the specified template type
func (o ValidateOptions) WithTemplateType(templateType string) ValidateOptions {
	o.TemplateType = templateType
	return o
}

// WithMaxErrors returns a copy of options with the specified max errors
func (o ValidateOptions) WithMaxErrors(maxErrors int) ValidateOptions {
	o.MaxErrors = maxErrors
	return o
}

// WithFailFast returns a copy of options with the specified fail-fast behavior
func (o ValidateOptions) WithFailFast(failFast bool) ValidateOptions {
	o.FailFast = failFast
	return o
}

// WithParallelWorkers returns a copy of options with the specified parallel workers
func (o ValidateOptions) WithParallelWorkers(workers int) ValidateOptions {
	o.ParallelWorkers = workers
	return o
}

// WithTimeout returns a copy of options with the specified timeout
func (o ValidateOptions) WithTimeout(timeout time.Duration) ValidateOptions {
	o.Timeout = timeout
	return o
}

// ================================================================================

// Validate validates the options and returns an error if invalid
func (o ValidateOptions) Validate() error {
	// Validate mode
	if !o.Mode.Valid() {
		return &ValidationError{
			Phase:    "options",
			Severity: "critical",
			Message:  "invalid validation mode: " + string(o.Mode),
			Code:     "invalid-mode",
		}
	}

	// Validate phases
	for _, phase := range o.Phases {
		if !phase.Valid() {
			return &ValidationError{
				Phase:    "options",
				Severity: "critical",
				Message:  "invalid validation phase: " + string(phase),
				Code:     "invalid-phase",
			}
		}
	}

	// Validate MaxErrors
	if o.MaxErrors < 0 {
		return &ValidationError{
			Phase:    "options",
			Severity: "critical",
			Message:  "MaxErrors must be >= 0",
			Code:     "invalid-max-errors",
		}
	}

	// Validate ParallelWorkers
	if o.ParallelWorkers < 0 {
		return &ValidationError{
			Phase:    "options",
			Severity: "critical",
			Message:  "ParallelWorkers must be >= 0",
			Code:     "invalid-parallel-workers",
		}
	}

	// Validate Timeout
	if o.Timeout <= 0 {
		return &ValidationError{
			Phase:    "options",
			Severity: "critical",
			Message:  "Timeout must be > 0",
			Code:     "invalid-timeout",
		}
	}

	return nil
}

// ================================================================================

// HasPhase returns true if the specified phase is enabled
func (o ValidateOptions) HasPhase(phase ValidationPhase) bool {
	// Empty phases = all phases
	if len(o.Phases) == 0 {
		return true
	}

	for _, p := range o.Phases {
		if p == phase {
			return true
		}
	}
	return false
}

// IsStrict returns true if mode is ModeStrict
func (o ValidateOptions) IsStrict() bool {
	return o.Mode == ModeStrict
}

// IsLenient returns true if mode is ModeLenient
func (o ValidateOptions) IsLenient() bool {
	return o.Mode == ModeLenient
}

// IsPermissive returns true if mode is ModePermissive
func (o ValidateOptions) IsPermissive() bool {
	return o.Mode == ModePermissive
}

// ================================================================================
