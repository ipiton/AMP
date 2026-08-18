package publishing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/ipiton/AMP/pkg/httperror"
)

// telegram_errors.go - Telegram Bot API error types and classification helpers
//
// Telegram API errors are represented by httperror.HTTPAPIError with
// Provider set to ProviderTelegram. See errors.go for unified helpers.

// Sentinel errors for common failure scenarios
var (
	// ErrMissingBotToken indicates the bot token is missing from the target configuration
	ErrMissingBotToken = errors.New("missing bot token in Telegram target configuration")

	// ErrMissingChatID indicates chat_id is missing from the target configuration
	ErrMissingChatID = errors.New("missing chat_id in Telegram target configuration")

	// ErrTelegramMessageTooLarge indicates message text exceeds Telegram's 4096 character limit
	ErrTelegramMessageTooLarge = errors.New("message text exceeds Telegram size limits")
)

// IsTelegramRateLimitError checks if a Telegram error is a rate limit error (429)
func IsTelegramRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *httperror.HTTPAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests
	}
	return false
}

// IsTelegramAuthError checks if a Telegram error is an authentication/authorization error
// 401: Unauthorized (invalid bot token)
// 403: Forbidden (bot was blocked by the user/kicked from the chat)
func IsTelegramAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *httperror.HTTPAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized ||
			apiErr.StatusCode == http.StatusForbidden
	}
	return false
}

// IsTelegramBadRequestError checks if a Telegram error is a bad request (400)
// Indicates invalid payload (malformed chat_id, invalid parse_mode markup, etc.)
func IsTelegramBadRequestError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *httperror.HTTPAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusBadRequest
	}
	return false
}

// IsTelegramServerError checks if a Telegram error is a server error (5xx)
func IsTelegramServerError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *httperror.HTTPAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= http.StatusInternalServerError
	}
	return false
}

// parseTelegramError parses a Telegram API error from an HTTP response.
// Extracts status code, error description, and retry_after (from the
// response body's "parameters" field, or the Retry-After header as a
// fallback). Returns httperror.HTTPAPIError with provider set to "telegram".
func parseTelegramError(resp *http.Response, body []byte) *httperror.HTTPAPIError {
	apiErr := &httperror.HTTPAPIError{
		StatusCode: resp.StatusCode,
		Provider:   ProviderTelegram,
	}

	var tgResp TelegramResponse
	if err := json.Unmarshal(body, &tgResp); err == nil && !tgResp.OK {
		apiErr.Message = tgResp.Description
		if tgResp.Parameters != nil && tgResp.Parameters.RetryAfter > 0 {
			apiErr.RetryAfter = tgResp.Parameters.RetryAfter
		}
	} else {
		// Fallback: use raw body as error message
		apiErr.Message = string(body)
	}

	// Fallback to Retry-After header if not present in the response body
	if apiErr.RetryAfter == 0 {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				apiErr.RetryAfter = seconds
			}
		}
	}

	return apiErr
}
