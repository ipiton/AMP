# Target Refresh Mechanism

Refresh mechanism for dynamic publishing target updates.

## Overview

The **Refresh Manager** periodically refreshes publishing targets discovered from Kubernetes Secrets. This ensures targets stay up-to-date without service restarts. It is initialized in the active runtime via `internal/application/publishing_runtime.go`.

> **Historical note**: ранние ревизии этого документа описывали HTTP endpoints
> (`POST /api/v2/publishing/targets/refresh`, `GET /api/v2/publishing/targets/status`).
> Эти routes не смонтированы в текущем active router; refresh управляется
> программно через интерфейс `RefreshManager` (`RefreshNow`, `GetStatus`).

### Key Features

- **Periodic Refresh**: Background worker (5m interval, configurable)
- **Manual Refresh**: `RefreshNow()` for immediate updates
- **Retry Logic**: Exponential backoff (30s → 5m) for transient failures
- **Graceful Degradation**: Retains stale cache on failures
- **Observability**: Prometheus metrics + structured logging
- **Thread-Safe**: Concurrent-safe operations (RWMutex, single-flight)

## Quick Start

### 1. Create Refresh Manager

```go
import (
    "github.com/ipiton/AMP/internal/business/publishing"
    "log/slog"
)

// Default configuration (5m interval, 5 retries)
config := publishing.DefaultRefreshConfig()

// Create manager
refreshMgr, err := publishing.NewRefreshManager(
    discoveryMgr,
    config,
    slog.Default(),
    metricsRegistry,
)
if err != nil {
    log.Fatal(err)
}
```

### 2. Start Background Worker

```go
// Start periodic refresh (5m interval)
if err := refreshMgr.Start(); err != nil {
    log.Fatal(err)
}

// Graceful shutdown on exit
defer refreshMgr.Stop(30 * time.Second)
```

### 3. Manual Refresh

```go
// Trigger immediate refresh (async)
if err := refreshMgr.RefreshNow(); err != nil {
    log.Printf("refresh: %v", err)
}

// Check status
status := refreshMgr.GetStatus()
```

## Configuration

### Programmatic Configuration

```go
config := publishing.RefreshConfig{
    Interval:       10 * time.Minute,  // More frequent
    MaxRetries:     10,                 // More retries
    BaseBackoff:    1 * time.Minute,    // Longer backoff
    MaxBackoff:     10 * time.Minute,   // Higher cap
    RateLimitPer:   2 * time.Minute,    // More relaxed
    RefreshTimeout: 60 * time.Second,   // Longer timeout
    WarmupPeriod:   10 * time.Second,   // Shorter warmup
}

refreshMgr, _ := publishing.NewRefreshManager(discovery, config, logger, metrics)
```

## Prometheus Metrics

Метрики регистрируются в `pkg/metrics/v2/publishing.go`
(namespace `alert_history`, subsystem `publishing`):

### 1. `alert_history_publishing_refresh_operations_total`

**Type**: Counter
**Labels**: `source`, `status`
**Description**: Total refresh operations by source and status

```promql
rate(alert_history_publishing_refresh_operations_total{status="success"}[5m])
```

### 2. `alert_history_publishing_refresh_duration_seconds`

**Type**: Histogram
**Labels**: `source`
**Description**: Refresh operation duration by source

```promql
histogram_quantile(0.95, rate(alert_history_publishing_refresh_duration_seconds_bucket[5m]))
```

### 3. `alert_history_publishing_refresh_errors_total`

**Type**: Counter
**Labels**: `source`, `error_type`
**Description**: Refresh errors by source and error type

### 4. `alert_history_publishing_refresh_last_success_timestamp`

**Type**: Gauge
**Labels**: `source`
**Description**: Timestamp of last successful refresh by source

```promql
# Alert if stale (>15m)
(time() - alert_history_publishing_refresh_last_success_timestamp) > 900
```

### 5. `alert_history_publishing_refresh_operations_in_progress`

**Type**: Gauge
**Description**: Current refresh operations in progress

## Error Handling

### Transient Errors (Retry OK)

- Network timeout
- Connection refused
- 503 Service Unavailable
- DNS resolution failure

**Action**: Automatic retry with exponential backoff (30s → 5m)

### Permanent Errors (No Retry)

- 401 Unauthorized
- 403 Forbidden
- Invalid configuration
- Parse errors (bad JSON, base64)

**Action**: Fail immediately, log error, alert

### Retry Schedule

| Attempt | Backoff | Cumulative Time |
|---------|---------|-----------------|
| 1       | 0s      | 0s              |
| 2       | 30s     | 30s             |
| 3       | 1m      | 1m 30s          |
| 4       | 2m      | 3m 30s          |
| 5       | 4m      | 7m 30s          |
| 6       | 5m      | 12m 30s (max)   |

## Troubleshooting

### Problem: Refresh never succeeds

**Symptoms**: All refreshes fail, targets stale

**Diagnosis**:
```bash
# Check metrics
curl http://localhost:8080/metrics | grep refresh_errors_total
```

**Solutions**:
1. Check K8s API connectivity (`kubectl get secrets`)
2. Verify RBAC permissions (ServiceAccount)
3. Check error logs for auth failures

### Problem: Refresh taking too long (>30s)

**Symptoms**: Timeouts, slow discovery

**Diagnosis**:
```promql
# Check p95 duration
histogram_quantile(0.95, alert_history_publishing_refresh_duration_seconds)
```

**Solutions**:
1. Increase `RefreshConfig.RefreshTimeout` (e.g., `60s`)
2. Reduce target count (filter secrets)
3. Check K8s API performance

### Problem: Rate limit errors

**Symptoms**: Manual refresh rejected by rate limiter

**Solutions**:
1. Wait between manual refreshes (default window: 1m)
2. Increase `RefreshConfig.RateLimitPer` (e.g., `2m`)
3. Use `GetStatus()` instead

## Performance

Benchmarks live in `refresh_bench_test.go`:
`go test ./internal/business/publishing -bench=Refresh -benchmem`

## Architecture

```
┌─────────────────────────────────────────────┐
│        RefreshManager                       │
├─────────────────────────────────────────────┤
│                                             │
│  ┌───────────────┐    ┌─────────────────┐ │
│  │ Background    │    │ RefreshNow()    │ │
│  │ Worker        │    │ (Manual)        │ │
│  │ (Periodic)    │    │                 │ │
│  └───────┬───────┘    └────────┬────────┘ │
│          │                     │          │
│          └──────┬──────────────┘          │
│                 │                         │
│      ┌──────────▼──────────┐             │
│      │ RefreshCoordinator  │             │
│      │ (Single-Flight)     │             │
│      └──────────┬──────────┘             │
│                 │                         │
│      ┌──────────▼──────────┐             │
│      │ Discovery Manager   │             │
│      └──────────┬──────────┘             │
│                 │                         │
└─────────────────┼─────────────────────────┘
                  │
       ┌──────────▼──────────┐
       │ K8s API Server      │
       │ (Secrets)           │
       └─────────────────────┘
```

## Security

- Rate limiting: ограничение частоты manual refresh (prevents DoS)
- Audit logging: all refresh attempts logged
- RBAC: K8s ServiceAccount permissions

## Dependencies

- `TargetDiscoveryManager` (`discovery*.go`)
- `internal/infrastructure/k8s` — K8s client
- `pkg/metrics/v2` — Prometheus metrics
- `log/slog` — structured logging

## References

- [K8s Client README](../../infrastructure/k8s/README.md)
- [Target Health Monitoring README](./HEALTH_MONITORING_README.md)
