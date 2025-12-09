package httperror

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestHTTPAPIError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *HTTPAPIError
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "nil HTTPAPIError",
		},
		{
			name: "basic error",
			err: &HTTPAPIError{
				StatusCode: 400,
				Message:    "bad request",
				Provider:   "slack",
			},
			expected: "slack API error 400: bad request",
		},
		{
			name: "error with retry-after",
			err: &HTTPAPIError{
				StatusCode: 429,
				Message:    "rate limit exceeded",
				Provider:   "pagerduty",
				RetryAfter: 60,
			},
			expected: "pagerduty API error 429: rate limit exceeded (retry after 60s)",
		},
		{
			name: "error with details",
			err: &HTTPAPIError{
				StatusCode: 422,
				Message:    "validation failed",
				Provider:   "rootly",
				Details:    []string{"field1 is required", "field2 is invalid"},
			},
			expected: "rootly API error 422: validation failed (details: [field1 is required field2 is invalid])",
		},
		{
			name: "empty provider",
			err: &HTTPAPIError{
				StatusCode: 500,
				Message:    "server error",
			},
			expected: "unknown API error 500: server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()
			if result != tt.expected {
				t.Errorf("Error() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestHTTPAPIError_Unwrap(t *testing.T) {
	cause := errors.New("underlying error")

	tests := []struct {
		name     string
		err      *HTTPAPIError
		expected error
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: nil,
		},
		{
			name: "no cause",
			err: &HTTPAPIError{
				StatusCode: 400,
				Message:    "bad request",
				Provider:   "slack",
			},
			expected: nil,
		},
		{
			name: "with cause",
			err: &HTTPAPIError{
				StatusCode: 500,
				Message:    "server error",
				Provider:   "slack",
				Cause:      cause,
			},
			expected: cause,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Unwrap()
			if result != tt.expected {
				t.Errorf("Unwrap() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHTTPAPIError_Classification(t *testing.T) {
	tests := []struct {
		name         string
		err          *HTTPAPIError
		isRetryable  bool
		isRateLimit  bool
		isServerErr  bool
		isClientErr  bool
		isAuthErr    bool
		isNotFound   bool
		isTimeout    bool
		isBadRequest bool
		isConflict   bool
		isValidation bool
		errType      string
	}{
		{
			name:        "nil error",
			err:         nil,
			isRetryable: false,
			errType:     "unknown",
		},
		{
			name: "rate limit (429)",
			err: &HTTPAPIError{
				StatusCode: 429,
				Message:    "rate limit",
				Provider:   "slack",
			},
			isRetryable: true,
			isRateLimit: true,
			isClientErr: true,
			errType:     "rate_limit",
		},
		{
			name: "server error (500)",
			err: &HTTPAPIError{
				StatusCode: 500,
				Message:    "internal error",
				Provider:   "pagerduty",
			},
			isRetryable: true,
			isServerErr: true,
			errType:     "server_error",
		},
		{
			name: "bad gateway (502)",
			err: &HTTPAPIError{
				StatusCode: 502,
				Message:    "bad gateway",
				Provider:   "rootly",
			},
			isRetryable: true,
			isServerErr: true,
			errType:     "server_error",
		},
		{
			name: "unauthorized (401)",
			err: &HTTPAPIError{
				StatusCode: 401,
				Message:    "unauthorized",
				Provider:   "webhook",
			},
			isRetryable: false,
			isClientErr: true,
			isAuthErr:   true,
			errType:     "auth_error",
		},
		{
			name: "forbidden (403)",
			err: &HTTPAPIError{
				StatusCode: 403,
				Message:    "forbidden",
				Provider:   "slack",
			},
			isRetryable: false,
			isClientErr: true,
			isAuthErr:   true,
			errType:     "auth_error",
		},
		{
			name: "not found (404)",
			err: &HTTPAPIError{
				StatusCode: 404,
				Message:    "not found",
				Provider:   "pagerduty",
			},
			isRetryable: false,
			isClientErr: true,
			isNotFound:  true,
			errType:     "not_found",
		},
		{
			name: "request timeout (408)",
			err: &HTTPAPIError{
				StatusCode: 408,
				Message:    "timeout",
				Provider:   "rootly",
			},
			isRetryable: false,
			isClientErr: true,
			isTimeout:   true,
			errType:     "timeout",
		},
		{
			name: "gateway timeout (504)",
			err: &HTTPAPIError{
				StatusCode: 504,
				Message:    "gateway timeout",
				Provider:   "webhook",
			},
			isRetryable: true,
			isServerErr: true,
			isTimeout:   true,
			errType:     "timeout",
		},
		{
			name: "bad request (400)",
			err: &HTTPAPIError{
				StatusCode: 400,
				Message:    "bad request",
				Provider:   "slack",
			},
			isRetryable:  false,
			isClientErr:  true,
			isBadRequest: true,
			errType:      "bad_request",
		},
		{
			name: "conflict (409)",
			err: &HTTPAPIError{
				StatusCode: 409,
				Message:    "conflict",
				Provider:   "pagerduty",
			},
			isRetryable: false,
			isClientErr: true,
			isConflict:  true,
			errType:     "conflict",
		},
		{
			name: "validation error (422)",
			err: &HTTPAPIError{
				StatusCode: 422,
				Message:    "validation failed",
				Provider:   "rootly",
			},
			isRetryable:  false,
			isClientErr:  true,
			isValidation: true,
			errType:      "validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsRetryable(); got != tt.isRetryable {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.isRetryable)
			}
			if got := tt.err.IsRateLimit(); got != tt.isRateLimit {
				t.Errorf("IsRateLimit() = %v, want %v", got, tt.isRateLimit)
			}
			if got := tt.err.IsServerError(); got != tt.isServerErr {
				t.Errorf("IsServerError() = %v, want %v", got, tt.isServerErr)
			}
			if got := tt.err.IsClientError(); got != tt.isClientErr {
				t.Errorf("IsClientError() = %v, want %v", got, tt.isClientErr)
			}
			if got := tt.err.IsAuthError(); got != tt.isAuthErr {
				t.Errorf("IsAuthError() = %v, want %v", got, tt.isAuthErr)
			}
			if got := tt.err.IsNotFound(); got != tt.isNotFound {
				t.Errorf("IsNotFound() = %v, want %v", got, tt.isNotFound)
			}
			if got := tt.err.IsTimeout(); got != tt.isTimeout {
				t.Errorf("IsTimeout() = %v, want %v", got, tt.isTimeout)
			}
			if got := tt.err.IsBadRequest(); got != tt.isBadRequest {
				t.Errorf("IsBadRequest() = %v, want %v", got, tt.isBadRequest)
			}
			if got := tt.err.IsConflict(); got != tt.isConflict {
				t.Errorf("IsConflict() = %v, want %v", got, tt.isConflict)
			}
			if got := tt.err.IsValidation(); got != tt.isValidation {
				t.Errorf("IsValidation() = %v, want %v", got, tt.isValidation)
			}
			if got := tt.err.Type(); got != tt.errType {
				t.Errorf("Type() = %v, want %v", got, tt.errType)
			}
		})
	}
}

func TestHTTPAPIError_Is(t *testing.T) {
	err1 := &HTTPAPIError{StatusCode: 429, Provider: "slack"}
	err2 := &HTTPAPIError{StatusCode: 429, Provider: "slack"}
	err3 := &HTTPAPIError{StatusCode: 429, Provider: "pagerduty"}
	err4 := &HTTPAPIError{StatusCode: 500, Provider: "slack"}

	tests := []struct {
		name     string
		err      *HTTPAPIError
		target   error
		expected bool
	}{
		{
			name:     "same status and provider",
			err:      err1,
			target:   err2,
			expected: true,
		},
		{
			name:     "different provider",
			err:      err1,
			target:   err3,
			expected: false,
		},
		{
			name:     "different status",
			err:      err1,
			target:   err4,
			expected: false,
		},
		{
			name:     "non-HTTPAPIError target",
			err:      err1,
			target:   errors.New("other error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			target:   err1,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Is(tt.target); got != tt.expected {
				t.Errorf("Is() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFactoryFunctions(t *testing.T) {
	t.Run("NewHTTPError", func(t *testing.T) {
		err := NewHTTPError(400, "bad request", "slack")
		if err.StatusCode != 400 {
			t.Errorf("StatusCode = %d, want 400", err.StatusCode)
		}
		if err.Message != "bad request" {
			t.Errorf("Message = %q, want %q", err.Message, "bad request")
		}
		if err.Provider != "slack" {
			t.Errorf("Provider = %q, want %q", err.Provider, "slack")
		}
	})

	t.Run("NewRateLimitError", func(t *testing.T) {
		err := NewRateLimitError("pagerduty", 60)
		if err.StatusCode != http.StatusTooManyRequests {
			t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusTooManyRequests)
		}
		if err.RetryAfter != 60 {
			t.Errorf("RetryAfter = %d, want 60", err.RetryAfter)
		}
		if !err.IsRateLimit() {
			t.Error("IsRateLimit() = false, want true")
		}
	})

	t.Run("NewTimeoutError", func(t *testing.T) {
		cause := errors.New("context deadline exceeded")
		err := NewTimeoutError("rootly", cause)
		if err.StatusCode != http.StatusGatewayTimeout {
			t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusGatewayTimeout)
		}
		if !errors.Is(err.Unwrap(), cause) {
			t.Error("Unwrap() should return cause")
		}
	})

	t.Run("NewAuthError", func(t *testing.T) {
		err := NewAuthError("webhook")
		if err.StatusCode != http.StatusUnauthorized {
			t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusUnauthorized)
		}
		if !err.IsAuthError() {
			t.Error("IsAuthError() = false, want true")
		}
	})

	t.Run("NewNotFoundError", func(t *testing.T) {
		err := NewNotFoundError("slack", "channel")
		if err.StatusCode != http.StatusNotFound {
			t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusNotFound)
		}
		if err.Message != "channel not found" {
			t.Errorf("Message = %q, want %q", err.Message, "channel not found")
		}
	})

	t.Run("NewValidationError", func(t *testing.T) {
		details := []string{"field1 required", "field2 invalid"}
		err := NewValidationError("pagerduty", details)
		if err.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusUnprocessableEntity)
		}
		if len(err.Details) != 2 {
			t.Errorf("Details len = %d, want 2", len(err.Details))
		}
	})

	t.Run("NewServiceUnavailableError", func(t *testing.T) {
		err := NewServiceUnavailableError("rootly", 30)
		if err.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusServiceUnavailable)
		}
		if err.RetryAfter != 30 {
			t.Errorf("RetryAfter = %d, want 30", err.RetryAfter)
		}
		if !err.IsRetryable() {
			t.Error("IsRetryable() = false, want true")
		}
	})
}

