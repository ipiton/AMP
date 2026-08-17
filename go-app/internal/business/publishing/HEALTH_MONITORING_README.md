# Target Health Monitoring

## Overview

**Target Health Monitoring** система периодически проверяет здоровье publishing targets (Rootly, PagerDuty, Slack, Webhooks) и предоставляет visibility через Prometheus metrics.

> **Historical note**: ранние ревизии этого документа описывали отдельный HTTP API
> (`/api/v2/publishing/targets/health*`). Эти endpoints не смонтированы в текущем
> active router (`internal/application/router.go`). HealthMonitor используется
> внутри `internal/application/service_registry.go`; наружу состояние доступно
> через Prometheus metrics.

### Key Features

- **Periodic Health Checks**: Background worker проверяет targets (default: каждые 2 минуты)
- **HTTP Connectivity Test**: TCP + HTTP GET (fail-fast strategy)
- **Parallel Execution**: Goroutine pool (default: max 10 concurrent checks)
- **Error Classification**: Timeout/DNS/TLS/Auth/HTTP errors с retry logic
- **Graceful Degradation**: Continues on errors, не блокирует alert pipeline
- **Prometheus Metrics**: через `pkg/metrics/v2` (subsystem `publishing`)
- **Thread-Safe**: RWMutex для cache

---

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│                     Target Health Monitor                      │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  ┌──────────────────┐     ┌──────────────────────────────┐   │
│  │  HealthMonitor   │────▶│  TargetDiscoveryManager      │   │
│  │  Interface       │     │                              │   │
│  └──────────────────┘     └──────────────────────────────┘   │
│           │                                                    │
│           │                                                    │
│  ┌────────▼────────────────────────────────────────────────┐  │
│  │  DefaultHealthMonitor                                   │  │
│  ├─────────────────────────────────────────────────────────┤  │
│  │  - Background Worker (periodic checks)                  │  │
│  │  - HTTP Health Checker (TCP + HTTP GET)                 │  │
│  │  - Status Cache (in-memory, O(1) lookup)                │  │
│  │  - Failure Detection (threshold: 3 consecutive)         │  │
│  │  - Retry Logic (1 retry for transient errors)           │  │
│  └─────────────────────────────────────────────────────────┘  │
│           │                                                    │
│           │                                                    │
│  ┌────────▼────────────────────────────────────────────────┐  │
│  │  Prometheus metrics (pkg/metrics/v2, publishing)        │  │
│  └─────────────────────────────────────────────────────────┘  │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Responsibility |
|-----------|---------------|
| `health.go` | Interface + data structures |
| `health_impl.go` | DefaultHealthMonitor implementation |
| `health_checker.go` | HTTP connectivity test + retry logic |
| `health_worker.go` | Background worker + parallel execution |
| `health_cache.go` | Thread-safe status cache |
| `health_status.go` | Status transitions & failure detection |
| `health_errors.go` | Error types & classification |

---

## Quick Start

### 1. Initialization

```go
import (
	"github.com/ipiton/AMP/internal/business/publishing"
)

// Initialize Health Monitor
healthConfig := publishing.DefaultHealthConfig()
healthConfig.CheckInterval = 2 * time.Minute  // Override defaults
healthConfig.HTTPTimeout = 5 * time.Second

healthMonitor, err := publishing.NewHealthMonitor(
	discoveryManager,
	healthConfig,
	logger,
	metricsRegistry,
)
if err != nil {
	log.Fatal(err)
}

// Start background worker
if err := healthMonitor.Start(); err != nil {
	log.Fatal(err)
}
defer healthMonitor.Stop(10 * time.Second)
```

### 2. Configuration

#### HealthConfig Struct

```go
type HealthConfig struct {
	// Timing
	CheckInterval time.Duration // Interval between checks
	HTTPTimeout   time.Duration // HTTP request timeout
	WarmupDelay   time.Duration // Delay before first check

	// Thresholds
	FailureThreshold  int           // Consecutive failures → unhealthy
	DegradedThreshold time.Duration // Latency threshold for degraded

	// Parallelism
	MaxConcurrentChecks int // Max parallel health checks

	// HTTP Client
	MaxIdleConns    int  // HTTP client connection pool
	TLSSkipVerify   bool // Skip TLS verification
	FollowRedirects bool // Follow HTTP redirects
	MaxRedirects    int  // Max redirect hops
}
```

