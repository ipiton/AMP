package silencing

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// Test buildListQuery

func TestBuildListQuery_EmptyFilter(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil, // Don't create metrics in tests to avoid duplicate registration
	}

	filter := SilenceFilter{}
	filter.ApplyDefaults()

	query, args := repo.buildListQuery(filter)

	// Should have base query with ORDER BY and LIMIT/OFFSET
	assert.Contains(t, query, "SELECT id, created_by, comment, starts_at, ends_at, matchers, status, created_at, updated_at")
	assert.Contains(t, query, "FROM silences")
	assert.Contains(t, query, "WHERE 1=1")
	assert.Contains(t, query, "ORDER BY created_at DESC")
	assert.Contains(t, query, "LIMIT")
	assert.Contains(t, query, "OFFSET")

	// Should have 2 args: limit and offset
	require.Len(t, args, 2, "Expected 2 args (limit, offset)")
	assert.Equal(t, 100, args[0], "Default limit should be 100")
	assert.Equal(t, 0, args[1], "Default offset should be 0")
}

func TestBuildListQuery_FilterByStatus(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	tests := []struct {
		name     string
		statuses []silencing.SilenceStatus
	}{
		{
			name:     "single status",
			statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		},
		{
			name: "multiple statuses",
			statuses: []silencing.SilenceStatus{
				silencing.SilenceStatusActive,
				silencing.SilenceStatusPending,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := SilenceFilter{
				Statuses: tt.statuses,
			}
			filter.ApplyDefaults()

			query, args := repo.buildListQuery(filter)

			// Should have status filter
			assert.Contains(t, query, "status = ANY($1)", "Query should filter by status")

			// First arg should be status array
			require.GreaterOrEqual(t, len(args), 3, "Should have at least 3 args (status, limit, offset)")
			statusStrings, ok := args[0].([]string)
			require.True(t, ok, "First arg should be string array")
			assert.Len(t, statusStrings, len(tt.statuses), "Status array length mismatch")
		})
	}
}

func TestBuildListQuery_FilterByCreator(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	filter := SilenceFilter{
		CreatedBy: "ops@example.com",
	}
	filter.ApplyDefaults()

	query, args := repo.buildListQuery(filter)

	// Should have creator filter
	assert.Contains(t, query, "created_by = $", "Query should filter by creator")

	// Should have creator in args
	require.GreaterOrEqual(t, len(args), 3, "Should have at least 3 args")
	assert.Equal(t, "ops@example.com", args[0], "Creator should be first arg")
}

func TestBuildListQuery_FilterByMatcherName(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	filter := SilenceFilter{
		MatcherName: "alertname",
	}
	filter.ApplyDefaults()

	query, args := repo.buildListQuery(filter)

	// Should have JSONB containment filter
	assert.Contains(t, query, "matchers @>", "Query should use JSONB containment")
	assert.Contains(t, query, "::jsonb", "Query should cast to JSONB")

	// Should have JSONB filter in args
	require.GreaterOrEqual(t, len(args), 3, "Should have at least 3 args")
	jsonbFilter, ok := args[0].(string)
	require.True(t, ok, "JSONB filter should be string")
	assert.Contains(t, jsonbFilter, `"name":"alertname"`, "JSONB filter should contain matcher name")
}

func TestBuildListQuery_FilterByMatcherValue(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	filter := SilenceFilter{
		MatcherValue: "HighCPU",
	}
	filter.ApplyDefaults()

	query, args := repo.buildListQuery(filter)

	// Should have JSONB containment filter
	assert.Contains(t, query, "matchers @>", "Query should use JSONB containment")

	// Should have JSONB filter in args
	require.GreaterOrEqual(t, len(args), 3, "Should have at least 3 args")
	jsonbFilter, ok := args[0].(string)
	require.True(t, ok, "JSONB filter should be string")
	assert.Contains(t, jsonbFilter, `"value":"HighCPU"`, "JSONB filter should contain matcher value")
}

