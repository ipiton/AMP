package publishing

import (
	"errors"
	"testing"
)

// ==================== WebhookError Tests ====================

func TestWebhookError_Creation(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeValidation,
		Message: "invalid URL",
		Cause:   errors.New("url parse error"),
	}

	if err.Type != ErrorTypeValidation {
		t.Errorf("Expected type=validation, got %v", err.Type)
	}
	if err.Message != "invalid URL" {
		t.Errorf("Expected message='invalid URL', got %s", err.Message)
	}
	if err.Cause == nil {
		t.Error("Expected wrapped error, got nil")
	}
}

func TestWebhookError_Error(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeAuth,
		Message: "authentication failed",
		Cause:   errors.New("invalid token"),
	}

	// Actual format: "[auth] authentication failed"
	expected := "[auth] authentication failed"
	if err.Error() != expected {
		t.Errorf("Expected error=%s, got %s", expected, err.Error())
	}
}

func TestWebhookError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	err := &WebhookError{
		Type:    ErrorTypeNetwork,
		Message: "network failure",
		Cause:   innerErr,
	}

	unwrapped := errors.Unwrap(err)
	if unwrapped != innerErr {
		t.Error("Failed to unwrap inner error")
	}
}

// ==================== ErrorType Tests ====================

func TestErrorType_String(t *testing.T) {
	tests := []struct {
		errorType ErrorType
		expected  string
	}{
		{ErrorTypeValidation, "validation"},
		{ErrorTypeAuth, "auth"},
		{ErrorTypeNetwork, "network"},
		{ErrorTypeTimeout, "timeout"},
		{ErrorTypeRateLimit, "rate_limit"},
		{ErrorTypeServer, "server"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.errorType.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.errorType.String())
			}
		})
	}
}

// ==================== Sentinel Errors Tests ====================

func TestSentinelErrors_EmptyURL(t *testing.T) {
	if ErrEmptyURL.Error() != "webhook URL cannot be empty" {
		t.Errorf("Unexpected error message: %s", ErrEmptyURL.Error())
	}
}

func TestSentinelErrors_InvalidURL(t *testing.T) {
	if ErrInvalidURL.Error() != "webhook URL is invalid" {
		t.Errorf("Unexpected error message: %s", ErrInvalidURL.Error())
	}
}

func TestSentinelErrors_InsecureScheme(t *testing.T) {
	if ErrInsecureScheme.Error() != "webhook URL must use HTTPS scheme" {
		t.Errorf("Unexpected error message: %s", ErrInsecureScheme.Error())
	}
}

func TestSentinelErrors_CredentialsInURL(t *testing.T) {
	if ErrCredentialsInURL.Error() != "webhook URL must not contain credentials" {
		t.Errorf("Unexpected error message: %s", ErrCredentialsInURL.Error())
	}
}

func TestSentinelErrors_BlockedHost(t *testing.T) {
	if ErrBlockedHost.Error() != "webhook URL host is blocked (localhost/private IP)" {
		t.Errorf("Unexpected error message: %s", ErrBlockedHost.Error())
	}
}

func TestSentinelErrors_PayloadTooLarge(t *testing.T) {
	if ErrPayloadTooLarge.Error() != "webhook payload exceeds maximum size" {
		t.Errorf("Unexpected error message: %s", ErrPayloadTooLarge.Error())
	}
}

func TestSentinelErrors_InvalidFormat(t *testing.T) {
	if ErrInvalidFormat.Error() != "webhook payload format is invalid" {
		t.Errorf("Unexpected error message: %s", ErrInvalidFormat.Error())
	}
}

func TestSentinelErrors_TooManyHeaders(t *testing.T) {
	if ErrTooManyHeaders.Error() != "webhook has too many headers" {
		t.Errorf("Unexpected error message: %s", ErrTooManyHeaders.Error())
	}
}

func TestSentinelErrors_HeaderValueTooLarge(t *testing.T) {
	if ErrHeaderValueTooLarge.Error() != "webhook header value exceeds maximum size" {
		t.Errorf("Unexpected error message: %s", ErrHeaderValueTooLarge.Error())
	}
}

