# Prometheus Alert Parser

## 📋 Overview

The **Prometheus Alert Parser** parses Prometheus alert webhooks. It integrates with AMP's webhook ingestion pipeline, supporting both Prometheus v1 (array) and v2 (grouped) alert formats.

### Key Capabilities

- **Multi-Format Support**: Prometheus v1 (direct arrays), v2 (grouped alerts), and Alertmanager webhooks
- **Auto-Detection**: Format detection based on payload structure
- **Thread-Safe**: Concurrent parsing
- **Validation**: Prometheus-specific validation rules (label names, state enums, timestamps)
- **Fingerprint Generation**: Deterministic SHA256 hash for alert deduplication
- **Strategy Pattern**: Dynamic parser selection via `UniversalWebhookHandler`

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Webhook Ingestion Flow                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  POST /webhook                                                    │
│       │                                                           │
│       ├─▶ WebhookDetector ─────────┐                            │
│       │   (Format Detection)        │                            │
│       │                             │                            │
│       │   ┌─────────────────────────┼─────────────────────┐     │
│       │   │ Detected Type           ▼                     │     │
│       │   │                                               │     │
│       ├───┼─▶ Alertmanager ──▶ AlertmanagerParser ────┐  │     │
│       │   │                                            │  │     │
│       ├───┼─▶ Prometheus v1 ──▶ PrometheusParser ────┤  │     │
│       │   │                                            │  │     │
│       ├───┼─▶ Prometheus v2 ──▶ PrometheusParser ────┤  │     │
│       │   │   (grouped)                                │  │     │
│       │   │                                            │  │     │
│       │   └────────────────────────────────────────────┘  │     │
│       │                                                    │     │
│       ├─▶ Parse JSON ──▶ Validate ──▶ ConvertToDomain ──┤     │
│       │                                                    │     │
│       └─▶ []*core.Alert ─────────────────────────────────┘     │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### Components

1. **PrometheusAlert**: Data model for individual alerts
2. **PrometheusAlertGroup**: Data model for grouped alerts (v2 format)
3. **PrometheusWebhook**: Unified structure for both v1 and v2 formats
4. **PrometheusParser**: Parser implementation (`WebhookParser` interface)
5. **PrometheusFormatDetector**: Format detection (v1 vs v2)
6. **WebhookValidator**: Prometheus-specific validation rules
7. **UniversalWebhookHandler**: Strategy pattern for dynamic parser selection

---

## ✨ Features

### Format Support

| Format | Structure | Detection | Support |
|--------|-----------|-----------|---------|
| **Prometheus v1** | `[{...alert...}]` | Array + `state`, `activeAt`, `generatorURL` | ✅ Full |
| **Prometheus v2** | `{"groups": [{...}]}` | Object + `groups` array | ✅ Full |
| **Alertmanager** | `{"alerts": [...]}` | Object + `receiver`, `version` | ✅ Backward compatible |

### Field Mapping

**Prometheus → Core.Alert:**

| Prometheus Field | Core.Alert Field | Transformation |
|-----------------|------------------|----------------|
| `labels.alertname` | `AlertName` | Direct copy |
| `labels.*` | `Labels` | Direct copy (all labels) |
| `annotations.*` | `Annotations` | Direct copy (all annotations) |
| `state` | `Status` | `firing`/`pending` → `firing`, `inactive` → `resolved` |
| `activeAt` | `StartsAt` | RFC3339 timestamp |
| `endsAt` | `EndsAt` | Optional timestamp |
| `generatorURL` | `GeneratorURL` | Direct copy |
| `fingerprint` | `Fingerprint` | Generated via SHA256 if missing |

### State Mapping

```go
// Prometheus state → core.AlertStatus
firing   → firing
pending  → firing   // Conservative: treat as firing
inactive → resolved
unknown  → firing   // Conservative: default to firing
```

### Validation Rules

1. **Required Fields**:
   - `labels.alertname` (non-empty)
   - `activeAt` (valid RFC3339 timestamp)
   - `generatorURL` (valid URL)

