# Default Notification Templates

**Package**: `github.com/ipiton/AMP/internal/notification/template/defaults`

---

## 📋 Overview

Default templates for Slack, PagerDuty, and Email notification receivers, providing an out-of-the-box experience compatible with Alertmanager's template syntax.

### Features

- **Slack Templates**: Messages with structured fields
- **PagerDuty Templates**: Incident descriptions with context
- **Email Templates**: HTML emails with plain-text fallback
- **Alertmanager Compatible**: Alertmanager template syntax
- **Type Safe**: Go structs with full type safety

---

## 🚀 Quick Start

### Basic Usage

```go
package main

import (
    "github.com/ipiton/AMP/internal/notification/template/defaults"
)

func main() {
    // Get all default templates
    registry := defaults.GetDefaultTemplates()

    // Access Slack templates
    slackTitle := registry.Slack.Title
    slackColor := registry.Slack.ColorFunc("critical") // Returns "danger"

    // Access PagerDuty templates
    pdDescription := registry.PagerDuty.Description
    pdSeverity := registry.PagerDuty.SeverityFunc("warning") // Returns "warning"

    // Access Email templates
    emailSubject := registry.Email.Subject
    emailHTML := registry.Email.HTML
    emailText := registry.Email.Text
}
```

### Validation

```go
// Validate all templates (useful for CI/CD)
if err := defaults.ValidateAllTemplates(); err != nil {
    log.Fatalf("Template validation failed: %v", err)
}
```

### Statistics

```go
// Get template statistics
stats := defaults.GetTemplateStats()
fmt.Printf("Total templates: %d\n",
    stats.SlackTemplateCount +
    stats.PagerDutyTemplateCount +
    stats.EmailTemplateCount)
fmt.Printf("Total size: %d bytes\n", stats.TotalSize)
```

---

## 📚 Templates Reference

### Slack Templates

#### Title
```go
defaults.DefaultSlackTitle
```
**Output**: `🔥 ALERT: HighCPU` or `✅ RESOLVED: HighCPU`

**Variables**:
- `.Status`: "firing" or "resolved"
- `.GroupLabels.alertname`: Alert name

#### Text
```go
defaults.DefaultSlackText
```
**Output**: `*3 alerts* in this group` or `CPU usage above 90% threshold`

**Variables**:
- `.Alerts`: Array of alerts
- `.Annotations.summary`: Alert summary

#### Pretext
```go
defaults.DefaultSlackPretext
```
**Output**: `Environment: *production* | Cluster: *us-east-1*`

**Variables**:
- `.CommonLabels.environment`: Environment name
- `.CommonLabels.cluster`: Cluster name

#### Fields (Single Alert)
```go
defaults.DefaultSlackFieldsSingle
```
**Output**: JSON array of field objects

**Variables**:
- `.CommonLabels.severity`: Alert severity
- `.CommonLabels.instance`: Instance identifier
- `.CommonAnnotations.description`: Alert description
- `.CommonAnnotations.runbook_url`: Runbook link

#### Fields (Multi Alert)
```go
defaults.DefaultSlackFieldsMulti
```
**Output**: JSON array with summary information

**Variables**:
- `.CommonLabels.severity`: Common severity
- `.CommonLabels.environment`: Environment
- `len .Alerts`: Alert count

#### Color Function
```go
color := defaults.GetSlackColor("critical") // Returns "danger"
```

**Mapping**:
- `critical`, `error` → `danger` (red)
- `warning` → `warning` (yellow)
- `info` → `good` (green)
- default → `#439FE0` (blue)

---

### PagerDuty Templates

#### Description
```go
defaults.DefaultPagerDutyDescription
```
**Output**: `[RESOLVED] HighCPU: CPU usage above threshold` (< 1024 chars)

**Variables**:
- `.Status`: "firing" or "resolved"
- `.GroupLabels.alertname`: Alert name
- `.CommonAnnotations.summary`: Alert summary

#### Details (Single Alert)
```go
defaults.DefaultPagerDutyDetailsSingle
```
**Output**: JSON object with detailed context

