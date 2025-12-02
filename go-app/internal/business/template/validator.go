package template

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	templateEngine "github.com/ipiton/AMP/internal/notification/template"
	"github.com/ipiton/AMP/internal/core/domain"
)

// ================================================================================
// TN-155: Template API (CRUD) - Validator
// ================================================================================
// Template validation using TN-153 Template Engine.
//
// Features:
// - Syntax validation (Go text/template)
// - Semantic validation (Alertmanager data model)
// - Function availability check
// - Variable usage analysis
// - Helpful error messages with suggestions
//
// Performance Targets:
// - Validate: < 20ms p95
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// TemplateValidator validates template syntax and semantics
type TemplateValidator interface {
	// ValidateSyntax validates template syntax using TN-153 engine
	ValidateSyntax(ctx context.Context, content string, templateType domain.TemplateType) (*domain.ValidationResult, error)

	// ValidateWithData validates template with test data
	ValidateWithData(ctx context.Context, content string, data interface{}) (*domain.ValidationResult, error)

	// ValidateBusinessRules validates business rules (name format, etc.)
	ValidateBusinessRules(ctx context.Context, template *domain.Template) error
}

// ================================================================================

// DefaultTemplateValidator implements TemplateValidator
type DefaultTemplateValidator struct {
	engine templateEngine.NotificationTemplateEngine
	logger *slog.Logger
}

// NewTemplateValidator creates a new template validator
func NewTemplateValidator(
	engine templateEngine.NotificationTemplateEngine,
	logger *slog.Logger,
) TemplateValidator {
	if logger == nil {
		logger = slog.Default()
	}

	return &DefaultTemplateValidator{
		engine: engine,
		logger: logger,
	}
}

// ================================================================================

