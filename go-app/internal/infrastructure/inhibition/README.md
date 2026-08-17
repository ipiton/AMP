# Inhibition Rules Engine

---

## Overview

The Inhibition Rules Engine provides a powerful mechanism for suppressing (inhibiting) target alerts when source alerts are firing. This is useful for reducing alert noise by silencing alerts about dependent services when the root cause is known.

**Example Use Case**: When a `NodeDown` alert fires, all `InstanceDown` alerts on that node should be inhibited, as they are symptoms of the node failure.

### Key Features

- **Alertmanager-compatible rule semantics**
- **YAML Configuration** - standard Alertmanager format
- **Exact & Regex Matching** - flexible label matching
- **Pre-compiled Regex** - patterns compiled once at parse time
- **Two-Tier Cache** - in-memory + Redis for distributed state
- **Thread-Safe** - safe for concurrent use
- **Prometheus Metrics** - observability

---

## Quick Start

### 1. Install

```go
import (
    "github.com/ipiton/AMP/internal/infrastructure/inhibition"
)
```

### 2. Configuration

Create `config/inhibition.yaml`:

```yaml
inhibit_rules:
  - name: "node-down-inhibits-instance-down"
    source_match:
      alertname: "NodeDown"
      severity: "critical"
    target_match:
      alertname: "InstanceDown"
    equal:
      - node
      - cluster
```

### 3. Parse Rules

```go
// Create parser
parser := inhibition.NewParser()

// Load rules from file
config, err := parser.ParseFile("config/inhibition.yaml")
if err != nil {
    log.Fatalf("Failed to parse config: %v", err)
}

log.Printf("Loaded %d inhibition rules", config.RuleCount())
```

### 4. Create Matcher

```go
// Create cache for active alerts
cache := inhibition.NewTwoTierAlertCache(redisCache, logger)
defer cache.Stop()

// Create matcher with parsed rules
matcher := inhibition.NewMatcher(cache, config.Rules, logger)
```

### 5. Check Inhibition

```go
// Check if target alert should be inhibited
result, err := matcher.ShouldInhibit(ctx, targetAlert)
if err != nil {
    log.Errorf("Inhibition check failed: %v", err)
}

if result.Matched {
    log.Printf("Alert inhibited by rule: %s", result.Rule.Name)
    // Don't send this alert
} else {
    log.Println("Alert allowed, sending notification")
    // Send alert
}
```

---

## API Reference

### Parser

#### InhibitionParser Interface

```go
type InhibitionParser interface {
    Parse(data []byte) (*InhibitionConfig, error)
    ParseFile(path string) (*InhibitionConfig, error)
    ParseString(yaml string) (*InhibitionConfig, error)
    ParseReader(r io.Reader) (*InhibitionConfig, error)
    Validate(config *InhibitionConfig) error
}
```

#### NewParser()

Creates a new inhibition parser.

```go
parser := inhibition.NewParser()
```

**Returns:**
- `*DefaultInhibitionParser` - initialized parser (thread-safe)

---

### Matcher

#### InhibitionMatcher Interface

```go
type InhibitionMatcher interface {
    ShouldInhibit(ctx context.Context, targetAlert *core.Alert) (*MatchResult, error)
    FindInhibitors(ctx context.Context, targetAlert *core.Alert) ([]*InhibitionRule, error)
    MatchRule(rule *InhibitionRule, sourceAlert, targetAlert *core.Alert) bool
}
```

#### NewMatcher(rules []InhibitionRule)

Creates a new inhibition matcher.

```go
matcher := inhibition.NewMatcher(config.Rules)
```

**Parameters:**
- `rules` - slice of inhibition rules from parsed config

**Returns:**
- `*DefaultInhibitionMatcher` - initialized matcher

---

### Cache

#### ActiveAlertCache Interface

```go
type ActiveAlertCache interface {
    GetFiringAlerts(ctx context.Context) ([]*core.Alert, error)
    AddFiringAlert(ctx context.Context, alert *core.Alert) error
    RemoveAlert(ctx context.Context, fingerprint string) error
    Stop()
}
```

#### NewTwoTierAlertCache(redisCache cache.Cache, logger *slog.Logger)

Creates a two-tier alert cache (in-memory + Redis).

```go
cache := inhibition.NewTwoTierAlertCache(redisCache, logger)
defer cache.Stop()
```

**Parameters:**
- `redisCache` - Redis cache for L2 (can be nil for L1-only mode)
- `logger` - structured logger

**Returns:**
- `*TwoTierAlertCache` - initialized cache with background cleanup worker

