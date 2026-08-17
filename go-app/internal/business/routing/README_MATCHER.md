# Route Matcher — README

**Package**: `internal/business/routing`

---

## Overview

The **RouteMatcher** evaluates if alerts match routing rules with support for 4 Alertmanager-compatible operators: `=`, `!=`, `=~`, `!~`.

**Features**:
- 4 matcher operators (Alertmanager-compatible semantics)
- Regex caching with O(1) lookup
- Early exit optimization (stop on first non-match)
- Prometheus metrics + structured logging
- Thread-safe concurrent matching
- Context cancellation support

---

## Quick Start

### 1. Create a RouteMatcher

```go
package main

import (
    "github.com/ipiton/AMP/internal/business/routing"
)

func main() {
    // Create matcher with default options
    matcher := routing.NewRouteMatcher(nil, routing.DefaultMatcherOptions())

    // Or with custom options
    opts := routing.MatcherOptions{
        EnableLogging:       true,  // Enable debug logging
        EnableMetrics:       true,  // Enable Prometheus metrics
        CacheSize:           1000,  // Max regex patterns
        EnableOptimizations: true,  // Alertname pre-filter
    }
    matcher := routing.NewRouteMatcher(nil, opts)
}
```

### 2. Match an Alert Against a Node

```go
// Create an alert
alert := &routing.Alert{
    Labels: map[string]string{
        "alertname": "HighCPU",
        "severity":  "critical",
        "namespace": "production",
    },
}

// Create a route node with matchers
node := &routing.RouteNode{
    Matchers: []routing.Matcher{
        {Name: "severity", Value: "critical", IsRegex: false, IsNegative: false},  // =
        {Name: "namespace", Value: "prod.*", IsRegex: true, IsNegative: false},    // =~
    },
}

// Check if alert matches node
matches := matcher.MatchesNode(node, alert)
// matches = true (both matchers passed)
```

### 3. Find Matching Routes in a Tree

```go
// Build a route tree (tree_builder.go)
tree, _ := routing.NewTreeBuilder(config, opts).Build()

// Find all matching routes
result := matcher.FindMatchingRoutes(tree, alert)

if result.Empty() {
    // No matches: use root default
    receiver := tree.Root.Receiver
} else {
    // Use first match
    node := result.First()
    receiver := node.Receiver

    fmt.Printf("Matched route: %s\n", node.Path)
    fmt.Printf("Receiver: %s\n", receiver)
    fmt.Printf("Duration: %v\n", result.Duration)
    fmt.Printf("Cache hit rate: %.2f%%\n", result.CacheHitRate() * 100)
}
```

---

## Matcher Operators

### Equality (`=`)

**Operator**: `=`
**Example**: `severity="critical"`
**Matches**: Label value exactly equals matcher value
**Missing Label**: Does NOT match

```go
matcher := routing.Matcher{
    Name:       "severity",
    Value:      "critical",
    IsRegex:    false,
    IsNegative: false,
}

alert1 := &routing.Alert{Labels: map[string]string{"severity": "critical"}}
matcher.MatchesNode(node, alert1) // true

alert2 := &routing.Alert{Labels: map[string]string{"severity": "warning"}}
matcher.MatchesNode(node, alert2) // false

alert3 := &routing.Alert{Labels: map[string]string{}} // severity missing
matcher.MatchesNode(node, alert3) // false
```

### Inequality (`!=`)

**Operator**: `!=`
**Example**: `severity!="info"`
**Matches**: Label value does NOT equal matcher value OR label is missing
**Missing Label**: DOES match (!)

```go
matcher := routing.Matcher{
    Name:       "severity",
    Value:      "info",
    IsRegex:    false,
    IsNegative: true,
}

alert1 := &routing.Alert{Labels: map[string]string{"severity": "critical"}}
matcher.MatchesNode(node, alert1) // true (critical != info)

alert2 := &routing.Alert{Labels: map[string]string{"severity": "info"}}
matcher.MatchesNode(node, alert2) // false (info == info)

alert3 := &routing.Alert{Labels: map[string]string{}} // severity missing
matcher.MatchesNode(node, alert3) // true (missing matches !=)
```