func TestSentinelErrors_InvalidTimeout(t *testing.T) {
	if ErrInvalidTimeout.Error() != "webhook timeout must be between 1s and 60s" {
		t.Errorf("Unexpected error message: %s", ErrInvalidTimeout.Error())
	}
}

func TestSentinelErrors_InvalidRetryConfig(t *testing.T) {
	if ErrInvalidRetryConfig.Error() != "webhook retry configuration is invalid" {
		t.Errorf("Unexpected error message: %s", ErrInvalidRetryConfig.Error())
	}
}

func TestSentinelErrors_MissingAuthToken(t *testing.T) {
	if ErrMissingAuthToken.Error() != "bearer token is required but not provided" {
		t.Errorf("Unexpected error message: %s", ErrMissingAuthToken.Error())
	}
}

func TestSentinelErrors_MissingBasicAuthCredentials(t *testing.T) {
	if ErrMissingBasicAuthCredentials.Error() != "basic auth username/password required but not provided" {
		t.Errorf("Unexpected error message: %s", ErrMissingBasicAuthCredentials.Error())
	}
}

func TestSentinelErrors_MissingAPIKey(t *testing.T) {
	if ErrMissingAPIKey.Error() != "API key is required but not provided" {
		t.Errorf("Unexpected error message: %s", ErrMissingAPIKey.Error())
	}
}

func TestSentinelErrors_NoCustomHeaders(t *testing.T) {
	if ErrNoCustomHeaders.Error() != "custom headers are required but not provided" {
		t.Errorf("Unexpected error message: %s", ErrNoCustomHeaders.Error())
	}
}

// ==================== Error Classification Tests ====================

func TestIsWebhookRetryableError_NetworkError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeNetwork,
		Message: "connection refused",
	}

	if !IsWebhookRetryableError(err) {
		t.Error("Network error should be retryable")
	}
}

func TestIsWebhookRetryableError_TimeoutError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeTimeout,
		Message: "request timeout",
	}

	if !IsWebhookRetryableError(err) {
		t.Error("Timeout error should be retryable")
	}
}

func TestIsWebhookRetryableError_RateLimitError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeRateLimit,
		Message: "rate limit exceeded",
	}

	if !IsWebhookRetryableError(err) {
		t.Error("Rate limit error should be retryable")
	}
}

func TestIsWebhookRetryableError_ServerError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeServer,
		Message: "internal server error",
	}

	if !IsWebhookRetryableError(err) {
		t.Error("Server error should be retryable")
	}
}

func TestIsWebhookRetryableError_ValidationError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeValidation,
		Message: "invalid payload",
	}

	if IsWebhookRetryableError(err) {
		t.Error("Validation error should NOT be retryable")
	}
}

func TestIsWebhookRetryableError_AuthError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeAuth,
		Message: "unauthorized",
	}

	if IsWebhookRetryableError(err) {
		t.Error("Auth error should NOT be retryable")
	}
}

func TestIsWebhookRetryableError_NonWebhookError(t *testing.T) {
	err := errors.New("generic error")

	if IsWebhookRetryableError(err) {
		t.Error("Non-webhook error should NOT be retryable")
	}
}

func TestIsWebhookPermanentError_ValidationError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeValidation,
		Message: "invalid URL",
	}

	if !IsWebhookPermanentError(err) {
		t.Error("Validation error should be permanent")
	}
}

func TestIsWebhookPermanentError_AuthError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeAuth,
		Message: "unauthorized",
	}

	if !IsWebhookPermanentError(err) {
		t.Error("Auth error should be permanent")
	}
}

func TestIsWebhookPermanentError_NetworkError(t *testing.T) {
	err := &WebhookError{
		Type:    ErrorTypeNetwork,
		Message: "connection refused",
	}

	if IsWebhookPermanentError(err) {
		t.Error("Network error should NOT be permanent")
	}
}

