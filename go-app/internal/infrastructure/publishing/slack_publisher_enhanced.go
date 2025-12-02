package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// slack_publisher_enhanced.go - Enhanced Slack publisher with full lifecycle management
// Implements AlertPublisher interface with message tracking and threading support

// EnhancedSlackPublisher implements AlertPublisher with full Slack webhook support
// Provides message lifecycle management (post, thread reply) and message tracking
type EnhancedSlackPublisher struct {
	client    SlackWebhookClient
	cache     MessageIDCache // For tracking message timestamps (threading)
	metrics   *SlackMetrics
	formatter AlertFormatter
	logger    *slog.Logger
}

// NewEnhancedSlackPublisher creates a new enhanced Slack publisher
// cache: Message ID cache for tracking message timestamps (for threading)
// metrics: Prometheus metrics recorder
// formatter: Alert formatter (TN-051) for converting alerts to Slack format
func NewEnhancedSlackPublisher(
	client SlackWebhookClient,
	cache MessageIDCache,
	metrics *SlackMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedSlackPublisher{
		client:    client,
		cache:     cache,
		metrics:   metrics,
		formatter: formatter,
		logger:    logger.With("component", "slack_publisher"),
	}
}

// Publish publishes enriched alert to Slack
// Routes to postMessage() or replyInThread() based on alert status and cache
func (p *EnhancedSlackPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	alert := enrichedAlert.Alert
	fingerprint := alert.Fingerprint

	p.logger.InfoContext(ctx, "Publishing alert to Slack",
		slog.String("fingerprint", fingerprint),
		slog.String("alert_name", alert.AlertName),
		slog.String("status", string(alert.Status)))

	// Check cache for existing message
	entry, found := p.cache.Get(fingerprint)

	// Determine action based on alert status and cache
	switch alert.Status {
	case core.StatusFiring:
		if found {
			// Alert still firing - reply in thread
			p.metrics.CacheHits.Inc()
			return p.replyInThread(ctx, entry.ThreadTS, enrichedAlert, "🔴 Still firing")
		}
		// New firing alert - post new message
		p.metrics.CacheMisses.Inc()
		return p.postMessage(ctx, enrichedAlert, fingerprint)

	case core.StatusResolved:
		if found {
			// Alert resolved - reply in thread
			p.metrics.CacheHits.Inc()
			return p.replyInThread(ctx, entry.ThreadTS, enrichedAlert, "🟢 Resolved")
		}
		// Resolved alert without firing message (cache miss) - post new message with resolved status
		p.logger.WarnContext(ctx, "Resolved alert without firing message (cache miss), posting new message",
			slog.String("fingerprint", fingerprint))
		p.metrics.CacheMisses.Inc()
		return p.postMessage(ctx, enrichedAlert, fingerprint)

	default:
		return fmt.Errorf("unknown alert status: %s", alert.Status)
	}
}

// Name returns publisher name
func (p *EnhancedSlackPublisher) Name() string {
	return "Slack"
}

// postMessage posts a new message to Slack channel
// Formats alert using TN-051 formatter, posts to Slack, caches message timestamp
func (p *EnhancedSlackPublisher) postMessage(ctx context.Context, enrichedAlert *core.EnrichedAlert, fingerprint string) error {
	startTime := time.Now()

	// Format alert using TN-051 formatter
	formattedPayload, err := p.formatter.FormatAlert(ctx, enrichedAlert, core.FormatSlack)
	if err != nil {
		p.metrics.MessageErrors.WithLabelValues("format_error").Inc()
		return fmt.Errorf("failed to format alert: %w", err)
	}

	// Build SlackMessage from formatted payload
	message := p.buildMessage(formattedPayload)

	// Post message to Slack
	resp, err := p.client.PostMessage(ctx, message)
	if err != nil {
		p.metrics.MessageErrors.WithLabelValues(classifySlackError(err)).Inc()
		p.metrics.APIDuration.WithLabelValues("post_message", "error").Observe(time.Since(startTime).Seconds())
		return fmt.Errorf("failed to post message: %w", err)
	}

	// Cache message timestamp for threading
	entry := &MessageEntry{
		MessageTS: resp.TS,
		ThreadTS:  resp.TS, // First message is thread root
		CreatedAt: time.Now(),
	}
	p.cache.Store(fingerprint, entry)

	// Record metrics
	p.metrics.MessagesPosted.WithLabelValues("success").Inc()
	p.metrics.APIDuration.WithLabelValues("post_message", "success").Observe(time.Since(startTime).Seconds())

	p.logger.InfoContext(ctx, "Message posted successfully",
		slog.String("fingerprint", fingerprint),
		slog.String("message_ts", resp.TS))

	return nil
}

