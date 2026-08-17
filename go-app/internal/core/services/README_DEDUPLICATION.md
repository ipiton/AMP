# Alert Deduplication & Fingerprinting

## Overview

Deduplication service обеспечивает идемпотентную обработку алертов через fingerprinting и автоматическую дедупликацию дубликатов.

### Key Features

- **Alertmanager-compatible fingerprinting** (FNV-1a algorithm)
- **Automatic deduplication** (create/update/ignore logic)
- **Prometheus metrics** (4 metrics для monitoring)
- **Graceful degradation** (continues работа при errors)
- **Thread-safe** (concurrent processing)

---

## Architecture

```
Incoming Alert
    ↓
[1] Generate Fingerprint (FNV-1a)
    ↓
[2] Check Database (GetAlertByFingerprint)
    ↓
[3] Decide Action:
    - New → CREATE alert
    - Changed → UPDATE alert
    - Duplicate → IGNORE alert
    ↓
[4] Record Metrics
```

---

## Usage

### Basic Usage

```go
import "github.com/ipiton/AMP/internal/core/services"

// Initialize
fingerprintGen := services.NewFingerprintGenerator(&services.FingerprintConfig{
    Algorithm: services.AlgorithmFNV1a,
})

deduplicationService, err := services.NewDeduplicationService(&services.DeduplicationConfig{
    Storage:         alertStorage,
    Fingerprint:     fingerprintGen,
    Logger:          logger,
    BusinessMetrics: businessMetrics,
})

// Process alert
result, err := deduplicationService.ProcessAlert(ctx, alert)
if err != nil {
    return err
}

switch result.Action {
case services.ProcessActionCreated:
    // New alert created
case services.ProcessActionUpdated:
    // Existing alert updated
case services.ProcessActionIgnored:
    // Duplicate - skip processing
}
```

### Integration (AlertProcessor)

```go
// In alert_processor.go
if p.deduplication != nil {
    result, err := p.deduplication.ProcessAlert(ctx, alert)
    if err != nil {
        p.logger.Error("Deduplication failed", "error", err)
        // Continue processing (graceful degradation)
    } else if result.Action == ProcessActionIgnored {
        // Skip duplicate alerts
        return nil
    }
    alert = result.Alert // Use deduplicated alert
}
```

---

## Fingerprinting

### FNV-1a Algorithm (Recommended)

**Properties:**
- Deterministic: same labels → same fingerprint
- Alertmanager-compatible
- Output: 16 hex characters

**How it works:**
1. Sort labels alphabetically
2. Hash each key-value pair using FNV-1a (64-bit)
3. Return hex string

**Example:**
```go
labels := map[string]string{
    "alertname": "HighCPU",
    "severity": "critical",
    "instance": "server-1",
}
fingerprint := generator.GenerateFromLabels(labels)
// Result: "5dcb02f5cf018484" (16 hex chars)
```

### SHA-256 Algorithm (Legacy)

- Slower than FNV-1a
- Output: 64 hex characters
- Use for backward compatibility only

---

## Deduplication Logic

### ProcessAlert Flow

```go
func ProcessAlert(ctx, alert) (ProcessResult, error):
    1. Generate fingerprint (if not present)
    2. Query database by fingerprint
    3. If NOT FOUND:
        → CREATE new alert
        → Return ProcessActionCreated
    4. If FOUND:
        a. Compare status/EndsAt
        b. If CHANGED:
            → UPDATE existing alert
            → Return ProcessActionUpdated
        c. If IDENTICAL:
            → IGNORE duplicate
            → Return ProcessActionIgnored
    5. Record metrics
```

### Update Detection

Alert is considered "changed" if:
- **Status changed** (firing → resolved или обратно)
- **EndsAt changed** (timestamp modified)
- **EndsAt nil status changed** (nil → time или обратно)

### Examples

**Scenario 1: New Alert**
```go
alert1 := &Alert{AlertName: "HighCPU", Status: StatusFiring}
result, _ := service.ProcessAlert(ctx, alert1)
// result.Action = ProcessActionCreated
```

**Scenario 2: Status Changed**
```go
alert2 := &Alert{
    AlertName: "HighCPU",
    Status: StatusResolved,  // Changed from Firing
    Fingerprint: "5dcb02f5cf018484", // Same fingerprint
}
result, _ := service.ProcessAlert(ctx, alert2)
// result.Action = ProcessActionUpdated
```

**Scenario 3: Exact Duplicate**
```go
alert3 := &Alert{
    AlertName: "HighCPU",
    Status: StatusResolved,  // Same as alert2
    Fingerprint: "5dcb02f5cf018484",
}
result, _ := service.ProcessAlert(ctx, alert3)
// result.Action = ProcessActionIgnored
```

---

## Metrics

### Prometheus Metrics (4 total)

#### 1. `alert_history_business_deduplication_created_total`
- **Type:** Counter
- **Labels:** source (webhook)
- **Description:** Number of new alerts created

#### 2. `alert_history_business_deduplication_updated_total`
- **Type:** Counter
- **Labels:** status_from, status_to
- **Description:** Number of alerts updated

#### 3. `alert_history_business_deduplication_ignored_total`
- **Type:** Counter
- **Labels:** reason (duplicate)
- **Description:** Number of duplicate alerts ignored

#### 4. `alert_history_business_deduplication_duration_seconds`
- **Type:** Histogram
- **Labels:** action (created/updated/ignored)
- **Buckets:** 1µs, 5µs, 10µs, 50µs, 100µs, 500µs, 1ms, 5ms, 10ms
- **Description:** Deduplication operation duration

