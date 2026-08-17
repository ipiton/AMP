package publishing

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
)

// newRootlyTestError builds a Rootly-flavored HTTPAPIError for tests.
func newRootlyTestError(statusCode int, message string) *httperror.HTTPAPIError {
	return httperror.NewHTTPError(statusCode, message, ProviderRootly)
}

func TestRootlyAPIError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *httperror.HTTPAPIError
	}{
		{
			name: "With message",
			err:  newRootlyTestError(400, "Bad Request - Missing field"),
		},
		{
			name: "Server error",
			err:  newRootlyTestError(500, "Internal Server Error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, tt.err.Error(), "rootly")
		})
	}
}

func TestRootlyAPIError_IsRetryable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Too Many Requests", http.StatusTooManyRequests, true},
		{"Service Unavailable", http.StatusServiceUnavailable, true},
		{"Gateway Timeout", http.StatusGatewayTimeout, true},
		{"Internal Server Error", http.StatusInternalServerError, true},
		{"Bad Request", http.StatusBadRequest, false},
		{"Unauthorized", http.StatusUnauthorized, false},
		{"Not Found", http.StatusNotFound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsRetryable())
		})
	}
}

func TestRootlyAPIError_IsRateLimit(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Rate Limit", http.StatusTooManyRequests, true},
		{"Bad Request", http.StatusBadRequest, false},
		{"Server Error", http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsRateLimit())
		})
	}
}

func TestRootlyAPIError_IsValidation(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Unprocessable Entity", http.StatusUnprocessableEntity, true},
		{"Not Found", http.StatusNotFound, false},
		{"Server Error", http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsValidation())
		})
	}
}

func TestRootlyAPIError_IsAuthError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Unauthorized", http.StatusUnauthorized, true},
		{"Forbidden", http.StatusForbidden, true}, // HTTPAPIError.IsAuthError includes 403
		{"Bad Request", http.StatusBadRequest, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			// Use IsAuthError method from httperror
			assert.Equal(t, tt.expected, err.IsAuthError())
		})
	}
}

func TestRootlyAPIError_IsNotFound(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Not Found", http.StatusNotFound, true},
		{"Bad Request", http.StatusBadRequest, false},
		{"Unauthorized", http.StatusUnauthorized, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsNotFound())
		})
	}
}

func TestRootlyAPIError_IsConflict(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Conflict", http.StatusConflict, true},
		{"Bad Request", http.StatusBadRequest, false},
		{"Not Found", http.StatusNotFound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsConflict())
		})
	}
}

