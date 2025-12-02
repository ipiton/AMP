package publishing

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// AlertValidator validates EnrichedAlert before formatting.
//
// Provides comprehensive validation with detailed error messages.
type AlertValidator interface {
	// Validate checks all validation rules
	//
	// Returns:
	//   []ValidationError: List of validation errors (empty if valid)
	Validate(alert *core.EnrichedAlert) []ValidationError
}

// DefaultAlertValidator implements comprehensive validation
type DefaultAlertValidator struct {
	rules []ValidationRule
}

// ValidationRule defines a single validation rule
type ValidationRule interface {
	// Validate checks if alert passes this rule
	//
	// Returns:
	//   *ValidationError: Error if validation fails, nil if passes
	Validate(alert *core.EnrichedAlert) *ValidationError
}

// NewDefaultAlertValidator creates validator with all 15+ rules
func NewDefaultAlertValidator() AlertValidator {
	return &DefaultAlertValidator{
		rules: []ValidationRule{
			// Nil checks
			&NotNilRule{},
			&AlertNotNilRule{},

			// Required fields
			&AlertNameRequiredRule{},
			&FingerprintRequiredRule{},
			&StatusRequiredRule{},
			&StartsAtRequiredRule{},

			// Format validation
			&StatusValidRule{},
			&FingerprintFormatRule{},
			&AlertNameFormatRule{},
			&GeneratorURLFormatRule{},

			// Labels/Annotations validation
			&LabelsNotNilRule{},
			&AnnotationsNotNilRule{},
			&LabelKeysValidRule{},
			&AnnotationKeysValidRule{},

			// Time validation
			&StartsAtReasonableRule{},
			&EndsAtAfterStartsAtRule{},

			// Classification validation (optional)
			&ClassificationValidRule{},
		},
	}
}

// Validate implements AlertValidator.Validate
func (v *DefaultAlertValidator) Validate(alert *core.EnrichedAlert) []ValidationError {
	var errors []ValidationError

	for _, rule := range v.rules {
		if err := rule.Validate(alert); err != nil {
			errors = append(errors, *err)
		}
	}

	return errors
}

// ===== Validation Rules (15+) =====

// 1. NotNilRule: EnrichedAlert must not be nil
type NotNilRule struct{}

func (r *NotNilRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil {
		return &ValidationError{
			Field:      "alert",
			Message:    "enriched alert is nil",
			Suggestion: "Ensure alert is not nil before formatting",
		}
	}
	return nil
}

// 2. AlertNotNilRule: Inner Alert must not be nil
type AlertNotNilRule struct{}

func (r *AlertNotNilRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return &ValidationError{
			Field:      "alert.Alert",
			Message:    "alert is nil",
			Suggestion: "Ensure alert.Alert is not nil",
		}
	}
	return nil
}

// 3. AlertNameRequiredRule: AlertName must not be empty
type AlertNameRequiredRule struct{}

func (r *AlertNameRequiredRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil // Handled by AlertNotNilRule
	}

	if alert.Alert.AlertName == "" {
		return &ValidationError{
			Field:      "alert.AlertName",
			Message:    "alert name is empty",
			Suggestion: "Provide a valid alert name (e.g., 'HighCPUUsage')",
		}
	}
	return nil
}

// 4. FingerprintRequiredRule: Fingerprint must not be empty
type FingerprintRequiredRule struct{}

func (r *FingerprintRequiredRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	if alert.Alert.Fingerprint == "" {
		return &ValidationError{
			Field:      "alert.Fingerprint",
			Message:    "fingerprint is empty",
			Suggestion: "Generate fingerprint using alert labels hash",
		}
	}
	return nil
}

// 5. StatusRequiredRule: Status must not be empty
type StatusRequiredRule struct{}

func (r *StatusRequiredRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	if alert.Alert.Status == "" {
		return &ValidationError{
			Field:      "alert.Status",
			Message:    "status is empty",
			Suggestion: "Set status to 'firing' or 'resolved'",
		}
	}
	return nil
}

// 6. StartsAtRequiredRule: StartsAt must not be zero
type StartsAtRequiredRule struct{}

func (r *StartsAtRequiredRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	if alert.Alert.StartsAt.IsZero() {
		return &ValidationError{
			Field:      "alert.StartsAt",
			Message:    "starts_at is zero time",
			Suggestion: "Set starts_at to alert creation time",
		}
	}
	return nil
}

