// Package main demonstrates how to implement a custom alert publisher.
//
// This example shows:
//   - How to implement the AlertPublisher interface
//   - How to integrate with Alert History Service
//   - How to format alerts for custom targets
//   - How to handle errors and retries
//
// Custom publishers can send alerts to:
//   - Internal systems (ticketing, ITSM)
//   - Communication platforms (MS Teams, Discord)
//   - Monitoring tools (Datadog, New Relic)
//   - Custom webhooks
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ipiton/AMP/pkg/core/domain"
)

// ================================================================================
// Custom MS Teams Publisher Example
// ================================================================================

// MSTeamsPublisher implements AlertPublisher for Microsoft Teams.
//
// This publisher:
//   - Formats alerts as Adaptive Cards
//   - Posts to MS Teams webhooks
//   - Supports rich formatting (colors, buttons, etc.)
//   - Handles retries and error cases
type MSTeamsPublisher struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewMSTeamsPublisher creates a new MS Teams publisher.
func NewMSTeamsPublisher(timeout time.Duration) *MSTeamsPublisher {
	return &MSTeamsPublisher{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// Name returns the publisher name (required by interface).
func (p *MSTeamsPublisher) Name() string {
	return "ms-teams"
}

// Type returns the publisher type (required by interface).
func (p *MSTeamsPublisher) Type() string {
	return "teams"
}

// Publish sends alert to MS Teams (required by interface).
func (p *MSTeamsPublisher) Publish(ctx context.Context, alert *domain.EnrichedAlert, target *PublishingTarget) error {
	// Format alert as Adaptive Card
	card := p.formatAsAdaptiveCard(alert)

	// Marshall to JSON
	payload, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("failed to marshal adaptive card: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", target.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MS Teams webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// Health checks publisher health (required by interface).
func (p *MSTeamsPublisher) Health(ctx context.Context) error {
	// MS Teams webhooks don't have a dedicated health endpoint
	// We consider it healthy if HTTP client is working
	return nil
}

// Shutdown gracefully shuts down publisher (required by interface).
func (p *MSTeamsPublisher) Shutdown(ctx context.Context) error {
	// Close HTTP client connections
	p.httpClient.CloseIdleConnections()
	return nil
}

// ================================================================================
// MS Teams Adaptive Card Formatting
// ================================================================================

// AdaptiveCard represents an MS Teams Adaptive Card.
//
// Adaptive Cards are a platform-agnostic way to create rich,
// interactive cards for Teams, Outlook, and other Microsoft products.
//
// Spec: https://adaptivecards.io/explorer/
type AdaptiveCard struct {
	Type        string      `json:"type"`
	Version     string      `json:"version"`
	Body        []CardElement `json:"body"`
	Actions     []CardAction  `json:"actions,omitempty"`
}

// CardElement represents a card element (text, image, etc.).
type CardElement struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Size   string `json:"size,omitempty"`
	Weight string `json:"weight,omitempty"`
	Color  string `json:"color,omitempty"`
	Wrap   bool   `json:"wrap,omitempty"`
}

// CardAction represents an action button.
type CardAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// formatAsAdaptiveCard converts alert to MS Teams Adaptive Card.
func (p *MSTeamsPublisher) formatAsAdaptiveCard(alert *domain.EnrichedAlert) *AdaptiveCard {
	// Determine color based on severity
	color := p.severityColor(alert.EffectiveSeverity())

	// Build card elements
	elements := []CardElement{
		// Title
		{
			Type:   "TextBlock",
			Text:   fmt.Sprintf("🚨 Alert: %s", alert.Alert.AlertName),
			Size:   "large",
			Weight: "bolder",
			Color:  color,
		},
		// Status
		{
			Type: "TextBlock",
			Text: fmt.Sprintf("**Status**: %s", alert.Alert.Status),
			Wrap: true,
		},
	}

	// Add severity if available
	if alert.HasClassification() {
		elements = append(elements, CardElement{
			Type: "TextBlock",
			Text: fmt.Sprintf("**Severity**: %s (confidence: %.0f%%)",
				alert.Classification.Severity,
				alert.Classification.Confidence*100),
			Wrap: true,
		})
	}

	// Add summary from annotations
	if summary, ok := alert.Alert.Annotations["summary"]; ok {
		elements = append(elements, CardElement{
			Type: "TextBlock",
			Text: fmt.Sprintf("**Summary**: %s", summary),
			Wrap: true,
		})
	}

	// Add labels
	labelsText := ""
	for k, v := range alert.Alert.Labels {
		labelsText += fmt.Sprintf("• %s: %s\n", k, v)
	}
	elements = append(elements, CardElement{
		Type: "TextBlock",
		Text: "**Labels**:\n" + labelsText,
		Wrap: true,
	})

	// Add recommendations if available
	if alert.HasClassification() && len(alert.Classification.Recommendations) > 0 {
		recText := ""
		for _, rec := range alert.Classification.Recommendations {
			recText += fmt.Sprintf("• %s\n", rec)
		}
		elements = append(elements, CardElement{
			Type: "TextBlock",
			Text: "**Recommendations**:\n" + recText,
			Wrap: true,
		})
	}

	// Add timestamp
	elements = append(elements, CardElement{
		Type: "TextBlock",
		Text: fmt.Sprintf("**Started**: %s", alert.Alert.StartsAt.Format(time.RFC3339)),
		Wrap: true,
	})

	// Build actions (buttons)
	actions := []CardAction{}

	// Add runbook link if available
	if runbook, ok := alert.Alert.Annotations["runbook_url"]; ok {
		actions = append(actions, CardAction{
			Type:  "Action.OpenUrl",
			Title: "📖 View Runbook",
			URL:   runbook,
		})
	}

	// Add dashboard link if available
	if dashboard, ok := alert.Alert.Annotations["dashboard_url"]; ok {
		actions = append(actions, CardAction{
			Type:  "Action.OpenUrl",
			Title: "📊 View Dashboard",
			URL:   dashboard,
		})
	}

	// Build complete card
	return &AdaptiveCard{
		Type:    "AdaptiveCard",
		Version: "1.4",
		Body:    elements,
		Actions: actions,
	}
}

// severityColor returns MS Teams color for severity.
func (p *MSTeamsPublisher) severityColor(severity domain.AlertSeverity) string {
	switch severity {
	case domain.SeverityCritical:
		return "attention" // Red
	case domain.SeverityWarning:
		return "warning" // Yellow
	case domain.SeverityInfo:
		return "good" // Green
	default:
		return "default" // Gray
	}
}

// ================================================================================
// Publishing Target Configuration
// ================================================================================

// PublishingTarget defines where to publish alerts.
type PublishingTarget struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	WebhookURL string            `json:"webhook_url"`
	Headers    map[string]string `json:"headers"`
	Enabled    bool              `json:"enabled"`
}

// ================================================================================
// Usage Example
// ================================================================================

func main() {
	// Create publisher
	publisher := NewMSTeamsPublisher(10 * time.Second)

	// Example enriched alert
	alert := &domain.EnrichedAlert{
		Alert: &domain.Alert{
			Fingerprint: "abc123",
			AlertName:   "HighCPU",
			Status:      domain.StatusFiring,
			Labels: map[string]string{
				"alertname": "HighCPU",
				"severity":  "critical",
				"namespace": "production",
				"instance":  "server-01",
			},
			Annotations: map[string]string{
				"summary":       "CPU usage above 90%",
				"runbook_url":   "https://runbooks.example.com/high-cpu",
				"dashboard_url": "https://grafana.example.com/d/cpu",
			},
			StartsAt: time.Now(),
		},
		Classification: &domain.ClassificationResult{
			Severity:   domain.SeverityCritical,
			Confidence: 0.95,
			Recommendations: []string{
				"Check for memory leaks",
				"Review recent deployments",
			},
		},
		ProcessingTimestamp: time.Now(),
	}

	// Publishing target
	target := &PublishingTarget{
		Name:       "ops-team",
		Type:       "teams",
		WebhookURL: "https://outlook.office.com/webhook/YOUR-WEBHOOK-URL",
		Enabled:    true,
	}

	// Publish alert
	ctx := context.Background()
	err := publisher.Publish(ctx, alert, target)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("✅ Alert published to MS Teams!")
}

// ================================================================================
// Integration with Alert History Service
// ================================================================================
//
// To integrate your custom publisher:
//
// 1. Implement interfaces.AlertPublisher interface (see above)
//
// 2. Register your publisher in main.go:
//
//    publisher := NewMSTeamsPublisher(10 * time.Second)
//    registry.Register("ms-teams", publisher)
//
// 3. Configure publishing targets:
//
//    config.yml:
//      publishing:
//        targets:
//          - name: ops-team
//            type: ms-teams
//            webhook_url: https://outlook.office.com/webhook/...
//            enabled: true
//            filters:
//              - field: severity
//                op: eq
//                value: critical
//
// 4. Deploy and monitor:
//
//    - Monitor publishing success rate
//    - Track publishing latency
//    - Set up alerts for publishing failures
//    - Monitor webhook health
//
// That's it! Alerts will now be published to MS Teams.
//
// ================================================================================
// Advanced Features
// ================================================================================
//
// You can extend this example with:
//
// 1. **Retry Logic**:
//    - Implement exponential backoff
//    - Retry on transient failures
//    - Dead letter queue for failed messages
//
// 2. **Batching**:
//    - Group multiple alerts into single card
//    - Reduce webhook calls
//    - Better UX for high-volume scenarios
//
// 3. **Threading**:
//    - Use MS Teams threading for related alerts
//    - Group alerts by fingerprint
//    - Provide update notifications
//
// 4. **Interactive Actions**:
//    - Add "Acknowledge" button
//    - Add "Silence" button
//    - Add "Escalate" button
//    - Callback webhooks for actions
//
// 5. **Metrics**:
//    - Track publish latency
//    - Track success/failure rates
//    - Track webhook response times
//    - Alert on publisher degradation
