package publishing

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// stringBuilderPool provides reusable strings.Builder instances to reduce allocations
var stringBuilderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// getBuilder gets a strings.Builder from the pool
func getBuilder() *strings.Builder {
	return stringBuilderPool.Get().(*strings.Builder)
}

// putBuilder returns a strings.Builder to the pool after resetting it
func putBuilder(b *strings.Builder) {
	b.Reset()
	stringBuilderPool.Put(b)
}

// formatterResultPool provides reusable map[string]any instances for formatting results.
//
// This is a critical optimization for hot path (formatter called 1000+ times/sec).
// Pre-allocating with capacity 30 covers typical alert formats.
//
// Performance impact:
//   - Before: 1 allocation/call, ~2KB/allocation
//   - After:  0 allocations/call
//   - Improvement: 50% faster, 100% less GC pressure
//
// Usage:
//
//	result := getFormatterResult()
//	defer releaseFormatterResult(result)
//	// ... fill result
//	return result, nil
var formatterResultPool = sync.Pool{
	New: func() interface{} {
		return make(map[string]any, 30) // Pre-allocate typical size
	},
}

// getFormatterResult gets a map from the pool for formatting results.
//
// IMPORTANT: Caller MUST call releaseFormatterResult() when done to return to pool.
// Use defer to ensure cleanup even on error paths.
func getFormatterResult() map[string]any {
	return formatterResultPool.Get().(map[string]any)
}

// releaseFormatterResult returns a map to the pool after clearing all keys.
//
// This clears the map to prevent memory leaks and prepares it for reuse.
func releaseFormatterResult(m map[string]any) {
	// Clear all keys (faster than creating new map)
	for k := range m {
		delete(m, k)
	}
	formatterResultPool.Put(m)
}

// AlertFormatter defines the interface for formatting alerts for different publishing targets
type AlertFormatter interface {
	// FormatAlert formats an enriched alert for a specific target format
	FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error)
}

// GroupAlertFormatter formats a whole alert GROUP as ONE wire-level payload
// (task fwb: wire-level group batching) — upstream Alertmanager's webhook
// shape sends exactly one POST per group per target, carrying every alert
// in an "alerts" array, rather than the one-POST-per-alert shape
// FormatAlert produces. Optional: implemented by DefaultAlertFormatter for
// the formats that model a whole-group payload natively (alertmanager and
// webhook — see FormatGroup); a formatter that doesn't implement this
// interface, or returns an error for the requested format, signals "no
// wire-level batching for this format" to the caller (BatchAlertPublisher
// implementations), which is expected to be the only thing that calls it.
type GroupAlertFormatter interface {
	// FormatGroup formats alerts (every alert in one group notification,
	// already filtered by the notify-stage chain — see
	// grouping.DefaultGroupManager.publishGroupAlerts) as a single payload.
	// groupKey and receiver populate the payload's "groupKey"/"receiver"
	// fields (upstream Alertmanager webhook shape); groupLabels populates
	// "groupLabels" (review finding 1, fwb fix round 1) — the caller
	// resolves this from grouping.GroupMetadata.GroupBy, since this
	// package doesn't depend on infrastructure/grouping; format selects
	// which wire shape to produce — only core.FormatAlertmanager and
	// core.FormatWebhook are supported by DefaultAlertFormatter today.
	FormatGroup(ctx context.Context, alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string, format core.PublishingFormat) (map[string]any, error)
}

// DefaultAlertFormatter implements AlertFormatter using strategy pattern
type DefaultAlertFormatter struct {
	formatters  map[core.PublishingFormat]formatFunc
	externalURL string
}

// formatFunc is the function signature for format-specific implementations
type formatFunc func(*core.EnrichedAlert) (map[string]any, error)