// 7. StatusValidRule: Status must be 'firing' or 'resolved'
type StatusValidRule struct{}

func (r *StatusValidRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	status := alert.Alert.Status
	if status != core.StatusFiring && status != core.StatusResolved {
		return &ValidationError{
			Field:      "alert.Status",
			Message:    fmt.Sprintf("invalid status: %s", status),
			Value:      string(status),
			Suggestion: "Status must be 'firing' or 'resolved'",
		}
	}
	return nil
}

// 8. FingerprintFormatRule: Fingerprint format validation
type FingerprintFormatRule struct{}

var fingerprintRegex = regexp.MustCompile(`^[a-f0-9]{16,}$`)

func (r *FingerprintFormatRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil || alert.Alert.Fingerprint == "" {
		return nil // Handled by other rules
	}

	fp := alert.Alert.Fingerprint
	if !fingerprintRegex.MatchString(fp) {
		return &ValidationError{
			Field:      "alert.Fingerprint",
			Message:    "fingerprint has invalid format",
			Value:      fp,
			Suggestion: "Fingerprint should be lowercase hex string (16+ chars)",
		}
	}
	return nil
}

// 9. AlertNameFormatRule: AlertName format validation
type AlertNameFormatRule struct{}

var alertNameRegex = regexp.MustCompile(`^[A-Z][a-zA-Z0-9_-]+$`)

func (r *AlertNameFormatRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil || alert.Alert.AlertName == "" {
		return nil
	}

	name := alert.Alert.AlertName
	if !alertNameRegex.MatchString(name) {
		return &ValidationError{
			Field:      "alert.AlertName",
			Message:    "alert name has invalid format",
			Value:      name,
			Suggestion: "Alert name should start with uppercase letter and contain only alphanumeric, dash, underscore",
		}
	}
	return nil
}

// 10. GeneratorURLFormatRule: GeneratorURL must be valid URL
type GeneratorURLFormatRule struct{}

func (r *GeneratorURLFormatRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil || alert.Alert.GeneratorURL == nil {
		return nil // Optional field
	}

	urlStr := *alert.Alert.GeneratorURL
	if urlStr == "" {
		return nil // Empty is OK (optional)
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return &ValidationError{
			Field:      "alert.GeneratorURL",
			Message:    fmt.Sprintf("generator URL is invalid: %v", err),
			Value:      urlStr,
			Suggestion: "Provide valid URL (e.g., 'http://prometheus:9090/graph')",
		}
	}

	// Require scheme (http/https)
	if parsedURL.Scheme == "" {
		return &ValidationError{
			Field:      "alert.GeneratorURL",
			Message:    "generator URL must include scheme (http:// or https://)",
			Value:      urlStr,
			Suggestion: "Provide valid URL (e.g., 'http://prometheus:9090/graph')",
		}
	}

	return nil
}

// 11. LabelsNotNilRule: Labels map must not be nil
type LabelsNotNilRule struct{}

func (r *LabelsNotNilRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	if alert.Alert.Labels == nil {
		return &ValidationError{
			Field:      "alert.Labels",
			Message:    "labels map is nil",
			Suggestion: "Initialize labels map (can be empty: make(map[string]string))",
		}
	}
	return nil
}

// 12. AnnotationsNotNilRule: Annotations map must not be nil
type AnnotationsNotNilRule struct{}

func (r *AnnotationsNotNilRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	if alert.Alert.Annotations == nil {
		return &ValidationError{
			Field:      "alert.Annotations",
			Message:    "annotations map is nil",
			Suggestion: "Initialize annotations map (can be empty: make(map[string]string))",
		}
	}
	return nil
}

// 13. LabelKeysValidRule: Label keys must be valid
type LabelKeysValidRule struct{}

var labelKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (r *LabelKeysValidRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil || alert.Alert.Labels == nil {
		return nil
	}

	for key := range alert.Alert.Labels {
		if !labelKeyRegex.MatchString(key) {
			return &ValidationError{
				Field:      "alert.Labels",
				Message:    fmt.Sprintf("invalid label key: %s", key),
				Value:      key,
				Suggestion: "Label keys must start with letter/underscore and contain only alphanumeric/underscore",
			}
		}
	}
	return nil
}

// 14. AnnotationKeysValidRule: Annotation keys must be valid
type AnnotationKeysValidRule struct{}