2. **Label Names**:
   - Must match `[a-zA-Z_][a-zA-Z0-9_]*` (Prometheus convention)
   - No empty label names or values

3. **State Enum**:
   - Must be `firing`, `pending`, or `inactive`

4. **Timestamps**:
   - `activeAt` must not be in the future (tolerance: 5 minutes)
   - Must be valid RFC3339 format

5. **URL Format**:
   - `generatorURL` must be a valid HTTP/HTTPS URL

---

## 🚀 Quick Start

### 1. Import Package

```go
import (
    "github.com/ipiton/AMP/internal/infrastructure/webhook"
)
```

### 2. Create Parser

```go
// Option A: Direct parser usage
parser := webhook.NewPrometheusParser(externalURL)

// Option B: Use UniversalWebhookHandler (recommended)
handler := webhook.NewUniversalWebhookHandler(processor, logger, externalURL)
// Handler auto-selects parser based on detected format
```

### 3. Parse Prometheus v1 Webhook

```go
payload := []byte(`[
    {
        "labels": {
            "alertname": "HighCPU",
            "instance": "server-1:9100",
            "job": "node-exporter",
            "severity": "warning"
        },
        "annotations": {
            "summary": "CPU usage is above 80%",
            "description": "Host server-1 CPU usage is 85%"
        },
        "state": "firing",
        "activeAt": "2025-11-18T10:00:00Z",
        "generatorURL": "http://prometheus:9090/graph"
    }
]`)

// Parse
webhook, err := parser.Parse(payload)
if err != nil {
    log.Fatal(err)
}

// Validate
validationResult := parser.Validate(webhook)
if !validationResult.Valid {
    log.Fatal("Validation failed:", validationResult.Errors)
}

// Convert to domain model
alerts, err := parser.ConvertToDomain(webhook)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Parsed %d alerts\n", len(alerts))
fmt.Printf("Alert: %s, Status: %s\n", alerts[0].AlertName, alerts[0].Status)
```

### 4. Parse Prometheus v2 Grouped Webhook

```go
payload := []byte(`{
    "groups": [
        {
            "labels": {
                "job": "api",
                "severity": "critical"
            },
            "alerts": [
                {
                    "labels": {
                        "alertname": "HighLatency",
                        "instance": "api-1"
                    },
                    "annotations": {
                        "summary": "API latency is high"
                    },
                    "state": "firing",
                    "activeAt": "2025-11-18T10:05:00Z",
                    "generatorURL": "http://prometheus:9090"
                }
            ]
        }
    ]
}`)

// Parse (same API)
webhook, err := parser.Parse(payload)
// Group labels are automatically merged into alert labels
```

### 5. Use UniversalWebhookHandler (Auto-Detection)

```go
handler := webhook.NewUniversalWebhookHandler(alertProcessor, logger, externalURL)

req := &webhook.HandleWebhookRequest{
    Payload:     payload,
    ContentType: "application/json",
}

ctx := context.Background()
resp, err := handler.HandleWebhook(ctx, req)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Status: %s\n", resp.Status)
fmt.Printf("Alerts processed: %d\n", resp.AlertsProcessed)
fmt.Printf("Webhook type: %s\n", resp.WebhookType) // "prometheus"
```

---

## 📊 Format Support Details

### Prometheus v1 Format (Array)

**Endpoint**: `GET /api/v1/alerts` (direct Prometheus API)

**Structure**:
```json
[
    {
        "labels": { "alertname": "...", ... },
        "annotations": { ... },
        "state": "firing|pending|inactive",
        "activeAt": "2025-11-18T10:00:00Z",
        "endsAt": "2025-11-18T10:10:00Z",
        "generatorURL": "http://prometheus:9090",
        "value": "85.5"
    }
]
```

**Detection**: Array structure + presence of `state`, `activeAt`, `generatorURL` fields

**Features**:
- Direct array of alerts
- Simple flat structure
- No grouping
- Used by Prometheus `/api/v1/alerts` endpoint

### Prometheus v2 Format (Grouped)

**Endpoint**: `GET /api/v2/alerts` (Prometheus 2.0+)

