# Notification Template Engine

**Package**: `internal/notification/template`
**Purpose**: Go text/template engine for notification messages (Slack, PagerDuty, Email)

---

## 📖 Overview

Template engine for processing Go `text/template` in notification receiver configs, providing:

- **Template Functions**: Alertmanager-compatible functions for formatting
- **LRU Caching**: parsed templates cached with SHA256 keys
- **Parallel Execution**: ExecuteMultiple() for batch processing
- **Thread-Safe**: Safe for concurrent use
- **Graceful Degradation**: Fallback to raw template on errors
- **Hot Reload**: Cache invalidation on SIGHUP

---

## 🚀 Quick Start

### 1. Create Engine

```go
import "github.com/ipiton/AMP/internal/notification/template"

// Production mode (default settings)
engine, err := template.NewNotificationTemplateEngine(
    template.DefaultTemplateEngineOptions(),
)
```

### 2. Prepare Template Data

```go
data := template.NewTemplateData(
    "firing",
    map[string]string{
        "alertname": "HighCPU",
        "severity":  "critical",
        "instance":  "prod-1",
    },
    map[string]string{
        "summary":     "CPU usage is high",
        "description": "CPU > 90% for 5 minutes",
    },
    time.Now(),
)
```

### 3. Execute Template

```go
ctx := context.Background()
tmpl := "🔥 {{ .GroupLabels.alertname }} - {{ .Status | toUpper }}"
result, err := engine.Execute(ctx, tmpl, data)
// result: "🔥 HighCPU - FIRING"
```

---

## 📚 Template Functions

### Time Functions

```go
{{ .StartsAt | humanizeTimestamp }}  // "2 hours ago"
{{ .StartsAt | since }}              // "2h 30m"
{{ .StartsAt | date "2006-01-02" }}  // "2025-11-22"
{{ .Duration | humanizeDuration }}   // "1h 30m"
```

### String Functions

```go
{{ .Labels.alertname | toUpper }}           // "HIGHCPU"
{{ .Annotations.description | truncate 50 }} // "CPU usage is..."
{{ .Labels | sortedPairs | join ", " }}     // "alertname=HighCPU, severity=critical"
```

### Math Functions

```go
{{ .Value | humanize }}      // "1.23k"
{{ .Value | humanize1024 }}  // "1.2 KiB"
{{ add .Value 10 }}          // arithmetic
{{ round .Value }}           // rounding
```

### Conditional Functions

```go
{{ .Labels.severity | default "unknown" }}
{{ if empty .Annotations.runbook_url }}No runbook{{ end }}
{{ ternary "CRITICAL" "OK" (gt .Value 100) }}
```

### URL Functions

```go
{{ .Labels.instance | urlEncode }}
{{ .ExternalURL | pathJoin "/alerts" .Fingerprint }}
```

### Collection Functions

```go
{{ .Labels | sortAlpha }}
{{ .Labels | reverse }}
{{ .Labels | uniq }}
```

### Encoding Functions

```go
{{ .Labels.alertname | b64enc }}
{{ .Labels | toJson }}
{{ .Labels | toPrettyJson }}
```

---

## 🔗 Receiver Integration

### Slack

```go
config := &template.SlackConfig{
    Title: "🔥 {{ .GroupLabels.alertname }} - {{ .Status }}",
    Text: `*Severity*: {{ .Labels.severity }}
*Instance*: {{ .Labels.instance }}
*Started*: {{ .StartsAt | humanizeTimestamp }}`,
    Fields: []*template.SlackField{
        {
            Title: "Value",
            Value: "{{ .Value | humanize }}",
        },
    },
}

processed, err := template.ProcessSlackConfig(ctx, engine, config, data)
```

### PagerDuty

```go
config := &template.PagerDutyConfig{
    Summary: "{{ .Labels.severity | toUpper }}: {{ .GroupLabels.alertname }}",
    Details: map[string]string{
        "instance": "{{ .Labels.instance }}",
        "value":    "{{ .Value | humanize }}",
        "started":  "{{ .StartsAt | humanizeTimestamp }}",
    },
}

processed, err := template.ProcessPagerDutyConfig(ctx, engine, config, data)
```

### Email

```go
config := &template.EmailConfig{
    Subject: "[{{ .Labels.severity }}] {{ .GroupLabels.alertname }}",
    Body: `Alert: {{ .GroupLabels.alertname }}
Status: {{ .Status }}
Started: {{ .StartsAt | date "2006-01-02 15:04:05" }}

{{ .Annotations.description }}`,
}

processed, err := template.ProcessEmailConfig(ctx, engine, config, data)
```

---

## ⚡ Performance

Benchmarks live in `benchmarks_test.go`; run `go test ./internal/notification/template -bench=. -benchmem` for current numbers.

### Cache Statistics

