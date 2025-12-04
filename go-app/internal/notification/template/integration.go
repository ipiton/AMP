package template

import (
	"context"
	"fmt"
)

// ================================================================================
// TN-153: Template Engine - Receiver Integration
// ================================================================================
// Integration helpers for processing templates in receiver configs.
//
// Supported Receivers:
// - Slack: Title, Text, Pretext, Fields
// - PagerDuty: Summary, Details
// - Email: Subject, Body (FUTURE - TN-154)
// - Webhook: Custom fields
//
// Features:
// - Parallel template execution
// - Error handling per field
// - Backward compatibility (non-template strings work as-is)
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// SlackConfig represents Slack receiver configuration
// This is a simplified version - actual implementation in routing package
type SlackConfig struct {
	Title    string
	Text     string
	Pretext  string
	Fields   []*SlackField
	Username string
	Channel  string
}

// SlackField represents a Slack message field
type SlackField struct {
	Title string
	Value string
	Short bool
}

// PagerDutyConfig represents PagerDuty receiver configuration
type PagerDutyConfig struct {
	Summary string
	Details map[string]string
}

// EmailConfig represents Email receiver configuration (FUTURE - TN-154)
type EmailConfig struct {
	Subject string
	Body    string
	To      []string
}

// WebhookConfig represents generic webhook configuration
type WebhookConfig struct {
	URL    string
	Fields map[string]string
}

// ProcessSlackConfig renders templates in SlackConfig.
//
// Processes:
// - Title: Main message title
// - Text: Message body
// - Pretext: Text above message
// - Fields: Array of title-value pairs
//
// Parameters:
//   - ctx: Context with timeout
//   - engine: Template engine
//   - config: Slack configuration
//   - data: Template data
//
// Returns:
//   - *SlackConfig: Processed configuration with rendered templates
//   - error: If any template fails
//
// Example:
//
//	config := &SlackConfig{
//	    Title: "🔥 {{ .GroupLabels.alertname }} - {{ .Status }}",
//	    Text:  "*Severity*: {{ .Labels.severity }}",
//	}
//	processed, err := ProcessSlackConfig(ctx, engine, config, data)
func ProcessSlackConfig(
	ctx context.Context,
	engine NotificationTemplateEngine,
	config *SlackConfig,
	data *TemplateData,
) (*SlackConfig, error) {
	if config == nil {
		return nil, fmt.Errorf("slack config is nil")
	}

	// Clone config to avoid mutation
	processed := &SlackConfig{
		Username: config.Username,
		Channel:  config.Channel,
	}

	// Collect templates to execute
	templates := make(map[string]string)
	if config.Title != "" {
		templates["title"] = config.Title
	}
	if config.Text != "" {
		templates["text"] = config.Text
	}
	if config.Pretext != "" {
		templates["pretext"] = config.Pretext
	}

	// Execute templates in parallel
	if len(templates) > 0 {
		results, err := engine.ExecuteMultiple(ctx, templates, data)
		if err != nil {
			return nil, fmt.Errorf("failed to render slack templates: %w", err)
		}

		processed.Title = results["title"]
		processed.Text = results["text"]
		processed.Pretext = results["pretext"]
	}

	// Process fields
	if len(config.Fields) > 0 {
		processed.Fields = make([]*SlackField, len(config.Fields))
		for i, field := range config.Fields {
			processedField := &SlackField{
				Short: field.Short,
			}

			// Render field title
			if field.Title != "" {
				title, err := engine.Execute(ctx, field.Title, data)
				if err != nil {
					return nil, fmt.Errorf("failed to render field[%d].title: %w", i, err)
				}
				processedField.Title = title
			}

			// Render field value
			if field.Value != "" {
				value, err := engine.Execute(ctx, field.Value, data)
				if err != nil {
					return nil, fmt.Errorf("failed to render field[%d].value: %w", i, err)
				}
				processedField.Value = value
			}

			processed.Fields[i] = processedField
		}
	}

	return processed, nil
}

// ProcessPagerDutyConfig renders templates in PagerDutyConfig.
//
// Processes:
// - Summary: Incident summary
// - Details: Key-value details map
//
// Parameters:
//   - ctx: Context with timeout
//   - engine: Template engine
//   - config: PagerDuty configuration
//   - data: Template data
//
// Returns:
//   - *PagerDutyConfig: Processed configuration
//   - error: If any template fails
//
// Example:
//
//	config := &PagerDutyConfig{
//	    Summary: "{{ .Labels.severity | toUpper }}: {{ .GroupLabels.alertname }}",
//	    Details: map[string]string{
//	        "instance": "{{ .Labels.instance }}",
//	        "value":    "{{ .Value | humanize }}",
//	    },
//	}
//	processed, err := ProcessPagerDutyConfig(ctx, engine, config, data)
func ProcessPagerDutyConfig(
	ctx context.Context,
	engine NotificationTemplateEngine,
	config *PagerDutyConfig,
	data *TemplateData,
) (*PagerDutyConfig, error) {
	if config == nil {
		return nil, fmt.Errorf("pagerduty config is nil")
	}

	processed := &PagerDutyConfig{}

	// Render summary
	if config.Summary != "" {
		summary, err := engine.Execute(ctx, config.Summary, data)
		if err != nil {
			return nil, fmt.Errorf("failed to render summary: %w", err)
		}
		processed.Summary = summary
	}

	// Render details map
	if config.Details != nil && len(config.Details) > 0 {
		// Execute all detail templates in parallel
		results, err := engine.ExecuteMultiple(ctx, config.Details, data)
		if err != nil {
			return nil, fmt.Errorf("failed to render details: %w", err)
		}
		processed.Details = results
	}

	return processed, nil
}

