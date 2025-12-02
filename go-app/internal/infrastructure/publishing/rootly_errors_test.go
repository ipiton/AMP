package publishing

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRootlyAPIError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *RootlyAPIError
		expected string
	}{
		{
			name: "With source",
			err: &RootlyAPIError{
				StatusCode: 400,
				Title:      "Bad Request",
				Detail:     "Missing required field",
				Source:     "/data/attributes/title",
			},
			expected: "Rootly API error 400: Bad Request - Missing required field (field: /data/attributes/title)",
		},
		{
			name: "Without source",
			err: &RootlyAPIError{
				StatusCode: 500,
				Title:      "Internal Server Error",
				Detail:     "Server error occurred",
			},
			expected: "Rootly API error 500: Internal Server Error - Server error occurred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsValidation())
		})
	}
}

func TestRootlyAPIError_IsAuth(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"Unauthorized", http.StatusUnauthorized, true},
		{"Forbidden", http.StatusForbidden, false},
		{"Bad Request", http.StatusBadRequest, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &RootlyAPIError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsAuth())
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsConflict())
		})
	}
}

func TestIsRootlyAPIError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Is RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 400},
			expected: true,
		},
		{
			name:     "Is not RootlyAPIError",
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
			result := IsRootlyAPIError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Retryable RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 429},
			expected: true,
		},
		{
			name:     "Non-retryable RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 400},
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
			result := IsRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRootlyAPIError_ErrorClassification(t *testing.T) {
	// Test comprehensive error classification
	err := &RootlyAPIError{
		StatusCode: http.StatusTooManyRequests,
		Title:      "Rate limit exceeded",
		Detail:     "You have exceeded the rate limit",
	}

	assert.True(t, err.IsRateLimit())
	assert.True(t, err.IsRetryable())
	assert.False(t, err.IsAuth())
	assert.False(t, err.IsValidation())
	assert.False(t, err.IsNotFound())
	assert.False(t, err.IsConflict())
}

func BenchmarkRootlyAPIError_ErrorMethod(b *testing.B) {
	err := &RootlyAPIError{
		StatusCode: 400,
		Title:      "Bad Request",
		Detail:     "Invalid field",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = err.Error()
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "NotFound RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 404},
			expected: true,
		},
		{
			name:     "Other RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 500},
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
			result := IsNotFoundError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Conflict RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 409},
			expected: true,
		},
		{
			name:     "Other RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 400},
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
			result := IsConflictError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Auth RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 401},
			expected: true,
		},
		{
			name:     "Other RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 404},
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
			result := IsAuthError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "RateLimit RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 429},
			expected: true,
		},
		{
			name:     "Other RootlyAPIError",
			err:      &RootlyAPIError{StatusCode: 500},
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
			result := IsRateLimitError(tt.err)
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsForbidden())
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
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
			err := &RootlyAPIError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsClientError())
		})
	}
}

func BenchmarkRootlyAPIError_IsRetryable(b *testing.B) {
	err := &RootlyAPIError{
		StatusCode: http.StatusTooManyRequests,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = err.IsRetryable()
	}
}