```go
stats := engine.GetCacheStats()
fmt.Printf("Hit ratio: %.2f%%\n", stats.HitRatio*100)
fmt.Printf("Cache size: %d\n", stats.Size)
```

---

## 🔄 Hot Reload

```go
// On SIGHUP signal
engine.InvalidateCache()
```

Cache is automatically invalidated when config is reloaded, ensuring templates are re-parsed with updated configuration.

---

## 🛡️ Error Handling

### Parse Errors

```go
result, err := engine.Execute(ctx, "{{ .Invalid", data)
if template.IsParseError(err) {
    // Handle parse error
}
```

### Execution Errors

```go
result, err := engine.Execute(ctx, "{{ .NonExistent }}", data)
if template.IsExecuteError(err) {
    // Handle execution error
}
// With FallbackOnError=true, returns raw template
```

### Timeout Errors

```go
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()

result, err := engine.Execute(ctx, slowTemplate, data)
if template.IsTimeoutError(err) {
    // Handle timeout
}
```

---

## 📊 Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                NotificationTemplateEngine                    │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  Template Parser                                      │  │
│  │  • Parse Go text/template                             │  │
│  │  • Validate syntax                                    │  │
│  │  • Cache parsed templates (LRU, SHA256 keys)         │  │
│  └───────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  Template Executor                                    │  │
│  │  • Execute with TemplateData                          │  │
│  │  • Apply custom functions                             │  │
│  │  • Handle errors gracefully                           │  │
│  │  • Context timeout support (5s)                       │  │
│  └───────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  Function Library                                     │  │
│  │  • Time: humanizeTimestamp, since, date               │  │
│  │  • String: toUpper, truncate, join                    │  │
│  │  • Math: humanize, add, round                         │  │
│  │  • Conditional: default, empty, ternary               │  │
│  │  • Sprig integration for extended functions           │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## 📁 Package Structure

```
internal/notification/template/
├── engine.go          # NotificationTemplateEngine interface + impl
├── data.go            # TemplateData struct
├── alert.go           # Alert data helpers
├── functions.go       # Template functions
├── cache.go           # LRU template cache
├── errors.go          # Error types
├── integration.go     # Receiver integration helpers
├── defaults/          # Default receiver templates
└── README.md          # This file
```

---

## 🔧 Configuration

```go
opts := template.TemplateEngineOptions{
    CacheSize:        1000,              // Max cached templates
    ExecutionTimeout: 5 * time.Second,   // Max execution time
    FallbackOnError:  true,              // Return raw template on error
    Logger:           slog.Default(),    // Structured logger
}

engine, err := template.NewNotificationTemplateEngine(opts)
```

---

## 📝 Examples

### Example 1: Simple Alert Title

```go
tmpl := "{{ .GroupLabels.alertname }} is {{ .Status }}"
data := template.NewTemplateData("firing",
    map[string]string{"alertname": "HighCPU"},
    nil, time.Now())

result, _ := engine.Execute(ctx, tmpl, data)
// result: "HighCPU is firing"
```

### Example 2: Formatted Slack Message

```go
tmpl := `🔥 *{{ .GroupLabels.alertname }}* - {{ .Status | toUpper }}

*Severity*: {{ .Labels.severity | default "unknown" }}
*Instance*: {{ .Labels.instance }}
*Started*: {{ .StartsAt | humanizeTimestamp }}
*Value*: {{ .Value | humanize }}

{{ if .Annotations.runbook_url }}
📖 [Runbook]({{ .Annotations.runbook_url }})
{{ end }}`

result, _ := engine.Execute(ctx, tmpl, data)
```

### Example 3: PagerDuty Incident Details

```go
tmpl := `Alert: {{ .GroupLabels.alertname }}
Severity: {{ .Labels.severity | toUpper }}
Instance: {{ .Labels.instance }}
Value: {{ .Value | humanize }}
Duration: {{ .Duration | humanizeDuration }}
Started: {{ .StartsAt | date "2006-01-02 15:04:05" }}`

result, _ := engine.Execute(ctx, tmpl, data)
```

---

## 🧪 Testing

```go
func TestTemplateExecution(t *testing.T) {
    engine, _ := template.NewNotificationTemplateEngine(
        template.DefaultTemplateEngineOptions(),
    )

    data := template.NewTemplateData("firing",
        map[string]string{"alertname": "HighCPU"},
        nil, time.Now())

    result, err := engine.Execute(context.Background(),
        "{{ .Labels.alertname | toUpper }}", data)

    assert.NoError(t, err)
    assert.Equal(t, "HIGHCPU", result)
}
```

---

## 🔒 Security

- **Sandboxed Execution**: Templates cannot access filesystem or network
- **Timeout Protection**: 5s max execution time per template
- **No Arbitrary Code**: Only predefined functions allowed
- **Input Validation**: Template data validated before execution

---

## 📚 Additional Resources

- [Alertmanager Templates](https://prometheus.io/docs/alerting/latest/notifications/) - Upstream documentation