func TestBuildListQuery_FilterByTimeRange(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	oneHourLater := now.Add(1 * time.Hour)

	tests := []struct {
		name          string
		filter        SilenceFilter
		expectedQuery string
	}{
		{
			name: "StartsAfter",
			filter: SilenceFilter{
				StartsAfter: &oneHourAgo,
			},
			expectedQuery: "starts_at >= $",
		},
		{
			name: "StartsBefore",
			filter: SilenceFilter{
				StartsBefore: &oneHourLater,
			},
			expectedQuery: "starts_at <= $",
		},
		{
			name: "EndsAfter",
			filter: SilenceFilter{
				EndsAfter: &oneHourAgo,
			},
			expectedQuery: "ends_at >= $",
		},
		{
			name: "EndsBefore",
			filter: SilenceFilter{
				EndsBefore: &oneHourLater,
			},
			expectedQuery: "ends_at <= $",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.filter.ApplyDefaults()
			query, args := repo.buildListQuery(tt.filter)

			// Should have time range filter
			assert.Contains(t, query, tt.expectedQuery, "Query should have time range filter")

			// Should have timestamp in args
			require.GreaterOrEqual(t, len(args), 3, "Should have at least 3 args")
			_, ok := args[0].(time.Time)
			require.True(t, ok, "First arg should be time.Time")
		})
	}
}

func TestBuildListQuery_Pagination(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	tests := []struct {
		name           string
		limit          int
		offset         int
		expectedLimit  int
		expectedOffset int
	}{
		{
			name:           "default values",
			limit:          0,
			offset:         0,
			expectedLimit:  100,
			expectedOffset: 0,
		},
		{
			name:           "custom values",
			limit:          50,
			offset:         100,
			expectedLimit:  50,
			expectedOffset: 100,
		},
		{
			name:           "max limit",
			limit:          1000,
			offset:         0,
			expectedLimit:  1000,
			expectedOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := SilenceFilter{
				Limit:  tt.limit,
				Offset: tt.offset,
			}
			filter.ApplyDefaults()

			query, args := repo.buildListQuery(filter)

			// Should have LIMIT and OFFSET
			assert.Contains(t, query, "LIMIT", "Query should have LIMIT")
			assert.Contains(t, query, "OFFSET", "Query should have OFFSET")

			// Last 2 args should be limit and offset
			require.GreaterOrEqual(t, len(args), 2, "Should have at least 2 args")
			assert.Equal(t, tt.expectedLimit, args[len(args)-2], "Limit mismatch")
			assert.Equal(t, tt.expectedOffset, args[len(args)-1], "Offset mismatch")
		})
	}
}

func TestBuildListQuery_Sorting(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	tests := []struct {
		name          string
		orderBy       string
		orderDesc     bool
		expectedQuery string
	}{
		{
			name:          "default: created_at DESC",
			orderBy:       "",
			orderDesc:     false, // Will be ignored, DESC is default
			expectedQuery: "ORDER BY created_at DESC",
		},
		{
			name:          "starts_at ASC",
			orderBy:       "starts_at",
			orderDesc:     false,
			expectedQuery: "ORDER BY starts_at ASC",
		},
		{
			name:          "ends_at DESC",
			orderBy:       "ends_at",
			orderDesc:     true,
			expectedQuery: "ORDER BY ends_at DESC",
		},
		{
			name:          "updated_at DESC",
			orderBy:       "updated_at",
			orderDesc:     true,
			expectedQuery: "ORDER BY updated_at DESC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := SilenceFilter{
				OrderBy:   tt.orderBy,
				OrderDesc: tt.orderDesc,
			}
			filter.ApplyDefaults()

			query, args := repo.buildListQuery(filter)

			// Should have correct ORDER BY clause
			assert.Contains(t, query, tt.expectedQuery, "Order by clause mismatch")

			// Should still have limit and offset
			require.Len(t, args, 2, "Should have 2 args (limit, offset)")
		})
	}
}

func TestBuildListQuery_CombinedFilters(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	now := time.Now()
	filter := SilenceFilter{
		Statuses:    []silencing.SilenceStatus{silencing.SilenceStatusActive},
		CreatedBy:   "ops@example.com",
		MatcherName: "alertname",
		StartsAfter: &now,
		Limit:       50,
		Offset:      10,
		OrderBy:     "starts_at",
		OrderDesc:   false,
	}

	query, args := repo.buildListQuery(filter)

	// Should have all filters
	assert.Contains(t, query, "status = ANY($", "Should filter by status")
	assert.Contains(t, query, "created_by = $", "Should filter by creator")
	assert.Contains(t, query, "matchers @> $", "Should filter by matcher")
	assert.Contains(t, query, "starts_at >= $", "Should filter by time range")
	assert.Contains(t, query, "ORDER BY starts_at ASC", "Should have correct sorting")
	assert.Contains(t, query, "LIMIT", "Should have limit")
	assert.Contains(t, query, "OFFSET", "Should have offset")

	// Should have 6 args: status, creator, matcher, starts_after, limit, offset
	require.Len(t, args, 6, "Should have 6 args")
}

