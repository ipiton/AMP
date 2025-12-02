package routing

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

// Parser limits and thresholds
const (
	// MaxConfigSize is the maximum allowed configuration file size (10 MB)
	// Protects against YAML bombs
	MaxConfigSize = 10 * 1024 * 1024 // 10 MB

	// MaxRouteDepth is the maximum nesting depth for routes
	// Protects against stack overflow and cycle detection complexity
	MaxRouteDepth = 10

	// MaxRoutes is the maximum number of routes in the configuration
	// Protects against memory exhaustion
	MaxRoutes = 10000

	// MaxReceivers is the maximum number of receivers
	MaxReceivers = 5000

	// MaxMatchersPerRoute is the maximum number of matchers per route
	MaxMatchersPerRoute = 100
)

// RouteConfigParser parses Alertmanager-compatible route configurations.
// Implements 4-layer validation:
//  1. YAML syntax validation
//  2. Structural validation (validator tags)
//  3. Semantic validation (business rules)
//  4. Security validation (YAML bombs, SSRF)
type RouteConfigParser struct {
	validator *validator.Validate
}

// NewRouteConfigParser creates a new parser with validation.
func NewRouteConfigParser() *RouteConfigParser {
	v := validator.New()

	// Register custom validators
	v.RegisterValidation("alphanum_hyphen", validateAlphanumHyphen)
	v.RegisterValidation("https_production", validateHTTPSProduction)
	v.RegisterValidation("slack_channel", validateSlackChannel)
	v.RegisterValidation("emoji", validateEmoji)
	v.RegisterValidation("slack_color", validateSlackColor)

	return &RouteConfigParser{
		validator: v,
	}
}

// Parse parses route configuration from bytes.
//
// Steps:
//  1. YAML unmarshaling
//  2. Required fields validation
//  3. Apply defaults recursively
//  4. Structural validation
//  5. Semantic validation
//  6. Compile regex patterns
//  7. Build receiver index
//  8. Set metadata
//
// Returns ValidationErrors if validation fails.
func (p *RouteConfigParser) Parse(data []byte) (*RouteConfig, error) {
	started := time.Now()

	// Step 1: YAML unmarshaling
	var config RouteConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	// Step 2: Validate required fields
	if config.Route == nil {
		return nil, fmt.Errorf("route is required")
	}
	if len(config.Receivers) == 0 {
		return nil, fmt.Errorf("at least one receiver is required")
	}

	// Step 3: Apply defaults
	p.applyDefaults(&config)

	// Step 4: Structural validation (validator tags)
	if err := p.validator.Struct(&config); err != nil {
		return nil, p.formatValidationErrors(err)
	}

	// Step 5: Semantic validation (business rules)
	if err := p.validateSemantics(&config); err != nil {
		return nil, err
	}

	// Step 6: Compile regex patterns
	if err := p.compileRegexPatterns(&config); err != nil {
		return nil, err
	}

	// Step 7: Build receiver index
	p.buildReceiverIndex(&config)

	// Step 8: Set metadata
	config.Version = 1
	config.LoadedAt = time.Now()

	duration := time.Since(started)
	slog.Info("config parsed successfully",
		"routes", countRoutes(config.Route),
		"receivers", len(config.Receivers),
		"duration_ms", duration.Milliseconds(),
	)

	return &config, nil
}