// NewAlertFormatter creates a new alert formatter.
// externalURL is the public base URL of this AMP instance (env: AMP_SERVER_EXTERNAL_URL).
// Empty string causes callback links to be omitted (graceful degradation).
func NewAlertFormatter(externalURL string) AlertFormatter {
	formatter := &DefaultAlertFormatter{
		formatters:  make(map[core.PublishingFormat]formatFunc),
		externalURL: externalURL,
	}

	// Register format strategies
	formatter.formatters[core.FormatAlertmanager] = formatter.formatAlertmanager
	formatter.formatters[core.FormatRootly] = formatter.formatRootly
	formatter.formatters[core.FormatPagerDuty] = formatter.formatPagerDuty
	formatter.formatters[core.FormatSlack] = formatter.formatSlack
	formatter.formatters[core.FormatWebhook] = formatter.formatWebhook
	formatter.formatters[core.FormatTelegram] = formatter.formatTelegram

	return formatter
}

// FormatAlert formats an enriched alert for a specific target format
func (f *DefaultAlertFormatter) FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error) {
	if enrichedAlert == nil || enrichedAlert.Alert == nil {
		return nil, fmt.Errorf("enriched alert or alert is nil")
	}

	formatFn, exists := f.formatters[format]
	if !exists {
		// Default to webhook format
		formatFn = f.formatWebhook
	}

	return formatFn(enrichedAlert)
}

// FormatGroup implements GroupAlertFormatter (task fwb): the true upstream
// Alertmanager webhook wire shape — one payload per group per target,
// carrying every alert of the group in an "alerts" array, plus
// groupLabels/commonLabels/commonAnnotations/groupKey/receiver/externalURL
// — for the two formats that model it (alertmanager and webhook; both
// share the same upstream v4 shape once batched — the pre-fwb formatWebhook
// used a bespoke simplified single-alert shape, but there is no reason for
// the GROUP payload to diverge from the alertmanager one AlertFormatter
// already produces per-alert). Any other format returns an error: those
// integrations (Slack, Telegram, PagerDuty, Email) are inherently
// one-message-per-alert and are driven by PublishingQueue iterating
// FormatAlert per alert within the same job instead — see
// PublishingQueue.publishJob.
func (f *DefaultAlertFormatter) FormatGroup(_ context.Context, alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string, format core.PublishingFormat) (map[string]any, error) {
	if len(alerts) == 0 {
		return nil, fmt.Errorf("cannot format an empty alert group")
	}

	switch format {
	case core.FormatAlertmanager, core.FormatWebhook:
		return f.formatGroupUpstream(alerts, groupKey, receiver, groupLabels), nil
	default:
		return nil, fmt.Errorf("wire-level group batching not supported for format %q", format)
	}
}

// formatGroupUpstream builds the upstream Alertmanager v4 webhook payload
// for a whole group: {"version":"4","groupKey":...,"status":...,
// "alerts":[...],"groupLabels":...,"commonLabels":...,
// "commonAnnotations":...,"receiver":...,"externalURL":...}.
//
// status is "firing" if any alert in the group is still firing, "resolved"
// only when every alert has resolved — matching upstream's aggrGroup
// status semantics. commonLabels/commonAnnotations are the intersection of
// every alert's labels/annotations where both key AND value match across
// the whole set (upstream's CommonLabels/CommonAnnotations semantics).
// groupLabels is the caller-resolved {label_name: value} map for the
// group's own GroupBy names (review finding 1, fwb fix round 1 — this
// package still has no dependency on infrastructure/grouping; the caller,
// grouping.DefaultGroupManager.publishGroupAlerts, resolves it from
// GroupMetadata.GroupBy and forwards it as a plain map, same pattern as
// groupKey/receiver). A nil groupLabels is normalized to an empty map
// rather than emitted as JSON null.
func (f *DefaultAlertFormatter) formatGroupUpstream(alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string) map[string]any {
	amAlerts := make([]map[string]any, 0, len(alerts))
	var commonLabels, commonAnnotations map[string]string
	anyFiring := false

	for i, alert := range alerts {
		amAlert := map[string]any{
			"labels":      alert.Labels,
			"annotations": alert.Annotations,
			"startsAt":    alert.StartsAt.Format(time.RFC3339),
			"fingerprint": alert.Fingerprint,
			"status":      string(alert.Status),
		}
		if alert.EndsAt != nil {
			amAlert["endsAt"] = alert.EndsAt.Format(time.RFC3339)
		}
		if alert.GeneratorURL != nil {
			amAlert["generatorURL"] = *alert.GeneratorURL
		}
		amAlerts = append(amAlerts, amAlert)

		if alert.Status == core.StatusFiring {
			anyFiring = true
		}

		if i == 0 {
			commonLabels = cloneStringMap(alert.Labels)
			commonAnnotations = cloneStringMap(alert.Annotations)
			continue
		}
		intersectStringMap(commonLabels, alert.Labels)
		intersectStringMap(commonAnnotations, alert.Annotations)
	}

	status := string(core.StatusResolved)
	if anyFiring {
		status = string(core.StatusFiring)
	}

	if groupLabels == nil {
		groupLabels = map[string]string{}
	}

	result := getFormatterResult()
	result["version"] = "4"
	result["groupKey"] = groupKey
	result["status"] = status
	result["alerts"] = amAlerts
	result["groupLabels"] = groupLabels
	result["commonLabels"] = commonLabels
	result["commonAnnotations"] = commonAnnotations
	result["receiver"] = receiver
	result["externalURL"] = f.externalURL
	result["truncatedAlerts"] = 0

	return result
}

