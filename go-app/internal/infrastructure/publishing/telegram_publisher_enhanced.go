package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/httperror"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// telegram_publisher_enhanced.go - Enhanced Telegram publisher with full lifecycle management
// Implements AlertPublisher interface, rendering alerts as HTML-formatted
// Telegram messages via the Bot API sendMessage method.
//
// Unlike the Slack publisher, Telegram notifications are not threaded:
// upstream Alertmanager's telegram_config sends one independent message per
// notification, so no message-id cache is required here. Rendering itself
// goes through the shared AlertFormatter (core.FormatTelegram), same as
// Slack's primary postMessage path uses core.FormatSlack - this publisher
// only truncates the formatter's output to Telegram's message size limit.

// EnhancedTelegramPublisher implements AlertPublisher with full Telegram Bot API support
type EnhancedTelegramPublisher struct {
	*BaseEnhancedPublisher // Embedded base publisher for common functionality
	client                 TelegramClient
	chatID                 string
	messageThreadID        int
	disableNotifications   bool
	parseMode              string
}

// NewEnhancedTelegramPublisher creates a new enhanced Telegram publisher.
// client: Telegram Bot API client used to send messages.
// chatID: target chat/channel/group identifier (numeric id or "@channelusername").
// messageThreadID: forum topic thread id (0 to omit).
// disableNotifications: send messages silently when true.
// metrics: Prometheus metrics recorder.
// formatter: Alert formatter used to render the message body (core.FormatTelegram).
func NewEnhancedTelegramPublisher(
	client TelegramClient,
	chatID string,
	messageThreadID int,
	disableNotifications bool,
	metrics *v2.PublishingMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedTelegramPublisher{
		BaseEnhancedPublisher: NewBaseEnhancedPublisher(
			metrics,
			formatter,
			logger.With("component", "telegram_publisher"),
		),
		client:               client,
		chatID:               chatID,
		messageThreadID:      messageThreadID,
		disableNotifications: disableNotifications,
		parseMode:            TelegramParseModeHTML,
	}
}

// Publish publishes an enriched alert to Telegram
func (p *EnhancedTelegramPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	alert := enrichedAlert.Alert

	p.LogPublishStart(ctx, ProviderTelegram, enrichedAlert)

	switch alert.Status {
	case core.StatusFiring, core.StatusResolved:
		// supported
	default:
		return fmt.Errorf("unknown alert status: %s", alert.Status)
	}

	startTime := time.Now()

	message, err := p.buildMessage(ctx, enrichedAlert)
	if err != nil {
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(ProviderTelegram, "send_message", "format_error")
		}
		return err
	}

	resp, err := p.client.SendMessage(ctx, message)
	if err != nil {
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(ProviderTelegram, "send_message", classifyTelegramError(err))
			p.GetMetrics().RecordAPIDuration(ProviderTelegram, "send_message", "POST", time.Since(startTime))
		}
		return fmt.Errorf("failed to send message: %w", err)
	}

	if p.GetMetrics() != nil {
		p.GetMetrics().RecordMessage(ProviderTelegram, "success")
		p.GetMetrics().RecordAPIDuration(ProviderTelegram, "send_message", "POST", time.Since(startTime))
	}

	messageID := 0
	if resp != nil && resp.Result != nil {
		messageID = resp.Result.MessageID
	}
	p.GetLogger().InfoContext(ctx, "Message sent successfully",
		slog.String("fingerprint", alert.Fingerprint),
		slog.Int("message_id", messageID))

	return nil
}

// Name returns publisher name
func (p *EnhancedTelegramPublisher) Name() string {
	return "Telegram"
}

// buildMessage renders the enriched alert as a TelegramMessage.
// Rendering (including HTML-escaping) goes through the shared AlertFormatter
// (core.FormatTelegram) - same primary-path pattern as Slack's postMessage,
// which formats via core.FormatSlack. The formatter's output text is then
// truncated to Telegram's 4096-character limit.
func (p *EnhancedTelegramPublisher) buildMessage(ctx context.Context, enrichedAlert *core.EnrichedAlert) (*TelegramMessage, error) {
	payload, err := p.GetFormatter().FormatAlert(ctx, enrichedAlert, core.FormatTelegram)
	if err != nil {
		return nil, fmt.Errorf("failed to format alert: %w", err)
	}

	text, _ := payload["text"].(string)
	text = TruncateTelegramMessage(text)

	return &TelegramMessage{
		ChatID:              p.chatID,
		Text:                text,
		ParseMode:           p.parseMode,
		MessageThreadID:     p.messageThreadID,
		DisableNotification: p.disableNotifications,
	}, nil
}

// classifyTelegramError classifies error for metrics labeling
func classifyTelegramError(err error) string {
	if err == nil {
		return "unknown"
	}

	// Check for Telegram API error
	if (&httperror.PublishingClassifier{}).IsRetryable(err) {
		if IsTelegramRateLimitError(err) {
			return "rate_limit"
		}
		if IsTelegramServerError(err) {
			return "server_error"
		}
		return "api_error"
	}

	// Check for specific error types
	if IsTelegramAuthError(err) {
		return "auth_error"
	}
	if IsTelegramBadRequestError(err) {
		return "bad_request"
	}

	// Default: network error
	return "network_error"
}