### Regex Match (`=~`)

**Operator**: `=~`
**Example**: `namespace=~"prod.*"`
**Matches**: Label value matches regex pattern
**Missing Label**: Does NOT match

```go
matcher := routing.Matcher{
    Name:       "namespace",
    Value:      "prod.*",
    IsRegex:    true,
    IsNegative: false,
}

alert1 := &routing.Alert{Labels: map[string]string{"namespace": "prod-us"}}
matcher.MatchesNode(node, alert1) // true (matches prod.*)

alert2 := &routing.Alert{Labels: map[string]string{"namespace": "dev-us"}}
matcher.MatchesNode(node, alert2) // false (doesn't match prod.*)

alert3 := &routing.Alert{Labels: map[string]string{}} // namespace missing
matcher.MatchesNode(node, alert3) // false
```

### Negative Regex (`!~`)

**Operator**: `!~`
**Example**: `namespace!~"dev.*"`
**Matches**: Label value does NOT match regex pattern OR label is missing
**Missing Label**: DOES match (!)

```go
matcher := routing.Matcher{
    Name:       "namespace",
    Value:      "dev.*",
    IsRegex:    true,
    IsNegative: true,
}

alert1 := &routing.Alert{Labels: map[string]string{"namespace": "prod-us"}}
matcher.MatchesNode(node, alert1) // true (doesn't match dev.*)

alert2 := &routing.Alert{Labels: map[string]string{"namespace": "dev-us"}}
matcher.MatchesNode(node, alert2) // false (matches dev.*)

alert3 := &routing.Alert{Labels: map[string]string{}} // namespace missing
matcher.MatchesNode(node, alert3) // true (missing matches !~)
```

---

## Truth Table

| Operator | Label Exists | Label Value | Matcher Value | Result |
|----------|--------------|-------------|---------------|--------|
| `=`      | Yes          | "critical"  | "critical"    | ✅ Match |
| `=`      | Yes          | "warning"   | "critical"    | ❌ No match |
| `=`      | No           | -           | "critical"    | ❌ No match |
| `!=`     | Yes          | "critical"  | "warning"     | ✅ Match |
| `!=`     | Yes          | "warning"   | "warning"     | ❌ No match |
| `!=`     | No           | -           | "warning"     | ✅ Match (!) |
| `=~`     | Yes          | "prod-us"   | "prod.*"      | ✅ Match |
| `=~`     | Yes          | "dev-us"    | "prod.*"      | ❌ No match |
| `=~`     | No           | -           | "prod.*"      | ❌ No match |
| `!~`     | Yes          | "dev-us"    | "prod.*"      | ✅ Match |
| `!~`     | Yes          | "prod-us"   | "prod.*"      | ❌ No match |
| `!~`     | No           | -           | "prod.*"      | ✅ Match (!) |

**Key Insight**: Negative operators (`!=`, `!~`) match when label is **missing**.

---

## API Reference

### Types

#### RouteMatcher

```go
type RouteMatcher struct {
    // Private fields
}

// NewRouteMatcher creates a new matcher
func NewRouteMatcher(
    compiledPatterns map[string]*regexp.Regexp,
    opts MatcherOptions,
) *RouteMatcher

// MatchesNode checks if alert matches node
func (m *RouteMatcher) MatchesNode(
    node *RouteNode,
    alert *Alert,
) bool

// FindMatchingRoutes finds all matching routes
func (m *RouteMatcher) FindMatchingRoutes(
    tree *RouteTree,
    alert *Alert,
) *MatchResult

// FindMatchingRoutesWithContext finds routes with timeout
func (m *RouteMatcher) FindMatchingRoutesWithContext(
    ctx context.Context,
    tree *RouteTree,
    alert *Alert,
) (*MatchResult, error)

// GetMetrics returns Prometheus metrics
func (m *RouteMatcher) GetMetrics() *MatcherMetrics

// GetCacheStats returns regex cache statistics
func (m *RouteMatcher) GetCacheStats() CacheStats
```

