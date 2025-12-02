package silencing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// Test helpers

// createTestSilence creates a valid test silence for testing purposes.
func createTestSilence() *silencing.Silence {
	return &silencing.Silence{
		CreatedBy: "test@example.com",
		Comment:   "Test silence for unit testing",
		StartsAt:  time.Now().Add(1 * time.Hour),
		EndsAt:    time.Now().Add(3 * time.Hour),
		Matchers: []silencing.Matcher{
			{
				Name:    "alertname",
				Value:   "TestAlert",
				Type:    silencing.MatcherTypeEqual,
				IsRegex: false,
			},
			{
				Name:    "severity",
				Value:   "critical",
				Type:    silencing.MatcherTypeEqual,
				IsRegex: false,
			},
		},
	}
}

// assertSilenceEqual asserts that two silences are equal (ignoring timestamps).
func assertSilenceEqual(t *testing.T, expected, actual *silencing.Silence) {
	assert.Equal(t, expected.ID, actual.ID, "ID mismatch")
	assert.Equal(t, expected.CreatedBy, actual.CreatedBy, "CreatedBy mismatch")
	assert.Equal(t, expected.Comment, actual.Comment, "Comment mismatch")
	assert.WithinDuration(t, expected.StartsAt, actual.StartsAt, time.Second, "StartsAt mismatch")
	assert.WithinDuration(t, expected.EndsAt, actual.EndsAt, time.Second, "EndsAt mismatch")
	assert.Equal(t, expected.Status, actual.Status, "Status mismatch")
	assert.Len(t, actual.Matchers, len(expected.Matchers), "Matchers count mismatch")
	for i, matcher := range expected.Matchers {
		assert.Equal(t, matcher.Name, actual.Matchers[i].Name, "Matcher name mismatch")
		assert.Equal(t, matcher.Value, actual.Matchers[i].Value, "Matcher value mismatch")
		assert.Equal(t, matcher.Type, actual.Matchers[i].Type, "Matcher type mismatch")
	}
}

// ===========================
// CreateSilence Tests (8)
// ===========================

// TestCreateSilence_Success tests successful silence creation.
// Note: This requires a real database connection (testcontainers in integration tests).
// For unit tests, we document the expected behavior.
func TestCreateSilence_Success(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Validate silence
	// 2. Generate UUID if empty
	// 3. Calculate status
	// 4. Marshal matchers to JSONB
	// 5. Insert into database
	// 6. Return silence with ID and CreatedAt
	// 7. Record metrics
	// 8. Log operation
}

// TestCreateSilence_ValidationError tests validation error handling.
func TestCreateSilence_ValidationError(t *testing.T) {
	tests := []struct {
		name          string
		modifySilence func(*silencing.Silence)
		expectedError string
	}{
		{
			name: "empty CreatedBy",
			modifySilence: func(s *silencing.Silence) {
				s.CreatedBy = ""
			},
			expectedError: "validation failed",
		},
		{
			name: "comment too short",
			modifySilence: func(s *silencing.Silence) {
				s.Comment = "ab"
			},
			expectedError: "validation failed",
		},
		{
			name: "invalid time range",
			modifySilence: func(s *silencing.Silence) {
				s.EndsAt = s.StartsAt.Add(-1 * time.Hour)
			},
			expectedError: "validation failed",
		},
		{
			name: "no matchers",
			modifySilence: func(s *silencing.Silence) {
				s.Matchers = []silencing.Matcher{}
			},
			expectedError: "validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Skip("Requires database connection - validation logic tested in core/silencing")

			// Expected behavior:
			// CreateSilence should return ErrValidation when silence.Validate() fails
		})
	}
}

// TestCreateSilence_DuplicateID tests duplicate ID error handling.
func TestCreateSilence_DuplicateID(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create silence with ID "test-id-1"
	// 2. Try to create another silence with same ID
	// 3. Should return ErrSilenceExists
	// 4. Metrics error counter should increment
}

// TestCreateSilence_EmptyID tests automatic UUID generation.
func TestCreateSilence_EmptyID(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create silence with empty ID
	// 2. Repository generates UUID automatically
	// 3. Returned silence should have valid UUID
	// 4. UUID should be parseable via uuid.Parse()
}