**Defaults** (`DefaultHealthConfig()`):
- `CheckInterval`: 2m
- `HTTPTimeout`: 5s
- `WarmupDelay`: 10s
- `FailureThreshold`: 3
- `DegradedThreshold`: 5s (latency)
- `MaxConcurrentChecks`: 10
- `MaxIdleConns`: 100
- `FollowRedirects`: true, `MaxRedirects`: 3

---

## Programmatic Access

HTTP endpoints для health-статусов не смонтированы в active runtime.
Состояние доступно программно через интерфейс `HealthMonitor`
(`GetHealth`, `GetHealthByName`, `GetStats`, `CheckNow` — см. `health.go`)
и через Prometheus metrics.

---

## Health Check Logic

### 1. HTTP Connectivity Test

**Flow**:
```
1. Parse target URL (validate format)
   │
   ├─ Error → return ErrorTypeUnknown
   │
2. TCP Handshake (fail fast)
   │
   ├─ Success → proceed to HTTP request
   │
   ├─ Timeout → return ErrorTypeTimeout (transient)
   │
   ├─ DNS error → return ErrorTypeNetwork (transient)
   │
   ├─ Connection refused → return ErrorTypeNetwork (transient)
   │
3. HTTP GET Request
   │
   ├─ HTTP 200-299 → Success (healthy)
   │
   ├─ HTTP 401/403 → return ErrorTypeAuth (permanent)
   │
   ├─ HTTP 4xx/5xx → return ErrorTypeHTTP (permanent)
   │
   ├─ Timeout → return ErrorTypeTimeout (transient)
   │
4. Measure Latency
   │
   └─ Return result (success/failure + latency)
```

**Performance**:
- Success: ~100-300ms (HTTP roundtrip)
- Timeout: ~5s (max timeout)
- TCP failure: ~50ms (fail fast)

---

### 2. Error Classification

| Error Type | Transient | Retry | Example |
|------------|-----------|-------|---------|
| `ErrorTypeTimeout` | ✅ Yes | ✅ Yes | Connection timeout after 5s |
| `ErrorTypeNetwork` | ✅ Yes | ✅ Yes | Connection refused, DNS failure |
| `ErrorTypeAuth` | ❌ No | ❌ No | HTTP 401/403 Unauthorized |
| `ErrorTypeHTTP` | ❌ No | ❌ No | HTTP 4xx/5xx status code |
| `ErrorTypeConfig` | ❌ No | ❌ No | Invalid target URL |
| `ErrorTypeCancelled` | ❌ No | ❌ No | Context cancelled |
| `ErrorTypeUnknown` | ⚠️ Maybe | ✅ Yes | Other errors |

**Retry Strategy**:
- **Transient errors**: 1 retry after 100ms
- **Permanent errors**: No retry (fail immediately)
- **Unknown errors**: 1 retry (defensive strategy)

---

### 3. Failure Detection

**Health Status Transitions**:
```
unknown → healthy (first successful check)
   │
   ├─ 1 failure → degraded
   │      │
   │      ├─ 1 more failure (total 2) → degraded
   │      │      │
   │      │      ├─ 1 more failure (total 3) → unhealthy
   │      │      │
   │      │      └─ success → healthy (reset)
   │      │
   │      └─ success → healthy (reset)
   │
   └─ success → healthy (reset)
```

**Thresholds** (configurable):
- **Degraded**: failures ниже `FailureThreshold`, либо latency >= `DegradedThreshold` (default 5s)
- **Unhealthy**: `FailureThreshold` consecutive failures (default: 3)

**Recovery Detection**:
- Single successful check → immediately transitions to `healthy`
- Resets consecutive failure counter

---

## Prometheus Metrics

Метрики регистрируются в `pkg/metrics/v2/publishing.go`
(namespace `alert_history`, subsystem `publishing`):

### 1. alert_history_publishing_health_checks_total

**Type**: Counter
**Labels**: `target`, `status`

**Description**: Health checks by target and status.

**PromQL Examples**:
```promql
# Total health checks per target
sum by (target) (alert_history_publishing_health_checks_total)

# Success rate per target
sum by (target) (rate(alert_history_publishing_health_checks_total{status="success"}[5m]))
  / sum by (target) (rate(alert_history_publishing_health_checks_total[5m]))
```

---