**Variables**:
- `.CommonLabels.*`: All common labels
- `.CommonAnnotations.*`: All common annotations
- `.GeneratorURL`: Source of the alert

#### Details (Multi Alert)
```go
defaults.DefaultPagerDutyDetailsMulti
```
**Output**: JSON object with summary information

**Variables**:
- `len .Alerts`: Alert count
- `.CommonLabels.*`: Common labels
- `.Status`: Alert status

#### Severity Function
```go
severity := defaults.GetPagerDutySeverity("critical") // Returns "critical"
```

**Mapping**:
- `critical` → `critical`
- `error` → `error`
- `warning` → `warning`
- `info`, default → `info`

---

### Email Templates

#### Subject
```go
defaults.DefaultEmailSubject
```
**Output**: `[ALERT] HighCPU (3 alerts)` or `[RESOLVED] HighCPU (1 alert)`

**Variables**:
- `.Status`: "firing" or "resolved"
- `.GroupLabels.alertname`: Alert name
- `len .Alerts`: Alert count

#### HTML Body
```go
defaults.DefaultEmailHTML
```
**Output**: Professional, responsive HTML email (< 100KB)

**Features**:
- Responsive design (mobile + desktop)
- Status-based header color (red/green)
- Alert table with all details
- Common labels section
- Runbook button
- Professional footer
- Inline CSS (email client compatible)

**Variables**: All TemplateData fields

#### Text Body
```go
defaults.DefaultEmailText
```
**Output**: Plain text fallback for email clients

**Variables**: All TemplateData fields

---

## 🎨 Template Data Model

All templates have access to the following data structure:

```go
type TemplateData struct {
    // Alert is the current alert being processed
    Alert *Alert

    // Labels are the alert's labels
    Labels map[string]string

    // Annotations are the alert's annotations
    Annotations map[string]string

    // GroupLabels are the labels used for grouping
    GroupLabels map[string]string

    // CommonLabels are labels common to all alerts in group
    CommonLabels map[string]string

    // CommonAnnotations are annotations common to all alerts
    CommonAnnotations map[string]string

    // ExternalURL is the Alertmanager external URL
    ExternalURL string

    // GeneratorURL is the source of the alert
    GeneratorURL string

    // Status is "firing" or "resolved"
    Status string

    // Receiver is the receiver name
    Receiver string
}
```

---

## 📊 Size Limits

| Receiver | Limit | Default Size | Status |
|----------|-------|--------------|--------|
| Slack | < 3000 chars | ~500 chars | ✅ Valid |
| PagerDuty Description | < 1024 chars | ~150 chars | ✅ Valid |
| Email HTML | < 100KB | ~10KB | ✅ Valid |

All templates are validated to ensure they stay within API limits.

---

## 🧪 Testing

### Run All Tests

```bash
go test ./internal/notification/template/defaults -v
```

### Run with Coverage

```bash
go test ./internal/notification/template/defaults -cover
```

### Run Benchmarks

```bash
go test ./internal/notification/template/defaults -bench=.
```

---

## 🔧 Customization

### Override Individual Templates

```go
registry := defaults.GetDefaultTemplates()

// Use default title but custom text
slackTitle := registry.Slack.Title
customText := "Custom alert message: {{ .Annotations.summary }}"
```

### Create Custom Registry

```go
customRegistry := &defaults.TemplateRegistry{
    Slack: &defaults.SlackTemplates{
        Title: "Custom: {{ .GroupLabels.alertname }}",
        Text:  registry.Slack.Text, // Use default
        // ... other fields
    },
    PagerDuty: registry.PagerDuty, // Use all defaults
    Email:     registry.Email,      // Use all defaults
}
```

---

## 🔗 Integration

### With the Template Engine (`internal/notification/template`)

```go
import (
    "github.com/ipiton/AMP/internal/notification/template"
    "github.com/ipiton/AMP/internal/notification/template/defaults"
)

// Get default templates
registry := defaults.GetDefaultTemplates()

// Create template engine
engine := template.NewNotificationTemplateEngine(...)

// Execute template
data := &template.TemplateData{...}
result, err := engine.Execute(ctx, registry.Slack.Title, data)
```