// TestCreateSilence_InvalidUUID tests invalid UUID error handling.
func TestCreateSilence_InvalidUUID(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create silence with invalid UUID (e.g., "not-a-uuid")
	// 2. Should return ErrInvalidUUID
	// 3. Metrics error counter should increment
}

// TestCreateSilence_MarshalError tests JSONB marshal error handling.
func TestCreateSilence_MarshalError(t *testing.T) {
	// This test is difficult to trigger without mocking json.Marshal
	// Matchers are simple structs that always marshal successfully
	t.Skip("Difficult to trigger without mocking - covered by integration tests")

	// Expected behavior:
	// If json.Marshal(matchers) fails:
	// 1. Should return error with "marshal matchers" prefix
	// 2. Metrics error counter should increment with error_type="marshal"
}

// TestCreateSilence_DatabaseError tests database connection error handling.
func TestCreateSilence_DatabaseError(t *testing.T) {
	t.Skip("Requires mock/testcontainers - see integration tests")

	// Expected behavior:
	// If database connection fails or query fails:
	// 1. Should return error with "insert silence" prefix
	// 2. Metrics error counter should increment with error_type="insert"
	// 3. No partial data should be committed (transaction rollback)
}

// TestCreateSilence_ContextCancelled tests context cancellation handling.
func TestCreateSilence_ContextCancelled(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create context with immediate cancellation
	// 2. Call CreateSilence with cancelled context
	// 3. Should return context.Canceled error
	// 4. No data should be inserted
}

// ===========================
// GetSilenceByID Tests (5)
// ===========================

// TestGetSilenceByID_Found tests successful silence retrieval.
func TestGetSilenceByID_Found(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create a test silence
	// 2. Retrieve it by ID
	// 3. Returned silence should match created silence
	// 4. All fields should be populated correctly
	// 5. Matchers should be unmarshaled from JSONB
}

// TestGetSilenceByID_NotFound tests silence not found error.
func TestGetSilenceByID_NotFound(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Generate random UUID that doesn't exist
	// 2. Call GetSilenceByID with non-existent UUID
	// 3. Should return ErrSilenceNotFound
	// 4. Metrics error counter should increment with error_type="not_found"
}

// TestGetSilenceByID_InvalidUUID tests invalid UUID error handling.
func TestGetSilenceByID_InvalidUUID(t *testing.T) {
	// This test can run without database
	// Create repository with nil pool (will panic on actual query, but we test validation first)
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	ctx := context.Background()

	tests := []struct {
		name string
		id   string
	}{
		{"empty string", ""},
		{"invalid format", "not-a-uuid"},
		{"partial uuid", "550e8400-e29b"},
		{"too long", "550e8400-e29b-41d4-a716-446655440000-extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repo.GetSilenceByID(ctx, tt.id)

			// Should return ErrInvalidUUID
			assert.ErrorIs(t, err, ErrInvalidUUID, "Expected ErrInvalidUUID")
			assert.Contains(t, err.Error(), "invalid UUID format", "Error message should mention UUID format")
		})
	}
}

// TestGetSilenceByID_DatabaseError tests database query error handling.
func TestGetSilenceByID_DatabaseError(t *testing.T) {
	t.Skip("Requires mock/testcontainers - see integration tests")

	// Expected behavior:
	// If database query fails:
	// 1. Should return error with "query silence" prefix
	// 2. Metrics error counter should increment with error_type="query"
}

// TestGetSilenceByID_UnmarshalError tests JSONB unmarshal error handling.
func TestGetSilenceByID_UnmarshalError(t *testing.T) {
	t.Skip("Requires mock/testcontainers with corrupt data - see integration tests")

	// Expected behavior:
	// If database has corrupt JSONB data:
	// 1. Should return error with "unmarshal matchers" prefix
	// 2. Metrics error counter should increment with error_type="unmarshal"
}

// ===========================
// UpdateSilence Tests (6)
// ===========================

// TestUpdateSilence_Success tests successful silence update.
func TestUpdateSilence_Success(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create a test silence
	// 2. Fetch it by ID
	// 3. Modify comment and ends_at
	// 4. Call UpdateSilence
	// 5. Should succeed
	// 6. UpdatedAt should be set
	// 7. Fetch again to verify changes
}

// TestUpdateSilence_NotFound tests update of non-existent silence.
func TestUpdateSilence_NotFound(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create silence with non-existent ID
	// 2. Call UpdateSilence
	// 3. Should return ErrSilenceNotFound
	// 4. Metrics error counter should increment with error_type="not_found"
}