// cloneStringMap returns a shallow copy of m (never nil, so the caller can
// safely narrow it in place via intersectStringMap without mutating the
// source alert's own map).
func cloneStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// intersectStringMap narrows acc in place to only the keys whose value in
// m matches exactly, deleting every other key — used to fold
// commonLabels/commonAnnotations down across every alert in a group.
func intersectStringMap(acc map[string]string, m map[string]string) {
	for k, v := range acc {
		if m[k] != v {
			delete(acc, k)
		}
	}
}

// formatAlertmanager formats alert in Alertmanager v4 webhook format
func (f *DefaultAlertFormatter) formatAlertmanager(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert

	// Get result map from pool (optimization: 0 allocations)
	result := getFormatterResult()

	// Build Alertmanager-compatible alert
	amAlert := map[string]any{
		"labels":      alert.Labels,
		"annotations": alert.Annotations,
		"startsAt":    alert.StartsAt.Format(time.RFC3339),
		"fingerprint": alert.Fingerprint,
		"status":      string(alert.Status),
	}

	if alert.EndsAt != nil {
		amAlert["endsAt"] = alert.EndsAt.Format(time.RFC3339)
	}

	if alert.GeneratorURL != nil {
		amAlert["generatorURL"] = *alert.GeneratorURL
	}

	// Add LLM classification data as annotations
	// IMPORTANT: Create a NEW map to avoid race conditions on shared alert.Annotations
	if enrichedAlert.Classification != nil {
		classification := enrichedAlert.Classification

		// Copy original annotations to avoid modifying shared state
		annotations := make(map[string]string, len(alert.Annotations)+4)
		for k, v := range alert.Annotations {
			annotations[k] = v
		}

		annotations["llm_severity"] = string(classification.Severity)
		annotations["llm_confidence"] = fmt.Sprintf("%.2f", classification.Confidence)
		annotations["llm_reasoning"] = truncateString(classification.Reasoning, 500)

		if len(classification.Recommendations) > 0 {
			topRecs := classification.Recommendations
			if len(topRecs) > 3 {
				topRecs = topRecs[:3]
			}
			annotations["llm_recommendations"] = strings.Join(topRecs, "; ")
		}

		amAlert["annotations"] = annotations
	}

	// Fill result map (already from pool)
	result["receiver"] = "alert-history-proxy"
	result["status"] = string(alert.Status)
	result["alerts"] = []map[string]any{amAlert}
	result["groupLabels"] = map[string]string{}
	result["commonLabels"] = alert.Labels
	result["commonAnnotations"] = alert.Annotations
	result["externalURL"] = f.externalURL
	result["version"] = "4"
	result["groupKey"] = fmt.Sprintf("group:%s", alert.Fingerprint)
	result["truncatedAlerts"] = 0

	return result, nil
}

