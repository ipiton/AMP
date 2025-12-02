package publishing

import (
	"fmt"
	"net/url"
	"regexp"

	"github.com/ipiton/AMP/internal/core"
)

// Target name pattern: alphanumeric + hyphens, 1-63 chars (K8s DNS-1123 subdomain)
var targetNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// validateTarget validates PublishingTarget configuration.
//
// Validation Rules:
//  1. Required fields: name, type, url, format
//  2. Name: alphanumeric + hyphens, 1-63 chars (DNS-1123 compliant)
//  3. Type: one of [rootly, pagerduty, slack, webhook]
//  4. URL: valid HTTP/HTTPS URL
//  5. Format: one of [alertmanager, rootly, pagerduty, slack, webhook]
//  6. Type-Format compatibility (e.g., type=rootly requires format=rootly)
//  7. Headers: no empty keys/values
//
// Returns:
//   - Empty slice if valid
//   - Slice of ValidationError if invalid (can have multiple errors)
//
// Performance:
//   - Target: <200µs per target
//   - Goal (150%): <100µs per target
//
// Example:
//
//	target := &core.PublishingTarget{
//	    Name: "test@target",  // invalid (@ not allowed)
//	    Type: "rootly",
//	    URL: "not-a-url",     // invalid (not a URL)
//	    Format: "slack",      // invalid (type/format mismatch)
//	}
//
//	errs := validateTarget(target)
//	if len(errs) > 0 {
//	    for _, err := range errs {
//	        log.Warn("Validation failed", "error", err)
//	    }
//	    // Skip invalid target
//	}
func validateTarget(target *core.PublishingTarget) []ValidationError {
	var errors []ValidationError

	// Validate name (required, DNS-1123 compliant)
	if target.Name == "" {
		errors = append(errors, NewValidationError(
			"name",
			"field is required",
			target.Name,
		))
	} else if !isValidTargetName(target.Name) {
		errors = append(errors, NewValidationError(
			"name",
			"must be lowercase alphanumeric with hyphens (DNS-1123 subdomain)",
			target.Name,
		))
	}

	// Validate type (required, enum)
	if target.Type == "" {
		errors = append(errors, NewValidationError(
			"type",
			"field is required",
			target.Type,
		))
	} else if !isValidTargetType(target.Type) {
		errors = append(errors, NewValidationError(
			"type",
			"must be one of: rootly, pagerduty, slack, webhook",
			target.Type,
		))
	}

	// Validate URL (required, valid HTTP/HTTPS)
	if target.URL == "" {
		errors = append(errors, NewValidationError(
			"url",
			"field is required",
			target.URL,
		))
	} else if !isValidURL(target.URL) {
		errors = append(errors, NewValidationError(
			"url",
			"must be valid HTTP or HTTPS URL",
			target.URL,
		))
	}

	// Validate format (required, enum)
	if target.Format == "" {
		errors = append(errors, NewValidationError(
			"format",
			"field is required",
			string(target.Format),
		))
	} else if !isValidFormat(string(target.Format)) {
		errors = append(errors, NewValidationError(
			"format",
			"must be one of: alertmanager, rootly, pagerduty, slack, webhook",
			string(target.Format),
		))
	}

	// Validate type-format compatibility
	if target.Type != "" && target.Format != "" {
		if !isCompatibleTypeFormat(target.Type, string(target.Format)) {
			errors = append(errors, NewValidationError(
				"format",
				fmt.Sprintf("format '%s' incompatible with type '%s'", target.Format, target.Type),
				string(target.Format),
			))
		}
	}

	// Validate headers (no empty keys/values)
	for key, value := range target.Headers {
		if key == "" {
			errors = append(errors, NewValidationError(
				"headers",
				"header key cannot be empty",
				"<empty key>",
			))
		}
		if value == "" {
			errors = append(errors, NewValidationError(
				"headers",
				fmt.Sprintf("header value for '%s' cannot be empty", key),
				value,
			))
		}
	}

	return errors
}