// ProcessEmailConfig renders templates in EmailConfig.
//
// Processes:
// - Subject: Email subject line
// - Body: Email body (plain text or HTML)
//
// Parameters:
//   - ctx: Context with timeout
//   - engine: Template engine
//   - config: Email configuration
//   - data: Template data
//
// Returns:
//   - *EmailConfig: Processed configuration
//   - error: If any template fails
//
// Example:
//
//	config := &EmailConfig{
//	    Subject: "[{{ .Labels.severity }}] {{ .GroupLabels.alertname }}",
//	    Body: `Alert: {{ .GroupLabels.alertname }}
//	Status: {{ .Status }}
//	Started: {{ .StartsAt | date "2006-01-02 15:04:05" }}`,
//	}
//	processed, err := ProcessEmailConfig(ctx, engine, config, data)
func ProcessEmailConfig(
	ctx context.Context,
	engine NotificationTemplateEngine,
	config *EmailConfig,
	data *TemplateData,
) (*EmailConfig, error) {
	if config == nil {
		return nil, fmt.Errorf("email config is nil")
	}

	processed := &EmailConfig{
		To: config.To,
	}

	// Collect templates
	templates := make(map[string]string)
	if config.Subject != "" {
		templates["subject"] = config.Subject
	}
	if config.Body != "" {
		templates["body"] = config.Body
	}

	// Execute templates in parallel
	if len(templates) > 0 {
		results, err := engine.ExecuteMultiple(ctx, templates, data)
		if err != nil {
			return nil, fmt.Errorf("failed to render email templates: %w", err)
		}

		processed.Subject = results["subject"]
		processed.Body = results["body"]
	}

	return processed, nil
}

// ProcessWebhookConfig renders templates in WebhookConfig.
//
// Processes:
// - Fields: Custom key-value fields
//
// Parameters:
//   - ctx: Context with timeout
//   - engine: Template engine
//   - config: Webhook configuration
//   - data: Template data
//
// Returns:
//   - *WebhookConfig: Processed configuration
//   - error: If any template fails
//
// Example:
//
//	config := &WebhookConfig{
//	    URL: "https://webhook.site/xxx",
//	    Fields: map[string]string{
//	        "alert":    "{{ .GroupLabels.alertname }}",
//	        "severity": "{{ .Labels.severity }}",
//	    },
//	}
//	processed, err := ProcessWebhookConfig(ctx, engine, config, data)
func ProcessWebhookConfig(
	ctx context.Context,
	engine NotificationTemplateEngine,
	config *WebhookConfig,
	data *TemplateData,
) (*WebhookConfig, error) {
	if config == nil {
		return nil, fmt.Errorf("webhook config is nil")
	}

	processed := &WebhookConfig{
		URL: config.URL,
	}

	// Render fields map
	if config.Fields != nil && len(config.Fields) > 0 {
		results, err := engine.ExecuteMultiple(ctx, config.Fields, data)
		if err != nil {
			return nil, fmt.Errorf("failed to render webhook fields: %w", err)
		}
		processed.Fields = results
	}

	return processed, nil
}

// IsTemplateString checks if string contains template syntax.
//
// Simple heuristic: checks for {{ and }} markers.
// Used for backward compatibility - non-template strings are passed through as-is.
//
// Parameters:
//   - s: String to check
//
// Returns:
//   - bool: True if string appears to be a template
//
// Example:
//
//	IsTemplateString("{{ .Labels.alertname }}")  // true
//	IsTemplateString("Static text")              // false
func IsTemplateString(s string) bool {
	return len(s) > 0 && (contains(s, "{{") && contains(s, "}}"))
}

// contains checks if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexString(s, substr) >= 0
}

// indexString returns index of substring in string
func indexString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// ProcessTemplateOrPassthrough processes string as template if it contains template syntax,
// otherwise returns it as-is.
//
// This provides backward compatibility for configs that don't use templates.
//
// Parameters:
//   - ctx: Context with timeout
//   - engine: Template engine
//   - s: String to process
//   - data: Template data
//
// Returns:
//   - string: Rendered template or original string
//   - error: If template processing fails
//
// Example:
//
//	// Template string
//	result, _ := ProcessTemplateOrPassthrough(ctx, engine, "{{ .Labels.alertname }}", data)
//	// result: "HighCPU"
//
//	// Non-template string
//	result, _ := ProcessTemplateOrPassthrough(ctx, engine, "Static alert", data)
//	// result: "Static alert"
func ProcessTemplateOrPassthrough(
	ctx context.Context,
	engine NotificationTemplateEngine,
	s string,
	data *TemplateData,
) (string, error) {
	if !IsTemplateString(s) {
		return s, nil
	}
	return engine.Execute(ctx, s, data)
}