**Performance:**
- L1 cache hit: < 1µs (in-memory)
- L2 cache hit: < 10ms (Redis)
- Graceful fallback on Redis failure

---

## Configuration Format

### Inhibition Rule Structure

```yaml
inhibit_rules:
  - name: "rule-name"                    # Optional: rule name for metrics

    # Source alert conditions (inhibitor - the alert causing inhibition)
    source_match:                         # Exact label matches
      alertname: "NodeDown"
      severity: "critical"
    source_match_re:                      # Regex label matches
      service: "^(api|web).*"

    # Target alert conditions (inhibited - the alert being suppressed)
    target_match:                         # Exact label matches
      alertname: "InstanceDown"
    target_match_re:                      # Regex label matches
      severity: "warning|info"

    # Equality constraints
    equal:                                # Labels that must match
      - node
      - cluster
```

### Field Descriptions

#### source_match (optional)

Exact label matches for the source alert (inhibitor). The source alert must have all specified labels with exact values.

**Example:**
```yaml
source_match:
  alertname: "NodeDown"
  severity: "critical"
```

#### source_match_re (optional)

Regex label matches for the source alert. Label values are matched against regular expressions.

**Example:**
```yaml
source_match_re:
  service: "^(api|web).*"
  environment: "prod.*"
```

**Note:** Uses Go RE2 regex syntax (no backreferences).

#### target_match (optional)

Exact label matches for the target alert (inhibited).

**Example:**
```yaml
target_match:
  alertname: "InstanceDown"
  severity: "warning"
```

#### target_match_re (optional)

Regex label matches for the target alert.

**Example:**
```yaml
target_match_re:
  severity: "warning|info"
  alertname: ".*Down$"
```

#### equal (optional)

Labels that must have the same value in both source and target alerts. If any of these labels is missing in either alert, the rule does not match.

**Example:**
```yaml
equal:
  - cluster    # Must have same cluster label
  - namespace  # Must have same namespace label
  - node       # Must have same node label
```

### Validation Rules

1. **At least ONE of** `source_match` or `source_match_re` must be present
2. **At least ONE of** `target_match` or `target_match_re` must be present
3. `equal` can be empty (no equality checks)
4. Label names must match Prometheus naming conventions: `^[a-zA-Z_][a-zA-Z0-9_]*$`
5. Regex patterns must compile with Go `regexp` package

---

## Examples

### Example 1: Node Down Inhibits Instance Down

```yaml
inhibit_rules:
  - name: "node-down-inhibits-instance-down"
    source_match:
      alertname: "NodeDown"
      severity: "critical"
    target_match:
      alertname: "InstanceDown"
    equal:
      - node
      - cluster
```

**Behavior**: When `NodeDown` fires on `node1` in `cluster-prod`, all `InstanceDown` alerts on `node1` in `cluster-prod` will be inhibited.

---

### Example 2: Critical Inhibits Warning/Info

```yaml
inhibit_rules:
  - name: "critical-inhibits-warning"
    source_match:
      severity: "critical"
    target_match_re:
      severity: "warning|info"
    equal:
      - service
      - namespace
```

**Behavior**: When a `critical` severity alert fires for a service, all `warning` and `info` alerts for the same service in the same namespace will be inhibited.

---

### Example 3: Database Primary Down Inhibits Replica Alerts

```yaml
inhibit_rules:
  - name: "db-primary-down-inhibits-replica-alerts"
    source_match:
      alertname: "DatabasePrimaryDown"
      component: "database"
    target_match_re:
      alertname: "(DatabaseReplicaDown|DatabaseReplicationLag)"
    equal:
      - cluster
      - environment
```

**Behavior**: When the database primary is down, all replica-related alerts in the same cluster and environment are inhibited.

---

### Example 4: Network Partition Inhibits Connectivity Alerts

```yaml
inhibit_rules:
  - name: "network-partition-inhibits-connectivity"
    source_match:
      alertname: "NetworkPartition"
    target_match_re:
      alertname: ".*(Unreachable|Timeout|ConnectionFailed)"
    equal:
      - datacenter
      - zone
```

**Behavior**: During a network partition, all connectivity-related alerts in the same datacenter and zone are inhibited.

---

## Performance

Benchmarks live in the package tests:
`go test -bench=. -benchmem ./internal/infrastructure/inhibition/...`

Design highlights:
- Pre-compiled regex patterns
- Optimized label matching
- Cache with background cleanup

---

## Prometheus Metrics

Метрики регистрируются в `pkg/metrics` `BusinessMetrics`
(namespace `alert_history`):