// TestUpdateSilence_OptimisticLockConflict tests optimistic locking.
func TestUpdateSilence_OptimisticLockConflict(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior (optimistic locking flow):
	// 1. Create silence
	// 2. Fetch silence twice (silence1 and silence2, same UpdatedAt)
	// 3. Update silence1 → succeeds, UpdatedAt changes
	// 4. Update silence2 → fails with ErrSilenceConflict
	// 5. Fetch fresh data (silence3)
	// 6. Update silence3 → succeeds
}

// TestUpdateSilence_ValidationError tests validation error handling.
func TestUpdateSilence_ValidationError(t *testing.T) {
	t.Skip("Requires database connection - validation logic tested in core/silencing")

	// Expected behavior:
	// UpdateSilence should return ErrValidation when silence.Validate() fails
}

// TestUpdateSilence_DatabaseError tests database error handling.
func TestUpdateSilence_DatabaseError(t *testing.T) {
	t.Skip("Requires mock/testcontainers - see integration tests")

	// Expected behavior:
	// If database update fails:
	// 1. Should return error with "update silence" prefix
	// 2. Metrics error counter should increment with error_type="update"
}

// TestUpdateSilence_ContextCancelled tests context cancellation.
func TestUpdateSilence_ContextCancelled(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create context with immediate cancellation
	// 2. Call UpdateSilence with cancelled context
	// 3. Should return context.Canceled error
	// 4. No data should be updated
}

// ===========================
// DeleteSilence Tests (4)
// ===========================

// TestDeleteSilence_Success tests successful silence deletion.
func TestDeleteSilence_Success(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create a test silence
	// 2. Verify it exists (GetSilenceByID succeeds)
	// 3. Delete it
	// 4. Verify it no longer exists (GetSilenceByID returns ErrSilenceNotFound)
	// 5. Metrics active_silences gauge should decrement
}

// TestDeleteSilence_NotFound tests deletion of non-existent silence.
func TestDeleteSilence_NotFound(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Generate random UUID that doesn't exist
	// 2. Call DeleteSilence
	// 3. Should return ErrSilenceNotFound
	// 4. Metrics error counter should increment with error_type="not_found"
}

// TestDeleteSilence_InvalidUUID tests invalid UUID error handling.
func TestDeleteSilence_InvalidUUID(t *testing.T) {
	// This test can run without database
	repo := &PostgresSilenceRepository{
		logger:  slog.Default(),
		metrics: nil,
	}

	ctx := context.Background()

	tests := []struct {
		name string
		id   string
	}{
		{"empty string", ""},
		{"invalid format", "not-a-uuid"},
		{"partial uuid", "550e8400-e29b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.DeleteSilence(ctx, tt.id)

			// Should return ErrInvalidUUID
			assert.ErrorIs(t, err, ErrInvalidUUID, "Expected ErrInvalidUUID")
		})
	}
}

// TestDeleteSilence_DatabaseError tests database error handling.
func TestDeleteSilence_DatabaseError(t *testing.T) {
	t.Skip("Requires mock/testcontainers - see integration tests")

	// Expected behavior:
	// If database delete fails:
	// 1. Should return error with "delete silence" prefix
	// 2. Metrics error counter should increment with error_type="delete"
}

// ===========================
// Helper method tests
// ===========================

// TestSilenceExists tests the silenceExists helper method.
func TestSilenceExists(t *testing.T) {
	t.Skip("Requires database connection - see integration tests")

	// Expected behavior:
	// 1. Create silence → silenceExists returns true
	// 2. Delete silence → silenceExists returns false
	// 3. Random UUID → silenceExists returns false
}

// ===========================
// Metrics tests
// ===========================

// TestMetrics_Operations tests that operations are counted.
func TestMetrics_Operations(t *testing.T) {
	t.Skip("Requires integration test with prometheus registry inspection")

	// Expected behavior:
	// After each operation:
	// - Operations counter should increment
	// - OperationDuration histogram should record duration
	// - Errors counter should increment on error
	// - ActiveSilences gauge should update on create/delete
}

