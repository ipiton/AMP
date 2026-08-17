# Alert History Repository

Alert history repository with querying, analytics, and observability.

> **Historical note**: ранние ревизии этого документа описывали HTTP endpoints
> (`GET /history`, `/history/recent`, `/history/stats`, `/history/top`,
> `/history/flapping`) и handler `HistoryHandlerV2`. Эти routes не смонтированы
> в текущем active router, а handler отсутствует в коде. Repository доступен
> программно через интерфейс `core.AlertHistoryRepository`.

## Features

- **Pagination** - Pagination with configurable page sizes
- **Sorting** - Sort by created_at, starts_at, ends_at, status, severity
- **Filtering** - By status, severity, namespace, labels, time range
- **Analytics** - Aggregated stats, top alerts, flapping detection
- **Prometheus Metrics** - Query duration, errors, result counts, cache hits
- **SQL** - Queries designed around существующие индексы

---

## Architecture

```
AlertHistoryRepository → AlertStorage → PostgreSQL
```

- **AlertHistoryRepository**: High-level interface for history operations
- **AlertStorage**: Low-level CRUD operations
- **Metrics**: Prometheus metrics for all operations

## Repository Methods

```go
GetHistory(ctx, req *core.HistoryRequest) (*core.HistoryResponse, error)
GetAlertsByFingerprint(ctx, fingerprint string, limit int) ([]*core.Alert, error)
GetRecentAlerts(ctx, limit int) ([]*core.Alert, error)
GetAggregatedStats(ctx, timeRange *core.TimeRange) (*core.AggregatedStats, error)
GetTopAlerts(ctx, timeRange *core.TimeRange, limit int) ([]*core.TopAlert, error)
GetFlappingAlerts(ctx, timeRange *core.TimeRange, threshold int) ([]*core.FlappingAlert, error)
```

---

## Prometheus Metrics

All operations emit Prometheus metrics (namespace `alert_history`,
subsystem `infra_repository` / `infra_cache`):

### 1. `alert_history_infra_repository_query_duration_seconds`
**Type**: Histogram
**Labels**: `operation`, `status`
**Description**: Duration of alert history queries

### 2. `alert_history_infra_repository_query_errors_total`
**Type**: Counter
**Labels**: `operation`, `error_type`
**Description**: Total number of query errors

### 3. `alert_history_infra_repository_query_results_total`
**Type**: Histogram
**Labels**: `operation`
**Description**: Number of results returned

### 4. `alert_history_infra_cache_hits_total`
**Type**: Counter
**Labels**: `cache_type`
**Description**: Cache hit statistics

---

## Code Examples

### Creating Repository

```go
package main

import (
    "log/slog"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/ipiton/AMP/internal/core"
    "github.com/ipiton/AMP/internal/infrastructure/repository"
)

func main() {
    // Initialize database
    pool, err := pgxpool.New(ctx, "postgres://...")
    if err != nil {
        panic(err)
    }
    defer pool.Close()

    // storage — любая реализация core.AlertStorage поверх той же БД

    // Create repository
    historyRepo := repository.NewPostgresHistoryRepository(pool, storage, slog.Default())
}
```

### Querying History

```go
ctx := context.Background()

// Build request
req := &core.HistoryRequest{
    Filters: &core.AlertFilters{
        Status: func() *core.AlertStatus {
            s := core.StatusFiring
            return &s
        }(),
        Severity: stringPtr("critical"),
        Namespace: stringPtr("production"),
    },
    Pagination: &core.Pagination{
        Page:    1,
        PerPage: 50,
    },
    Sorting: &core.Sorting{
        Field: "starts_at",
        Order: core.SortOrderDesc,
    },
}

// Get history
response, err := historyRepo.GetHistory(ctx, req)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Total: %d, Page: %d/%d\n",
    response.Total,
    response.Page,
    response.TotalPages)

for _, alert := range response.Alerts {
    fmt.Printf("- %s: %s\n", alert.AlertName, alert.Status)
}
```

### Getting Stats

```go
timeRange := &core.TimeRange{
    From: timePtr(time.Now().Add(-24 * time.Hour)),
    To:   timePtr(time.Now()),
}

stats, err := historyRepo.GetAggregatedStats(ctx, timeRange)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Total alerts: %d\n", stats.TotalAlerts)
fmt.Printf("Firing: %d, Resolved: %d\n",
    stats.FiringAlerts,
    stats.ResolvedAlerts)

for severity, count := range stats.AlertsBySeverity {
    fmt.Printf("- %s: %d\n", severity, count)
}
```

---

## Performance

### Query Optimization

All queries are optimized with:
- Indexes on frequently queried columns
- JSONB operators for label filtering
- COUNT optimization with early exit
- Efficient pagination with LIMIT/OFFSET

### Recommended Indexes

```sql
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);
CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_starts_at ON alerts(starts_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_fingerprint ON alerts(fingerprint);

-- JSONB indexes for label filtering
CREATE INDEX IF NOT EXISTS idx_alerts_labels_severity ON alerts((labels->>'severity'));
CREATE INDEX IF NOT EXISTS idx_alerts_labels_namespace ON alerts((labels->>'namespace'));
CREATE INDEX IF NOT EXISTS idx_alerts_labels_gin ON alerts USING GIN (labels);
```

---

## Testing

### Running Tests

```bash
# Unit tests
go test ./internal/core/... -v -cover

# Repository tests (requires PostgreSQL)
go test ./internal/infrastructure/repository/... -v -cover

# Benchmarks
go test ./internal/core/... -bench=. -benchmem

# Coverage report
go test ./internal/core/... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

---

## Error Handling

All errors are properly wrapped and categorized:

```go
// Validation errors
ErrInvalidPagination
ErrInvalidPage
ErrInvalidPerPage
ErrPerPageTooLarge
ErrInvalidSortField
ErrInvalidSortOrder

// Database errors (wrapped)
fmt.Errorf("failed to query alerts: %w", err)
```

---

## Production Deployment

### Configuration

```yaml
# config.yaml
database:
  host: postgres.production.svc.cluster.local
  port: 5432
  name: alert_history
  max_connections: 100
  max_idle_connections: 25
  connection_max_lifetime: 5m

history:
  default_page_size: 50
  max_page_size: 1000
  cache_ttl: 5m
```

### Monitoring

Watch these Prometheus metrics:
- Query duration P95 < 100ms
- Error rate < 1%
- Cache hit rate > 80%
- Active connections < 80% of max

### Scaling

- Horizontal: Multiple replicas with load balancer
- Vertical: Increase database connection pool
- Caching: Redis for frequently accessed data
- Read replicas: For analytics queries

---

## Troubleshooting

### Slow Queries

1. Check indexes: `EXPLAIN ANALYZE SELECT ...`
2. Monitor query duration metrics
3. Optimize filters (use indexed columns)
4. Consider partitioning for large tables

### High Memory Usage

1. Reduce page size (default: 50)
2. Limit aggregation queries
3. Add pagination to all results
4. Monitor Go heap profile

### Database Locks

1. Use shorter transactions
2. Add connection pool timeout
3. Monitor lock wait metrics
4. Consider read replicas

---

## License

Part of AMP. See `LICENSE` in the repository root.
