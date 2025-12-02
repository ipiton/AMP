package publishing

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPagerDutyAPIError_Error(t *testing.T) {
	err := &PagerDutyAPIError{
		StatusCode: 400,
		Message:    "Bad request",
		Errors:     []string{"Field 'summary' is required"},
	}

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
		{504, "server_error"},
		{999, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expectedType, func(t *testing.T) {
			err := &PagerDutyAPIError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expectedType, err.Type())
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"rate limit error", ErrRateLimitExceeded, true},
		{"timeout error", ErrAPITimeout, true},
		{"connection error", ErrAPIConnection, true},
		{"API error 429", &PagerDutyAPIError{StatusCode: 429}, true},
		{"API error 500", &PagerDutyAPIError{StatusCode: 500}, true},
		{"API error 400", &PagerDutyAPIError{StatusCode: 400}, false},
		{"random error", errors.New("random"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryable(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRateLimit(t *testing.T) {
	assert.True(t, IsRateLimit(ErrRateLimitExceeded))
	assert.True(t, IsRateLimit(&PagerDutyAPIError{StatusCode: 429}))
	assert.False(t, IsRateLimit(&PagerDutyAPIError{StatusCode: 400}))
	assert.False(t, IsRateLimit(nil))
}

func TestIsPagerDutyAuthError(t *testing.T) {
	assert.True(t, IsPagerDutyAuthError(&PagerDutyAPIError{StatusCode: 401}))
	assert.True(t, IsPagerDutyAuthError(&PagerDutyAPIError{StatusCode: 403}))
	assert.False(t, IsPagerDutyAuthError(&PagerDutyAPIError{StatusCode: 400}))
	assert.False(t, IsPagerDutyAuthError(nil))
}

func TestIsBadRequest(t *testing.T) {
	assert.True(t, IsBadRequest(ErrInvalidRequest))
	assert.True(t, IsBadRequest(&PagerDutyAPIError{StatusCode: 400}))
	assert.False(t, IsBadRequest(&PagerDutyAPIError{StatusCode: 500}))
	assert.False(t, IsBadRequest(nil))
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, IsNotFound(&PagerDutyAPIError{StatusCode: 404}))
	assert.False(t, IsNotFound(&PagerDutyAPIError{StatusCode: 400}))
	assert.False(t, IsNotFound(nil))
}

func TestIsServerError(t *testing.T) {
	assert.True(t, IsServerError(&PagerDutyAPIError{StatusCode: 500}))
	assert.True(t, IsServerError(&PagerDutyAPIError{StatusCode: 502}))
	assert.True(t, IsServerError(&PagerDutyAPIError{StatusCode: 503}))
	assert.False(t, IsServerError(&PagerDutyAPIError{StatusCode: 400}))
	assert.False(t, IsServerError(nil))
}

func TestIsTimeout(t *testing.T) {
	assert.True(t, IsTimeout(ErrAPITimeout))
	assert.False(t, IsTimeout(errors.New("random")))
	assert.False(t, IsTimeout(nil))
}

func TestIsConnectionError(t *testing.T) {
	assert.True(t, IsConnectionError(ErrAPIConnection))
	assert.False(t, IsConnectionError(errors.New("random")))
	assert.False(t, IsConnectionError(nil))
}