**Structure**:
```json
{
    "groups": [
        {
            "labels": { "job": "api", "severity": "warning" },
            "alerts": [
                {
                    "labels": { "alertname": "...", ... },
                    "annotations": { ... },
                    "state": "firing",
                    "activeAt": "2025-11-18T10:00:00Z",
                    "generatorURL": "http://prometheus:9090"
                }
            ]
        }
    ]
}
```

**Detection**: Object structure + `groups` array with `labels` and `alerts` fields

**Features**:
- Alerts grouped by common labels (e.g., `job`, `severity`)
- Group labels merged into individual alert labels
- Alert labels take precedence over group labels
- Reduces payload size for similar alerts

**Label Merging Example**:
```go
// Input:
Group Labels: {"job": "api", "severity": "warning"}
Alert Labels: {"alertname": "HighLatency", "instance": "api-1", "severity": "critical"}

// Output (merged):
Alert Labels: {"job": "api", "severity": "critical", "alertname": "HighLatency", "instance": "api-1"}
// Note: Alert's "severity: critical" overrides group's "severity: warning"
```

---

## 🔄 Field Mapping Tables

### Labels Mapping

| Source | Field | Destination | Transformation |
|--------|-------|-------------|----------------|
| Prometheus | `labels` | `core.Alert.Labels` | Direct copy (map[string]string) |
| Prometheus | `labels.alertname` | `core.Alert.AlertName` | Required, extracted |
| Prometheus v2 | `groups[].labels` | Merged into `Alert.Labels` | Group labels merged (alert labels take precedence) |

### Annotations Mapping

| Source | Field | Destination | Notes |
|--------|-------|-------------|-------|
| Prometheus | `annotations` | `core.Alert.Annotations` | Direct copy (optional) |
| Prometheus | `annotations.summary` | `Annotations["summary"]` | Common field |
| Prometheus | `annotations.description` | `Annotations["description"]` | Common field |

### Status/State Mapping

| Prometheus `state` | Core `AlertStatus` | Rationale |
|-------------------|-------------------|-----------|
| `firing` | `firing` | Direct mapping |
| `pending` | `firing` | Conservative: treat pending as firing to avoid missing alerts |
| `inactive` | `resolved` | Inactive means alert is no longer firing |
| `unknown` / empty | `firing` | Conservative: default to firing for safety |

### Timestamp Mapping

| Prometheus Field | Core Field | Format | Notes |
|-----------------|-----------|--------|-------|
| `activeAt` | `StartsAt` | RFC3339 | Required, when alert became active |
| `endsAt` | `EndsAt` | RFC3339 | Optional, when alert will end (for resolved alerts) |
| N/A | `UpdatedAt` | time.Now() | Set during conversion |

### URL Mapping

| Prometheus Field | Core Field | Validation |
|-----------------|-----------|-----------|
| `generatorURL` | `GeneratorURL` | Must be valid HTTP/HTTPS URL, required |

### Fingerprint Generation

**Algorithm**:
```go
func generateFingerprint(alertName string, labels map[string]string) string {
    // 1. Sort label keys
    keys := sortedKeys(labels)

    // 2. Build canonical string
    parts := []string{alertName}
    for _, k := range keys {
        parts = append(parts, fmt.Sprintf("%s=%s", k, labels[k]))
    }
    canonical := strings.Join(parts, "|")

    // 3. SHA256 hash
    hash := sha256.Sum256([]byte(canonical))
    return fmt.Sprintf("%x", hash)
}
```

**Example**:
```
Input:
  alertName: "HighCPU"
  labels: {"instance": "server-1", "job": "api", "severity": "warning"}

Canonical String:
  "HighCPU|instance=server-1|job=api|severity=warning"

Fingerprint (SHA256):
  "a3b2c1d4e5f6..."
```

---

## ⚡ Performance

Benchmarks live in `prometheus_bench_test.go`; run
`go test -bench=. -benchmem ./internal/infrastructure/webhook/` for current numbers.

---

## 🧪 Testing

### Running Tests

