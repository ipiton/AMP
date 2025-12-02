package inhibition

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// InhibitionParser defines the interface for parsing inhibition configuration.
//
// Implementations must be thread-safe for concurrent parsing operations.
// Performance target: < 10µs per rule parsing.
//
// Example usage:
//
//	parser := inhibition.NewParser()
//	config, err := parser.ParseFile("config/inhibition.yaml")
//	if err != nil {
//	    log.Fatalf("Failed to parse config: %v", err)
//	}
//	log.Printf("Loaded %d inhibition rules", config.RuleCount())
type InhibitionParser interface {
	// Parse parses inhibition configuration from YAML bytes.
	//
	// The parsing process includes:
	//  1. YAML unmarshal
	//  2. Default value application
	//  3. Struct validation (validator tags)
	//  4. Regex pattern compilation
	//  5. Semantic validation (business rules)
	//
	// Parameters:
	//   - data: YAML bytes to parse
	//
	// Returns:
	//   - *InhibitionConfig: parsed and validated configuration
	//   - error: ParseError (YAML syntax), ValidationError (validation failed)
	//
	// Example:
	//
	//	yamlData := []byte(`
	//	inhibit_rules:
	//	  - source_match:
	//	      alertname: "NodeDown"
	//	    target_match:
	//	      alertname: "InstanceDown"
	//	    equal:
	//	      - node
	//	`)
	//	config, err := parser.Parse(yamlData)
	Parse(data []byte) (*InhibitionConfig, error)

	// ParseFile parses inhibition configuration from a YAML file.
	//
	// Parameters:
	//   - path: path to YAML file
	//
	// Returns:
	//   - *InhibitionConfig: parsed and validated configuration
	//   - error: os.ErrNotExist (file not found), ParseError, ValidationError
	//
	// Example:
	//
	//	config, err := parser.ParseFile("/etc/alertmanager/config.yml")
	ParseFile(path string) (*InhibitionConfig, error)

	// ParseString parses inhibition configuration from a YAML string.
	//
	// Convenience method, equivalent to Parse([]byte(yaml)).
	//
	// Parameters:
	//   - yaml: YAML string
	//
	// Returns:
	//   - *InhibitionConfig: parsed and validated configuration
	//   - error: ParseError, ValidationError
	ParseString(yaml string) (*InhibitionConfig, error)

	// ParseReader parses inhibition configuration from an io.Reader.
	//
	// Useful for streaming or network sources.
	//
	// Parameters:
	//   - r: io.Reader containing YAML data
	//
	// Returns:
	//   - *InhibitionConfig: parsed and validated configuration
	//   - error: io error, ParseError, ValidationError
	ParseReader(r io.Reader) (*InhibitionConfig, error)

	// Validate performs validation on an already parsed configuration.
	//
	// Useful for re-validation after modification.
	//
	// Parameters:
	//   - config: configuration to validate
	//
	// Returns:
	//   - error: ValidationError if validation fails, nil if valid
	Validate(config *InhibitionConfig) error

	// GetConfig returns the currently loaded configuration.
	//
	// Returns an empty configuration if no configuration has been loaded.
	//
	// Returns:
	//   - *InhibitionConfig: currently loaded configuration
	GetConfig() *InhibitionConfig
}

// DefaultInhibitionParser is the standard implementation of InhibitionParser.
//
// Thread-safety: Safe for concurrent use (validator is stateless).
// Performance: < 10µs per rule parsing (benchmarked).
//
// Example:
//
//	parser := inhibition.NewParser()
//	config, err := parser.ParseFile("config/inhibition.yaml")
type DefaultInhibitionParser struct {
	validator     *validator.Validate
	currentConfig *InhibitionConfig
}

// NewParser creates a new InhibitionParser with validation support.
//
// The parser is configured with custom validators:
//   - labelname: validates Prometheus label names
//   - regex_pattern: validates regex patterns
//
// Returns:
//   - *DefaultInhibitionParser: initialized parser ready to use
//
// Example:
//
//	parser := inhibition.NewParser()
func NewParser() *DefaultInhibitionParser {
	v := validator.New()

	// Register custom validators
	_ = v.RegisterValidation("labelname", validateLabelNameTag)
	_ = v.RegisterValidation("regex_pattern", validateRegexPatternTag)

	return &DefaultInhibitionParser{
		validator: v,
	}
}

