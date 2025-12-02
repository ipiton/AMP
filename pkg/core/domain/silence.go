package domain

import (
	"fmt"
	"regexp"
	"time"
)

// ================================================================================
// Silence Domain Model - OSS Core
// ================================================================================
// Pure domain model for alert silencing (Alertmanager-compatible).
// Zero dependencies (stdlib only).

// Silence represents a silence rule that suppresses alerts matching specific criteria.
//
// Silences are used to:
//   - Suppress known issues during maintenance windows
//   - Reduce alert noise for expected events
//   - Temporarily disable alerts for debugging
//
// A silence consists of:
//   - Time range (StartsAt to EndsAt)
//   - Label matchers (which alerts to silence)
//   - Metadata (creator, comment, why)
//   - Auto-calculated status (pending/active/expired)
//
// 100% Alertmanager API v2 compatible.
//
// Example:
//
//	silence := &Silence{
//	    ID:        "550e8400-e29b-41d4-a716-446655440000",
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Planned database maintenance",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers: []Matcher{
//	        {Name: "alertname", Value: "DatabaseDown", Type: MatcherTypeEqual},
//	        {Name: "instance", Value: "db-01", Type: MatcherTypeEqual},
//	    },
//	}
type Silence struct {
	// ID is the unique identifier for this silence (UUID v4).
	// Generated automatically when creating a new silence.
	// Example: "550e8400-e29b-41d4-a716-446655440000"
	ID string `json:"id"`

	// CreatedBy is the email or username of the silence creator.
	// Required field, maximum 255 characters.
	// Example: "ops@example.com", "john.doe", "alice@company.com"
	CreatedBy string `json:"createdBy"`

	// Comment is a required description explaining why this silence exists.
	// Minimum 3 characters, maximum 1024 characters.
	// Should explain:
	//   - What is being silenced
	//   - Why it's being silenced
	//   - Expected duration/resolution
	//
	// Example: "Planned database maintenance for version upgrade. Expected downtime: 2 hours."
	Comment string `json:"comment"`

	// StartsAt is when the silence becomes active.
	// Alerts matching the matchers will be suppressed starting from this time.
	// Can be in the future (creates "pending" silence).
	StartsAt time.Time `json:"startsAt"`

	// EndsAt is when the silence expires.
	// Must be after StartsAt.
	// Alerts will resume normal notification after this time.
	// Maximum duration: 90 days from StartsAt (configurable).
	EndsAt time.Time `json:"endsAt"`

	// Matchers defines which alerts should be silenced.
	// At least 1 matcher required, maximum 100 matchers.
	// All matchers must match (AND logic) for an alert to be silenced.
	//
	// Example:
	//   Matchers: []Matcher{
	//       {Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
	//       {Name: "severity", Value: "warning", Type: MatcherTypeEqual},
	//   }
	Matchers []Matcher `json:"matchers"`

	// Status represents the current state of the silence.
	// Auto-calculated based on StartsAt, EndsAt, and current time.
	// Use CalculateStatus() to update this field.
	//
	// Values: "pending", "active", "expired"
	Status SilenceStatus `json:"status"`

	// CreatedAt is when this silence was created.
	// Set automatically by the system.
	CreatedAt time.Time `json:"createdAt"`

	// UpdatedAt is when this silence was last updated.
	// Nil if never updated.
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// SilenceStatus represents the state of a silence.
type SilenceStatus string

const (
	// SilenceStatusPending indicates the silence has not yet started.
	// Current time is before StartsAt.
	SilenceStatusPending SilenceStatus = "pending"

	// SilenceStatusActive indicates the silence is currently active.
	// Current time is between StartsAt and EndsAt.
	// Matching alerts are being suppressed.
	SilenceStatusActive SilenceStatus = "active"

	// SilenceStatusExpired indicates the silence has ended.
	// Current time is after EndsAt.
	// Matching alerts are no longer suppressed.
	SilenceStatusExpired SilenceStatus = "expired"
)

// Matcher defines a label matching criterion for silences.
//
// Supports 4 types of matching:
//   - = (equal): Exact string match
//   - != (not equal): String inequality
//   - =~ (regex): Regular expression match
//   - !~ (not regex): Regular expression non-match
//
// Example:
//
//	// Exact match
//	{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual}
//
//	// Regex match (all prod instances)
//	{Name: "instance", Value: "prod-.*", Type: MatcherTypeRegex}
//
//	// Negation (not critical)
//	{Name: "severity", Value: "critical", Type: MatcherTypeNotEqual}
type Matcher struct {
	// Name is the label name to match against.
	// Must follow Prometheus label naming rules: [a-zA-Z_][a-zA-Z0-9_]*
	// Example: "alertname", "severity", "instance", "job"
	Name string `json:"name"`

	// Value is the value to match (or regex pattern for regex matchers).
	// For exact matchers (=, !=): plain string
	// For regex matchers (=~, !~): valid RE2 regex pattern
	//
	// Examples:
	//   - Exact: "HighCPU"
	//   - Regex: "prod-.*" (matches "prod-01", "prod-02", etc.)
	Value string `json:"value"`

	// Type specifies the matching operation.
	// Must be one of: "=", "!=", "=~", "!~"
	Type MatcherType `json:"type"`

	// IsRegex indicates if this is a regex matcher (=~ or !~).
	// Auto-set based on Type.
	// Used for optimization (compile regex once).
	IsRegex bool `json:"isRegex"`
}

// MatcherType represents the type of label matching.
type MatcherType string

const (
	// MatcherTypeEqual (=) matches if label value equals matcher value exactly.
	MatcherTypeEqual MatcherType = "="

	// MatcherTypeNotEqual (!=) matches if label value does NOT equal matcher value.
	MatcherTypeNotEqual MatcherType = "!="

	// MatcherTypeRegex (=~) matches if label value matches regex pattern.
	// Uses RE2 regex syntax (Go regexp package).
	MatcherTypeRegex MatcherType = "=~"

	// MatcherTypeNotRegex (!~) matches if label value does NOT match regex pattern.
	MatcherTypeNotRegex MatcherType = "!~"
)

// ================================================================================
// Silence Methods
// ================================================================================

// Validate checks if the silence is valid.
//
// Validation rules:
//   - ID must be a valid UUID v4 (36 characters)
//   - CreatedBy must not be empty (max 255 chars)
//   - Comment must be 3-1024 characters
//   - StartsAt must not be zero
//   - EndsAt must be after StartsAt
//   - At least 1 matcher, maximum 100 matchers
//   - All matchers must be valid
//
// Returns:
//   - nil if valid
//   - error with validation message if invalid
func (s *Silence) Validate() error {
	// ID validation
	if len(s.ID) != 36 {
		return fmt.Errorf("id must be UUID v4 (36 characters), got %d", len(s.ID))
	}

	// CreatedBy validation
	if s.CreatedBy == "" {
		return fmt.Errorf("createdBy is required")
	}
	if len(s.CreatedBy) > 255 {
		return fmt.Errorf("createdBy must be max 255 characters, got %d", len(s.CreatedBy))
	}

	// Comment validation
	if len(s.Comment) < 3 {
		return fmt.Errorf("comment must be at least 3 characters, got %d", len(s.Comment))
	}
	if len(s.Comment) > 1024 {
		return fmt.Errorf("comment must be max 1024 characters, got %d", len(s.Comment))
	}

	// Time validation
	if s.StartsAt.IsZero() {
		return fmt.Errorf("startsAt is required")
	}
	if s.EndsAt.IsZero() {
		return fmt.Errorf("endsAt is required")
	}
	if !s.EndsAt.After(s.StartsAt) {
		return fmt.Errorf("endsAt must be after startsAt")
	}

	// Duration validation (optional, can be configurable)
	duration := s.EndsAt.Sub(s.StartsAt)
	if duration > 90*24*time.Hour {
		return fmt.Errorf("silence duration must be max 90 days, got %v", duration)
	}

	// Matchers validation
	if len(s.Matchers) == 0 {
		return fmt.Errorf("at least one matcher is required")
	}
	if len(s.Matchers) > 100 {
		return fmt.Errorf("maximum 100 matchers allowed, got %d", len(s.Matchers))
	}

	for i, matcher := range s.Matchers {
		if err := matcher.Validate(); err != nil {
			return fmt.Errorf("matcher %d invalid: %w", i, err)
		}
	}

	return nil
}

// CalculateStatus calculates the current status based on time.
//
// Status logic:
//   - pending: now < StartsAt
//   - active: StartsAt <= now < EndsAt
//   - expired: now >= EndsAt
//
// This method does NOT modify the Silence.Status field.
// To update the field, assign the returned value:
//
//	silence.Status = silence.CalculateStatus()
func (s *Silence) CalculateStatus() SilenceStatus {
	now := time.Now()
	if now.Before(s.StartsAt) {
		return SilenceStatusPending
	}
	if now.Before(s.EndsAt) {
		return SilenceStatusActive
	}
	return SilenceStatusExpired
}

// IsActive returns true if the silence is currently active.
// Convenience method equivalent to:
//
//	silence.CalculateStatus() == SilenceStatusActive
func (s *Silence) IsActive() bool {
	return s.CalculateStatus() == SilenceStatusActive
}

// IsPending returns true if the silence has not yet started.
func (s *Silence) IsPending() bool {
	return s.CalculateStatus() == SilenceStatusPending
}

// IsExpired returns true if the silence has ended.
func (s *Silence) IsExpired() bool {
	return s.CalculateStatus() == SilenceStatusExpired
}

// Duration returns the total duration of the silence.
func (s *Silence) Duration() time.Duration {
	return s.EndsAt.Sub(s.StartsAt)
}

// TimeRemaining returns how much time is left until the silence expires.
// Returns 0 if the silence has already expired.
func (s *Silence) TimeRemaining() time.Duration {
	if s.IsExpired() {
		return 0
	}
	remaining := time.Until(s.EndsAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// MatchesAlert checks if this silence matches the given alert.
// Returns true if ALL matchers match (AND logic).
func (s *Silence) MatchesAlert(alert *Alert) (bool, error) {
	if alert == nil || alert.Labels == nil {
		return false, fmt.Errorf("alert or alert.Labels is nil")
	}

	for _, matcher := range s.Matchers {
		matches, err := matcher.Matches(alert.Labels)
		if err != nil {
			return false, fmt.Errorf("matcher %s failed: %w", matcher.Name, err)
		}
		if !matches {
			return false, nil // One matcher failed, silence doesn't match
		}
	}

	return true, nil // All matchers passed
}

// ================================================================================
// Matcher Methods
// ================================================================================

// Validate checks if the matcher is valid.
//
// Validation rules:
//   - Name must not be empty and match [a-zA-Z_][a-zA-Z0-9_]*
//   - Value must not be empty
//   - Type must be one of: =, !=, =~, !~
//   - For regex types (=~, !~), Value must be valid regex
//
// Returns:
//   - nil if valid
//   - error with validation message if invalid
func (m *Matcher) Validate() error {
	// Name validation
	if m.Name == "" {
		return fmt.Errorf("matcher name is required")
	}
	validName := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if !validName.MatchString(m.Name) {
		return fmt.Errorf("invalid matcher name: %s (must match [a-zA-Z_][a-zA-Z0-9_]*)", m.Name)
	}

	// Value validation
	if m.Value == "" {
		return fmt.Errorf("matcher value is required")
	}

	// Type validation
	switch m.Type {
	case MatcherTypeEqual, MatcherTypeNotEqual, MatcherTypeRegex, MatcherTypeNotRegex:
		// Valid types
	default:
		return fmt.Errorf("invalid matcher type: %s (must be =, !=, =~, or !~)", m.Type)
	}

	// Set IsRegex flag
	m.IsRegex = (m.Type == MatcherTypeRegex || m.Type == MatcherTypeNotRegex)

	// Regex validation
	if m.IsRegex {
		if _, err := regexp.Compile(m.Value); err != nil {
			return fmt.Errorf("invalid regex pattern %q: %w", m.Value, err)
		}
	}

	return nil
}

// Matches checks if the matcher matches the given label value.
//
// Matching logic:
//   - = : label value equals matcher value
//   - != : label value does not equal matcher value
//   - =~ : label value matches regex pattern
//   - !~ : label value does not match regex pattern
//
// For missing labels:
//   - = and =~ return false (label doesn't match)
//   - != and !~ return true (label is "not equal" when missing)
//
// Returns:
//   - (true, nil) if matches
//   - (false, nil) if doesn't match
//   - (false, error) if validation/regex error
func (m *Matcher) Matches(labels map[string]string) (bool, error) {
	labelValue, exists := labels[m.Name]

	switch m.Type {
	case MatcherTypeEqual:
		return exists && labelValue == m.Value, nil

	case MatcherTypeNotEqual:
		return !exists || labelValue != m.Value, nil

	case MatcherTypeRegex:
		if !exists {
			return false, nil
		}
		re, err := regexp.Compile(m.Value)
		if err != nil {
			return false, fmt.Errorf("invalid regex: %w", err)
		}
		return re.MatchString(labelValue), nil

	case MatcherTypeNotRegex:
		if !exists {
			return true, nil // Missing label "doesn't match" any regex
		}
		re, err := regexp.Compile(m.Value)
		if err != nil {
			return false, fmt.Errorf("invalid regex: %w", err)
		}
		return !re.MatchString(labelValue), nil

	default:
		return false, fmt.Errorf("unknown matcher type: %s", m.Type)
	}
}