### 2. alert_history_publishing_health_check_duration_seconds

**Type**: Histogram
**Labels**: `target`

**Description**: Health check duration by target.

**PromQL Examples**:
```promql
# p95 health check latency
histogram_quantile(0.95, sum(rate(alert_history_publishing_health_check_duration_seconds_bucket[5m])) by (le, target))
```

---

### 3. alert_history_publishing_target_health_status

**Type**: Gauge
**Labels**: `target`, `target_type`

**Description**: Target health status (0=unknown, 1=healthy, 2=degraded, 3=unhealthy).

**PromQL Examples**:
```promql
# Unhealthy targets
alert_history_publishing_target_health_status == 3
```

---

### 4. alert_history_publishing_target_consecutive_failures

**Type**: Gauge
**Labels**: `target`

**Description**: Consecutive failures count for target.

---

### 5. alert_history_publishing_target_success_rate

**Type**: Gauge
**Labels**: `target`

**Description**: Success rate (0-100%) for target.

---

## Troubleshooting

### Problem 1: All targets show "unknown" status

**Symptoms**:
- All targets have `status: "unknown"`
- `last_check_time` is nil

**Causes**:
1. Health monitor not started
2. Background worker crashed
3. Discovery manager has no targets

**Solutions**:
```bash
# Check metrics
curl http://localhost:8080/metrics | grep alert_history_publishing_health

# Check logs for the health monitor start message
```

---

### Problem 2: Health checks timing out

**Symptoms**:
- `error_message: "connection timeout after 5s"`
- High p95 latency (>5s)

**Causes**:
1. Target is slow to respond
2. Network latency
3. HTTP timeout too short

**Solutions**:
- Увеличить `HealthConfig.HTTPTimeout` (default: 5s)
- Проверить connectivity вручную: `curl -v -m 10 <target-url>`

---

### Problem 3: Too many false positives

**Symptoms**:
- Targets flapping between healthy/unhealthy
- Alerts firing frequently

**Causes**:
1. Failure threshold too low (default: 3)
2. Network instability

**Solutions**:
- Увеличить `HealthConfig.FailureThreshold` (default: 3)
- Увеличить `HealthConfig.CheckInterval` (default: 2m)

---

## Performance

Бенчмарки в `health_bench_test.go`:
`go test ./internal/business/publishing -bench=Health -benchmem`

---

## Dependencies

- `TargetDiscoveryManager` (`discovery*.go`) — источник списка targets
- `internal/infrastructure/k8s` — K8s client для secrets discovery
- `pkg/metrics/v2` — Prometheus metrics registry
- `log/slog` — structured logging
- `RefreshManager` (`refresh_*.go`) — auto-refresh targets (optional)

---

## Testing

### Unit Tests

```bash
# Run all health monitoring tests
cd go-app/internal/business/publishing
go test -v -run TestHealth

# With coverage
go test -v -coverprofile=coverage.out
go tool cover -html=coverage.out
```

**Target**: 80%+ coverage

---

### Integration Tests

```bash
# Test full health check flow
go test -v -run TestHealthMonitor_Integration

# Test with real K8s cluster
export KUBECONFIG=~/.kube/config
go test -v -tags=integration -run TestHealthMonitor_K8s
```

---

### Manual Testing

```bash
# 1. Start server, then check Prometheus metrics
curl http://localhost:8080/metrics | grep alert_history_publishing_health
```

---

## Production Deployment

### 1. Configure RBAC (K8s)

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: amp-health-monitor
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list"]
```

---

### 3. Verify Deployment

```bash
# Check metrics
curl http://localhost:8080/metrics | grep alert_history_publishing_health
```

---

## FAQ

**Q: How often are targets checked?**
A: Every 2 minutes by default. Configurable via `HealthConfig.CheckInterval`.

**Q: What happens if a target is unhealthy?**
A: The system continues processing alerts normally. Health status is informational only and doesn't block the alert pipeline.

**Q: Does health monitoring impact alert processing?**
A: No. Health checks run in background goroutines and don't block alert processing.

**Q: What if TargetDiscoveryManager returns no targets?**
A: Health monitor gracefully handles empty target lists. It logs a warning and waits for next discovery cycle.

---

## Related Documentation

- [K8s Client README](../../infrastructure/k8s/README.md)
- [Target Refresh README](./REFRESH_README.md)
