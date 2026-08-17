package publishing

import (
	"errors"
	"testing"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
)

// newPagerDutyTestError builds a PagerDuty-flavored HTTPAPIError for tests.
func newPagerDutyTestError(statusCode int, message string, details []string) *httperror.HTTPAPIError {
	return httperror.NewHTTPErrorWithDetails(statusCode, message, ProviderPagerDuty, details)
}

func TestPagerDutyAPIError_Error(t *testing.T) {
	err := newPagerDutyTestError(400, "Bad request", []string{"Field 'summary' is required"})

	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "Bad request")
}

func TestPagerDutyAPIError_Type(t *testing.T) {
	tests := []struct {
		statusCode   int
		expectedType string
	}{
		{400, "bad_request"},
		{401, "auth_error"},
		{403, "auth_error"},
		{404, "not_found"},
		{429, "rate_limit"},
		{500, "server_error"},
		{502, "server_error"},
		{503, "server_error"},
		{504, "timeout"}, // 504 is classified as timeout in httperror
		{999, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expectedType, func(t *testing.T) {
			err := newPagerDutyTestError(tt.statusCode, "test", nil)
			assert.Equal(t, tt.expectedType, err.Type())
		})
	}
}

func TestPagerDutyRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"API error 429", newPagerDutyTestError(429, "rate limited", nil), true},
		{"API error 500", newPagerDutyTestError(500, "server error", nil), true},
		{"API error 400", newPagerDutyTestError(400, "bad request", nil), false},
		{"random error", errors.New("random"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httperror.IsRetryable(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPagerDutyRateLimit(t *testing.T) {
	assert.True(t, httperror.IsRateLimit(newPagerDutyTestError(429, "rate limit", nil)))
	assert.False(t, httperror.IsRateLimit(newPagerDutyTestError(400, "bad request", nil)))
	assert.False(t, httperror.IsRateLimit(nil))
}

func TestPagerDutyAuthError(t *testing.T) {
	assert.True(t, httperror.IsAuthError(newPagerDutyTestError(401, "unauthorized", nil)))
	assert.True(t, httperror.IsAuthError(newPagerDutyTestError(403, "forbidden", nil)))
	assert.False(t, httperror.IsAuthError(newPagerDutyTestError(400, "bad request", nil)))
	assert.False(t, httperror.IsAuthError(nil))
}

func TestPagerDutyBadRequest(t *testing.T) {
	assert.True(t, newPagerDutyTestError(400, "bad request", nil).IsBadRequest())
	assert.False(t, newPagerDutyTestError(500, "server error", nil).IsBadRequest())
}

func TestPagerDutyNotFound(t *testing.T) {
	assert.True(t, httperror.IsNotFound(newPagerDutyTestError(404, "not found", nil)))
	assert.False(t, httperror.IsNotFound(newPagerDutyTestError(400, "bad request", nil)))
	assert.False(t, httperror.IsNotFound(nil))
}

func TestPagerDutyServerError(t *testing.T) {
	assert.True(t, httperror.IsServerError(newPagerDutyTestError(500, "server error", nil)))
	assert.True(t, httperror.IsServerError(newPagerDutyTestError(502, "bad gateway", nil)))
	assert.True(t, httperror.IsServerError(newPagerDutyTestError(503, "service unavailable", nil)))
	assert.False(t, httperror.IsServerError(newPagerDutyTestError(400, "bad request", nil)))
	assert.False(t, httperror.IsServerError(nil))
}

func TestPagerDutyTimeout(t *testing.T) {
	assert.True(t, httperror.IsTimeout(newPagerDutyTestError(504, "gateway timeout", nil)))
	assert.False(t, httperror.IsTimeout(errors.New("random")))
	assert.False(t, httperror.IsTimeout(nil))
}

// Test the unified IsPublishing* functions work correctly with PagerDuty errors
func TestUnifiedPublishingFunctions_WithPagerDuty(t *testing.T) {
	pdError := newPagerDutyTestError(429, "rate limited", nil)

	assert.True(t, IsPublishingRetryable(pdError))
	assert.True(t, IsPublishingRateLimit(pdError))
	assert.False(t, IsPublishingAuthError(pdError))

	authError := newPagerDutyTestError(401, "unauthorized", nil)
	assert.True(t, IsPublishingAuthError(authError))
	assert.False(t, IsPublishingRetryable(authError))
}

// Test that httperror functions also work with PagerDuty errors
func TestHTTPErrorFunctions_WithPagerDuty(t *testing.T) {
	pdError := newPagerDutyTestError(429, "rate limited", nil)

	assert.True(t, httperror.IsRateLimit(pdError))
	assert.True(t, httperror.IsRetryable(pdError))

	serverError := newPagerDutyTestError(500, "server error", nil)
	assert.True(t, httperror.IsServerError(serverError))
	assert.True(t, httperror.IsRetryable(serverError))
}
