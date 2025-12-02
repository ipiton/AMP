package publishing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// AlertFormatter defines the interface for formatting alerts for different publishing targets
type AlertFormatter interface {
	// FormatAlert formats an enriched alert for a specific target format
	FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error)
}

// DefaultAlertFormatter implements AlertFormatter using strategy pattern
type DefaultAlertFormatter struct {
	formatters map[core.PublishingFormat]formatFunc
}

// formatFunc is the function signature for format-specific implementations
type formatFunc func(*core.EnrichedAlert) (map[string]any, error)

// NewAlertFormatter creates a new alert formatter
func NewAlertFormatter() AlertFormatter {
	formatter := &DefaultAlertFormatter{
		formatters: make(map[core.PublishingFormat]formatFunc),
	}

	// Register format strategies
	formatter.formatters[core.FormatAlertmanager] = formatter.formatAlertmanager
	formatter.formatters[core.FormatRootly] = formatter.formatRootly
	formatter.formatters[core.FormatPagerDuty] = formatter.formatPagerDuty
	formatter.formatters[core.FormatSlack] = formatter.formatSlack
	formatter.formatters[core.FormatWebhook] = formatter.formatWebhook

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

// formatAlertmanager formats alert in Alertmanager v4 webhook format
func (f *DefaultAlertFormatter) formatAlertmanager(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert

	// Build Alertmanager-compatible alert
	amAlert := map[string]any{
		"labels":       alert.Labels,
		"annotations":  alert.Annotations,
		"startsAt":     alert.StartsAt.Format(time.RFC3339),
		"fingerprint":  alert.Fingerprint,
		"status":       string(alert.Status),
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

	// Wrap in Alertmanager webhook structure
	return map[string]any{
		"receiver": "alert-history-proxy",
		"status":   string(alert.Status),
		"alerts":   []map[string]any{amAlert},
		"groupLabels": map[string]string{},
		"commonLabels": alert.Labels,
		"commonAnnotations": alert.Annotations,
		"externalURL": "",
		"version": "4",
		"groupKey": fmt.Sprintf("group:%s", alert.Fingerprint),
		"truncatedAlerts": 0,
	}, nil
}

// formatRootly formats alert for Rootly incident management
func (f *DefaultAlertFormatter) formatRootly(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

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

	// Build description
	description := fmt.Sprintf("**Alert:** %s\n", alert.AlertName)
	description += fmt.Sprintf("**Status:** %s\n", alert.Status)
	description += fmt.Sprintf("**Namespace:** %s\n", namespace)
	description += fmt.Sprintf("**Started:** %s\n", alert.StartsAt.Format(time.RFC3339))

	if classification != nil {
		description += "\n**AI Classification:**\n"
		description += fmt.Sprintf("- **Severity:** %s\n", classification.Severity)
		description += fmt.Sprintf("- **Confidence:** %.0f%%\n", classification.Confidence*100)
		description += fmt.Sprintf("- **Reasoning:** %s\n", classification.Reasoning)

		if len(classification.Recommendations) > 0 {
			description += "\n**Recommendations:**\n"
			for i, rec := range classification.Recommendations {
				if i >= 5 {
					break
				}
				description += fmt.Sprintf("%d. %s\n", i+1, rec)
			}
		}
	}

	// Add labels as tags
	description += "\n**Labels:**\n"
	for k, v := range alert.Labels {
		description += fmt.Sprintf("- %s: %s\n", k, v)
	}

	return map[string]any{
		"title":       title,
		"description": description,
		"severity":    severity,
		"status":      "started",
		"tags":        labelsToTags(alert.Labels),
		"environment": namespace,
		"started_at":  alert.StartsAt.Format(time.RFC3339),
	}, nil
}

// formatPagerDuty formats alert for PagerDuty Events API v2
func (f *DefaultAlertFormatter) formatPagerDuty(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

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

	// Build summary
	summary := fmt.Sprintf("[%s] %s", alert.AlertName, alert.Status)
	if classification != nil {
		summary += fmt.Sprintf(" - AI: %s (%.0f%%)", classification.Severity, classification.Confidence*100)
	}

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

	return map[string]any{
		"event_action": eventAction,
		"dedup_key":    alert.Fingerprint,
		"payload": map[string]any{
			"summary":        summary,
			"severity":       severity,
			"source":         "alert-history-service",
			"timestamp":      alert.StartsAt.Format(time.RFC3339),
			"custom_details": details,
		},
	}, nil
}

// formatSlack formats alert for Slack webhook with Blocks API
func (f *DefaultAlertFormatter) formatSlack(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert
	classification := enrichedAlert.Classification

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

	// Build header
	header := fmt.Sprintf("%s *%s* - %s", emoji, alert.AlertName, alert.Status)

	// Build text sections
	var blocks []map[string]any

	// Header block
	blocks = append(blocks, map[string]any{
		"type": "header",
		"text": map[string]any{
			"type": "plain_text",
			"text": header,
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
			recsText := "*Recommendations:*\n"
			for i, rec := range classification.Recommendations {
				if i >= 3 {
					break
				}
				recsText += fmt.Sprintf("• %s\n", rec)
			}

			blocks = append(blocks, map[string]any{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": recsText,
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

	return map[string]any{
		"blocks": blocks,
		"attachments": []map[string]any{
			{
				"color": color,
				"fields": fields,
			},
		},
	}, nil
}

// formatWebhook formats alert for generic webhook (simple JSON)
func (f *DefaultAlertFormatter) formatWebhook(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
	alert := enrichedAlert.Alert

	payload := map[string]any{
		"alert_name":  alert.AlertName,
		"fingerprint": alert.Fingerprint,
		"status":      string(alert.Status),
		"labels":      alert.Labels,
		"annotations": alert.Annotations,
		"starts_at":   alert.StartsAt.Format(time.RFC3339),
	}

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
		json.Unmarshal(classificationJSON, &classificationMap)
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