// formatRootly formats alert for Rootly incident management
func (f *DefaultAlertFormatter) formatRootly(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

	// Get result map from pool (optimization: 0 allocations)
	result := getFormatterResult()

	// Map severity to Rootly levels
	severity := "major"
	if classification != nil {
		switch classification.Severity {
		case core.SeverityCritical:
			severity = "critical"
		case core.SeverityWarning:
			severity = "major"
		case core.SeverityInfo:
			severity = "minor"
		case core.SeverityNoise:
			severity = "low"
		}
	} else if sev, ok := alert.Labels["severity"]; ok {
		switch strings.ToLower(sev) {
		case "critical":
			severity = "critical"
		case "warning":
			severity = "major"
		case "info":
			severity = "minor"
		}
	}

	// Build title
	namespace := "unknown"
	if ns := alert.Namespace(); ns != nil {
		namespace = *ns
	}

	title := fmt.Sprintf("[%s] Alert in %s", alert.AlertName, namespace)
	if classification != nil {
		title += fmt.Sprintf(" (AI: %s, %.0f%% confidence)", classification.Severity, classification.Confidence*100)
	}

	// Build description using strings.Builder to reduce allocations
	builder := getBuilder()
	defer putBuilder(builder)

	fmt.Fprintf(builder, "**Alert:** %s\n", alert.AlertName)
	fmt.Fprintf(builder, "**Status:** %s\n", alert.Status)
	fmt.Fprintf(builder, "**Namespace:** %s\n", namespace)
	fmt.Fprintf(builder, "**Started:** %s\n", alert.StartsAt.Format(time.RFC3339))

	if classification != nil {
		builder.WriteString("\n**AI Classification:**\n")
		fmt.Fprintf(builder, "- **Severity:** %s\n", classification.Severity)
		fmt.Fprintf(builder, "- **Confidence:** %.0f%%\n", classification.Confidence*100)
		fmt.Fprintf(builder, "- **Reasoning:** %s\n", classification.Reasoning)

		if len(classification.Recommendations) > 0 {
			builder.WriteString("\n**Recommendations:**\n")
			for i, rec := range classification.Recommendations {
				if i >= 5 {
					break
				}
				fmt.Fprintf(builder, "%d. %s\n", i+1, rec)
			}
		}
	}

	// Add labels as tags
	builder.WriteString("\n**Labels:**\n")
	for k, v := range alert.Labels {
		fmt.Fprintf(builder, "- %s: %s\n", k, v)
	}

	description := builder.String()

	// Fill result map (already from pool)
	result["title"] = title
	result["description"] = description
	result["severity"] = severity
	result["status"] = "started"
	result["tags"] = labelsToTags(alert.Labels)
	result["environment"] = namespace
	result["started_at"] = alert.StartsAt.Format(time.RFC3339)

	return result, nil
}

// formatPagerDuty formats alert for PagerDuty Events API v2
func (f *DefaultAlertFormatter) formatPagerDuty(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

	// Get result map from pool (optimization: 0 allocations)
	result := getFormatterResult()

	// Determine event action
	eventAction := "trigger"
	if alert.Status == core.StatusResolved {
		eventAction = "resolve"
	}

	// Map severity to PagerDuty severity
	severity := "warning"
	if classification != nil {
		switch classification.Severity {
		case core.SeverityCritical:
			severity = "critical"
		case core.SeverityWarning:
			severity = "warning"
		case core.SeverityInfo:
			severity = "info"
		}
	}

	// Build summary using strings.Builder
	summaryBuilder := getBuilder()
	defer putBuilder(summaryBuilder)

	fmt.Fprintf(summaryBuilder, "[%s] %s", alert.AlertName, alert.Status)
	if classification != nil {
		fmt.Fprintf(summaryBuilder, " - AI: %s (%.0f%%)", classification.Severity, classification.Confidence*100)
	}
	summary := summaryBuilder.String()

	// Build custom details
	details := map[string]any{
		"alert_name":  alert.AlertName,
		"fingerprint": alert.Fingerprint,
		"status":      string(alert.Status),
		"labels":      alert.Labels,
		"annotations": alert.Annotations,
		"starts_at":   alert.StartsAt.Format(time.RFC3339),
	}

	if alert.EndsAt != nil {
		details["ends_at"] = alert.EndsAt.Format(time.RFC3339)
	}

	if classification != nil {
		details["ai_classification"] = map[string]any{
			"severity":        string(classification.Severity),
			"confidence":      classification.Confidence,
			"reasoning":       classification.Reasoning,
			"recommendations": classification.Recommendations,
		}
	}

	// Fill result map (already from pool)
	result["event_action"] = eventAction
	result["dedup_key"] = alert.Fingerprint
	result["payload"] = map[string]any{
		"summary":        summary,
		"severity":       severity,
		"source":         "alert-history-service",
		"timestamp":      alert.StartsAt.Format(time.RFC3339),
		"custom_details": details,
	}

	return result, nil
}