// Test buildCountQuery

func TestBuildCountQuery_EmptyFilter(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	filter := SilenceFilter{}

	query, args := repo.buildCountQuery(filter)

	// Should have COUNT query without ORDER BY, LIMIT, OFFSET
	assert.Contains(t, query, "SELECT COUNT(*)")
	assert.Contains(t, query, "FROM silences")
	assert.Contains(t, query, "WHERE 1=1")
	assert.NotContains(t, query, "ORDER BY", "COUNT query should not have ORDER BY")
	assert.NotContains(t, query, "LIMIT", "COUNT query should not have LIMIT")
	assert.NotContains(t, query, "OFFSET", "COUNT query should not have OFFSET")

	// Should have no args for empty filter
	assert.Empty(t, args, "Empty filter should have no args")
}

func TestBuildCountQuery_WithFilters(t *testing.T) {
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	now := time.Now()
	filter := SilenceFilter{
		Statuses:    []silencing.SilenceStatus{silencing.SilenceStatusActive},
		CreatedBy:   "ops@example.com",
		MatcherName: "alertname",
		StartsAfter: &now,
	}

	query, args := repo.buildCountQuery(filter)

	// Should have all filters (same as list query)
	assert.Contains(t, query, "SELECT COUNT(*)")
	assert.Contains(t, query, "status = ANY($", "Should filter by status")
	assert.Contains(t, query, "created_by = $", "Should filter by creator")
	assert.Contains(t, query, "matchers @> $", "Should filter by matcher")
	assert.Contains(t, query, "starts_at >= $", "Should filter by time range")

	// Should NOT have ORDER BY, LIMIT, OFFSET
	assert.NotContains(t, query, "ORDER BY")
	assert.NotContains(t, query, "LIMIT")
	assert.NotContains(t, query, "OFFSET")

	// Should have 4 args (no limit/offset)
	require.Len(t, args, 4, "Should have 4 args")
}

// Test helper functions

func TestSanitizeOrderBy(t *testing.T) {
	tests := []struct {
		name     string
		orderBy  string
		expected bool
	}{
		{"created_at", "created_at", true},
		{"starts_at", "starts_at", true},
		{"ends_at", "ends_at", true},
		{"updated_at", "updated_at", true},
		{"invalid field", "invalid_field", false},
		{"sql injection attempt", "created_at; DROP TABLE silences;", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeOrderBy(tt.orderBy)
			assert.Equal(t, tt.expected, result, "Sanitization result mismatch")
		})
	}
}

func TestEscapeJSONBValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no special chars",
			input:    "alertname",
			expected: "alertname",
		},
		{
			name:     "double quotes",
			input:    `test"value`,
			expected: `test\"value`,
		},
		{
			name:     "backslashes",
			input:    `test\value`,
			expected: `test\\value`,
		},
		{
			name:     "both quotes and backslashes",
			input:    `test\"value`,
			expected: `test\\\"value`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeJSONBValue(tt.input)
			assert.Equal(t, tt.expected, result, "Escape result mismatch")
		})
	}
}

func TestBuildJSONBContainmentFilter(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		expected string
	}{
		{
			name:     "simple name filter",
			field:    "name",
			value:    "alertname",
			expected: `[{"name":"alertname"}]`,
		},
		{
			name:     "simple value filter",
			field:    "value",
			value:    "HighCPU",
			expected: `[{"value":"HighCPU"}]`,
		},
		{
			name:     "value with quotes",
			field:    "value",
			value:    `test"value`,
			expected: `[{"value":"test\"value"}]`,
		},
		{
			name:     "value with backslash",
			field:    "value",
			value:    `test\value`,
			expected: `[{"value":"test\\value"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildJSONBContainmentFilter(tt.field, tt.value)
			assert.Equal(t, tt.expected, result, "JSONB filter mismatch")
		})
	}
}
