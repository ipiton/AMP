# TwoTierAlertCache - Active Alert Cache

## Overview

**TwoTierAlertCache** is a caching solution for active firing alerts in the AMP inhibition system. It implements a two-tier caching strategy (L1 memory + L2 Redis) with observability and graceful degradation.

---

## Architecture

```
┌──────────────────────────────────────┐
│   TwoTierAlertCache                  │
│   (L1 read path, L2 shared state)   │
└─────────┬────────────────────────────┘
          │
          ├──> L1: In-Memory (the only tier GetFiringAlerts reads)
          │     - 1000 alerts max (configurable)
          │     - Thread-safe concurrent access
          │     - Background cleanup + L2 → L1 sync worker
          │
          └──> L2: Redis
                - Shared between replicas
                - Key TTL = until the alert's EndsAt
                - Merged into L1 every SyncInterval (5s)
```

### Key Features

**Two-Tier Caching**
- **Source lifetime (upstream semantics)**: a cached alert is an inhibition source while it is firing and its `EndsAt` is in the future, checked at query time. An alert without `endsAt` gets `EndsAt = now + resolve_timeout` (`ResolveTimeout` option); every `AddFiringAlert`, unchanged re-sends included, extends it. `StartsAt` never evicts.
- **L1 Cache**: In-memory map; when full, expired alerts go first, then the one closest to its `EndsAt`
- **L2 Cache**: Redis, written through on add/remove; a background pass merges it into L1 so sources received by other replicas become visible (and disappear when resolved there) within `SyncInterval`
- **Graceful degradation**: a Redis error keeps the current L1 state

**Observability** (Prometheus Metrics)
- `alert_history_inhibition_cache_hits_total` (by tier: l1, l2)
- `alert_history_inhibition_cache_misses_total`
- `alert_history_inhibition_cache_evictions_total`
- `alert_history_inhibition_cache_size` (current L1 size)
- `alert_history_inhibition_cache_operations_total` (by operation)
- `alert_history_inhibition_cache_operation_duration_seconds` (histogram)

**Operational Behavior**
- Thread-safe concurrent access
- Graceful Redis failures
- Background cleanup worker (removes expired alerts every 1 minute)
- Configurable capacity, cleanup and sync intervals, resolve timeout

---

## Usage

### Basic Usage

```go
import (
    "context"
    "github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// Create cache with defaults (L1-only, no Redis)
cache := inhibition.NewTwoTierAlertCache(nil, logger)
defer cache.Stop()

// Add alert
alert := &core.Alert{
    AlertName:   "HighCPU",
    Fingerprint: "abc123",
    Status:      "firing",
    StartsAt:    time.Now(),
}
err := cache.AddFiringAlert(ctx, alert)

// Get all firing alerts
alerts, err := cache.GetFiringAlerts(ctx)

// Remove alert
err = cache.RemoveAlert(ctx, "abc123")
```

### With Redis (L1 + L2)

```go
// Create Redis cache
redisCache, err := cache.NewCache(config.Redis)
if err != nil {
    return err
}

// Create two-tier cache
cache := inhibition.NewTwoTierAlertCache(redisCache, logger)
defer cache.Stop()

// All operations use both L1 and L2 automatically
_ = cache.AddFiringAlert(ctx, alert) // Adds to L1 + L2 (best-effort)
```

### Custom Configuration

```go
opts := &inhibition.AlertCacheOptions{
    CleanupInterval: 30 * time.Second,  // Cleanup every 30s
    L1Max:           5000,                // Max 5000 alerts in L1
    SyncInterval:    2 * time.Second,     // Merge Redis into L1 every 2s
    ResolveTimeout:  func() time.Duration { return 5 * time.Minute },
    Metrics:         customMetrics,       // Custom Prometheus metrics
}

cache := inhibition.NewTwoTierAlertCacheWithOptions(redisCache, logger, opts)
defer cache.Stop()
```

---

## Configuration

| Option | Default | Description |
|--------|---------|-------------|
| `CleanupInterval` | 1 minute | How often to run background cleanup |
| `L1Max` | 1000 | Maximum alerts in L1 cache |
| `SyncInterval` | 5 seconds | How often L1 is merged with Redis (cross-replica visibility) |
| `ResolveTimeout` | `alertconv.DefaultResolveTimeout` (5m) | `global.resolve_timeout` provider for alerts without `endsAt`, read on every add |
| `Metrics` | Auto-created | Prometheus metrics instance (singleton) |

---

## Performance

Benchmarks live in `cache_test.go`:
`go test -bench=TwoTierAlertCache -benchmem ./internal/infrastructure/inhibition/...`

---

## Tests

Test categories in `cache_test.go`: happy path, concurrent access,
stress, edge cases (fingerprints, contexts, timestamps, resolved vs firing).

---

## Prometheus Metrics

