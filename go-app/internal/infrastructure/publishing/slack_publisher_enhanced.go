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

// slack_publisher_enhanced.go - Enhanced Slack publisher with full lifecycle management
// Implements AlertPublisher interface with message tracking and threading support

// EnhancedSlackPublisher implements AlertPublisher with full Slack webhook support
// Provides message lifecycle management (post, thread reply) and message tracking
type EnhancedSlackPublisher struct {
	*BaseEnhancedPublisher                    // Embedded base publisher for common functionality
	client                 SlackWebhookClient // Slack-specific webhook client
	cache                  MessageIDCache     // For tracking message timestamps (threading)
}

// NewEnhancedSlackPublisher creates a new enhanced Slack publisher
// cache: Message ID cache for tracking message timestamps (for threading)
// metrics: Prometheus metrics recorder
// formatter: Alert formatter (TN-051) for converting alerts to Slack format
func NewEnhancedSlackPublisher(
	client SlackWebhookClient,
	cache MessageIDCache,
	metrics *v2.PublishingMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
) AlertPublisher {
	return &EnhancedSlackPublisher{
		BaseEnhancedPublisher: NewBaseEnhancedPublisher(
			metrics,
			formatter,
			logger.With("component", "slack_publisher"),
		),
		client: client,
		cache:  cache,
	}
}

// Publish publishes enriched alert to Slack
// Routes to postMessage() or replyInThread() based on alert status and cache
func (p *EnhancedSlackPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	alert := enrichedAlert.Alert
	fingerprint := alert.Fingerprint

	p.LogPublishStart(ctx, v2.ProviderSlack, enrichedAlert)

	// Check cache for existing message
	entry, found := p.cache.Get(fingerprint)

	// Determine action based on alert status and cache
	switch alert.Status {
	case core.StatusFiring:
		if found {
			// Alert still firing - reply in thread
			p.RecordCacheHit(v2.ProviderSlack)
			return p.replyInThread(ctx, entry.ThreadTS, enrichedAlert, "🔴 Still firing")
		}
		// New firing alert - post new message
		p.RecordCacheMiss(v2.ProviderSlack)
		return p.postMessage(ctx, enrichedAlert, fingerprint)

	case core.StatusResolved:
		if found {
			// Alert resolved - reply in thread
			p.RecordCacheHit(v2.ProviderSlack)
			return p.replyInThread(ctx, entry.ThreadTS, enrichedAlert, "🟢 Resolved")
		}
		// Resolved alert without firing message (cache miss) - post new message with resolved status
		p.GetLogger().WarnContext(ctx, "Resolved alert without firing message (cache miss), posting new message",
			slog.String("fingerprint", fingerprint))
		p.RecordCacheMiss(v2.ProviderSlack)
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
	formattedPayload, err := p.GetFormatter().FormatAlert(ctx, enrichedAlert, core.FormatSlack)
	if err != nil {
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(v2.ProviderSlack, "post_message", "format_error")
		}
		return fmt.Errorf("failed to format alert: %w", err)
	}

	// Build SlackMessage from formatted payload
	message := p.buildMessage(formattedPayload)

	// Post message to Slack
	resp, err := p.client.PostMessage(ctx, message)
	if err != nil {
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(v2.ProviderSlack, "post_message", classifySlackError(err))
			p.GetMetrics().RecordAPIDuration(v2.ProviderSlack, "post_message", "POST", time.Since(startTime))
		}
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
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordMessage(v2.ProviderSlack, "success")
		p.GetMetrics().RecordAPIDuration(v2.ProviderSlack, "post_message", "POST", time.Since(startTime))
	}

	p.GetLogger().InfoContext(ctx, "Message posted successfully",
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
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(v2.ProviderSlack, "thread_reply", classifySlackError(err))
			p.GetMetrics().RecordAPIDuration(v2.ProviderSlack, "thread_reply", "POST", time.Since(startTime))
		}
		return fmt.Errorf("failed to reply in thread: %w", err)
	}

	// Record metrics
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordThreadReply("success")
		p.GetMetrics().RecordAPIDuration(v2.ProviderSlack, "thread_reply", "POST", time.Since(startTime))
	}

	p.GetLogger().InfoContext(ctx, "Thread reply posted successfully",
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
	for _, blockMap := range toMapSlice(payload["blocks"]) {
		message.Blocks = append(message.Blocks, p.buildBlock(blockMap))
	}

	// Extract attachments (color coding)
	for _, attachMap := range toMapSlice(payload["attachments"]) {
		message.Attachments = append(message.Attachments, p.buildAttachment(attachMap))
	}

	return message
}

// toMapSlice normalizes a formatter payload field that is logically a slice
// of maps but may arrive as either concrete Go type (review wave 5, finding
// C1): formatSlack builds its own "blocks"/"attachments"/"fields" values as
// []map[string]any (Go-native construction), while a generic JSON decode (or
// a hand-built test payload, see slack_publisher_test.go) produces
// []interface{} with map[string]interface{} elements. []map[string]any and
// []interface{} are DIFFERENT concrete slice types in Go — a type assertion
// for one never matches a value of the other, even though the per-element map
// type (map[string]any / map[string]interface{}) is the exact same type under
// the "any" alias. That mismatch is why buildMessage/buildBlock silently
// dropped every block/attachment/field from the real formatter's output: the
// assertion always missed, so the loop body never ran, and nothing errored.
func toMapSlice(v any) []map[string]any {
	switch vv := v.(type) {
	case []map[string]any:
		return vv
	case []interface{}:
		if len(vv) == 0 {
			return nil
		}
		out := make([]map[string]any, 0, len(vv))
		for _, item := range vv {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
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

	// Extract fields (for section blocks) — same []map[string]any vs
	// []interface{} mismatch as buildMessage's blocks/attachments, so this
	// goes through the same toMapSlice normalizer.
	for _, fieldMap := range toMapSlice(blockMap["fields"]) {
		field := Field{}
		if fieldType, ok := fieldMap["type"].(string); ok {
			field.Type = fieldType
		}
		if fieldText, ok := fieldMap["text"].(string); ok {
			field.Text = fieldText
		}
		block.Fields = append(block.Fields, field)
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
	if (&httperror.PublishingClassifier{}).IsRetryable(err) {
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