func TestIsWebhookPermanentError_NonWebhookError(t *testing.T) {
	err := errors.New("generic error")

	if IsWebhookPermanentError(err) {
		t.Error("Non-webhook error should NOT be permanent")
	}
}

// ==================== HTTP Error Classification Tests ====================

func TestClassifyHTTPError_2xx(t *testing.T) {
	tests := []int{200, 201, 202, 204}
	for _, code := range tests {
		errType := classifyErrorType(code)
		// 2xx codes don't create errors, so classifyErrorType returns 0 (ErrorTypeValidation by default)
		// We just check that no panic occurs
		_ = errType
	}
}

func TestClassifyHTTPError_400(t *testing.T) {
	errType := classifyErrorType(400)
	if errType != ErrorTypeValidation {
		t.Errorf("Status 400 should map to validation, got %v", errType)
	}
	// Check it's permanent
	err := &WebhookError{Type: errType}
	if !IsWebhookPermanentError(err) {
		t.Error("400 should be permanent")
	}
}

func TestClassifyHTTPError_401(t *testing.T) {
	errType := classifyErrorType(401)
	if errType != ErrorTypeAuth {
		t.Errorf("Status 401 should map to auth, got %v", errType)
	}
	err := &WebhookError{Type: errType}
	if !IsWebhookPermanentError(err) {
		t.Error("401 should be permanent")
	}
}

func TestClassifyHTTPError_403(t *testing.T) {
	errType := classifyErrorType(403)
	if errType != ErrorTypeAuth {
		t.Errorf("Status 403 should map to auth, got %v", errType)
	}
	err := &WebhookError{Type: errType}
	if !IsWebhookPermanentError(err) {
		t.Error("403 should be permanent")
	}
}

func TestClassifyHTTPError_404(t *testing.T) {
	errType := classifyErrorType(404)
	if errType != ErrorTypeValidation {
		t.Errorf("Status 404 should map to validation, got %v", errType)
	}
	err := &WebhookError{Type: errType}
	if !IsWebhookPermanentError(err) {
		t.Error("404 should be permanent")
	}
}

func TestClassifyHTTPError_429(t *testing.T) {
	errType := classifyErrorType(429)
	if errType != ErrorTypeRateLimit {
		t.Errorf("Status 429 should map to rate_limit, got %v", errType)
	}
	err := &WebhookError{Type: errType}
	if !IsWebhookRetryableError(err) {
		t.Error("429 should be retryable")
	}
}

func TestClassifyHTTPError_5xx(t *testing.T) {
	tests := []int{500, 502, 503, 504}
	for _, code := range tests {
		errType := classifyErrorType(code)
		if errType != ErrorTypeServer {
			t.Errorf("Status %d should map to server, got %v", code, errType)
		}
		err := &WebhookError{Type: errType}
		if !IsWebhookRetryableError(err) {
			t.Errorf("%d should be retryable", code)
		}
	}
}

func TestClassifyErrorType_400(t *testing.T) {
	result := classifyErrorType(400)
	if result != ErrorTypeValidation {
		t.Errorf("Status 400 should map to validation, got %v", result)
	}
}

func TestClassifyErrorType_401(t *testing.T) {
	result := classifyErrorType(401)
	if result != ErrorTypeAuth {
		t.Errorf("Status 401 should map to auth, got %v", result)
	}
}

func TestClassifyErrorType_403(t *testing.T) {
	result := classifyErrorType(403)
	if result != ErrorTypeAuth {
		t.Errorf("Status 403 should map to auth, got %v", result)
	}
}

func TestClassifyErrorType_429(t *testing.T) {
	result := classifyErrorType(429)
	if result != ErrorTypeRateLimit {
		t.Errorf("Status 429 should map to rate_limit, got %v", result)
	}
}

func TestClassifyErrorType_5xx(t *testing.T) {
	tests := []int{500, 502, 503, 504}
	for _, code := range tests {
		result := classifyErrorType(code)
		if result != ErrorTypeServer {
			t.Errorf("Status %d should map to server, got %v", code, result)
		}
	}
}
