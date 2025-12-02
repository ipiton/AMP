package silencing

import (
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

// Validate validates the Silence and returns an error if any field is invalid.
// All validation errors are wrapped with context about which field failed.
//
// Validation rules:
//   - ID: Must be valid UUID v4 (if set, can be empty for new silences)
//   - CreatedBy: Required, non-empty, max 255 characters
//   - Comment: Required, min 3 characters, max 1024 characters
//   - StartsAt: Required (checked implicitly by time.Time zero value)
//   - EndsAt: Required, must be strictly after StartsAt
//   - Matchers: Required, min 1 matcher, max 100 matchers, each must be valid
//
// Example usage:
//
//	silence := &Silence{
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Maintenance",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers:  []Matcher{{Name: "job", Value: "api", Type: "="}},
//	}
//	if err := silence.Validate(); err != nil {
//	    log.Printf("Invalid silence: %v", err)
//	    return err
//	}
func (s *Silence) Validate() error {
	// Validate ID (if set)
	if s.ID != "" {
		if _, err := uuid.Parse(s.ID); err != nil {
			return fmt.Errorf("%w: %s", ErrSilenceInvalidID, err)
		}
	}

	// Validate CreatedBy
	if s.CreatedBy == "" {
		return ErrSilenceInvalidCreatedBy
	}
	if len(s.CreatedBy) > 255 {
		return ErrSilenceInvalidCreatedBy
	}

	// Validate Comment
	if len(s.Comment) < 3 {
		return ErrSilenceInvalidComment
	}
	if len(s.Comment) > 1024 {
		return ErrSilenceInvalidComment
	}

	// Validate time range
	if s.EndsAt.Before(s.StartsAt) || s.EndsAt.Equal(s.StartsAt) {
		return ErrSilenceInvalidTimeRange
	}

	// Validate matchers
	if len(s.Matchers) == 0 {
		return ErrSilenceNoMatchers
	}
	if len(s.Matchers) > 100 {
		return ErrSilenceTooManyMatchers
	}

	// Validate each matcher
	for i, matcher := range s.Matchers {
		if err := matcher.Validate(); err != nil {
			return fmt.Errorf("matcher %d: %w", i, err)
		}
	}

	return nil
}

// Validate validates the Matcher and returns an error if any field is invalid.
// The IsRegex field is auto-set based on Type during validation.
//
// Validation rules:
//   - Name: Must be valid Prometheus label name ([a-zA-Z_][a-zA-Z0-9_]*)
//   - Value: Required, non-empty, max 1024 characters
//   - Type: Must be one of =, !=, =~, !~
//   - For regex matchers (=~ and !~): Value must be a valid regex pattern
//
// Example usage:
//
//	matcher := &Matcher{
//	    Name:  "severity",
//	    Value: "(critical|warning)",
//	    Type:  MatcherTypeRegex,
//	}
//	if err := matcher.Validate(); err != nil {
//	    log.Printf("Invalid matcher: %v", err)
//	    return err
//	}
//	// matcher.IsRegex is now automatically set to true
func (m *Matcher) Validate() error {
	// Validate Name (Prometheus label name format)
	if !isValidLabelName(m.Name) {
		return ErrMatcherInvalidName
	}

	// Validate Value
	if m.Value == "" {
		return ErrMatcherEmptyValue
	}
	if len(m.Value) > 1024 {
		return ErrMatcherValueTooLong
	}

	// Validate Type
	if !m.Type.IsValid() {
		return ErrMatcherInvalidType
	}

	// Auto-set IsRegex based on Type
	m.IsRegex = m.Type.IsRegexType()

	// Validate regex pattern if regex matcher
	if m.IsRegex {
		if _, err := regexp.Compile(m.Value); err != nil {
			return fmt.Errorf("%w: %s", ErrMatcherInvalidRegex, err)
		}
	}

	return nil
}

// isValidLabelName checks if a label name follows Prometheus naming conventions.
// Valid names must match the pattern: [a-zA-Z_][a-zA-Z0-9_]*
//
// Rules:
//   - Must not be empty
//   - First character must be [a-zA-Z_]
//   - Subsequent characters must be [a-zA-Z0-9_]
//
// Examples:
//   - Valid: "alertname", "job", "severity", "_internal", "label_1"
//   - Invalid: "9name" (starts with digit), "label-name" (contains hyphen), "" (empty)
func isValidLabelName(name string) bool {
	if name == "" {
		return false
	}

	// First character must be [a-zA-Z_]
	first := rune(name[0])
	if !isAlpha(first) && first != '_' {
		return false
	}

	// Subsequent characters must be [a-zA-Z0-9_]
	for _, r := range name[1:] {
		if !isAlphaNumeric(r) && r != '_' {
			return false
		}
	}

	return true
}

// isAlpha checks if a rune is an alphabetic character (a-z or A-Z).
func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isAlphaNumeric checks if a rune is an alphanumeric character (a-z, A-Z, or 0-9).
func isAlphaNumeric(r rune) bool {
	return isAlpha(r) || (r >= '0' && r <= '9')
}