```bash
# All tests
go test ./internal/infrastructure/webhook/

# Specific test file
go test ./internal/infrastructure/webhook/prometheus_parser_test.go

# With coverage
go test -cover ./internal/infrastructure/webhook/

# With race detector
go test -race ./internal/infrastructure/webhook/

# Verbose output
go test -v ./internal/infrastructure/webhook/
```

### Running Benchmarks

```bash
# All benchmarks
go test -bench=. -benchmem ./internal/infrastructure/webhook/

# Specific benchmark
go test -bench=BenchmarkParseSingleAlert -benchmem ./internal/infrastructure/webhook/

# Save results
go test -bench=. -benchmem ./internal/infrastructure/webhook/ > bench_results.txt
```

### Test Categories

1. **Unit Tests**:
   - Data models (JSON marshaling, methods)
   - Format detection (v1, v2, edge cases)
   - Parser (parse, validate, convert)
   - Validation (required fields, state enum, timestamps)
   - Handler (parser selection, fallback)

2. **Integration Tests**:
   - E2E handler flow (detection → parsing → validation → conversion)
   - Concurrent webhook processing
   - Multiple alerts in single webhook
   - Error handling

3. **Benchmarks**:
   - Format detection
   - Parse single/bulk alerts
   - Validation
   - Domain conversion
   - Fingerprint generation
   - Group flattening
   - Handler E2E
   - Concurrent parsing

---

## 🔧 Troubleshooting

### Common Issues

#### 1. "failed to detect Prometheus format: array structure but not valid Prometheus v1 format"

**Cause**: Empty array `[]` or array with invalid Prometheus alert structure

**Solution**:
- Ensure payload is not empty
- Verify all required fields present: `labels.alertname`, `state`, `activeAt`, `generatorURL`
- Check field names match Prometheus format (case-sensitive)

**Example**:
```json
// ❌ Invalid (missing required fields)
[{"labels": {"alertname": "Test"}}]

// ✅ Valid
[{"labels": {"alertname": "Test"}, "state": "firing", "activeAt": "2025-11-18T10:00:00Z", "generatorURL": "http://prom:9090"}]
```

#### 2. "validation failed: alertname is required"

**Cause**: Missing `alertname` label or empty value

**Solution**: Ensure all alerts have `labels.alertname` field

```json
// ❌ Invalid
{"labels": {}}

// ✅ Valid
{"labels": {"alertname": "MyAlert"}}
```

#### 3. "validation failed: invalid label name"

**Cause**: Label name doesn't match Prometheus convention `[a-zA-Z_][a-zA-Z0-9_]*`

**Solution**: Use only alphanumeric characters and underscores, starting with letter or underscore

```json
// ❌ Invalid
{"labels": {"my-label": "value"}}  // hyphens not allowed

// ✅ Valid
{"labels": {"my_label": "value"}}
```

#### 4. "validation failed: activeAt is in the future"

**Cause**: `activeAt` timestamp is more than 5 minutes in the future

**Solution**: Ensure server clocks are synchronized (use NTP)

#### 5. Parser selects wrong format

**Symptom**: Alertmanager webhook detected as Prometheus or vice versa

**Solution**: Check detection priority in `detector.go`:
1. Alertmanager (highest priority: `receiver`, `version` fields)
2. Prometheus v2 (medium: `groups` array)
3. Prometheus v1 (lowest: array + Prometheus fields)

**Workaround**: Manually specify parser if auto-detection fails

```go
// Force Prometheus parser
parser := webhook.NewPrometheusParser(externalURL)
webhook, err := parser.Parse(payload)
```

---

## 📚 Additional Resources

### Related Files

- `prometheus_models.go` - Data structures
- `prometheus_parser.go` - Parser implementation
- `prometheus_parser_test.go` - Unit tests
- `prometheus_bench_test.go` - Benchmarks
- `detector.go` - Format detection
- `validator.go` - Validation rules
- `handler.go` - UniversalWebhookHandler (Strategy pattern)

### References

- [Prometheus API Documentation](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts)
- [Alertmanager Webhook](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config)