// TestMetrics_ActiveSilences tests the active silences gauge.
func TestMetrics_ActiveSilences(t *testing.T) {
	t.Skip("Requires integration test with prometheus registry inspection")

	// Expected behavior:
	// - Create silence → gauge increments by 1 for status
	// - Delete silence → gauge decrements by 1 for "deleted"
	// - Gauge should reflect current database state
}

// ===========================
// Filter validation tests
// ===========================

// TestSilenceFilter_Validate tests filter validation logic.
func TestSilenceFilter_Validate(t *testing.T) {
	tests := []struct {
		name        string
		filter      SilenceFilter
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid filter with defaults",
			filter:      SilenceFilter{},
			expectError: false,
		},
		{
			name: "valid filter with all fields",
			filter: SilenceFilter{
				Statuses:  []silencing.SilenceStatus{silencing.SilenceStatusActive},
				CreatedBy: "test@example.com",
				Limit:     100,
				Offset:    0,
				OrderBy:   "created_at",
				OrderDesc: true,
			},
			expectError: false,
		},
		{
			name: "negative limit",
			filter: SilenceFilter{
				Limit: -1,
			},
			expectError: true,
			errorMsg:    "limit must be >= 0",
		},
		{
			name: "limit too large",
			filter: SilenceFilter{
				Limit: 1001,
			},
			expectError: true,
			errorMsg:    "limit must be <= 1000",
		},
		{
			name: "negative offset",
			filter: SilenceFilter{
				Offset: -1,
			},
			expectError: true,
			errorMsg:    "offset must be >= 0",
		},
		{
			name: "invalid order_by field",
			filter: SilenceFilter{
				OrderBy: "invalid_field",
			},
			expectError: true,
			errorMsg:    "invalid order_by field",
		},
		{
			name: "valid order_by fields",
			filter: SilenceFilter{
				OrderBy: "starts_at",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()

			if tt.expectError {
				require.Error(t, err, "Expected validation error")
				assert.ErrorIs(t, err, ErrInvalidFilter, "Expected ErrInvalidFilter")
				assert.Contains(t, err.Error(), tt.errorMsg, "Error message mismatch")
			} else {
				assert.NoError(t, err, "Expected no validation error")
			}
		})
	}
}

// TestSilenceFilter_ApplyDefaults tests default value application.
func TestSilenceFilter_ApplyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		filter   SilenceFilter
		expected SilenceFilter
	}{
		{
			name:   "empty filter gets defaults",
			filter: SilenceFilter{},
			expected: SilenceFilter{
				Limit:   100,
				OrderBy: "created_at",
			},
		},
		{
			name: "custom values are preserved",
			filter: SilenceFilter{
				Limit:     50,
				OrderBy:   "starts_at",
				OrderDesc: true,
			},
			expected: SilenceFilter{
				Limit:     50,
				OrderBy:   "starts_at",
				OrderDesc: true,
			},
		},
		{
			name: "partial defaults",
			filter: SilenceFilter{
				Limit: 200,
			},
			expected: SilenceFilter{
				Limit:   200,
				OrderBy: "created_at",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := tt.filter
			filter.ApplyDefaults()

			assert.Equal(t, tt.expected.Limit, filter.Limit, "Limit mismatch")
			assert.Equal(t, tt.expected.OrderBy, filter.OrderBy, "OrderBy mismatch")
			assert.Equal(t, tt.expected.OrderDesc, filter.OrderDesc, "OrderDesc mismatch")
		})
	}
}

// TestUUID_Generation tests UUID generation and validation.
func TestUUID_Generation(t *testing.T) {
	// Test that empty ID triggers UUID generation
	silence := createTestSilence()
	assert.Empty(t, silence.ID, "Test silence should have empty ID initially")

	// After CreateSilence, ID should be populated
	// This is tested in integration tests with real database
	t.Skip("UUID generation tested in integration tests")
}

// TestUUID_Validation tests UUID format validation.
func TestUUID_Validation(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"valid uuid v4", uuid.New().String(), true},
		{"empty string", "", false},
		{"invalid format", "not-a-uuid", false},
		{"partial uuid", "550e8400-e29b-41d4", false},
		{"uuid v1", "550e8400-e29b-11d4-a716-446655440000", true}, // Still valid UUID
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := uuid.Parse(tt.id)

			if tt.valid {
				assert.NoError(t, err, "Expected valid UUID")
			} else {
				assert.Error(t, err, "Expected invalid UUID")
			}
		})
	}
}
