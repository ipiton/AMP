package grouping

import (
	"fmt"
	"os"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// Parser defines the interface for parsing grouping configuration.
type Parser interface {
	// Parse parses grouping configuration from YAML bytes
	Parse(data []byte) (*GroupingConfig, error)

	// ParseFile parses grouping configuration from a YAML file
	ParseFile(path string) (*GroupingConfig, error)

	// ParseString parses grouping configuration from a YAML string
	ParseString(yaml string) (*GroupingConfig, error)
}

// DefaultParser is the standard implementation of Parser.
// It uses yaml.v3 for parsing and validator/v10 for validation.
type DefaultParser struct {
	validator *validator.Validate
}

// NewParser creates a new parser with validation support.
// The parser is configured with custom validators for label names and duration ranges.
func NewParser() *DefaultParser {
	v := validator.New()

	// Register custom validators
	_ = v.RegisterValidation("labelname", validateLabelNameTag)
	_ = v.RegisterValidation("duration_range", validateDurationRangeTag)

	return &DefaultParser{
		validator: v,
	}
}

// Parse implements Parser.Parse.
// It unmarshals YAML data into a GroupingConfig struct, applies defaults,
// and performs comprehensive validation.
//
// Returns:
//   - *GroupingConfig: the parsed and validated configuration
//   - error: ParseError for YAML syntax errors, ValidationErrors for validation failures
//
// Example:
//
//	data := []byte(`
//	route:
//	  receiver: 'default'
//	  group_by: ['alertname']
//	  group_wait: 30s
//	`)
//	config, err := parser.Parse(data)
func (p *DefaultParser) Parse(data []byte) (*GroupingConfig, error) {
	// Parse YAML
	var config GroupingConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, &ParseError{
			Field: "config",
			Err:   fmt.Errorf("invalid YAML syntax: %w", err),
		}
	}

	// Validate that route exists
	if config.Route == nil {
		return nil, NewConfigError("route configuration is required", "", nil)
	}

	// Apply defaults recursively
	applyRouteDefaults(config.Route)

	// Structural validation using validator tags
	if err := p.validator.Struct(&config); err != nil {
		if validationErrs, ok := err.(validator.ValidationErrors); ok {
			return nil, convertValidatorErrors(validationErrs)
		}
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Semantic validation (custom business rules)
	if err := p.validateSemantics(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// ParseFile implements Parser.ParseFile.
// It reads a YAML file and parses it using Parse.
//
// Example:
//
//	config, err := parser.ParseFile("/etc/alertmanager/config.yml")
func (p *DefaultParser) ParseFile(path string) (*GroupingConfig, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, NewConfigError("failed to read config file", path, err)
	}

	// Parse
	config, err := p.Parse(data)
	if err != nil {
		// Add source file context to error
		if configErr, ok := err.(*ConfigError); ok {
			configErr.Source = path
			return nil, configErr
		}
		return nil, NewConfigError("failed to parse config", path, err)
	}

	// Set source metadata
	config.Route.source = path

	return config, nil
}

// ParseString implements Parser.ParseString.
// Convenience method for parsing configuration from a string.
// Useful for testing and inline configuration.
//
// Example:
//
//	config, err := parser.ParseString(`
//	route:
//	  receiver: 'default'
//	  group_by: ['alertname']
//	`)
func (p *DefaultParser) ParseString(yamlStr string) (*GroupingConfig, error) {
	return p.Parse([]byte(yamlStr))
}

// validateSemantics performs semantic validation on the configuration.
// This includes custom business rules that go beyond structural validation.
func (p *DefaultParser) validateSemantics(config *GroupingConfig) error {
	var errors ValidationErrors

	// Validate the route tree recursively
	p.validateRouteSemantics(config.Route, &errors, "route")

	if errors.HasErrors() {
		return errors
	}

	return nil
}

// validateRouteSemantics recursively validates a route and its nested routes.
// It accumulates errors in the provided ValidationErrors slice.
func (p *DefaultParser) validateRouteSemantics(route *Route, errors *ValidationErrors, path string) {
	// Validate label names in group_by (unless special grouping)
	if !route.HasSpecialGrouping() && !route.IsGlobalGroup() {
		for _, label := range route.GroupBy {
			if !isValidLabelName(label) {
				errors.Add(
					fmt.Sprintf("%s.group_by", path),
					label,
					"labelname",
					fmt.Sprintf("invalid label name '%s': must match [a-zA-Z_][a-zA-Z0-9_]*", label),
				)
			}
		}
	}

	// Validate timer ranges
	if route.GroupWait != nil {
		if err := validateGroupWaitRange(route.GroupWait.Duration); err != nil {
			errors.Add(
				fmt.Sprintf("%s.group_wait", path),
				route.GroupWait.String(),
				"range",
				err.Error(),
			)
		}
	}

	if route.GroupInterval != nil {
		if err := validateGroupIntervalRange(route.GroupInterval.Duration); err != nil {
			errors.Add(
				fmt.Sprintf("%s.group_interval", path),
				route.GroupInterval.String(),
				"range",
				err.Error(),
			)
		}
	}

	if route.RepeatInterval != nil {
		if err := validateRepeatIntervalRange(route.RepeatInterval.Duration); err != nil {
			errors.Add(
				fmt.Sprintf("%s.repeat_interval", path),
				route.RepeatInterval.String(),
				"range",
				err.Error(),
			)
		}
	}

	// Validate match/match_re not both empty if specified
	if route.Match != nil && len(route.Match) == 0 &&
		route.MatchRE != nil && len(route.MatchRE) == 0 {
		errors.Add(
			fmt.Sprintf("%s.match", path),
			"",
			"empty_matchers",
			"at least one match or match_re must be specified if matchers are present",
		)
	}

	// Recursively validate nested routes
	for i, nestedRoute := range route.Routes {
		nestedPath := fmt.Sprintf("%s.routes[%d]", path, i)
		p.validateRouteSemantics(nestedRoute, errors, nestedPath)
	}

	// Validate nesting depth (protect against too deep recursion)
	depth := calculateRouteDepth(route)
	if depth > maxRouteDepth {
		errors.Add(
			path,
			fmt.Sprintf("%d", depth),
			"max_depth",
			fmt.Sprintf("route nesting depth (%d) exceeds maximum allowed (%d)", depth, maxRouteDepth),
		)
	}
}

// applyRouteDefaults recursively applies default values to a route and its nested routes.
func applyRouteDefaults(route *Route) {
	if route == nil {
		return
	}

	// Apply defaults to this route
	route.Defaults()

	// Recursively apply to nested routes
	for _, nestedRoute := range route.Routes {
		applyRouteDefaults(nestedRoute)
	}
}

// calculateRouteDepth calculates the maximum depth of the route tree.
func calculateRouteDepth(route *Route) int {
	if route == nil || len(route.Routes) == 0 {
		return 1
	}

	maxDepth := 0
	for _, nestedRoute := range route.Routes {
		depth := calculateRouteDepth(nestedRoute)
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	return maxDepth + 1
}

// convertValidatorErrors converts validator.ValidationErrors to our ValidationErrors type.
func convertValidatorErrors(errs validator.ValidationErrors) ValidationErrors {
	var errors ValidationErrors

	for _, err := range errs {
		errors.Add(
			err.Field(),
			fmt.Sprintf("%v", err.Value()),
			err.Tag(),
			getValidationMessage(err),
		)
	}

	return errors
}

// getValidationMessage returns a user-friendly message for a validation error.
func getValidationMessage(err validator.FieldError) string {
	switch err.Tag() {
	case "required":
		return fmt.Sprintf("field '%s' is required", err.Field())
	case "min":
		return fmt.Sprintf("field '%s' must have at least %s items", err.Field(), err.Param())
	case "max":
		return fmt.Sprintf("field '%s' must have at most %s items", err.Field(), err.Param())
	case "gte":
		return fmt.Sprintf("field '%s' must be greater than or equal to %s", err.Field(), err.Param())
	case "lte":
		return fmt.Sprintf("field '%s' must be less than or equal to %s", err.Field(), err.Param())
	case "labelname":
		return fmt.Sprintf("field '%s' must be a valid Prometheus label name", err.Field())
	case "duration_range":
		return fmt.Sprintf("field '%s' duration out of allowed range", err.Field())
	default:
		return fmt.Sprintf("field '%s' failed validation: %s", err.Field(), err.Tag())
	}
}

// validateLabelNameTag is a custom validator tag for label names.
// Used by validator/v10 for struct tag validation.
func validateLabelNameTag(fl validator.FieldLevel) bool {
	labelName := fl.Field().String()
	return isValidLabelName(labelName)
}

// validateDurationRangeTag is a custom validator tag for duration ranges.
// Used by validator/v10 for struct tag validation.
func validateDurationRangeTag(fl validator.FieldLevel) bool {
	// This is a placeholder - actual validation is done in validateSemantics
	// because we need context about which timer field we're validating
	return true
}

// Constants for validation
const (
	// maxRouteDepth is the maximum allowed nesting depth for routes
	maxRouteDepth = 10

	// maxConfigSize is the maximum allowed size for config files (10MB)
	maxConfigSize = 10 * 1024 * 1024
)