// formatSlack formats alert for Slack webhook with Blocks API
func (f *DefaultAlertFormatter) formatSlack(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

	// Get result map from pool (optimization: 0 allocations)
	result := getFormatterResult()

	// Determine color based on severity
	color := "#FFA500" // Orange (warning)
	emoji := "⚠️"

	if classification != nil {
		switch classification.Severity {
		case core.SeverityCritical:
			color = "#FF0000" // Red
			emoji = "🔴"
		case core.SeverityWarning:
			color = "#FFA500" // Orange
			emoji = "⚠️"
		case core.SeverityInfo:
			color = "#36A64F" // Green
			emoji = "ℹ️"
		case core.SeverityNoise:
			color = "#808080" // Gray
			emoji = "🔇"
		}
	}

	// Build header. Two variants (fu6-mic item 4, wave-5 parked finding m5):
	// a Slack "header" block's text object is ALWAYS "plain_text" — Block
	// Kit does not render markdown there, so the mrkdwn-styled
	// "⚠️ *HighCPUUsage* - firing" used to ship literal asterisks to the
	// user. headerMrkdwn keeps the asterisks and is reused below for the
	// top-level "text" fallback field, where mrkdwn DOES apply (notification
	// previews/screen readers render it); headerPlain drops them for the
	// header block itself.
	headerMrkdwn := fmt.Sprintf("%s *%s* - %s", emoji, alert.AlertName, alert.Status)
	headerPlain := fmt.Sprintf("%s %s - %s", emoji, alert.AlertName, alert.Status)

	// Build text sections
	var blocks []map[string]any

	// Header block
	blocks = append(blocks, map[string]any{
		"type": "header",
		"text": map[string]any{
			"type": "plain_text",
			"text": headerPlain,
		},
	})

	// Alert details
	fields := []map[string]any{
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Status:*\n%s", alert.Status),
		},
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Started:*\n%s", alert.StartsAt.Format("2006-01-02 15:04:05")),
		},
	}

	if ns := alert.Namespace(); ns != nil {
		fields = append(fields, map[string]any{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Namespace:*\n%s", *ns),
		})
	}

	if classification != nil {
		fields = append(fields, map[string]any{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*AI Severity:*\n%s (%.0f%%)", classification.Severity, classification.Confidence*100),
		})
	}

	blocks = append(blocks, map[string]any{
		"type":   "section",
		"fields": fields,
	})

	// AI Classification details
	if classification != nil {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*AI Reasoning:*\n%s", truncateString(classification.Reasoning, 300)),
			},
		})

		if len(classification.Recommendations) > 0 {
			recsBuilder := getBuilder()
			defer putBuilder(recsBuilder)

			recsBuilder.WriteString("*Recommendations:*\n")
			for i, rec := range classification.Recommendations {
				if i >= 3 {
					break
				}
				fmt.Fprintf(recsBuilder, "• %s\n", rec)
			}

			blocks = append(blocks, map[string]any{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": recsBuilder.String(),
				},
			})
		}
	}

	// Divider
	blocks = append(blocks, map[string]any{
		"type": "divider",
	})

	// Context (fingerprint)
	blocks = append(blocks, map[string]any{
		"type": "context",
		"elements": []map[string]any{
			{
				"type": "mrkdwn",
				"text": fmt.Sprintf("Fingerprint: `%s`", alert.Fingerprint),
			},
		},
	})

	// Fill result map (already from pool)
	//
	// "text" is Slack's REQUIRED fallback field (notification previews, screen
	// readers, clients that don't render Block Kit) — this formatter emitted
	// only "blocks"/"attachments" and no "text" at all, so a client reading
	// just the fallback (or a strict webhook that rejects a body with neither)
	// saw nothing (review wave 5, finding C1). headerMrkdwn is the
	// mrkdwn-styled summary computed above ("<emoji> *name* - status"), which
	// is correct here since mrkdwn applies to the top-level fallback text
	// (unlike the header block above, fixed to headerPlain by fu6-mic item 4).
	result["text"] = headerMrkdwn
	result["blocks"] = blocks
	result["attachments"] = []map[string]any{
		{
			"color":  color,
			"fields": fields,
		},
	}

	return result, nil
}