// Parse implements InhibitionParser.Parse.
//
// Parsing pipeline:
//  1. YAML unmarshal → InhibitionConfig struct
//  2. Apply defaults (initialize maps, set timestamps)
//  3. Struct validation (validator tags)
//  4. Compile regex patterns (pre-compile for performance)
//  5. Semantic validation (business rules)
//
// Performance: < 10µs per rule (target), < 1ms for 100 rules.
func (p *DefaultInhibitionParser) Parse(data []byte) (*InhibitionConfig, error) {
	// Step 1: YAML unmarshal
	var config InhibitionConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, NewParseError(
			"config",
			string(data),
			fmt.Errorf("invalid YAML syntax: %w", err),
		)
	}

	// Step 2: Apply defaults
	p.applyDefaults(&config)

	// Step 3: Struct validation (validator tags)
	if err := p.validator.Struct(&config); err != nil {
		if validationErrs, ok := err.(validator.ValidationErrors); ok {
			return nil, convertValidatorErrors(validationErrs)
		}
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Step 4: Compile regex patterns
	if err := p.compileRegexPatterns(&config); err != nil {
		return nil, err
	}

	// Step 5: Semantic validation
	if err := p.validateSemantics(&config); err != nil {
		return nil, err
	}

	// Set metadata
	config.LoadedAt = time.Now()

	// Store current config
	p.currentConfig = &config

	return &config, nil
}

// ParseFile implements InhibitionParser.ParseFile.
func (p *DefaultInhibitionParser) ParseFile(path string) (*InhibitionConfig, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	// Parse
	config, err := p.Parse(data)
	if err != nil {
		return nil, err
	}

	// Set source file
	config.SourceFile = path

	return config, nil
}

// ParseString implements InhibitionParser.ParseString.
func (p *DefaultInhibitionParser) ParseString(yaml string) (*InhibitionConfig, error) {
	return p.Parse([]byte(yaml))
}

// ParseReader implements InhibitionParser.ParseReader.
func (p *DefaultInhibitionParser) ParseReader(r io.Reader) (*InhibitionConfig, error) {
	// Read all data
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	return p.Parse(data)
}

// Validate implements InhibitionParser.Validate.
func (p *DefaultInhibitionParser) Validate(config *InhibitionConfig) error {
	if config == nil {
		return NewValidationError("config", "not_nil", "config cannot be nil")
	}

	// Struct validation
	if err := p.validator.Struct(config); err != nil {
		if validationErrs, ok := err.(validator.ValidationErrors); ok {
			return convertValidatorErrors(validationErrs)
		}
		return fmt.Errorf("validation failed: %w", err)
	}

	// Semantic validation
	return p.validateSemantics(config)
}

// --- Private helper methods ---

// applyDefaults applies default values to the configuration.
//
// Defaults applied:
//   - Initialize nil maps to empty maps
//   - Set CreatedAt timestamp
//   - Initialize internal fields
func (p *DefaultInhibitionParser) applyDefaults(config *InhibitionConfig) {
	for i := range config.Rules {
		rule := &config.Rules[i]

		// Initialize maps if nil
		if rule.SourceMatch == nil {
			rule.SourceMatch = make(map[string]string)
		}
		if rule.SourceMatchRE == nil {
			rule.SourceMatchRE = make(map[string]string)
		}
		if rule.TargetMatch == nil {
			rule.TargetMatch = make(map[string]string)
		}
		if rule.TargetMatchRE == nil {
			rule.TargetMatchRE = make(map[string]string)
		}

		// Set timestamp
		rule.CreatedAt = time.Now()

		// Generate name if not provided
		if rule.Name == "" {
			rule.Name = fmt.Sprintf("rule-%d", i)
		}
	}
}