#### MatcherOptions

```go
type MatcherOptions struct {
    EnableLogging       bool // Debug logging (default: false)
    EnableMetrics       bool // Prometheus metrics (default: true)
    CacheSize           int  // Max regex patterns (default: 1000)
    EnableOptimizations bool // Alertname pre-filter (default: true)
}

// DefaultMatcherOptions returns default options
func DefaultMatcherOptions() MatcherOptions
```

#### MatchResult

```go
type MatchResult struct {
    Matches           []*RouteNode // Matched routes (in order)
    Duration          time.Duration // Total matching time
    MatchersEvaluated int          // Matchers checked
    CacheHits         int          // Regex cache hits
    CacheMisses       int          // Regex cache misses
}

func (r *MatchResult) Empty() bool         // No matches?
func (r *MatchResult) First() *RouteNode   // First match (or nil)
func (r *MatchResult) Count() int          // Match count
func (r *MatchResult) CacheHitRate() float64 // Cache hit rate (0-1)
```

#### RegexCache

```go
type RegexCache struct {
    // Private fields
}

func NewRegexCache(maxSize int) *RegexCache
func (c *RegexCache) Get(pattern string) (*regexp.Regexp, bool)
func (c *RegexCache) Put(pattern string, regex *regexp.Regexp)
func (c *RegexCache) Preload(patterns map[string]*regexp.Regexp)
func (c *RegexCache) Stats() CacheStats
func (c *RegexCache) Clear()
```

---

## Prometheus Metrics

The RouteMatcher exposes 5 Prometheus metrics:

### 1. `alert_history_routing_matches_total`

**Type**: Counter (by `route_path`)
**Description**: Total number of route matches
**Labels**: `route_path` (e.g., "/routes[0]")

**Example PromQL**:
```promql
# Matches per second by route
rate(alert_history_routing_matches_total[5m])

# Top 10 most matched routes
topk(10, sum by (route_path) (alert_history_routing_matches_total))
```

### 2. `alert_history_routing_match_duration_seconds`

**Type**: Histogram
**Description**: Time to find matching routes
**Buckets**: 10µs to 10ms (exponential)

**Example PromQL**:
```promql
# P95 matching latency
histogram_quantile(0.95, rate(alert_history_routing_match_duration_seconds_bucket[5m]))

# Average matching latency
rate(alert_history_routing_match_duration_seconds_sum[5m]) /
rate(alert_history_routing_match_duration_seconds_count[5m])
```

### 3. `alert_history_routing_regex_cache_hits_total`

**Type**: Counter
**Description**: Regex cache hits

### 4. `alert_history_routing_regex_cache_misses_total`

**Type**: Counter
**Description**: Regex cache misses

**Example PromQL (Cache Hit Rate)**:
```promql
# Cache hit rate (%)
100 * rate(alert_history_routing_regex_cache_hits_total[5m]) /
(rate(alert_history_routing_regex_cache_hits_total[5m]) +
 rate(alert_history_routing_regex_cache_misses_total[5m]))
```

### 5. `alert_history_routing_regex_cache_size`

**Type**: Gauge
**Description**: Current regex cache size

**Example PromQL**:
```promql
# Current cache size
alert_history_routing_regex_cache_size

# Cache utilization (%)
100 * alert_history_routing_regex_cache_size / 1000
```

---

## Performance Guide

Run `go test -bench=. -benchmem ./internal/business/routing/...` for current numbers.

### Optimization Tips

**1. Pre-populate Regex Cache**

```go
// Create matcher with pre-compiled patterns
patterns := map[string]*regexp.Regexp{
    "prod.*": regexp.MustCompile("prod.*"),
}
matcher := routing.NewRouteMatcher(patterns, opts)
// First match will be fast (no compilation overhead)
```

**2. Enable Alertname Pre-filter**

```go
opts := routing.MatcherOptions{
    EnableOptimizations: true, // Default
}
// Pre-filter by alertname сокращает число проверяемых matchers
```

