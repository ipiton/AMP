package silencing

import (
	"time"
)

// Silence represents a silence rule that suppresses alerts matching specific criteria.
// It is fully compatible with Alertmanager API v2 silences.
//
// A silence consists of:
//   - Unique identifier (UUID v4)
//   - Time range (StartsAt to EndsAt)
//   - Label matchers to identify which alerts to silence
//   - Metadata (creator, comment)
//   - Auto-calculated status (pending/active/expired)
//
// Example usage:
//
//	silence := &Silence{
//	    ID:        "550e8400-e29b-41d4-a716-446655440000",
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Planned maintenance window",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers: []Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
//	        {Name: "job", Value: "api-server", Type: MatcherTypeEqual},
//	    },
//	}
//	if err := silence.Validate(); err != nil {
//	    log.Fatal(err)
//	}
type Silence struct {
	// ID is the unique identifier for this silence (UUID v4).
	// Generated automatically when creating a new silence.
	ID string `json:"id" db:"id"`

	// CreatedBy is the email or username of the user who created this silence.
	// Required field, maximum 255 characters.
	// Example: "ops@example.com", "john.doe"
	CreatedBy string `json:"createdBy" db:"created_by"`

	// Comment is a required description explaining why this silence was created.
	// Must be at least 3 characters, maximum 1024 characters.
	// Example: "Planned maintenance window for database upgrade"
	Comment string `json:"comment" db:"comment"`

	// StartsAt is the timestamp when the silence becomes active.
	// Alerts matching the matchers will be suppressed starting from this time.
	StartsAt time.Time `json:"startsAt" db:"starts_at"`

	// EndsAt is the timestamp when the silence expires.
	// Must be after StartsAt. Alerts will resume notification after this time.
	EndsAt time.Time `json:"endsAt" db:"ends_at"`

	// Matchers defines the label matching criteria for alerts to be silenced.
	// At least one matcher is required, maximum 100 matchers allowed.
	// Only alerts matching ALL matchers will be silenced (AND logic).
	Matchers []Matcher `json:"matchers" db:"matchers"`

	// Status represents the current state of the silence.
	// Auto-calculated based on StartsAt, EndsAt, and current time.
	// Use CalculateStatus() to update this field.
	Status SilenceStatus `json:"status" db:"status"`

	// CreatedAt is the timestamp when this silence was created.
	// Set automatically by the database.
	CreatedAt time.Time `json:"createdAt" db:"created_at"`

	// UpdatedAt is the timestamp of the last update to this silence.
	// Nil if the silence has never been updated.
	UpdatedAt *time.Time `json:"updatedAt,omitempty" db:"updated_at"`
}

// SilenceStatus represents the state of a silence.
// The status is determined by comparing the current time with StartsAt and EndsAt.
type SilenceStatus string

const (
	// SilenceStatusPending indicates the silence has not yet become active.
	// This means the current time is before StartsAt.
	SilenceStatusPending SilenceStatus = "pending"

	// SilenceStatusActive indicates the silence is currently active.
	// This means the current time is between StartsAt and EndsAt.
	// Alerts matching the matchers are currently being suppressed.
	SilenceStatusActive SilenceStatus = "active"

	// SilenceStatusExpired indicates the silence has expired.
	// This means the current time is after EndsAt.
	// Alerts matching the matchers are no longer being suppressed.
	SilenceStatusExpired SilenceStatus = "expired"
)

// CalculateStatus calculates and returns the current status of the silence
// based on StartsAt, EndsAt, and the current time.
//
// Status logic:
//   - pending: now < StartsAt
//   - active:  StartsAt <= now < EndsAt
//   - expired: now >= EndsAt
//
// This method does not modify the Silence.Status field. To update the field,
// assign the returned value:
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

// IsActive returns true if the silence is currently active (status == active).
// This is a convenience method equivalent to checking:
//
//	silence.CalculateStatus() == SilenceStatusActive
func (s *Silence) IsActive() bool {
	return s.CalculateStatus() == SilenceStatusActive
}

// Matcher defines a label matching criterion for silences.
// Supports four types of matching operations: =, !=, =~, !~
//
// A matcher consists of:
//   - Label name (Prometheus label format)
//   - Value to match (or regex pattern for regex matchers)
//   - Matching operator (type)
//   - IsRegex flag (auto-set based on type)
//
// Example usage:
//
//	// Exact match
//	matcher1 := Matcher{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual}
//
//	// Regex match
//	matcher2 := Matcher{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex}
//
//	// Not equal
//	matcher3 := Matcher{Name: "env", Value: "dev", Type: MatcherTypeNotEqual}
type Matcher struct {
	// Name is the label name to match against.
	// Must be a valid Prometheus label name: [a-zA-Z_][a-zA-Z0-9_]*
	// Examples: "alertname", "job", "severity", "instance"
	Name string `json:"name"`

	// Value is the value to match (or regex pattern for regex matchers).
	// For exact matchers (= and !=): exact string to compare
	// For regex matchers (=~ and !~): valid regex pattern
	// Maximum 1024 characters.
	Value string `json:"value"`

	// Type is the matching operator.
	// Must be one of: = (equal), != (not equal), =~ (regex), !~ (not regex)
	Type MatcherType `json:"type"`

	// IsRegex indicates whether this is a regex matcher (=~ or !~).
	// This field is auto-set based on Type during validation.
	// You typically don't need to set this manually.
	IsRegex bool `json:"isRegex"`
}

// MatcherType represents the type of label matching operation.
type MatcherType string

const (
	// MatcherTypeEqual (=) matches if the label value equals the specified value.
	// Example: {Name: "job", Value: "api-server", Type: MatcherTypeEqual}
	// Matches alerts where label "job" exactly equals "api-server"
	MatcherTypeEqual MatcherType = "="

	// MatcherTypeNotEqual (!=) matches if the label value does not equal the specified value.
	// Example: {Name: "env", Value: "prod", Type: MatcherTypeNotEqual}
	// Matches alerts where label "env" is NOT "prod"
	MatcherTypeNotEqual MatcherType = "!="

	// MatcherTypeRegex (=~) matches if the label value matches the regex pattern.
	// Example: {Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex}
	// Matches alerts where "severity" is either "critical" or "warning"
	MatcherTypeRegex MatcherType = "=~"

	// MatcherTypeNotRegex (!~) matches if the label value does NOT match the regex pattern.
	// Example: {Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex}
	// Matches alerts where "instance" does NOT contain "-dev-"
	MatcherTypeNotRegex MatcherType = "!~"
)

// IsValid checks if the MatcherType is one of the valid types.
// Returns true if the type is =, !=, =~, or !~, false otherwise.
func (mt MatcherType) IsValid() bool {
	switch mt {
	case MatcherTypeEqual, MatcherTypeNotEqual, MatcherTypeRegex, MatcherTypeNotRegex:
		return true
	default:
		return false
	}
}

// IsRegexType returns true if the MatcherType is a regex matcher (=~ or !~).
func (mt MatcherType) IsRegexType() bool {
	return mt == MatcherTypeRegex || mt == MatcherTypeNotRegex
}

// String returns the string representation of the MatcherType.
func (mt MatcherType) String() string {
	return string(mt)
}