// replyInThread replies to an existing message thread
// Used for "still firing" updates and "resolved" notifications
func (p *EnhancedSlackPublisher) replyInThread(ctx context.Context, threadTS string, enrichedAlert *core.EnrichedAlert, statusText string) error {
	startTime := time.Now()

	// Build simple reply message
	alert := enrichedAlert.Alert
	message := &SlackMessage{
		Text: fmt.Sprintf("%s - %s", statusText, alert.AlertName),
		Blocks: []Block{
			{
				Type: "section",
				Text: &Text{
					Type: "mrkdwn",
					Text: fmt.Sprintf("*%s*\n%s", statusText, time.Now().Format("2006-01-02 15:04:05")),
				},
			},
		},
	}

	// Add AI classification if available
	if enrichedAlert.Classification != nil {
		classification := enrichedAlert.Classification
		message.Blocks = append(message.Blocks, Block{
			Type: "context",
			Text: &Text{
				Type: "mrkdwn",
				Text: fmt.Sprintf("AI Severity: %s (%.0f%% confidence)", classification.Severity, classification.Confidence*100),
			},
		})
	}

	// Reply in thread
	_, err := p.client.ReplyInThread(ctx, threadTS, message)
	if err != nil {
		p.metrics.MessageErrors.WithLabelValues(classifySlackError(err)).Inc()
		p.metrics.APIDuration.WithLabelValues("thread_reply", "error").Observe(time.Since(startTime).Seconds())
		return fmt.Errorf("failed to reply in thread: %w", err)
	}

	// Record metrics
	p.metrics.ThreadReplies.Inc()
	p.metrics.APIDuration.WithLabelValues("thread_reply", "success").Observe(time.Since(startTime).Seconds())

	p.logger.InfoContext(ctx, "Thread reply posted successfully",
		slog.String("thread_ts", threadTS),
		slog.String("status", statusText))

	return nil
}

// buildMessage builds SlackMessage from formatted payload (TN-051 output)
// Converts formatter output (map[string]any) to Slack-specific structures
func (p *EnhancedSlackPublisher) buildMessage(payload map[string]any) *SlackMessage {
	message := &SlackMessage{}

	// Extract text (fallback)
	if text, ok := payload["text"].(string); ok {
		message.Text = text
	}

	// Extract blocks (Block Kit)
	if blocksRaw, ok := payload["blocks"].([]interface{}); ok {
		for _, blockRaw := range blocksRaw {
			if blockMap, ok := blockRaw.(map[string]interface{}); ok {
				message.Blocks = append(message.Blocks, p.buildBlock(blockMap))
			}
		}
	}

	// Extract attachments (color coding)
	if attachmentsRaw, ok := payload["attachments"].([]interface{}); ok {
		for _, attachRaw := range attachmentsRaw {
			if attachMap, ok := attachRaw.(map[string]interface{}); ok {
				message.Attachments = append(message.Attachments, p.buildAttachment(attachMap))
			}
		}
	}

	return message
}

// buildBlock builds Block from map (TN-051 formatter output)
func (p *EnhancedSlackPublisher) buildBlock(blockMap map[string]interface{}) Block {
	block := Block{}

	// Extract type
	if blockType, ok := blockMap["type"].(string); ok {
		block.Type = blockType
	}

	// Extract text
	if textMap, ok := blockMap["text"].(map[string]interface{}); ok {
		block.Text = &Text{}
		if textType, ok := textMap["type"].(string); ok {
			block.Text.Type = textType
		}
		if textContent, ok := textMap["text"].(string); ok {
			block.Text.Text = textContent
		}
	}

	// Extract fields (for section blocks)
	if fieldsRaw, ok := blockMap["fields"].([]interface{}); ok {
		for _, fieldRaw := range fieldsRaw {
			if fieldMap, ok := fieldRaw.(map[string]interface{}); ok {
				field := Field{}
				if fieldType, ok := fieldMap["type"].(string); ok {
					field.Type = fieldType
				}
				if fieldText, ok := fieldMap["text"].(string); ok {
					field.Text = fieldText
				}
				block.Fields = append(block.Fields, field)
			}
		}
	}

	return block
}

// buildAttachment builds Attachment from map (TN-051 formatter output)
func (p *EnhancedSlackPublisher) buildAttachment(attachMap map[string]interface{}) Attachment {
	attachment := Attachment{}

	// Extract color
	if color, ok := attachMap["color"].(string); ok {
		attachment.Color = color
	}

	// Extract text
	if text, ok := attachMap["text"].(string); ok {
		attachment.Text = text
	}

	return attachment
}

// classifySlackError classifies error for metrics labeling
func classifySlackError(err error) string {
	if err == nil {
		return "unknown"
	}

	// Check for Slack API error
	if IsSlackRetryableError(err) {
		if IsSlackRateLimitError(err) {
			return "rate_limit"
		}
		if IsSlackServerError(err) {
			return "server_error"
		}
		return "api_error"
	}

	// Check for specific error types
	if IsSlackAuthError(err) {
		return "auth_error"
	}
	if IsSlackBadRequestError(err) {
		return "bad_request"
	}

	// Default: network error
	return "network_error"
}