// ParseFile parses route configuration from a file.
func (p *RouteConfigParser) ParseFile(path string) (*RouteConfig, error) {
	// Check file size (YAML bomb protection)
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if stat.Size() > MaxConfigSize {
		return nil, fmt.Errorf(
			"config file too large: %d bytes (max: %d bytes)",
			stat.Size(),
			MaxConfigSize,
		)
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
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

// ParseString parses route configuration from a YAML string.
func (p *RouteConfigParser) ParseString(yamlStr string) (*RouteConfig, error) {
	return p.Parse([]byte(yamlStr))
}

// ValidateConfig validates an already-parsed configuration.
// Useful for testing and hot reload scenarios.
func (p *RouteConfigParser) ValidateConfig(config *RouteConfig) error {
	// Structural validation
	if err := p.validator.Struct(config); err != nil {
		return p.formatValidationErrors(err)
	}

	// Semantic validation
	return p.validateSemantics(config)
}

// applyDefaults applies default values recursively.
func (p *RouteConfigParser) applyDefaults(config *RouteConfig) {
	// Apply global defaults
	if config.Global != nil {
		config.Global.Defaults()
	}

	// Apply route defaults (via TN-121)
	if config.Route != nil {
		applyRouteDefaultsRecursive(config.Route)
	}

	// Apply receiver defaults
	for _, receiver := range config.Receivers {
		for _, cfg := range receiver.WebhookConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.PagerDutyConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.SlackConfigs {
			cfg.Defaults()
		}
		for _, cfg := range receiver.EmailConfigs {
			cfg.Defaults()
		}
	}
}

// validateSemantics performs semantic validation.
func (p *RouteConfigParser) validateSemantics(config *RouteConfig) error {
	var errors ValidationErrors

	// Build receiver index (for reference checking)
	receiverIndex := make(map[string]bool)
	for _, receiver := range config.Receivers {
		receiverIndex[receiver.Name] = true
	}

	// Validate route tree
	if err := p.validateRouteTree(config.Route, receiverIndex, &errors); err != nil {
		return err
	}

	// Validate receivers
	for i, receiver := range config.Receivers {
		// Check at least one config type
		if err := receiver.Validate(); err != nil {
			errors.Add(
				fmt.Sprintf("receivers[%d]", i),
				err.Error(),
				"Add at least one config: webhook_configs, pagerduty_configs, or slack_configs",
			)
		}
	}

	return errors.ErrType()
}

// validateRouteTree recursively validates the route tree.
func (p *RouteConfigParser) validateRouteTree(
	route *grouping.Route,
	receiverIndex map[string]bool,
	errors *ValidationErrors,
) error {
	if route == nil {
		return nil
	}

	// Check nesting depth
	depth := calculateRouteDepth(route)
	if depth > MaxRouteDepth {
		errors.Add(
			"route",
			fmt.Sprintf("route nesting too deep: %d (max: %d)", depth, MaxRouteDepth),
			"Flatten the route tree or increase MaxRouteDepth",
		)
		return errors.ErrType()
	}

	// Validate receiver reference
	if route.Receiver != "" && !receiverIndex[route.Receiver] {
		errors.Add(
			fmt.Sprintf("route[receiver=%s]", route.Receiver),
			fmt.Sprintf("receiver '%s' not found", route.Receiver),
			"Define the receiver in the 'receivers' section",
		)
	}

	// Recursively validate child routes
	for i, child := range route.Routes {
		if err := p.validateRouteTree(child, receiverIndex, errors); err != nil {
			return err
		}

		// Validate child has receiver (inherited or explicit)
		if child.Receiver == "" && route.Receiver == "" {
			errors.Add(
				fmt.Sprintf("route.routes[%d]", i),
				"child route has no receiver (not inherited from parent)",
				"Add 'receiver' field or set in parent route",
			)
		}
	}

	return errors.ErrType()
}

// compileRegexPatterns compiles all MatchRE patterns for performance.
func (p *RouteConfigParser) compileRegexPatterns(config *RouteConfig) error {
	config.CompiledRegex = make(map[*grouping.Route]map[string]*regexp.Regexp)

	var compileRoute func(*grouping.Route) error
	compileRoute = func(route *grouping.Route) error {
		if route == nil {
			return nil
		}

		// Compile MatchRE patterns
		if len(route.MatchRE) > 0 {
			patterns := make(map[string]*regexp.Regexp)
			for key, pattern := range route.MatchRE {
				regex, err := regexp.Compile(pattern)
				if err != nil {
					return fmt.Errorf(
						"invalid regex for route.match_re[%s]: %w",
						key,
						err,
					)
				}
				patterns[key] = regex
			}
			config.CompiledRegex[route] = patterns
		}

		// Recursively compile child routes
		for _, child := range route.Routes {
			if err := compileRoute(child); err != nil {
				return err
			}
		}

		return nil
	}

	return compileRoute(config.Route)
}

// buildReceiverIndex builds O(1) receiver lookup map.
func (p *RouteConfigParser) buildReceiverIndex(config *RouteConfig) {
	config.ReceiverIndex = make(map[string]*Receiver, len(config.Receivers))
	for _, receiver := range config.Receivers {
		config.ReceiverIndex[receiver.Name] = receiver
	}
}

// formatValidationErrors converts validator errors to ValidationErrors.
func (p *RouteConfigParser) formatValidationErrors(err error) error {
	var errors ValidationErrors

	validationErrors, ok := err.(validator.ValidationErrors)
	if !ok {
		return err
	}

	for _, fieldErr := range validationErrors {
		errors.Add(
			fieldErr.Namespace(),
			fieldErr.Error(),
			"", // No suggestion for struct tag errors
		)
	}

	return errors.ErrType()
}

// Custom validators

func validateAlphanumHyphen(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	for _, r := range value {
		if !((r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_') {
			return false
		}
	}

	return true
}

func validateHTTPSProduction(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// In development mode, allow HTTP (check env var)
	if os.Getenv("ENVIRONMENT") == "development" {
		return true
	}

	// In production, require HTTPS
	return len(value) >= 8 && value[:8] == "https://"
}

func validateSlackChannel(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// Format: #channel or @user
	return len(value) >= 2 && (value[0] == '#' || value[0] == '@')
}

func validateEmoji(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// Format: :emoji:
	return len(value) >= 3 && value[0] == ':' && value[len(value)-1] == ':'
}

func validateSlackColor(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true
	}

	// Predefined colors
	if value == "good" || value == "warning" || value == "danger" {
		return true
	}

	// Hex color
	if len(value) == 7 && value[0] == '#' {
		for _, r := range value[1:] {
			if !((r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'f') ||
				(r >= 'A' && r <= 'F')) {
				return false
			}
		}
		return true
	}

	return false
}

// Helper functions

func countRoutes(route *grouping.Route) int {
	if route == nil {
		return 0
	}

	count := 1
	for _, child := range route.Routes {
		count += countRoutes(child)
	}

	return count
}

func calculateRouteDepth(route *grouping.Route) int {
	if route == nil || len(route.Routes) == 0 {
		return 1
	}

	maxDepth := 0
	for _, child := range route.Routes {
		depth := calculateRouteDepth(child)
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	return maxDepth + 1
}

// applyRouteDefaultsRecursive applies defaults to route tree recursively.
func applyRouteDefaultsRecursive(route *grouping.Route) {
	if route == nil {
		return
	}

	// Apply defaults to current route
	route.Defaults()

	// Recursively apply to children
	for _, child := range route.Routes {
		applyRouteDefaultsRecursive(child)
	}
}
