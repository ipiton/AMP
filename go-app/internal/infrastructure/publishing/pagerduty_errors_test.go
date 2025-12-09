package publishing

import (
	"errors"
	"testing"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
)

func TestPagerDutyAPIError_Error(t *testing.T) {
	err := NewPagerDutyAPIError(400, "Bad request", []string{"Field 'summary' is required"})

	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "Bad request")
}

func TestPagerDutyAPIError_Type(t *testing.T) {
	tests := []struct {
		statusCode   int
		expectedType string
	}{
		{400, "bad_request"},
		{401, "unauthorized"},
		{403, "forbidden"},
		{404, "not_found"},
		{429, "rate_limit"},
		{500, "server_error"},
		{502, "server_error"},
		{503, "server_error"},
		{504, "timeout"}, // 504 is now classified as timeout in httperror
		{999, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expectedType, func(t *testing.T) {
			err := NewPagerDutyAPIError(tt.statusCode, "test", nil)
			assert.Equal(t, tt.expectedType, err.Type())
		})
	}
}

func TestIsPagerDutyRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"rate limit error", ErrRateLimitExceeded, true},
		{"timeout error", ErrAPITimeout, true},
		{"connection error", ErrAPIConnection, true},
		{"API error 429", NewPagerDutyAPIError(429, "rate limited", nil), true},
		{"API error 500", NewPagerDutyAPIError(500, "server error", nil), true},
		{"API error 400", NewPagerDutyAPIError(400, "bad request", nil), false},
		{"random error", errors.New("random"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPagerDutyRetryable(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsPagerDutyRateLimit(t *testing.T) {
	assert.True(t, IsPagerDutyRateLimit(ErrRateLimitExceeded))
	assert.True(t, IsPagerDutyRateLimit(NewPagerDutyAPIError(429, "rate limit", nil)))
	assert.False(t, IsPagerDutyRateLimit(NewPagerDutyAPIError(400, "bad request", nil)))
	assert.False(t, IsPagerDutyRateLimit(nil))
}

func TestIsPagerDutyAuthError(t *testing.T) {
	assert.True(t, IsPagerDutyAuthError(NewPagerDutyAPIError(401, "unauthorized", nil)))
	assert.True(t, IsPagerDutyAuthError(NewPagerDutyAPIError(403, "forbidden", nil)))
	assert.False(t, IsPagerDutyAuthError(NewPagerDutyAPIError(400, "bad request", nil)))
	assert.False(t, IsPagerDutyAuthError(nil))
}

func TestIsPagerDutyBadRequest(t *testing.T) {
	assert.True(t, IsPagerDutyBadRequest(ErrInvalidRequest))
	assert.True(t, IsPagerDutyBadRequest(NewPagerDutyAPIError(400, "bad request", nil)))
	assert.False(t, IsPagerDutyBadRequest(NewPagerDutyAPIError(500, "server error", nil)))
	assert.False(t, IsPagerDutyBadRequest(nil))
}

func TestIsPagerDutyNotFound(t *testing.T) {
	assert.True(t, IsPagerDutyNotFound(NewPagerDutyAPIError(404, "not found", nil)))
	assert.False(t, IsPagerDutyNotFound(NewPagerDutyAPIError(400, "bad request", nil)))
	assert.False(t, IsPagerDutyNotFound(nil))
}

func TestIsPagerDutyServerError(t *testing.T) {
	assert.True(t, IsPagerDutyServerError(NewPagerDutyAPIError(500, "server error", nil)))
	assert.True(t, IsPagerDutyServerError(NewPagerDutyAPIError(502, "bad gateway", nil)))
	assert.True(t, IsPagerDutyServerError(NewPagerDutyAPIError(503, "service unavailable", nil)))
	assert.False(t, IsPagerDutyServerError(NewPagerDutyAPIError(400, "bad request", nil)))
	assert.False(t, IsPagerDutyServerError(nil))
}

func TestIsPagerDutyTimeout(t *testing.T) {
	assert.True(t, IsPagerDutyTimeout(ErrAPITimeout))
	assert.False(t, IsPagerDutyTimeout(errors.New("random")))
	assert.False(t, IsPagerDutyTimeout(nil))
}

func TestIsPagerDutyConnectionError(t *testing.T) {
	assert.True(t, IsPagerDutyConnectionError(ErrAPIConnection))
	assert.False(t, IsPagerDutyConnectionError(errors.New("random")))
	assert.False(t, IsPagerDutyConnectionError(nil))
}

// Test the unified IsPublishing* functions work correctly with PagerDuty errors
func TestUnifiedPublishingFunctions_WithPagerDuty(t *testing.T) {
	pdError := NewPagerDutyAPIError(429, "rate limited", nil)

	assert.True(t, IsPublishingRetryable(pdError))
	assert.True(t, IsPublishingRateLimit(pdError))
	assert.False(t, IsPublishingAuthError(pdError))

	authError := NewPagerDutyAPIError(401, "unauthorized", nil)
	assert.True(t, IsPublishingAuthError(authError))
	assert.False(t, IsPublishingRetryable(authError))
}

// Test that httperror functions also work with PagerDuty errors
func TestHTTPErrorFunctions_WithPagerDuty(t *testing.T) {
	pdError := NewPagerDutyAPIError(429, "rate limited", nil)

	assert.True(t, httperror.IsRateLimit(pdError))
	assert.True(t, httperror.IsRetryable(pdError))

	serverError := NewPagerDutyAPIError(500, "server error", nil)
	assert.True(t, httperror.IsServerError(serverError))
	assert.True(t, httperror.IsRetryable(serverError))
}
