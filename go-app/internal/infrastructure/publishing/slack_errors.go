package publishing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/ipiton/AMP/pkg/httperror"
)

// slack_errors.go - Slack webhook API error types and classification helpers
//
// Slack API errors are represented by httperror.HTTPAPIError with
// Provider set to ProviderSlack. See errors.go for unified helpers.

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

// IsSlackRateLimitError checks if Slack error is a rate limit error (429)
// Rate limit: 1 message per second per webhook URL
func IsSlackRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *httperror.HTTPAPIError
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

	var apiErr *httperror.HTTPAPIError
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

	var apiErr *httperror.HTTPAPIError
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

	var apiErr *httperror.HTTPAPIError
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

	var apiErr *httperror.HTTPAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusInternalServerError ||
			apiErr.StatusCode == http.StatusServiceUnavailable
	}
	return false
}

// parseSlackError parses Slack API error from HTTP response.
// Extracts status code, error message, and Retry-After header.
// Returns httperror.HTTPAPIError with provider set to "slack".
func parseSlackError(resp *http.Response, body []byte) *httperror.HTTPAPIError {
	apiErr := &httperror.HTTPAPIError{
		StatusCode: resp.StatusCode,
		Provider:   ProviderSlack,
	}

	// Parse error from response body (JSON format: {"ok": false, "error": "..."})
	var slackResp SlackResponse
	if err := unmarshalJSON(body, &slackResp); err == nil && !slackResp.OK {
		apiErr.Message = slackResp.Error
	} else {
		// Fallback: use raw body as error message
		apiErr.Message = string(body)
	}

	// Extract Retry-After header (for 429 responses)
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			apiErr.RetryAfter = seconds
		}
	}

	return apiErr
}

// unmarshalJSON is a helper to unmarshal JSON
// Separated for easier mocking in tests
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
