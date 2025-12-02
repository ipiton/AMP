package grouping

import (
	"fmt"
	"regexp"
	"time"
)

// Label name validation regex (Prometheus standard)
var labelNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// isValidLabelName checks if a label name is valid according to Prometheus naming conventions.
// Valid label names must match: [a-zA-Z_][a-zA-Z0-9_]*
//
// Examples:
//   - alertname ✓
//   - cluster_name ✓
//   - __internal__ ✓
//   - alert-name ✗ (dash not allowed)
//   - 123alert ✗ (cannot start with digit)
func isValidLabelName(name string) bool {
	if name == "" {
		return false
	}
	return labelNameRegex.MatchString(name)
}

// validateGroupWaitRange validates that group_wait is within allowed range.
// Range: 0s to 1h (3600 seconds)
func validateGroupWaitRange(d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("group_wait must be non-negative, got %s", d)
	}
	if d > time.Hour {
		return fmt.Errorf("group_wait must be at most 1h, got %s", d)
	}
	return nil
}

// validateGroupIntervalRange validates that group_interval is within allowed range.
// Range: 1s to 24h (86400 seconds)
func validateGroupIntervalRange(d time.Duration) error {
	if d < time.Second {
		return fmt.Errorf("group_interval must be at least 1s, got %s", d)
	}
	if d > 24*time.Hour {
		return fmt.Errorf("group_interval must be at most 24h, got %s", d)
	}
	return nil
}

// validateRepeatIntervalRange validates that repeat_interval is within allowed range.
// Range: 1m to 168h (7 days / 604800 seconds)
func validateRepeatIntervalRange(d time.Duration) error {
	if d < time.Minute {
		return fmt.Errorf("repeat_interval must be at least 1m, got %s", d)
	}
	if d > 7*24*time.Hour {
		return fmt.Errorf("repeat_interval must be at most 168h (7 days), got %s", d)
	}
	return nil
}

// ValidateLabelNames validates a list of label names.
// Returns the first invalid label name and an error, or empty string and nil if all are valid.
func ValidateLabelNames(labels []string) (string, error) {
	for _, label := range labels {
		if !isValidLabelName(label) {
			return label, fmt.Errorf("invalid label name '%s': must match [a-zA-Z_][a-zA-Z0-9_]*", label)
		}
	}
	return "", nil
}

// ValidateGroupByLabels validates the group_by configuration.
// It checks for:
//   - Special value '...' (valid, disables grouping)
//   - Empty list (valid, creates global group)
//   - Valid label names (Prometheus standard)
func ValidateGroupByLabels(groupBy []string) error {
	// Empty is valid (global group)
	if len(groupBy) == 0 {
		return nil
	}

	// Special value '...' is valid
	if len(groupBy) == 1 && groupBy[0] == "..." {
		return nil
	}

	// Validate each label name
	invalidLabel, err := ValidateLabelNames(groupBy)
	if err != nil {
		return fmt.Errorf("group_by validation failed: invalid label '%s': must match [a-zA-Z_][a-zA-Z0-9_]*", invalidLabel)
	}

	return nil
}

// ValidateTimers validates all timer configurations in a route.
// This is a convenience function that validates all three timers at once.
func ValidateTimers(groupWait, groupInterval, repeatInterval *Duration) error {
	var errors ValidationErrors

	if groupWait != nil {
		if err := validateGroupWaitRange(groupWait.Duration); err != nil {
			errors.Add("group_wait", groupWait.String(), "range", err.Error())
		}
	}

	if groupInterval != nil {
		if err := validateGroupIntervalRange(groupInterval.Duration); err != nil {
			errors.Add("group_interval", groupInterval.String(), "range", err.Error())
		}
	}

	if repeatInterval != nil {
		if err := validateRepeatIntervalRange(repeatInterval.Duration); err != nil {
			errors.Add("repeat_interval", repeatInterval.String(), "range", err.Error())
		}
	}

	return errors.ToError()
}