func TestRootlyProviderDetection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Is Rootly error",
			err:      newRootlyTestError(400, "test"),
			expected: true,
		},
		{
			name:     "Is not Rootly error",
			err:      errors.New("generic error"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httperror.GetProvider(tt.err) == ProviderRootly
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Retryable Rootly error",
			err:      newRootlyTestError(429, "rate limit"),
			expected: true,
		},
		{
			name:     "Non-retryable Rootly error",
			err:      newRootlyTestError(400, "bad request"),
			expected: false,
		},
		{
			name:     "Generic error",
			err:      errors.New("generic error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httperror.IsRetryable(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyAPIError_ErrorClassification(t *testing.T) {
	// Test comprehensive error classification
	err := newRootlyTestError(http.StatusTooManyRequests, "Rate limit exceeded")

	assert.True(t, err.IsRateLimit())
	assert.True(t, err.IsRetryable())
	assert.False(t, err.IsAuthError())
	assert.False(t, err.IsValidation())
	assert.False(t, err.IsNotFound())
	assert.False(t, err.IsConflict())
}

func BenchmarkRootlyAPIError_ErrorMethod(b *testing.B) {
	err := newRootlyTestError(400, "Bad Request")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = err.Error()
	}
}

func TestRootlyNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "NotFound Rootly error",
			err:      newRootlyTestError(404, "not found"),
			expected: true,
		},
		{
			name:     "Other Rootly error",
			err:      newRootlyTestError(500, "server error"),
			expected: false,
		},
		{
			name:     "Generic error",
			err:      errors.New("generic"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httperror.IsNotFound(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Conflict Rootly error",
			err:      newRootlyTestError(409, "conflict"),
			expected: true,
		},
		{
			name:     "Other Rootly error",
			err:      newRootlyTestError(400, "bad request"),
			expected: false,
		},
		{
			name:     "Generic error",
			err:      errors.New("generic"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpErr := AsPublishingError(tt.err)
			result := httpErr != nil && httpErr.IsConflict()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyAuthError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Auth Rootly error",
			err:      newRootlyTestError(401, "unauthorized"),
			expected: true,
		},
		{
			name:     "Other Rootly error",
			err:      newRootlyTestError(404, "not found"),
			expected: false,
		},
		{
			name:     "Generic error",
			err:      errors.New("generic"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httperror.IsAuthError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "RateLimit Rootly error",
			err:      newRootlyTestError(429, "rate limit"),
			expected: true,
		},
		{
			name:     "Other Rootly error",
			err:      newRootlyTestError(500, "server error"),
			expected: false,
		},
		{
			name:     "Generic error",
			err:      errors.New("generic"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := httperror.IsRateLimit(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyAPIError_IsForbidden(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Forbidden", http.StatusForbidden, true},
		{"Unauthorized", http.StatusUnauthorized, false},
		{"Not Found", http.StatusNotFound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			// HTTPAPIError doesn't have IsForbidden method directly,
			// check via status code
			assert.Equal(t, tt.expected, err.StatusCode == http.StatusForbidden)
		})
	}
}

func TestRootlyAPIError_IsBadRequest(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Bad Request", http.StatusBadRequest, true},
		{"Unprocessable Entity", http.StatusUnprocessableEntity, false},
		{"Internal Server Error", http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsBadRequest())
		})
	}
}

func TestRootlyAPIError_IsServerError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Internal Server Error", http.StatusInternalServerError, true},
		{"Bad Gateway", http.StatusBadGateway, true},
		{"Service Unavailable", http.StatusServiceUnavailable, true},
		{"Bad Request", http.StatusBadRequest, false},
		{"Not Found", http.StatusNotFound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsServerError())
		})
	}
}

func TestRootlyAPIError_IsClientError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Bad Request", http.StatusBadRequest, true},
		{"Unauthorized", http.StatusUnauthorized, true},
		{"Not Found", http.StatusNotFound, true},
		{"Internal Server Error", http.StatusInternalServerError, false},
		{"Success", http.StatusOK, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newRootlyTestError(tt.statusCode, "test")
			assert.Equal(t, tt.expected, err.IsClientError())
		})
	}
}

func BenchmarkRootlyAPIError_IsRetryable(b *testing.B) {
	err := newRootlyTestError(http.StatusTooManyRequests, "rate limit")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = err.IsRetryable()
	}
}

// Test the unified publishing functions with Rootly errors
func TestUnifiedPublishingFunctions_WithRootly(t *testing.T) {
	rootlyError := newRootlyTestError(429, "rate limited")

	assert.True(t, IsPublishingRetryable(rootlyError))
	assert.True(t, IsPublishingRateLimit(rootlyError))
	assert.False(t, IsPublishingAuthError(rootlyError))

	authError := newRootlyTestError(401, "unauthorized")
	assert.True(t, IsPublishingAuthError(authError))
	assert.False(t, IsPublishingRetryable(authError))
}

// Test that httperror functions work with Rootly errors
func TestHTTPErrorFunctions_WithRootly(t *testing.T) {
	rootlyError := newRootlyTestError(429, "rate limited")

	assert.True(t, httperror.IsRateLimit(rootlyError))
	assert.True(t, httperror.IsRetryable(rootlyError))

	serverError := newRootlyTestError(500, "server error")
	assert.True(t, httperror.IsServerError(serverError))
	assert.True(t, httperror.IsRetryable(serverError))
}
