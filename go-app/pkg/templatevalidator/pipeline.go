package templatevalidator

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ================================================================================
// TN-156: Template Validator - Validation Pipeline
// ================================================================================
// Orchestrates validation through multiple phases with metrics tracking.
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// validationPipeline orchestrates validation phases
//
// Pipeline runs validators in sequence:
// 1. Syntax Validator
// 2. Semantic Validator
// 3. Security Validator
// 4. Best Practices Validator
//
// Each phase can stop pipeline if fail-fast enabled.
type validationPipeline struct {
	validators []SubValidator
	logger     *slog.Logger
}

// Run executes the validation pipeline
//
// Returns ValidationResult with aggregated errors, warnings, and suggestions.
func (p *validationPipeline) Run(
	ctx context.Context,
	content string,
	opts ValidateOptions,
) (*ValidationResult, error) {
	startTime := time.Now()

	// Create result
	result := &ValidationResult{
		Valid:       true,
		Errors:      []ValidationError{},
		Warnings:    []ValidationWarning{},
		Info:        []ValidationInfo{},
		Suggestions: []ValidationSuggestion{},
		Metrics: ValidationMetrics{
			TemplateSize:   len(content),
			PhaseDurations: make(map[string]time.Duration),
		},
	}

	// Track phase metrics
	phaseTimes := make(map[string]time.Duration)

	// Run each validator
	for _, validator := range p.validators {
		// Check if validator is enabled
		if !validator.Enabled(opts) {
			continue
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Log phase start
		if p.logger != nil {
			p.logger.Debug("running validation phase", "phase", validator.Name())
		}

		// Run validator
		phaseStart := time.Now()
		errors, warnings, suggestions, err := validator.Validate(ctx, content, opts)
		phaseDuration := time.Since(phaseStart)

		// Track phase duration
		phaseTimes[string(validator.Phase())] = phaseDuration

		// Check validator error
		if err != nil {
			return nil, fmt.Errorf("validator %s failed: %w", validator.Name(), err)
		}

		// Aggregate results
		result.Errors = append(result.Errors, errors...)
		result.Warnings = append(result.Warnings, warnings...)
		result.Suggestions = append(result.Suggestions, suggestions...)

		// Log phase completion
		if p.logger != nil {
			p.logger.Debug("validation phase complete",
				"phase", validator.Name(),
				"duration_ms", phaseDuration.Milliseconds(),
				"errors", len(errors),
				"warnings", len(warnings),
			)
		}

		// Check fail-fast
		if opts.FailFast && len(errors) > 0 {
			if p.logger != nil {
				p.logger.Info("stopping pipeline due to fail-fast", "phase", validator.Name())
			}
			break
		}

		// Check max errors
		if opts.MaxErrors > 0 && len(result.Errors) >= opts.MaxErrors {
			if p.logger != nil {
				p.logger.Info("stopping pipeline due to max errors reached",
					"max_errors", opts.MaxErrors,
					"current_errors", len(result.Errors),
				)
			}
			break
		}
	}

	// Determine validity
	result.Valid = p.isValid(result, opts)

	// Set metrics
	result.Metrics.Duration = time.Since(startTime)
	result.Metrics.PhaseDurations = phaseTimes

	// Log final result
	if p.logger != nil {
		p.logger.Info("validation complete",
			"valid", result.Valid,
			"errors", len(result.Errors),
			"warnings", len(result.Warnings),
			"suggestions", len(result.Suggestions),
			"duration_ms", result.Metrics.DurationMs(),
		)
	}

	return result, nil
}

// isValid determines if result is valid based on mode
//
// Strict mode: no errors and no warnings
// Lenient mode: no errors (warnings allowed)
// Permissive mode: no critical errors (high/medium errors allowed)
func (p *validationPipeline) isValid(result *ValidationResult, opts ValidateOptions) bool {
	switch opts.Mode {
	case ModeStrict:
		// Strict: no errors and no warnings
		return len(result.Errors) == 0 && len(result.Warnings) == 0

	case ModeLenient:
		// Lenient: no errors (warnings allowed)
		return len(result.Errors) == 0

	case ModePermissive:
		// Permissive: no critical errors (high/medium errors allowed)
		criticalCount := 0
		for _, err := range result.Errors {
			if err.IsCritical() {
				criticalCount++
			}
		}
		return criticalCount == 0

	default:
		// Default to lenient
		return len(result.Errors) == 0
	}
}

// ================================================================================