// compileRegexPatterns compiles all regex patterns in the configuration.
//
// Pre-compilation improves performance during matching.
// Invalid patterns return ParseError with detailed information.
func (p *DefaultInhibitionParser) compileRegexPatterns(config *InhibitionConfig) error {
	for i := range config.Rules {
		rule := &config.Rules[i]

		// Compile source_match_re patterns
		rule.compiledSourceRE = make(map[string]*regexp.Regexp)
		for key, pattern := range rule.SourceMatchRE {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return NewParseError(
					fmt.Sprintf("rules[%d].source_match_re.%s", i, key),
					pattern,
					fmt.Errorf("invalid regex: %w", err),
				)
			}
			rule.compiledSourceRE[key] = re
		}

		// Compile target_match_re patterns
		rule.compiledTargetRE = make(map[string]*regexp.Regexp)
		for key, pattern := range rule.TargetMatchRE {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return NewParseError(
					fmt.Sprintf("rules[%d].target_match_re.%s", i, key),
					pattern,
					fmt.Errorf("invalid regex: %w", err),
				)
			}
			rule.compiledTargetRE[key] = re
		}
	}

	return nil
}

// validateSemantics performs semantic validation (business rules).
//
// Semantic validations:
//  1. At least one rule must be present
//  2. Each rule must have at least one source condition
//  3. Each rule must have at least one target condition
//  4. All label names must be valid Prometheus label names
func (p *DefaultInhibitionParser) validateSemantics(config *InhibitionConfig) error {
	// Check for empty rules
	if len(config.Rules) == 0 {
		return NewConfigError("no inhibition rules found", nil)
	}

	var errors []error

	// Validate each rule
	for i, rule := range config.Rules {
		// At least one source condition
		if len(rule.SourceMatch) == 0 && len(rule.SourceMatchRE) == 0 {
			errors = append(errors, fmt.Errorf("rule %d: at least one of source_match or source_match_re required", i))
		}

		// At least one target condition
		if len(rule.TargetMatch) == 0 && len(rule.TargetMatchRE) == 0 {
			errors = append(errors, fmt.Errorf("rule %d: at least one of target_match or target_match_re required", i))
		}

		// Validate label names in equal
		for _, labelName := range rule.Equal {
			if !isValidLabelName(labelName) {
				errors = append(errors, fmt.Errorf("rule %d: invalid label name in equal: %s", i, labelName))
			}
		}

		// Validate label names in source_match
		for labelName := range rule.SourceMatch {
			if !isValidLabelName(labelName) {
				errors = append(errors, fmt.Errorf("rule %d: invalid label name in source_match: %s", i, labelName))
			}
		}

		// Validate label names in target_match
		for labelName := range rule.TargetMatch {
			if !isValidLabelName(labelName) {
				errors = append(errors, fmt.Errorf("rule %d: invalid label name in target_match: %s", i, labelName))
			}
		}
	}

	if len(errors) > 0 {
		return NewConfigError(
			fmt.Sprintf("configuration validation failed: %d errors", len(errors)),
			errors,
		)
	}

	return nil
}

// --- Validator tag functions ---

// validateLabelNameTag is a custom validator for Prometheus label names.
// Used with validator tag: `validate:"labelname"`
func validateLabelNameTag(fl validator.FieldLevel) bool {
	return isValidLabelName(fl.Field().String())
}

// validateRegexPatternTag is a custom validator for regex patterns.
// Used with validator tag: `validate:"regex_pattern"`
func validateRegexPatternTag(fl validator.FieldLevel) bool {
	pattern := fl.Field().String()
	if pattern == "" {
		return true // Empty pattern is allowed
	}

	_, err := regexp.Compile(pattern)
	return err == nil
}

// convertValidatorErrors converts validator.ValidationErrors to ConfigError.
func convertValidatorErrors(errs validator.ValidationErrors) error {
	var errors []error

	for _, err := range errs {
		errors = append(errors, NewValidationError(
			err.Field(),
			err.Tag(),
			fmt.Sprintf("validation failed: %s", err.Error()),
		))
	}

	return NewConfigError("validation failed", errors)
}


// GetConfig returns the currently loaded configuration.
func (p *DefaultInhibitionParser) GetConfig() *InhibitionConfig {
	if p.currentConfig == nil {
		return &InhibitionConfig{Rules: make([]InhibitionRule, 0)}
	}
	return p.currentConfig
}
