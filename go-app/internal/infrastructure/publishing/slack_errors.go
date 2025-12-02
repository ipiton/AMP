package publishing

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// slack_errors.go - Slack webhook API error types and classification helpers

// SlackAPIError represents a Slack webhook API error
// Contains HTTP status code, error message, and optional Retry-After header
type SlackAPIError struct {
	// StatusCode is the HTTP status code (429, 503, 400, 403, 404, 500, etc.)
	StatusCode int

	// ErrorMessage is the Slack error message
	// Examples: "invalid_payload", "channel_not_found", etc.
	ErrorMessage string

	// RetryAfter is the value from Retry-After header (in seconds)
	// Used for 429 (rate limit) responses
	// If 0, no Retry-After header was present
	RetryAfter int
}

// Error implements the error interface
// Returns formatted error message with status code and optional Retry-After
func (e *SlackAPIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("slack API error %d: %s (retry after %ds)", e.StatusCode, e.ErrorMessage, e.RetryAfter)
	}
	return fmt.Sprintf("slack API error %d: %s", e.StatusCode, e.ErrorMessage)
}

// Sentinel errors for common failure scenarios
var (
	// ErrMissingWebhookURL indicates webhook URL is missing from target configuration
	ErrMissingWebhookURL = errors.New("missing webhook URL in Slack target configuration")

	// ErrInvalidWebhookURL indicates webhook URL has invalid format
	// Valid format: https://hooks.slack.com/services/{workspace}/{channel}/{token}
	ErrInvalidWebhookURL = errors.New("invalid Slack webhook URL format")

	// ErrMessageTooLarge indicates message payload exceeds Slack limits
	// Limits: 50 blocks, 3000 chars per block, 3000 chars per text
	ErrMessageTooLarge = errors.New("message payload exceeds Slack size limits")
)

// IsSlackRetryableError checks if Slack error is retryable (transient failure)
// Retryable errors: 429 (rate limit), 503 (service unavailable), network errors
// Non-retryable errors: 400 (bad request), 403 (forbidden), 404 (not found), 500 (internal error)
func IsSlackRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Slack API error
	var apiErr *SlackAPIError
	if errors.As(err, &apiErr) {
		// Retry 429 (rate limit) and 503 (service unavailable)
		return apiErr.StatusCode == http.StatusTooManyRequests ||
			apiErr.StatusCode == http.StatusServiceUnavailable
	}

	// Check for network errors (timeout, connection refused, DNS)
	return isRetryableNetworkError(err)
}

// IsSlackRateLimitError checks if Slack error is a rate limit error (429)
// Rate limit: 1 message per second per webhook URL
func IsSlackRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *SlackAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests
	}
	return false
}

// IsSlackPermanentError checks if Slack error is permanent (don't retry)
// Permanent errors: 400 (bad request), 403 (forbidden), 404 (not found), 500 (internal error)
func IsSlackPermanentError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *SlackAPIError
	if errors.As(err, &apiErr) {
		// Don't retry client errors (4xx except 429) and server errors (5xx)
		return apiErr.StatusCode == http.StatusBadRequest ||
			apiErr.StatusCode == http.StatusForbidden ||
			apiErr.StatusCode == http.StatusNotFound ||
			apiErr.StatusCode == http.StatusInternalServerError
	}
	return false
}

// IsSlackAuthError checks if Slack error is authentication/authorization error (403, 404)
// 403: Invalid webhook URL (webhook token is invalid)
// 404: Webhook not found (webhook was revoked/deleted)
func IsSlackAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *SlackAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusForbidden ||
			apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsSlackBadRequestError checks if Slack error is bad request (400)
// Indicates invalid payload (malformed JSON, missing required fields, etc.)
func IsSlackBadRequestError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *SlackAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusBadRequest
	}
	return false
}

// IsSlackServerError checks if Slack error is server error (500, 503)
// 500: Internal server error (Slack infrastructure issue)
// 503: Service unavailable (Slack maintenance)
func IsSlackServerError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *SlackAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusInternalServerError ||
			apiErr.StatusCode == http.StatusServiceUnavailable
	}
	return false
}

// parseSlackError parses Slack API error from HTTP response
// Extracts status code, error message, and Retry-After header
func parseSlackError(resp *http.Response, body []byte) *SlackAPIError {
	apiErr := &SlackAPIError{
		StatusCode: resp.StatusCode,
	}

	// Parse error from response body (JSON format: {"ok": false, "error": "..."})
	var slackResp SlackResponse
	if err := unmarshalJSON(body, &slackResp); err == nil && !slackResp.OK {
		apiErr.ErrorMessage = slackResp.Error
	} else {
		// Fallback: use raw body as error message
		apiErr.ErrorMessage = string(body)
	}

	// Extract Retry-After header (for 429 responses)
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			apiErr.RetryAfter = seconds
		}
	}

	return apiErr
}

// isRetryableNetworkError checks if network error is retryable
// Retryable: timeout, connection refused, DNS errors
// Non-retryable: other errors (e.g., TLS handshake failure)
func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Check for net.Error (timeout, temporary)
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Retry if timeout or temporary error
		return netErr.Timeout() || netErr.Temporary()
	}

	// Check for connection refused (server not available)
	// This is retryable (server might come back online)
	if errors.Is(err, errors.New("connection refused")) {
		return true
	}

	return false
}

// unmarshalJSON is a helper to unmarshal JSON
// Separated for easier mocking in tests
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