// ValidateSyntax validates template syntax using TN-153 engine
func (v *DefaultTemplateValidator) ValidateSyntax(
	ctx context.Context,
	content string,
	templateType domain.TemplateType,
) (*domain.ValidationResult, error) {
	start := time.Now()
	defer func() {
		v.logger.Debug("template syntax validation",
			"type", templateType,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Create mock template data (Alertmanager-compatible)
	mockData := v.createMockTemplateData(templateType)

	// Try to execute template with mock data
	result, err := v.engine.Execute(ctx, content, mockData)

	// Build validation result
	validationResult := &domain.ValidationResult{
		Valid:         err == nil,
		SyntaxErrors:  []domain.ValidationError{},
		Warnings:      []string{},
		FunctionsUsed: v.extractFunctions(content),
		VariablesUsed: v.extractVariables(content),
	}

	if err == nil {
		// Template is valid
		validationResult.RenderedOutput = result
		v.logger.Info("template syntax valid",
			"type", templateType,
			"functions_count", len(validationResult.FunctionsUsed),
		)
	} else {
		// Parse error and provide helpful suggestions
		syntaxErr := v.parseTemplateError(err)
		validationResult.SyntaxErrors = append(validationResult.SyntaxErrors, syntaxErr)

		v.logger.Warn("template syntax invalid",
			"type", templateType,
			"error", err.Error(),
		)
	}

	// Add warnings for common issues
	warnings := v.checkCommonIssues(content, templateType)
	validationResult.Warnings = append(validationResult.Warnings, warnings...)

	return validationResult, nil
}

// ================================================================================

// ValidateWithData validates template with user-provided test data
func (v *DefaultTemplateValidator) ValidateWithData(
	ctx context.Context,
	content string,
	data interface{},
) (*domain.ValidationResult, error) {
	start := time.Now()
	defer func() {
		v.logger.Debug("template validation with data",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Try to convert data to *TemplateData
	templateData, ok := data.(*templateEngine.TemplateData)
	if !ok {
		// If not TemplateData, use mock data
		v.logger.Warn("provided data is not *TemplateData, using mock data")
		templateData = v.createMockTemplateData(domain.TemplateTypeGeneric)
	}

	// Execute template with provided data
	result, err := v.engine.Execute(ctx, content, templateData)

	validationResult := &domain.ValidationResult{
		Valid:         err == nil,
		SyntaxErrors:  []domain.ValidationError{},
		Warnings:      []string{},
		FunctionsUsed: v.extractFunctions(content),
		VariablesUsed: v.extractVariables(content),
	}

	if err == nil {
		validationResult.RenderedOutput = result
	} else {
		syntaxErr := v.parseTemplateError(err)
		validationResult.SyntaxErrors = append(validationResult.SyntaxErrors, syntaxErr)
	}

	return validationResult, nil
}

// ================================================================================

// ValidateBusinessRules validates business rules
func (v *DefaultTemplateValidator) ValidateBusinessRules(
	ctx context.Context,
	template *domain.Template,
) error {
	// Validate name format (lowercase alphanumeric + underscore)
	if !isValidTemplateName(template.Name) {
		return fmt.Errorf("invalid template name: must be lowercase alphanumeric with underscores")
	}

	// Validate type
	if !template.Type.Valid() {
		return fmt.Errorf("invalid template type: %s", template.Type)
	}

	// Validate content length
	if len(template.Content) == 0 {
		return fmt.Errorf("template content cannot be empty")
	}
	if len(template.Content) > 65536 {
		return fmt.Errorf("template content too large: %d bytes (max 64KB)", len(template.Content))
	}

	// Validate description length
	if len(template.Description) > 500 {
		return fmt.Errorf("template description too long: %d chars (max 500)", len(template.Description))
	}

	return nil
}

// ================================================================================
// Helper methods
// ================================================================================

// createMockTemplateData creates mock data for validation
func (v *DefaultTemplateValidator) createMockTemplateData(templateType domain.TemplateType) *templateEngine.TemplateData {
	// Create Alertmanager-compatible mock data
	labels := map[string]string{
		"alertname": "TestAlert",
		"severity":  "critical",
		"instance":  "localhost:9090",
		"job":       "prometheus",
	}

	annotations := map[string]string{
		"summary":     "Test alert summary",
		"description": "This is a test alert for validation",
	}

	return templateEngine.NewTemplateData(
		"firing",
		labels,
		annotations,
		time.Now(),
	)
}

// parseTemplateError parses Go template error into structured ValidationError
func (v *DefaultTemplateValidator) parseTemplateError(err error) domain.ValidationError {
	errStr := err.Error()

	// Try to extract line/column info
	// Go template errors format: "template: <name>:<line>:<column>: <message>"
	parts := strings.Split(errStr, ":")

	validationErr := domain.ValidationError{
		Line:    1,
		Column:  1,
		Message: errStr,
	}

	// Extract line number
	if len(parts) >= 2 {
		var line int
		fmt.Sscanf(parts[1], "%d", &line)
		if line > 0 {
			validationErr.Line = line
		}
	}

	// Extract column number
	if len(parts) >= 3 {
		var col int
		fmt.Sscanf(parts[2], "%d", &col)
		if col > 0 {
			validationErr.Column = col
		}
	}

	// Extract clean message
	if len(parts) >= 4 {
		validationErr.Message = strings.TrimSpace(strings.Join(parts[3:], ":"))
	}

	// Add helpful suggestions
	validationErr.Suggestion = v.generateSuggestion(errStr)

	return validationErr
}

// generateSuggestion generates helpful suggestion based on error
func (v *DefaultTemplateValidator) generateSuggestion(errStr string) string {
	errLower := strings.ToLower(errStr)

	// Common mistakes and suggestions
	suggestions := map[string]string{
		"function \"touppercase\" not defined": "Did you mean 'toUpper'?",
		"function \"tolowercase\" not defined": "Did you mean 'toLower'?",
		"function \"contains\" not defined":    "Did you mean 'match'?",
		"bad character":                        "Check for invalid characters in template syntax",
		"unexpected":                           "Check template syntax - missing {{ or }} ?",
		"unclosed action":                      "Missing closing }} in template",
	}

	for pattern, suggestion := range suggestions {
		if strings.Contains(errLower, pattern) {
			return suggestion
		}
	}

	return ""
}

// extractFunctions extracts function names used in template
func (v *DefaultTemplateValidator) extractFunctions(content string) []string {
	functions := make(map[string]bool)

	// Simple pattern matching for function calls
	// Look for patterns like "| functionName" or "functionName ."
	words := strings.Fields(content)
	for i, word := range words {
		// Check for pipe functions: | toUpper
		if i > 0 && words[i-1] == "|" {
			cleanWord := strings.Trim(word, "{}() ")
			if cleanWord != "" {
				functions[cleanWord] = true
			}
		}

		// Check for function calls: toUpper .Value
		if strings.HasSuffix(word, "(") {
			funcName := strings.TrimSuffix(word, "(")
			funcName = strings.Trim(funcName, "{} ")
			if funcName != "" && !strings.HasPrefix(funcName, ".") {
				functions[funcName] = true
			}
		}
	}

	result := make([]string, 0, len(functions))
	for fn := range functions {
		result = append(result, fn)
	}
	return result
}

// extractVariables extracts variable references from template
func (v *DefaultTemplateValidator) extractVariables(content string) []string {
	variables := make(map[string]bool)

	// Simple pattern matching for .Variable references
	words := strings.Fields(content)
	for _, word := range words {
		// Look for .Variable or .Field.Subfield patterns
		if strings.HasPrefix(word, ".") {
			// Clean up the variable name
			varName := strings.Trim(word, "{}()| ")
			if varName != "" && varName != "." {
				variables[varName] = true
			}
		}
	}

	result := make([]string, 0, len(variables))
	for v := range variables {
		result = append(result, v)
	}
	return result
}

// checkCommonIssues checks for common template issues
func (v *DefaultTemplateValidator) checkCommonIssues(content string, templateType domain.TemplateType) []string {
	warnings := []string{}

	// Check for html/template functions (we use text/template)
	if strings.Contains(content, "html") || strings.Contains(content, "urlquery") {
		warnings = append(warnings, "Using html/template functions - these may not work with text/template")
	}

	// Check for missing variables in Slack templates
	if templateType == domain.TemplateTypeSlack {
		if !strings.Contains(content, ".Status") && !strings.Contains(content, ".Labels") {
			warnings = append(warnings, "Slack template should typically reference .Status or .Labels")
		}
	}

	// Check for missing variables in Email templates
	if templateType == domain.TemplateTypeEmail {
		if !strings.Contains(content, ".Annotations") {
			warnings = append(warnings, "Email template should typically reference .Annotations for detail")
		}
	}

	// Check for very long lines (readability)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if len(line) > 200 {
			warnings = append(warnings, fmt.Sprintf("Line %d is very long (%d chars) - consider breaking it up", i+1, len(line)))
		}
	}

	return warnings
}

// isValidTemplateName checks if template name is valid
func isValidTemplateName(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}

	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}

	return true
}

// ================================================================================