### With Receiver Configurations

```go
// Slack configuration
slackConfig := &routing.SlackConfig{
    APIURL:  "https://hooks.slack.com/...",
    Channel: "#alerts",
    Title:   registry.Slack.Title,    // Use default
    Text:    registry.Slack.Text,     // Use default
    Color:   "{{ .CommonLabels.severity | slackColor }}",
}

// PagerDuty configuration
pdConfig := &routing.PagerDutyConfig{
    RoutingKey:  "your-routing-key",
    Description: registry.PagerDuty.Description, // Use default
    Severity:    "{{ .CommonLabels.severity | pdSeverity }}",
}

// Email configuration
emailConfig := &routing.EmailConfig{
    To:      "team@example.com",
    Subject: registry.Email.Subject, // Use default
    HTML:    registry.Email.HTML,    // Use default
    Text:    registry.Email.Text,    // Use default
}
```

---

## 📝 Examples

### Example 1: Single Firing Alert

**Input Data**:
```go
data := &TemplateData{
    Status: "firing",
    GroupLabels: map[string]string{
        "alertname": "HighCPU",
    },
    CommonLabels: map[string]string{
        "severity":    "critical",
        "environment": "production",
        "instance":    "web-01.example.com",
    },
    CommonAnnotations: map[string]string{
        "summary":     "CPU usage is 95%",
        "description": "CPU usage has been above 90% for 5 minutes",
    },
    Alerts: []*Alert{{...}}, // 1 alert
}
```

**Slack Output**:
- Title: `🔥 ALERT: HighCPU`
- Text: `CPU usage is 95%`
- Color: `danger` (red)

**PagerDuty Output**:
- Description: `HighCPU: CPU usage is 95%`
- Severity: `critical`

**Email Output**:
- Subject: `[ALERT] HighCPU (1 alert)`
- HTML: Professional red-themed email with alert details

### Example 2: Multiple Resolved Alerts

**Input Data**:
```go
data := &TemplateData{
    Status: "resolved",
    GroupLabels: map[string]string{
        "alertname": "HighMemory",
    },
    Alerts: []*Alert{{...}, {...}, {...}}, // 3 alerts
}
```

**Slack Output**:
- Title: `✅ RESOLVED: HighMemory`
- Text: `*3 alerts* in this group`
- Color: `good` (green)

**Email Output**:
- Subject: `[RESOLVED] HighMemory (3 alerts)`
- HTML: Professional green-themed email with 3-row alert table

---

## 🛠️ Troubleshooting

### Template Size Exceeded

**Problem**: Slack message > 3000 chars

**Solution**:
```go
// Validate before sending
if !defaults.ValidateSlackMessageSize(title, text, pretext, fields) {
    // Truncate or use summary template
}
```

### Missing Template Variables

**Problem**: Template references undefined variable

**Solution**: Provide default values
```go
{{ .CommonLabels.environment | default "unknown" }}
```

### Template Syntax Errors

**Problem**: Template fails to parse

**Solution**: Validate templates at startup
```go
if err := defaults.ValidateAllTemplates(); err != nil {
    log.Fatal(err)
}
```

---

## 📦 Package Structure

```
defaults/
├── slack.go           # Slack templates and helpers
├── slack_test.go      # Slack tests
├── pagerduty.go       # PagerDuty templates and helpers
├── pagerduty_test.go  # PagerDuty tests
├── email.go           # Email templates and helpers
├── email_test.go      # Email tests
├── defaults.go        # Template registry
├── defaults_test.go   # Registry tests
└── README.md          # This file
```

---

## 📚 Related Documentation

- [Template Engine](../README.md) - Core template engine

---

## 🤝 Contributing

When adding new templates:

1. Add template constant to appropriate file (slack.go, pagerduty.go, email.go)
2. Add to template struct
3. Update GetDefault*Templates() function
4. Add comprehensive tests
5. Update this README
6. Validate size limits

---

## 📄 License

Part of AMP. See `LICENSE` in the repository root.
