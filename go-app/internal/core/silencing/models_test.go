package silencing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Silence Tests
// ============================================================================

func TestSilence_ValidateValid(t *testing.T) {
	silence := &Silence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		CreatedBy: "ops@example.com",
		Comment:   "Planned maintenance window",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(2 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.NoError(t, err)
}

func TestSilence_ValidateInvalidID(t *testing.T) {
	silence := &Silence{
		ID:        "not-a-uuid",
		CreatedBy: "ops@example.com",
		Comment:   "Test silence",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidID)
}

func TestSilence_ValidateEmptyCreatedBy(t *testing.T) {
	silence := &Silence{
		CreatedBy: "",
		Comment:   "Test silence",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidCreatedBy)
}

func TestSilence_ValidateCreatedByTooLong(t *testing.T) {
	longCreator := string(make([]byte, 256)) // 256 chars, exceeds 255 limit
	for i := range longCreator {
		longCreator = string(append([]byte(longCreator[:i]), 'a'))
	}

	silence := &Silence{
		CreatedBy: string(make([]rune, 256)),
		Comment:   "Test silence",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidCreatedBy)
}

func TestSilence_ValidateCommentTooShort(t *testing.T) {
	silence := &Silence{
		CreatedBy: "ops@example.com",
		Comment:   "ab", // Only 2 chars, needs at least 3
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidComment)
}

func TestSilence_ValidateCommentTooLong(t *testing.T) {
	longComment := string(make([]rune, 1025)) // 1025 chars, exceeds 1024 limit

	silence := &Silence{
		CreatedBy: "ops@example.com",
		Comment:   longComment,
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidComment)
}

func TestSilence_ValidateInvalidTimeRange_EndsBeforeStarts(t *testing.T) {
	now := time.Now()
	silence := &Silence{
		CreatedBy: "ops@example.com",
		Comment:   "Test silence",
		StartsAt:  now,
		EndsAt:    now.Add(-1 * time.Hour), // EndsAt before StartsAt
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidTimeRange)
}

func TestSilence_ValidateInvalidTimeRange_EndsEqualsStarts(t *testing.T) {
	now := time.Now()
	silence := &Silence{
		CreatedBy: "ops@example.com",
		Comment:   "Test silence",
		StartsAt:  now,
		EndsAt:    now, // EndsAt equals StartsAt (invalid)
		Matchers: []Matcher{
			{Name: "alertname", Value: "test", Type: MatcherTypeEqual},
		},
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceInvalidTimeRange)
}

func TestSilence_ValidateNoMatchers(t *testing.T) {
	silence := &Silence{
		CreatedBy: "ops@example.com",
		Comment:   "Test silence",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers:  []Matcher{}, // Empty matchers
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceNoMatchers)
}

func TestSilence_ValidateTooManyMatchers(t *testing.T) {
	// Create 101 matchers (exceeds limit of 100)
	matchers := make([]Matcher, 101)
	for i := 0; i < 101; i++ {
		matchers[i] = Matcher{Name: "label", Value: "value", Type: MatcherTypeEqual}
	}

	silence := &Silence{
		CreatedBy: "ops@example.com",
		Comment:   "Test silence",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Matchers:  matchers,
	}

	err := silence.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSilenceTooManyMatchers)
}

func TestSilence_CalculateStatusPending(t *testing.T) {
	silence := &Silence{
		StartsAt: time.Now().Add(1 * time.Hour), // Starts in the future
		EndsAt:   time.Now().Add(2 * time.Hour),
	}

	status := silence.CalculateStatus()
	assert.Equal(t, SilenceStatusPending, status)
}

func TestSilence_CalculateStatusActive(t *testing.T) {
	silence := &Silence{
		StartsAt: time.Now().Add(-1 * time.Hour), // Started 1 hour ago
		EndsAt:   time.Now().Add(1 * time.Hour),  // Ends in 1 hour
	}

	status := silence.CalculateStatus()
	assert.Equal(t, SilenceStatusActive, status)
}

func TestSilence_CalculateStatusExpired(t *testing.T) {
	silence := &Silence{
		StartsAt: time.Now().Add(-2 * time.Hour), // Started 2 hours ago
		EndsAt:   time.Now().Add(-1 * time.Hour), // Ended 1 hour ago
	}

	status := silence.CalculateStatus()
	assert.Equal(t, SilenceStatusExpired, status)
}