1. **alert_history_inhibition_check_total** (CounterVec) — inhibition checks
2. **alert_history_inhibition_match_total** (CounterVec) — matches per rule
3. **alert_history_inhibition_duration_seconds** (HistogramVec) — operation duration
4. **alert_history_inhibition_cache_hits_total** / **misses_total** / **evictions_total** — cache metrics (subsystem `inhibition_cache`)
5. **alert_history_inhibition_state_*** — state manager metrics (active, operations, records, removals, redis_errors)

Точные имена и label-схемы см. в `pkg/metrics/metrics.go`.

---

## Error Handling

### Error Types

#### ParseError

Returned when YAML parsing fails.

```go
var parseErr *inhibition.ParseError
if errors.As(err, &parseErr) {
    log.Printf("Parse error at field %s: %v", parseErr.Field, parseErr.Err)
}
```

#### ValidationError

Returned when validation fails.

```go
var valErr *inhibition.ValidationError
if errors.As(err, &valErr) {
    log.Printf("Validation error in field %s: %s", valErr.Field, valErr.Message)
}
```

#### ConfigError

Returned for high-level configuration errors.

```go
var confErr *inhibition.ConfigError
if errors.As(err, &confErr) {
    log.Printf("Config error: %s (%d errors)", confErr.Message, len(confErr.Errors))
}
```

### Error Helper Functions

```go
// Check if error is ParseError
if inhibition.IsParseError(err) {
    // Handle parse error
}

// Check if error is ValidationError
if inhibition.IsValidationError(err) {
    // Handle validation error
}

// Extract ValidationError from error chain
if valErr := inhibition.GetValidationError(err); valErr != nil {
    log.Printf("Validation failed for field: %s", valErr.Field)
}
```

---

## Best Practices

### 1. Order Rules by Importance

```yaml
inhibit_rules:
  # Critical infrastructure first
  - name: "cluster-down-inhibits-all"
    # ...

  # Node-level second
  - name: "node-down-inhibits-instance"
    # ...

  # Service-level last
  - name: "service-critical-inhibits-warning"
    # ...
```

### 2. Use Equal Labels Wisely

Always specify `equal` labels to ensure source and target are related:

```yaml
equal:
  - cluster     # Same cluster
  - namespace   # Same namespace
  - service     # Same service
```

### 3. Give Rules Descriptive Names

```yaml
- name: "node-down-inhibits-instance-down"  # Good: descriptive
# vs
- name: "rule1"                              # Bad: not descriptive
```

### 4. Test Rules Thoroughly

```go
// Test that inhibition happens when expected
result, _ := matcher.ShouldInhibit(ctx, targetAlert)
if !result.Matched {
    t.Error("Expected alert to be inhibited")
}

// Test that important alerts are NOT inhibited
result, _ = matcher.ShouldInhibit(ctx, criticalAlert)
if result.Matched {
    t.Error("Critical alert should not be inhibited")
}
```

### 5. Monitor Inhibition Metrics

```promql
# Check inhibition rate
rate(alert_history_business_inhibition_checks_total{result="inhibited"}[5m])

# Check most active inhibition rules
topk(10, rate(alert_history_business_inhibition_matches_total[5m]))

# Check cache hit rate
sum(rate(alert_history_business_inhibition_cache_hits_total[5m])) by (cache_level)
```

---

## Architecture

### Component Diagram

```
┌─────────────────────────────────────────────────────────┐
│             Inhibition Rules Engine                     │
└──────────────┬──────────────────────────────────────────┘
               │
               ├─> Parser (YAML → Rules)
               │     - InhibitionRule model
               │     - InhibitionConfig
               │     - YAML parsing + validation
               │     - Pre-compiled regex
               │
               ├─> Matcher (Rules → Decision)
               │     - InhibitionMatcher interface
               │     - Label matching (exact + regex)
               │     - Equal labels check
               │     - <1ms performance
               │
               ├─> Cache (Active Alerts)
               │     - L1: In-memory LRU (max 1000)
               │     - L2: Redis (distributed)
               │     - Background cleanup (1 min)
               │     - Graceful fallback
               │
               └─> Metrics (Observability)
                     - 6 Prometheus metrics
                     - Duration tracking
                     - Cache hit rates
```

### Data Flow