func (r *AnnotationKeysValidRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil || alert.Alert.Annotations == nil {
		return nil
	}

	for key := range alert.Alert.Annotations {
		if !labelKeyRegex.MatchString(key) {
			return &ValidationError{
				Field:      "alert.Annotations",
				Message:    fmt.Sprintf("invalid annotation key: %s", key),
				Value:      key,
				Suggestion: "Annotation keys must start with letter/underscore and contain only alphanumeric/underscore",
			}
		}
	}
	return nil
}

// 15. StartsAtReasonableRule: StartsAt must be reasonable (not too far in past/future)
type StartsAtReasonableRule struct{}

func (r *StartsAtReasonableRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil || alert.Alert.StartsAt.IsZero() {
		return nil
	}

	now := time.Now()
	startsAt := alert.Alert.StartsAt

	// Check if too far in past (> 1 year)
	if startsAt.Before(now.Add(-365 * 24 * time.Hour)) {
		return &ValidationError{
			Field:      "alert.StartsAt",
			Message:    "starts_at is too far in the past (> 1 year)",
			Value:      startsAt.Format(time.RFC3339),
			Suggestion: "Verify starts_at timestamp is correct",
		}
	}

	// Check if in future (> 1 hour, allow small clock skew)
	if startsAt.After(now.Add(1 * time.Hour)) {
		return &ValidationError{
			Field:      "alert.StartsAt",
			Message:    "starts_at is in the future (> 1 hour)",
			Value:      startsAt.Format(time.RFC3339),
			Suggestion: "Verify starts_at timestamp is not in future",
		}
	}

	return nil
}

// 16. EndsAtAfterStartsAtRule: EndsAt must be after StartsAt
type EndsAtAfterStartsAtRule struct{}

func (r *EndsAtAfterStartsAtRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Alert == nil {
		return nil
	}

	startsAt := alert.Alert.StartsAt
	endsAt := alert.Alert.EndsAt

	// EndsAt is optional (nil for firing alerts)
	if endsAt == nil || endsAt.IsZero() {
		return nil
	}

	if !endsAt.After(startsAt) {
		return &ValidationError{
			Field:      "alert.EndsAt",
			Message:    "ends_at must be after starts_at",
			Value:      fmt.Sprintf("starts=%s, ends=%s", startsAt.Format(time.RFC3339), endsAt.Format(time.RFC3339)),
			Suggestion: "Ensure ends_at > starts_at",
		}
	}

	return nil
}

// 17. ClassificationValidRule: Classification validation (if present)
type ClassificationValidRule struct{}

func (r *ClassificationValidRule) Validate(alert *core.EnrichedAlert) *ValidationError {
	if alert == nil || alert.Classification == nil {
		return nil // Classification is optional
	}

	classification := alert.Classification

	// Validate severity
	validSeverities := []core.AlertSeverity{
		core.SeverityCritical,
		core.SeverityWarning,
		core.SeverityInfo,
		core.SeverityNoise,
	}
	severityValid := false
	for _, valid := range validSeverities {
		if classification.Severity == valid {
			severityValid = true
			break
		}
	}
	if !severityValid {
		return &ValidationError{
			Field:      "classification.Severity",
			Message:    fmt.Sprintf("invalid severity: %s", classification.Severity),
			Value:      string(classification.Severity),
			Suggestion: "Severity must be one of: critical, warning, info, noise",
		}
	}

	// Validate confidence (0.0 - 1.0)
	if classification.Confidence < 0.0 || classification.Confidence > 1.0 {
		return &ValidationError{
			Field:      "classification.Confidence",
			Message:    fmt.Sprintf("confidence out of range: %.2f", classification.Confidence),
			Value:      fmt.Sprintf("%.2f", classification.Confidence),
			Suggestion: "Confidence must be between 0.0 and 1.0",
		}
	}

	// Validate reasoning not empty (if confidence > 0)
	if classification.Confidence > 0 && strings.TrimSpace(classification.Reasoning) == "" {
		return &ValidationError{
			Field:      "classification.Reasoning",
			Message:    "reasoning is empty but confidence > 0",
			Suggestion: "Provide reasoning for non-zero confidence classification",
		}
	}

	return nil
}

// Helper: FormatValidationErrors formats errors for display
func FormatValidationErrors(errors []ValidationError) string {
	if len(errors) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Validation failed with %d error(s):\n", len(errors)))

	for i, err := range errors {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, err.Error()))
		if err.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("   Suggestion: %s\n", err.Suggestion))
		}
	}

	return sb.String()
}