func TestStandaloneFunctions(t *testing.T) {
	t.Run("IsRetryable", func(t *testing.T) {
		tests := []struct {
			name     string
			err      error
			expected bool
		}{
			{
				name:     "nil",
				err:      nil,
				expected: false,
			},
			{
				name:     "rate limit error",
				err:      NewRateLimitError("slack", 60),
				expected: true,
			},
			{
				name:     "server error",
				err:      NewServerError("pagerduty", nil),
				expected: true,
			},
			{
				name:     "auth error",
				err:      NewAuthError("rootly"),
				expected: false,
			},
			{
				name:     "wrapped retryable",
				err:      fmt.Errorf("wrapped: %w", NewRateLimitError("webhook", 30)),
				expected: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := IsRetryable(tt.err); got != tt.expected {
					t.Errorf("IsRetryable() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("GetRetryAfter", func(t *testing.T) {
		tests := []struct {
			name     string
			err      error
			expected int
		}{
			{
				name:     "nil",
				err:      nil,
				expected: 0,
			},
			{
				name:     "rate limit with retry-after",
				err:      NewRateLimitError("slack", 60),
				expected: 60,
			},
			{
				name:     "error without retry-after",
				err:      NewAuthError("pagerduty"),
				expected: 0,
			},
			{
				name:     "wrapped",
				err:      fmt.Errorf("wrapped: %w", NewServiceUnavailableError("rootly", 120)),
				expected: 120,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := GetRetryAfter(tt.err); got != tt.expected {
					t.Errorf("GetRetryAfter() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("GetProvider", func(t *testing.T) {
		tests := []struct {
			name     string
			err      error
			expected string
		}{
			{
				name:     "nil",
				err:      nil,
				expected: "unknown",
			},
			{
				name:     "slack error",
				err:      NewHTTPError(500, "error", "slack"),
				expected: "slack",
			},
			{
				name:     "non-http error",
				err:      errors.New("other"),
				expected: "unknown",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := GetProvider(tt.err); got != tt.expected {
					t.Errorf("GetProvider() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("GetErrorType", func(t *testing.T) {
		tests := []struct {
			name     string
			err      error
			expected string
		}{
			{
				name:     "nil",
				err:      nil,
				expected: "unknown",
			},
			{
				name:     "rate limit",
				err:      NewRateLimitError("slack", 60),
				expected: "rate_limit",
			},
			{
				name:     "server error",
				err:      NewServerError("pagerduty", nil),
				expected: "server_error",
			},
			{
				name:     "auth error",
				err:      NewAuthError("rootly"),
				expected: "auth_error",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := GetErrorType(tt.err); got != tt.expected {
					t.Errorf("GetErrorType() = %v, want %v", got, tt.expected)
				}
			})
		}
	})
}

func TestErrorsAsIntegration(t *testing.T) {
	// Test that errors.As works correctly with wrapped errors
	original := NewRateLimitError("slack", 60)
	wrapped := fmt.Errorf("failed to send: %w", original)
	doubleWrapped := fmt.Errorf("publishing error: %w", wrapped)

	var httpErr *HTTPAPIError

	// Should work with single wrap
	if !errors.As(wrapped, &httpErr) {
		t.Error("errors.As failed with wrapped error")
	}
	if httpErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", httpErr.StatusCode)
	}

	// Should work with double wrap
	httpErr = nil
	if !errors.As(doubleWrapped, &httpErr) {
		t.Error("errors.As failed with double wrapped error")
	}
	if httpErr.Provider != "slack" {
		t.Errorf("Provider = %q, want %q", httpErr.Provider, "slack")
	}
}