### Example PromQL Queries

```promql
# Deduplication rate (% of duplicates)
rate(alert_history_business_deduplication_ignored_total[5m])
/
sum(rate(alert_history_business_deduplication_*_total[5m]))

# Average deduplication latency
histogram_quantile(0.99,
  rate(alert_history_business_deduplication_duration_seconds_bucket[5m])
)

# Status transition rates
rate(alert_history_business_deduplication_updated_total{
  status_from="firing",status_to="resolved"
}[5m])
```

---

## Performance

Benchmarks live in `fingerprint_bench_test.go` and `deduplication_bench_test.go`:

```bash
go test ./internal/core/services -bench=. -benchmem
```

---

## Error Handling

### Graceful Degradation

Service продолжает работу даже при errors:

```go
result, err := deduplication.ProcessAlert(ctx, alert)
if err != nil {
    logger.Error("Deduplication failed", "error", err)
    // Continue processing - service degraded but operational
}
```

### Error Types

- **`ErrAlertNotFound`** - Alert не найден (ожидаемо для новых alerts)
- **`"alert is nil"`** - Validation error
- **`"storage error"`** - Database проблемы (wrapped error)
- **`"failed to generate fingerprint"`** - No labels (empty alert)

---

## Testing

**Test Files:**
- `deduplication_test.go`
- `fingerprint_test.go`
- `deduplication_integration_test.go`
- `*_bench_test.go`

### Run Tests

```bash
# Unit tests only
go test ./internal/core/services -run "(TestNewDeduplication|TestProcessAlert|TestNewFingerprint)"

# With coverage
go test ./internal/core/services -run "(TestDedup|TestFinger)" -coverprofile=coverage.out
go tool cover -html=coverage.out

# Benchmarks
go test ./internal/core/services -bench=. -benchmem

# Integration tests (requires PostgreSQL)
export TEST_DATABASE_DSN="postgres://user:pass@localhost:5432/testdb"
go test ./internal/core/services -run "TestDeduplicationIntegration"
```

---

## Configuration

### Environment Variables

```bash
# Database (required for deduplication)
DATABASE_URL=postgres://user:pass@localhost:5432/alerts

# Metrics (optional)
METRICS_ENABLED=true
METRICS_PATH=/metrics
```

### Config File (config.yaml)

```yaml
database:
  url: "postgres://user:pass@localhost:5432/alerts"
  max_connections: 25

metrics:
  enabled: true
  path: "/metrics"
```

---

## Troubleshooting

### Problem: High duplicate rate

**Symptoms:** Too many ProcessActionIgnored
**Causes:**
- Webhook retries от Alertmanager
- Multiple Alertmanager instances
- Client-side retries

**Solution:** This is EXPECTED behavior - deduplication working correctly

### Problem: Fingerprint collisions

**Symptoms:** Different alerts with same fingerprint
**Probability:** FNV-1a: ~1 in 18 quintillion for random labels
**Solution:** Collision virtually impossible for real workloads

### Problem: Slow deduplication

**Symptoms:** High p99 latency (>10ms)
**Causes:**
- Database slow queries
- Network latency
- Lock contention

**Solution:**
1. Check database indexes on `fingerprint` column
2. Monitor database connection pool
3. Check `alert_history_business_deduplication_duration_seconds` metrics

---

## Migration Guide

### From Legacy Fingerprinting

If migrating from SHA-256 to FNV-1a:

```go
// 1. Enable dual algorithm support
generator := NewFingerprintGenerator(&FingerprintConfig{
    Algorithm: AlgorithmFNV1a,
})

// 2. Generate both fingerprints for transition period
fpNew := generator.GenerateWithAlgorithm(labels, AlgorithmFNV1a)
fpOld := generator.GenerateWithAlgorithm(labels, AlgorithmSHA256)

// 3. Query database with both
existing, _ := storage.GetAlertByFingerprint(ctx, fpNew)
if existing == nil {
    existing, _ = storage.GetAlertByFingerprint(ctx, fpOld)
}
```

---

## API Reference

### FingerprintGenerator

```go
type FingerprintGenerator interface {
    Generate(alert *core.Alert) string
    GenerateFromLabels(labels map[string]string) string
    GenerateWithAlgorithm(labels map[string]string, algorithm FingerprintAlgorithm) string
    GetAlgorithm() FingerprintAlgorithm
}
```

### DeduplicationService

```go
type DeduplicationService interface {
    ProcessAlert(ctx context.Context, alert *core.Alert) (*ProcessResult, error)
    GetDuplicateStats(ctx context.Context) (*DuplicateStats, error)
    ResetStats(ctx context.Context) error
}
```

### ProcessResult

```go
type ProcessResult struct {
    Action ProcessAction            // created/updated/ignored
    Alert *core.Alert                // Processed alert
    ExistingID *string               // ID of existing alert (for updates)
    IsUpdate bool                    // True if alert was updated
    IsDuplicate bool                 // True if duplicate ignored
    ProcessingTime time.Duration     // Time taken to process
}
```

---

## Best Practices

1. **Always enable deduplication** для production workloads
2. **Monitor metrics** (`alert_history_business_deduplication_*`)
3. **Use FNV-1a algorithm** (Alertmanager-compatible)
4. **Handle graceful degradation** (service continues при errors)
5. **Index fingerprint column** в database для fast lookups

---

## Support

**Code:**
- Implementation: `deduplication.go`, `fingerprint.go`
- Tests: `*_test.go`, `*_bench_test.go`
- Integration: `alert_processor.go`