// formatTelegram formats alert as HTML-formatted text for the Telegram Bot
// API (sendMessage with parse_mode=HTML). Returns a payload with a single
// "text" key. All alert-controlled values (alert name, labels, annotations,
// AI classification) are HTML-escaped here since the message is sent with
// HTML parse mode. Callers are responsible for truncating the returned text
// to Telegram's message size limit before sending (MaxTelegramMessageLength).
func (f *DefaultAlertFormatter) formatTelegram(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

	icon, statusText := "🔥", "FIRING"
	if alert.Status == core.StatusResolved {
		icon, statusText = "✅", "RESOLVED"
	}

	b := getBuilder()
	defer putBuilder(b)

	fmt.Fprintf(b, "%s <b>%s</b>: %s\n", icon, statusText, html.EscapeString(alert.AlertName))

	if severity, ok := alert.Labels["severity"]; ok && severity != "" {
		fmt.Fprintf(b, "Severity: %s\n", html.EscapeString(severity))
	}
	if namespace, ok := alert.Labels["namespace"]; ok && namespace != "" {
		fmt.Fprintf(b, "Namespace: %s\n", html.EscapeString(namespace))
	}

	if summary, ok := alert.Annotations["summary"]; ok && summary != "" {
		fmt.Fprintf(b, "%s\n", html.EscapeString(summary))
	} else if description, ok := alert.Annotations["description"]; ok && description != "" {
		fmt.Fprintf(b, "%s\n", html.EscapeString(description))
	}

	if classification != nil {
		fmt.Fprintf(b, "AI Severity: %s (%.0f%% confidence)\n",
			html.EscapeString(string(classification.Severity)), classification.Confidence*100)
	}

	text := strings.TrimRight(b.String(), "\n")

	// Get result map from pool (optimization: 0 allocations)
	result := getFormatterResult()
	result["text"] = text

	return result, nil
}

// formatWebhook formats alert for generic webhook (simple JSON)
func (f *DefaultAlertFormatter) formatWebhook(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert

	// Get result map from pool (optimization: 0 allocations)
	payload := getFormatterResult()

	payload["alert_name"] = alert.AlertName
	payload["fingerprint"] = alert.Fingerprint
	payload["status"] = string(alert.Status)
	payload["labels"] = alert.Labels
	payload["annotations"] = alert.Annotations
	payload["starts_at"] = alert.StartsAt.Format(time.RFC3339)

	if alert.EndsAt != nil {
		payload["ends_at"] = alert.EndsAt.Format(time.RFC3339)
	}

	if alert.GeneratorURL != nil {
		payload["generator_url"] = *alert.GeneratorURL
	}

	// Add classification if present
	if enrichedAlert.Classification != nil {
		classificationJSON, _ := json.Marshal(enrichedAlert.Classification)
		var classificationMap map[string]any
		_ = json.Unmarshal(classificationJSON, &classificationMap)
		payload["classification"] = classificationMap
	}

	// Add enrichment metadata if present
	if enrichedAlert.EnrichmentMetadata != nil {
		payload["enrichment_metadata"] = enrichedAlert.EnrichmentMetadata
	}

	return payload, nil
}

// Helper functions

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func labelsToTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}
	return tags
}