All metrics use the singleton pattern (registered once globally).

### 1. Cache Hits & Misses

```promql
# L1 cache hit rate
rate(alert_history_inhibition_cache_hits_total{tier="l1"}[5m])
/ rate(alert_history_inhibition_cache_operations_total{operation="get"}[5m])

# Failed L2 → L1 sync passes (tier="l2" counts sync passes: hit = read, miss = Redis error)
rate(alert_history_inhibition_cache_misses_total{tier="l2"}[5m])
```

### 2. Eviction Rate

```promql
# Alerts evicted per second
rate(alert_history_inhibition_cache_evictions_total[5m])
```

### 3. Cache Size

```promql
# Current L1 cache size
alert_history_inhibition_cache_size
```

### 4. Operation Latency

```promql
# p99 operation duration
histogram_quantile(0.99,
  rate(alert_history_inhibition_cache_operation_duration_seconds_bucket[5m])
)
```

---

## Implementation Details

### L1 Cache

- **Type**: map of `cacheEntry` (copy of the alert with `EndsAt` set, local write time, whether it is known to be in Redis)
- **Thread-safety**: `sync.RWMutex` for concurrent access
- **Eviction**: expired first, then the alert closest to its `EndsAt` (logged at WARN)
- **Capacity**: Configurable (default: 1000 alerts)
- **Cleanup**: Background worker removes expired alerts every 1 minute

### L2 Cache (Redis)

- **Key Pattern**: `inhibition:active_alerts:{fingerprint}`
- **Membership**: SET `inhibition:active_alerts:set`; members whose key expired are removed by the sync pass
- **TTL**: until the alert's `EndsAt`; an alert that already ended is not written
- **Fallback**: Best-effort writes (L1 continues on Redis failure)
- **Serialization**: JSON format

### Background Cleanup

```go
// Worker:
// 1. Every CleanupInterval (1m): drop alerts that are no longer active (EndsAt <= now)
// 2. With Redis, at start and every SyncInterval (5s): merge Redis into L1.
//    Local writes/removals made after a pass started win over what it read
//    (write time + tombstones); entries absent from Redis are dropped only
//    if they were known to be there (their own Redis write did not fail).
// Gracefully stops on cache.Stop()
```

---

## Error Handling

All errors are logged but don't fail operations (graceful degradation):

1. **Redis Unavailable**: Sync keeps the current L1; local sources keep working, other replicas' sources are not visible
2. **Context Cancelled**: Operations complete with L1 state
3. **Nil Alert**: Returns error immediately
4. **JSON Marshal Error**: Logs warning, continues with L1

---

## Thread Safety

✅ **Safe for concurrent use** from multiple goroutines:
- All operations protected by `sync.RWMutex`
- Read operations use `RLock()` (concurrent reads allowed)
- Write operations use `Lock()` (exclusive)
- Metrics use atomic operations (via Prometheus client)

---

## Production Deployment

### Recommended Configuration

```yaml
cache:
  cleanup_interval: 60s  # 1 minute
  l1_max: 1000           # Adjust based on memory

redis:
  enabled: true
  address: redis:6379
  db: 0
  pool_size: 10
```

### Monitoring Alerts

```yaml
# Cache hit rate below 80%
- alert: CacheLowHitRate
  expr: |
    rate(alert_history_inhibition_cache_hits_total{tier="l1"}[5m])
    / rate(alert_history_inhibition_cache_operations_total{operation="get"}[5m])
    < 0.8
  for: 5m

# High eviction rate (>10/s)
- alert: CacheHighEvictionRate
  expr: rate(alert_history_inhibition_cache_evictions_total[5m]) > 10
  for: 5m

# Redis unavailable (all L2 misses)
- alert: CacheRedisDown
  expr: rate(alert_history_inhibition_cache_misses_total{tier="l2"}[5m]) > 0
  for: 5m
```

---

## Development

### Running Tests

```bash
# All tests
go test ./internal/infrastructure/inhibition/...

# With coverage
go test -cover ./internal/infrastructure/inhibition/...

# With race detector
go test -race ./internal/infrastructure/inhibition/...

# Specific test
go test -v -run TestTwoTierAlertCache_ConcurrentAdds ./internal/infrastructure/inhibition/...

# Benchmarks
go test -bench=. -benchmem ./internal/infrastructure/inhibition/...
```

## Future Enhancements

Potential improvements:

1. **Sources only**: cache only alerts matching some rule's source side (index per rule, `INHIBIT-MATCH-PERF`)
2. **Push instead of poll**: Redis pub/sub instead of the periodic sync
3. **Compression**: Compress large alerts in Redis
4. **Metrics by Cache Instance**: Per-instance metrics (if multiple caches)
5. **Adaptive Capacity**: Auto-adjust L1 capacity based on memory pressure

---

## License

Part of AMP. See `LICENSE` in the repository root.