func TestSilence_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		silence  *Silence
		expected bool
	}{
		{
			name: "active silence",
			silence: &Silence{
				StartsAt: time.Now().Add(-1 * time.Hour),
				EndsAt:   time.Now().Add(1 * time.Hour),
			},
			expected: true,
		},
		{
			name: "pending silence",
			silence: &Silence{
				StartsAt: time.Now().Add(1 * time.Hour),
				EndsAt:   time.Now().Add(2 * time.Hour),
			},
			expected: false,
		},
		{
			name: "expired silence",
			silence: &Silence{
				StartsAt: time.Now().Add(-2 * time.Hour),
				EndsAt:   time.Now().Add(-1 * time.Hour),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.silence.IsActive()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSilence_JSONMarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second) // Truncate to avoid precision issues
	silence := &Silence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		CreatedBy: "ops@example.com",
		Comment:   "Test comment",
		StartsAt:  now,
		EndsAt:    now.Add(2 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		},
		Status:    SilenceStatusActive,
		CreatedAt: now,
	}

	data, err := json.Marshal(silence)
	require.NoError(t, err)

	var decoded Silence
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, silence.ID, decoded.ID)
	assert.Equal(t, silence.CreatedBy, decoded.CreatedBy)
	assert.Equal(t, silence.Comment, decoded.Comment)
	assert.Equal(t, silence.Status, decoded.Status)
	assert.Len(t, decoded.Matchers, 1)
	assert.Equal(t, "alertname", decoded.Matchers[0].Name)
}

func TestSilence_JSONUnmarshal(t *testing.T) {
	jsonData := `{
		"id": "550e8400-e29b-41d4-a716-446655440000",
		"createdBy": "ops@example.com",
		"comment": "Test comment",
		"startsAt": "2025-11-04T10:00:00Z",
		"endsAt": "2025-11-04T12:00:00Z",
		"matchers": [
			{"name": "alertname", "value": "HighCPU", "type": "=", "isRegex": false}
		],
		"status": "active",
		"createdAt": "2025-11-04T09:30:00Z"
	}`

	var silence Silence
	err := json.Unmarshal([]byte(jsonData), &silence)
	require.NoError(t, err)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", silence.ID)
	assert.Equal(t, "ops@example.com", silence.CreatedBy)
	assert.Equal(t, "Test comment", silence.Comment)
	assert.Equal(t, SilenceStatusActive, silence.Status)
	assert.Len(t, silence.Matchers, 1)
	assert.Equal(t, "alertname", silence.Matchers[0].Name)
	assert.Equal(t, "HighCPU", silence.Matchers[0].Value)
	assert.Equal(t, MatcherTypeEqual, silence.Matchers[0].Type)
}

// ============================================================================
// Matcher Tests
// ============================================================================

func TestMatcher_ValidateValidEqual(t *testing.T) {
	matcher := &Matcher{
		Name:  "alertname",
		Value: "HighCPU",
		Type:  MatcherTypeEqual,
	}

	err := matcher.Validate()
	assert.NoError(t, err)
	assert.False(t, matcher.IsRegex)
}

func TestMatcher_ValidateValidNotEqual(t *testing.T) {
	matcher := &Matcher{
		Name:  "env",
		Value: "prod",
		Type:  MatcherTypeNotEqual,
	}

	err := matcher.Validate()
	assert.NoError(t, err)
	assert.False(t, matcher.IsRegex)
}

func TestMatcher_ValidateValidRegex(t *testing.T) {
	matcher := &Matcher{
		Name:  "severity",
		Value: "(critical|warning)",
		Type:  MatcherTypeRegex,
	}

	err := matcher.Validate()
	assert.NoError(t, err)
	assert.True(t, matcher.IsRegex)
}

func TestMatcher_ValidateValidNotRegex(t *testing.T) {
	matcher := &Matcher{
		Name:  "instance",
		Value: ".*-dev-.*",
		Type:  MatcherTypeNotRegex,
	}

	err := matcher.Validate()
	assert.NoError(t, err)
	assert.True(t, matcher.IsRegex)
}

func TestMatcher_ValidateInvalidName(t *testing.T) {
	tests := []struct {
		name        string
		matcherName string
	}{
		{"starts with digit", "9name"},
		{"contains hyphen", "label-name"},
		{"contains dot", "label.name"},
		{"contains space", "label name"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := &Matcher{
				Name:  tt.matcherName,
				Value: "value",
				Type:  MatcherTypeEqual,
			}

			err := matcher.Validate()
			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrMatcherInvalidName)
		})
	}
}

func TestMatcher_ValidateEmptyValue(t *testing.T) {
	matcher := &Matcher{
		Name:  "alertname",
		Value: "",
		Type:  MatcherTypeEqual,
	}

	err := matcher.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrMatcherEmptyValue)
}

func TestMatcher_ValidateValueTooLong(t *testing.T) {
	longValue := string(make([]rune, 1025)) // 1025 chars

	matcher := &Matcher{
		Name:  "alertname",
		Value: longValue,
		Type:  MatcherTypeEqual,
	}

	err := matcher.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrMatcherValueTooLong)
}

func TestMatcher_ValidateInvalidType(t *testing.T) {
	matcher := &Matcher{
		Name:  "alertname",
		Value: "test",
		Type:  MatcherType("invalid"),
	}

	err := matcher.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrMatcherInvalidType)
}