```
1. Load Rules:
   config/inhibition.yaml → Parser → InhibitionConfig

2. Add Firing Alert:
   Alert → Cache.AddFiringAlert() → L1 Cache + Redis

3. Check Inhibition:
   Target Alert → Matcher.ShouldInhibit()
                → Cache.GetFiringAlerts()
                → For each firing alert:
                    → MatchRule(rule, firing, target)
                    → If matched: return MatchResult{Matched: true}
                → If no match: return MatchResult{Matched: false}

4. Record Metrics:
   MatchResult → Prometheus Metrics
```

---

## Testing

### Running Tests

```bash
# Run all tests
cd go-app/internal/infrastructure/inhibition
go test -v

# Run with coverage
go test -cover

# Run with race detector
go test -race

# Run benchmarks
go test -bench=. -benchmem
```

---

## Migration from Alertmanager

### Step 1: Export Alertmanager Config

Extract `inhibit_rules` section from Alertmanager config:

```yaml
# Alertmanager config.yml
inhibit_rules:
  - source_match:
      alertname: "NodeDown"
    target_match:
      alertname: "InstanceDown"
    equal:
      - node
```

### Step 2: Save to inhibition.yaml

Save the `inhibit_rules` section to `config/inhibition.yaml` (no changes needed - 100% compatible).

### Step 3: Load in Application

```go
parser := inhibition.NewParser()
config, err := parser.ParseFile("config/inhibition.yaml")
if err != nil {
    log.Fatalf("Failed to parse config: %v", err)
}
```

### Step 4: Integrate with Alert Processing

```go
matcher := inhibition.NewMatcher(config.Rules)

// In alert processing pipeline:
result, err := matcher.ShouldInhibit(ctx, alert)
if result.Matched {
    log.Printf("Alert inhibited by rule: %s", result.Rule.Name)
    return // Don't send this alert
}

// Continue with alert processing...
```

---

## Troubleshooting

### Issue: Rules not loading

**Symptom**: `parser.ParseFile()` returns error

**Solution**:
1. Check YAML syntax with online validator
2. Verify file path is correct
3. Check file permissions
4. Enable debug logging to see detailed error

```go
config, err := parser.ParseFile("config/inhibition.yaml")
if err != nil {
    if inhibition.IsParseError(err) {
        log.Printf("YAML syntax error: %v", err)
    } else if inhibition.IsValidationError(err) {
        log.Printf("Validation error: %v", err)
    }
}
```

### Issue: Alerts not being inhibited

**Symptom**: `ShouldInhibit()` returns `Matched=false` unexpectedly

**Solution**:
1. Check that source alert is in cache (use `GetFiringAlerts()`)
2. Verify `equal` labels match between source and target
3. Check regex patterns compile correctly
4. Enable debug logging to see match details

```go
// Debug: Check firing alerts
alerts, _ := cache.GetFiringAlerts(ctx)
log.Printf("Active alerts: %d", len(alerts))
for _, alert := range alerts {
    log.Printf("  - %s (labels: %v)", alert.AlertName, alert.Labels)
}
```

### Issue: High memory usage

**Symptom**: Memory grows over time

**Solution**:
1. Check cache capacity (default: 1000 alerts)
2. Verify background cleanup is running
3. Adjust cleanup interval if needed

```go
opts := &inhibition.AlertCacheOptions{
    L1Max:           500,                 // Reduce capacity
    CleanupInterval: 30 * time.Second,    // More frequent cleanup
}
cache := inhibition.NewTwoTierAlertCacheWithOptions(redis, logger, opts)
```

---

## FAQ

### Q: Can I use multiple `equal` labels?

**A:** Yes! You can specify multiple labels in the `equal` list. ALL labels must match for the rule to trigger.

```yaml
equal:
  - cluster
  - namespace
  - service
```

### Q: What happens if Redis is down?

**A:** The cache gracefully falls back to in-memory (L1) cache only. Inhibition continues to work for the current instance, but state is not shared across replicas.

### Q: Can I reload rules without restart?

**A:** Yes! Just parse the config again:

```go
newConfig, err := parser.ParseFile("config/inhibition.yaml")
if err != nil {
    log.Printf("Failed to reload: %v", err)
    return // Keep using old config
}

// Create new matcher with new rules
matcher = inhibition.NewMatcher(newConfig.Rules)
```

### Q: How do I debug why an alert isn't being inhibited?

**A:** Use the `FindInhibitors()` method to see all matching source alerts:

```go
inhibitors, err := matcher.FindInhibitors(ctx, targetAlert)
log.Printf("Found %d potential inhibitors", len(inhibitors))
for _, rule := range inhibitors {
    log.Printf("  - Rule: %s", rule.Name)
}
```

---

## License

See [LICENSE](../../../../LICENSE) for license information.