**3. Use continue=false When Possible**

```yaml
route:
  receiver: default
  routes:
    - match:
        severity: critical
      receiver: pagerduty
      continue: false  # Stop here (early exit)
```

**4. Monitor Cache Hit Rate**

```promql
# Target: >90% hit rate
100 * rate(alert_history_routing_regex_cache_hits_total[5m]) /
(rate(alert_history_routing_regex_cache_hits_total[5m]) +
 rate(alert_history_routing_regex_cache_misses_total[5m]))
```

If hit rate <90%, increase cache size:

```go
opts := routing.MatcherOptions{
    CacheSize: 2000, // Increase from 1000
}
```

---

## Integration Examples

### With the Route Tree Builder

```go
// Build tree from a RouteConfig
builder := routing.NewTreeBuilder(config, routing.DefaultBuildOptions())
tree, err := builder.Build()
if err != nil {
    return err
}

// Create matcher
matcher := routing.NewRouteMatcher(nil, routing.DefaultMatcherOptions())

// Match alerts
result := matcher.FindMatchingRoutes(tree, alert)
```

### With Alert Processing Pipeline

```go
type AlertProcessor struct {
    tree    *routing.RouteTree
    matcher *routing.RouteMatcher
}

func (p *AlertProcessor) Process(alert *routing.Alert) error {
    // Find matching route
    result := p.matcher.FindMatchingRoutes(p.tree, alert)

    if result.Empty() {
        // No match: use root default
        return p.publishToReceiver(alert, p.tree.Root.Receiver)
    }

    // Use first match
    node := result.First()
    return p.publishToReceiver(alert, node.Receiver)
}
```

---

## Troubleshooting

### Problem: Slow Matching (>1ms)

**Symptoms**: High p95 latency in `alert_history_routing_match_duration_seconds`

**Diagnosis**:
```promql
# Check p95 latency
histogram_quantile(0.95, rate(alert_history_routing_match_duration_seconds_bucket[5m]))
```

**Solutions**:
1. Enable alertname pre-filter: `opts.EnableOptimizations = true`
2. Reduce route tree depth (flatten nested routes)
3. Use continue=false to enable early exit
4. Pre-populate regex cache from config

### Problem: Low Cache Hit Rate (<90%)

**Symptoms**: High regex_cache_misses_total

**Diagnosis**:
```promql
# Calculate hit rate
100 * rate(alert_history_routing_regex_cache_hits_total[5m]) /
(rate(alert_history_routing_regex_cache_hits_total[5m]) +
 rate(alert_history_routing_regex_cache_misses_total[5m]))
```

**Solutions**:
1. Increase cache size: `opts.CacheSize = 2000`
2. Pre-populate cache via `NewRouteMatcher(patterns, opts)` or `RegexCache.Preload`
3. Check for duplicate patterns (consolidate)

### Problem: High Memory Usage

**Symptoms**: Regex cache size growing unbounded

**Diagnosis**:
```promql
# Check current cache size
alert_history_routing_regex_cache_size
```

**Solutions**:
1. LRU eviction is automatic (max 1000 by default)
2. Reduce cache size: `opts.CacheSize = 500`
3. Check for pattern proliferation (too many unique patterns)

---

## Testing

### Run All Tests

```bash
cd go-app/internal/business/routing
go test -v ./... -run TestMatcher
```

### Run Benchmarks

```bash
go test -bench=. -benchmem
```

### Check Test Coverage

```bash
go test -cover
```

---

## References

### Related Components (same package)
- Route Tree Builder (`tree_builder.go`, см. [README.md](./README.md))
- Route Evaluator (`evaluator*.go`)
- Multi-Receiver Support (`multi_receiver*.go`)

### External Documentation
- [Alertmanager Routing](https://prometheus.io/docs/alerting/latest/configuration/#route)
- [Prometheus Label Matching](https://prometheus.io/docs/prometheus/latest/querying/basics/#time-series-selectors)
- [Go regexp Package](https://pkg.go.dev/regexp)