func TestMatcher_ValidateInvalidRegex(t *testing.T) {
	matcher := &Matcher{
		Name:  "alertname",
		Value: "[invalid regex(",
		Type:  MatcherTypeRegex,
	}

	err := matcher.Validate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrMatcherInvalidRegex)
}

func TestMatcher_IsRegexAutoSet(t *testing.T) {
	tests := []struct {
		name     string
		mType    MatcherType
		expected bool
	}{
		{"equal", MatcherTypeEqual, false},
		{"not equal", MatcherTypeNotEqual, false},
		{"regex", MatcherTypeRegex, true},
		{"not regex", MatcherTypeNotRegex, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := &Matcher{
				Name:  "alertname",
				Value: "test",
				Type:  tt.mType,
			}

			err := matcher.Validate()
			require.NoError(t, err)
			assert.Equal(t, tt.expected, matcher.IsRegex)
		})
	}
}

// ============================================================================
// MatcherType Tests
// ============================================================================

func TestMatcherType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		mType    MatcherType
		expected bool
	}{
		{"equal", MatcherTypeEqual, true},
		{"not equal", MatcherTypeNotEqual, true},
		{"regex", MatcherTypeRegex, true},
		{"not regex", MatcherTypeNotRegex, true},
		{"invalid", MatcherType("invalid"), false},
		{"empty", MatcherType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mType.IsValid()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatcherType_IsRegexType(t *testing.T) {
	tests := []struct {
		name     string
		mType    MatcherType
		expected bool
	}{
		{"equal", MatcherTypeEqual, false},
		{"not equal", MatcherTypeNotEqual, false},
		{"regex", MatcherTypeRegex, true},
		{"not regex", MatcherTypeNotRegex, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mType.IsRegexType()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatcherType_String(t *testing.T) {
	tests := []struct {
		name     string
		mType    MatcherType
		expected string
	}{
		{"equal", MatcherTypeEqual, "="},
		{"not equal", MatcherTypeNotEqual, "!="},
		{"regex", MatcherTypeRegex, "=~"},
		{"not regex", MatcherTypeNotRegex, "!~"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mType.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ============================================================================
// Validator Helper Tests
// ============================================================================

func TestIsValidLabelName_Valid(t *testing.T) {
	validNames := []string{
		"alertname",
		"job",
		"severity",
		"_internal",
		"label_1",
		"_",
		"a",
		"A",
		"Z",
		"z",
		"label123",
		"__name__",
	}

	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			result := isValidLabelName(name)
			assert.True(t, result, "expected %q to be valid", name)
		})
	}
}

func TestIsValidLabelName_Invalid(t *testing.T) {
	invalidNames := []string{
		"",
		"9name",
		"label-name",
		"label.name",
		"label name",
		"label@name",
		"label:name",
		"label/name",
	}

	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			result := isValidLabelName(name)
			assert.False(t, result, "expected %q to be invalid", name)
		})
	}
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkSilence_Validate(b *testing.B) {
	silence := &Silence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		CreatedBy: "ops@example.com",
		Comment:   "Planned maintenance window",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(2 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
			{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = silence.Validate()
	}
}

func BenchmarkMatcher_Validate(b *testing.B) {
	matcher := &Matcher{
		Name:  "severity",
		Value: "(critical|warning)",
		Type:  MatcherTypeRegex,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = matcher.Validate()
	}
}

func BenchmarkSilence_CalculateStatus(b *testing.B) {
	silence := &Silence{
		StartsAt: time.Now().Add(-1 * time.Hour),
		EndsAt:   time.Now().Add(1 * time.Hour),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = silence.CalculateStatus()
	}
}

func BenchmarkIsValidLabelName(b *testing.B) {
	name := "alertname"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isValidLabelName(name)
	}
}

func BenchmarkSilence_JSONMarshal(b *testing.B) {
	silence := &Silence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		CreatedBy: "ops@example.com",
		Comment:   "Test comment",
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(2 * time.Hour),
		Matchers: []Matcher{
			{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
		},
		Status:    SilenceStatusActive,
		CreatedAt: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(silence)
	}
}

func BenchmarkSilence_JSONUnmarshal(b *testing.B) {
	jsonData := []byte(`{
		"id": "550e8400-e29b-41d4-a716-446655440000",
		"createdBy": "ops@example.com",
		"comment": "Test comment",
		"startsAt": "2025-11-04T10:00:00Z",
		"endsAt": "2025-11-04T12:00:00Z",
		"matchers": [
			{"name": "alertname", "value": "HighCPU", "type": "=", "isRegex": false}
		],
		"status": "active",
		"createdAt": "2025-11-04T09:30:00Z"
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var silence Silence
		_ = json.Unmarshal(jsonData, &silence)
	}
}