// ValidateRoute performs comprehensive validation on a Route.
// This includes:
//   - Receiver name is not empty
//   - GroupBy labels are valid
//   - Timers are within allowed ranges
//   - Match/MatchRE are not both empty if specified
//   - Nested routes are valid (recursive)
func ValidateRoute(route *Route) error {
	if route == nil {
		return fmt.Errorf("route cannot be nil")
	}

	var errors ValidationErrors

	// Validate receiver
	if route.Receiver == "" {
		errors.Add("receiver", "", "required", "receiver is required")
	}

	// Validate group_by
	if err := ValidateGroupByLabels(route.GroupBy); err != nil {
		errors.Add("group_by", fmt.Sprintf("%v", route.GroupBy), "labelname", err.Error())
	}

	// Validate timers
	if err := ValidateTimers(route.GroupWait, route.GroupInterval, route.RepeatInterval); err != nil {
		if timerErrs, ok := err.(ValidationErrors); ok {
			for _, e := range timerErrs {
				errors.AddError(e)
			}
		}
	}

	// Validate match/match_re not both empty if specified
	if route.Match != nil && len(route.Match) == 0 &&
		route.MatchRE != nil && len(route.MatchRE) == 0 {
		errors.Add("match", "", "empty_matchers", "at least one match or match_re must be specified if matchers are present")
	}

	// Recursively validate nested routes
	for i, nestedRoute := range route.Routes {
		if err := ValidateRoute(nestedRoute); err != nil {
			if nestedErrs, ok := err.(ValidationErrors); ok {
				for _, e := range nestedErrs {
					// Prefix field names with route index
					e.Field = fmt.Sprintf("routes[%d].%s", i, e.Field)
					errors.AddError(e)
				}
			} else {
				errors.Add(fmt.Sprintf("routes[%d]", i), "", "nested_validation", err.Error())
			}
		}
	}

	// Check nesting depth
	depth := calculateRouteDepth(route)
	if depth > maxRouteDepth {
		errors.Add("routes", fmt.Sprintf("%d", depth), "max_depth",
			fmt.Sprintf("route nesting depth (%d) exceeds maximum allowed (%d)", depth, maxRouteDepth))
	}

	return errors.ToError()
}

// ValidateConfig performs validation on the entire GroupingConfig.
// This is the top-level validation function that should be called after parsing.
func ValidateConfig(config *GroupingConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	if config.Route == nil {
		return fmt.Errorf("route configuration is required")
	}

	return ValidateRoute(config.Route)
}

// ValidateConfigCompat performs Alertmanager compatibility validation.
// This checks for known incompatibilities or unsupported features.
func ValidateConfigCompat(config *GroupingConfig) error {
	var warnings []string

	// Check for very short group_wait (might cause excessive notifications)
	if config.Route.GroupWait != nil && config.Route.GroupWait.Duration < 5*time.Second {
		warnings = append(warnings, fmt.Sprintf("group_wait=%s is very short, may cause excessive notifications", config.Route.GroupWait))
	}

	// Check for very long group_wait (might delay critical alerts)
	if config.Route.GroupWait != nil && config.Route.GroupWait.Duration > 10*time.Minute {
		warnings = append(warnings, fmt.Sprintf("group_wait=%s is very long, may delay critical alerts", config.Route.GroupWait))
	}

	// Check for very short group_interval (might cause notification spam)
	if config.Route.GroupInterval != nil && config.Route.GroupInterval.Duration < 30*time.Second {
		warnings = append(warnings, fmt.Sprintf("group_interval=%s is very short, may cause notification spam", config.Route.GroupInterval))
	}

	// Check for very short repeat_interval (might cause alert fatigue)
	if config.Route.RepeatInterval != nil && config.Route.RepeatInterval.Duration < 30*time.Minute {
		warnings = append(warnings, fmt.Sprintf("repeat_interval=%s is very short, may cause alert fatigue", config.Route.RepeatInterval))
	}

	// If there are warnings, return them as an info message
	// (not an error, but useful for operators)
	if len(warnings) > 0 {
		// For now, we just log warnings, not fail validation
		// In the future, this could return a special warning type
		return nil
	}

	return nil
}

// SanitizeConfig sanitizes the configuration by removing sensitive data.
// Useful for logging or exposing config via API.
func SanitizeConfig(config *GroupingConfig) *GroupingConfig {
	if config == nil {
		return nil
	}

	// Create a deep copy
	sanitized := &GroupingConfig{
		Route: config.Route.Clone(),
	}

	// Remove source file path (might contain sensitive info)
	sanitizeRouteSources(sanitized.Route)

	return sanitized
}

// sanitizeRouteSources recursively removes source file paths from routes.
func sanitizeRouteSources(route *Route) {
	if route == nil {
		return
	}

	route.source = ""

	for _, nestedRoute := range route.Routes {
		sanitizeRouteSources(nestedRoute)
	}
}