// isValidTargetName checks if name matches DNS-1123 subdomain pattern.
//
// Rules:
//   - Lowercase letters (a-z), numbers (0-9), hyphens (-)
//   - Must start and end with alphanumeric
//   - Length: 1-63 characters
//   - No uppercase, no underscores, no special chars
//
// Examples:
//   - Valid: "rootly-prod", "slack-ops-1", "webhook1"
//   - Invalid: "Rootly-Prod" (uppercase), "slack_ops" (underscore), "test@example" (special char)
//
// Regex: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
//
// Why DNS-1123: K8s uses this format for resource names, ensures compatibility.
func isValidTargetName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	return targetNameRegex.MatchString(name)
}

// isValidTargetType checks if type is one of allowed values.
//
// Allowed types:
//   - rootly: Rootly incident management
//   - pagerduty: PagerDuty incident response
//   - slack: Slack messaging
//   - webhook: Generic webhook (any endpoint)
//
// Case-sensitive: Must be lowercase.
func isValidTargetType(targetType string) bool {
	switch targetType {
	case "rootly", "pagerduty", "slack", "webhook":
		return true
	default:
		return false
	}
}

// isValidFormat checks if format is one of allowed values.
//
// Allowed formats:
//   - alertmanager: Alertmanager webhook format (Prometheus)
//   - rootly: Rootly API format
//   - pagerduty: PagerDuty Events API v2 format
//   - slack: Slack Incoming Webhook format
//   - webhook: Generic JSON webhook
//
// Case-sensitive: Must be lowercase.
func isValidFormat(format string) bool {
	switch format {
	case "alertmanager", "rootly", "pagerduty", "slack", "webhook":
		return true
	default:
		return false
	}
}

// isValidURL checks if URL is valid HTTP/HTTPS URL.
//
// Rules:
//   - Must start with http:// or https://
//   - Must have valid host (domain or IP)
//   - Path and query params optional
//   - Fragment (#) optional
//
// Examples:
//   - Valid: "https://api.rootly.io/v1/incidents", "http://localhost:8080/webhook"
//   - Invalid: "not-a-url", "ftp://example.com", "example.com" (missing scheme)
//
// Uses net/url.Parse for validation.
func isValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	// Must have scheme (http or https)
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}

	// Must have host
	if u.Host == "" {
		return false
	}

	return true
}

// isCompatibleTypeFormat checks type-format compatibility.
//
// Compatibility Matrix:
//
//	| Type       | Allowed Formats               | Notes                          |
//	|------------|-------------------------------|--------------------------------|
//	| rootly     | rootly                        | Strict: Rootly API only        |
//	| pagerduty  | pagerduty                     | Strict: PagerDuty Events API   |
//	| slack      | slack                         | Strict: Slack webhook only     |
//	| webhook    | alertmanager, webhook         | Flexible: any generic format   |
//
// Why strict for rootly/pagerduty/slack?
//   - These have specific API contracts (payload structure)
//   - Using wrong format would cause API errors
//
// Why flexible for webhook?
//   - Generic webhooks accept various formats
//   - Alertmanager format is common standard
//   - Custom webhook format for non-standard endpoints
//
// Example:
//
//	isCompatibleTypeFormat("rootly", "rootly")      // true
//	isCompatibleTypeFormat("rootly", "slack")       // false (wrong format)
//	isCompatibleTypeFormat("webhook", "alertmanager") // true (flexible)
func isCompatibleTypeFormat(targetType, format string) bool {
	compatibilityMap := map[string][]string{
		"rootly":     {"rootly"},
		"pagerduty":  {"pagerduty"},
		"slack":      {"slack"},
		"webhook":    {"alertmanager", "webhook"}, // webhooks are flexible
	}

	allowedFormats, ok := compatibilityMap[targetType]
	if !ok {
		return false // unknown type
	}

	for _, allowed := range allowedFormats {
		if format == allowed {
			return true
		}
	}
	return false
}
