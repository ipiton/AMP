package publishing

// telegram_models.go - Telegram Bot API data models
// Implements the Telegram Bot API sendMessage request/response envelope.
// https://core.telegram.org/bots/api#sendmessage

// TelegramMessage represents a Telegram Bot API sendMessage request payload
type TelegramMessage struct {
	// ChatID is the target chat, channel, or group identifier (required)
	// Numeric chat id (may be negative for groups/channels) or "@channelusername"
	ChatID string `json:"chat_id"`

	// Text is the message body (required, max 4096 characters)
	Text string `json:"text"`

	// ParseMode specifies message formatting: HTML, Markdown, or MarkdownV2
	ParseMode string `json:"parse_mode,omitempty"`

	// MessageThreadID targets a specific forum topic thread (optional)
	MessageThreadID int `json:"message_thread_id,omitempty"`

	// DisableNotification sends the message silently (no sound/vibration)
	DisableNotification bool `json:"disable_notification,omitempty"`

	// DisableWebPagePreview disables link previews in the message
	DisableWebPagePreview bool `json:"disable_web_page_preview,omitempty"`
}

// TelegramResponse represents the Telegram Bot API response envelope
type TelegramResponse struct {
	// OK indicates success (true) or failure (false)
	OK bool `json:"ok"`

	// Result contains the sent message details (present when OK is true)
	Result *TelegramResult `json:"result,omitempty"`

	// ErrorCode is the Telegram API error code (present when OK is false)
	ErrorCode int `json:"error_code,omitempty"`

	// Description contains the error message (present when OK is false)
	Description string `json:"description,omitempty"`

	// Parameters contains additional error metadata (e.g. retry_after)
	Parameters *TelegramResponseParameters `json:"parameters,omitempty"`
}

// TelegramResult represents the "result" field of a successful sendMessage response
type TelegramResult struct {
	// MessageID is the unique identifier of the sent message
	MessageID int `json:"message_id"`

	// Date is the Unix timestamp of when the message was sent
	Date int64 `json:"date,omitempty"`
}

// TelegramResponseParameters contains additional error metadata returned by
// the Telegram Bot API (e.g. rate limiting information)
type TelegramResponseParameters struct {
	// RetryAfter is the number of seconds to wait before retrying (429 responses)
	RetryAfter int `json:"retry_after,omitempty"`

	// MigrateToChatID is the new chat id when a group has been migrated to a supergroup
	MigrateToChatID int64 `json:"migrate_to_chat_id,omitempty"`
}

// Parse mode constants for Telegram messages
const (
	TelegramParseModeHTML       = "HTML"
	TelegramParseModeMarkdown   = "Markdown"
	TelegramParseModeMarkdownV2 = "MarkdownV2"
)

// DefaultTelegramAPIURL is the default Telegram Bot API base URL
const DefaultTelegramAPIURL = "https://api.telegram.org"

// MaxTelegramMessageLength is the maximum number of characters allowed in a
// single Telegram text message (Bot API limit).
const MaxTelegramMessageLength = 4096

// telegramTruncationSuffix is appended to messages that get truncated so the
// recipient knows the message was cut off.
const telegramTruncationSuffix = "… [truncated]"

// TruncateTelegramMessage truncates text to Telegram's 4096-character limit,
// appending a truncation marker when the text had to be cut. The input is
// measured and cut by rune (not byte) so multi-byte UTF-8 characters are
// never split.
func TruncateTelegramMessage(text string) string {
	runes := []rune(text)
	if len(runes) <= MaxTelegramMessageLength {
		return text
	}

	suffixRunes := []rune(telegramTruncationSuffix)
	cut := MaxTelegramMessageLength - len(suffixRunes)
	if cut < 0 {
		cut = 0
	}

	return string(runes[:cut]) + telegramTruncationSuffix
}
