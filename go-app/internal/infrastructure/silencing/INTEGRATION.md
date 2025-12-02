# Integration Guide: Wiring Silence Repository in main.go

This document describes how to integrate `PostgresSilenceRepository` into the Alert History service's `main.go`.

## Integration Points

### 1. Import Packages

```go
import (
    // ... existing imports ...

    "github.com/vitaliisemenov/alert-history/internal/infrastructure/silencing"
    "github.com/vitaliisemenov/alert-history/internal/core/silencing as coresilencing"
)
```

### 2. Initialize Silence Metrics

```go
// In main() function, after initializing BusinessMetrics
func main() {
    // ... existing initialization ...

    // Initialize silence metrics
    silenceMetrics := silencing.NewSilenceMetrics()

    logger.Info("silence metrics initialized")
}
```

### 3. Initialize Silence Repository

```go
// After PostgreSQL pool initialization
func main() {
    // ... existing pool initialization ...

    // Initialize silence repository
    silenceRepo := silencing.NewPostgresSilenceRepository(
        pgPool,
        logger,
        silenceMetrics,
    )

    logger.Info("silence repository initialized")
}
```

### 4. Start Silence Cleanup Worker

```go
// Start background workers
func main() {
    // ... existing workers ...

    // Start silence TTL cleanup worker
    go startSilenceCleanupWorker(ctx, silenceRepo, logger)

    logger.Info("silence cleanup worker started")
}

// Cleanup worker implementation
func startSilenceCleanupWorker(
    ctx context.Context,
    repo *silencing.PostgresSilenceRepository,
    logger *slog.Logger,
) {
    ticker := time.NewTicker(1 * time.Hour) // Run every hour
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            // Step 1: Expire silences that ended before now
            expiredCount, err := repo.ExpireSilences(ctx, time.Now(), false)
            if err != nil {
                logger.Error("failed to expire silences",
                    "error", err,
                )
            } else {
                logger.Info("expired silences",
                    "count", expiredCount,
                )
            }

            // Step 2: Delete silences expired 30+ days ago
            cutoff := time.Now().Add(-30 * 24 * time.Hour)
            deletedCount, err := repo.ExpireSilences(ctx, cutoff, true)
            if err != nil {
                logger.Error("failed to delete old silences",
                    "error", err,
                )
            } else {
                logger.Info("deleted old silences",
                    "count", deletedCount,
                )
            }

        case <-ctx.Done():
            logger.Info("silence cleanup worker stopped")
            return
        }
    }
}
```

### 5. Register Silence API Endpoints (Future - TN-134)

```go
// After HTTP router initialization
func main() {
    // ... existing HTTP handlers ...

    // Silence API endpoints (TN-134: Silence Manager Service)
    // TODO: Implement in TN-135 (Silence API Endpoints)

    // POST /api/v2/silences - Create silence
    // router.POST("/api/v2/silences", silenceHandler.CreateSilence)

    // GET /api/v2/silences - List silences
    // router.GET("/api/v2/silences", silenceHandler.ListSilences)

    // GET /api/v2/silences/:id - Get silence by ID
    // router.GET("/api/v2/silences/:id", silenceHandler.GetSilence)

    // PUT /api/v2/silences/:id - Update silence
    // router.PUT("/api/v2/silences/:id", silenceHandler.UpdateSilence)

    // DELETE /api/v2/silences/:id - Delete silence
    // router.DELETE("/api/v2/silences/:id", silenceHandler.DeleteSilence)

    logger.Info("silence API endpoints registered (pending TN-135)")
}
```

### 6. Graceful Shutdown

```go
// Graceful shutdown (already handled by context cancellation)
func main() {
    // ... existing shutdown logic ...

    // Cancel context to stop cleanup worker
    cancel()

    // Wait for cleanup worker to stop
    time.Sleep(1 * time.Second)

    logger.Info("silence repository shutdown complete")
}
```

## Complete Integration Example

Here's a complete example of how the integration looks in `cmd/server/main.go`:

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/vitaliisemenov/alert-history/internal/infrastructure/silencing"
)

func main() {
    // 1. Initialize logger
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

    // 2. Create context with cancellation
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // 3. Initialize PostgreSQL connection pool
    pgPool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
    if err != nil {
        logger.Error("failed to connect to database", "error", err)
        os.Exit(1)
    }
    defer pgPool.Close()

    // 4. Initialize silence metrics
    silenceMetrics := silencing.NewSilenceMetrics()
    logger.Info("silence metrics initialized")

    // 5. Initialize silence repository
    silenceRepo := silencing.NewPostgresSilenceRepository(
        pgPool,
        logger,
        silenceMetrics,
    )
    logger.Info("silence repository initialized")

    // 6. Start silence cleanup worker
    go startSilenceCleanupWorker(ctx, silenceRepo, logger)
    logger.Info("silence cleanup worker started")

    // 7. Initialize HTTP server (placeholder for TN-135)
    router := http.NewServeMux()

    // TODO: Register silence API endpoints (TN-135)
    // silenceHandler := handlers.NewSilenceHandler(silenceRepo, logger)
    // router.HandleFunc("/api/v2/silences", silenceHandler.HandleSilences)
    // router.HandleFunc("/api/v2/silences/", silenceHandler.HandleSilenceByID)

    server := &http.Server{
        Addr:    ":8080",
        Handler: router,
    }

    // 8. Start HTTP server
    go func() {
        logger.Info("starting HTTP server", "addr", server.Addr)
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            logger.Error("HTTP server error", "error", err)
        }
    }()

    // 9. Wait for shutdown signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

    <-sigCh
    logger.Info("shutdown signal received")

    // 10. Graceful shutdown
    cancel() // Stop cleanup worker

    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer shutdownCancel()

    if err := server.Shutdown(shutdownCtx); err != nil {
        logger.Error("HTTP server shutdown error", "error", err)
    }

    // Wait for cleanup worker to stop
    time.Sleep(1 * time.Second)

    logger.Info("shutdown complete")
}

func startSilenceCleanupWorker(
    ctx context.Context,
    repo *silencing.PostgresSilenceRepository,
    logger *slog.Logger,
) {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            expiredCount, err := repo.ExpireSilences(ctx, time.Now(), false)
            if err != nil {
                logger.Error("failed to expire silences", "error", err)
            } else {
                logger.Info("expired silences", "count", expiredCount)
            }

            cutoff := time.Now().Add(-30 * 24 * time.Hour)
            deletedCount, err := repo.ExpireSilences(ctx, cutoff, true)
            if err != nil {
                logger.Error("failed to delete old silences", "error", err)
            } else {
                logger.Info("deleted old silences", "count", deletedCount)
            }

        case <-ctx.Done():
            logger.Info("silence cleanup worker stopped")
            return
        }
    }
}
```

## Configuration

### Environment Variables

```bash
# Database connection
export DATABASE_URL="postgres://user:password@localhost:5432/alerthistory"

# Cleanup worker interval (optional, default: 1h)
export SILENCE_CLEANUP_INTERVAL="1h"

# TTL for old silences (optional, default: 30d)
export SILENCE_TTL_DAYS="30"
```

### Configuration File (config.yaml)

```yaml
database:
  url: postgres://user:password@localhost:5432/alerthistory
  max_connections: 50
  max_idle_connections: 10
  connection_max_lifetime: 30m

silence:
  cleanup_interval: 1h  # How often to run cleanup worker
  ttl_days: 30          # Delete silences expired > 30 days ago
```

## Health Checks

Add health check endpoint for silence repository:

```go
// GET /health/silences
func (h *HealthHandler) CheckSilenceRepository(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
    defer cancel()

    // Test database connectivity with simple query
    filter := silencing.SilenceFilter{Limit: 1}
    _, err := h.silenceRepo.ListSilences(ctx, filter)

    if err != nil {
        w.WriteHeader(http.StatusServiceUnavailable)
        json.NewEncoder(w).Encode(map[string]string{
            "status": "unhealthy",
            "error":  err.Error(),
        })
        return
    }

    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{
        "status": "healthy",
    })
}
```

## Prometheus Metrics Endpoint

Silence metrics are automatically registered with Prometheus and exposed via `/metrics`:

```go
import (
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
    // ... initialization ...

    // Expose Prometheus metrics
    router.Handle("/metrics", promhttp.Handler())

    logger.Info("Prometheus metrics endpoint registered")
}
```

## Dependencies

Update `go.mod`:

```go
require (
    github.com/jackc/pgx/v5 v5.5.0
    github.com/google/uuid v1.5.0
    github.com/prometheus/client_golang v1.18.0
)
```

Run:

```bash
go mod tidy
```

## Next Steps

1. **TN-134: Silence Manager Service** - Business logic layer on top of repository
2. **TN-135: Silence API Endpoints** - REST API handlers for silence CRUD operations
3. **TN-136: Silence Matching Integration** - Integrate silence matching into alert processing pipeline

## Testing Integration

### Manual Testing

```bash
# 1. Start PostgreSQL
docker run -d --name postgres \
  -e POSTGRES_PASSWORD=password \
  -e POSTGRES_DB=alerthistory \
  -p 5432:5432 \
  postgres:16

# 2. Run migrations
psql -h localhost -U postgres -d alerthistory -f migrations/create_silences_table.sql

# 3. Start service
export DATABASE_URL="postgres://postgres:password@localhost:5432/alerthistory"
go run cmd/server/main.go

# 4. Check logs for initialization
# Expected output:
#   level=INFO msg="silence metrics initialized"
#   level=INFO msg="silence repository initialized"
#   level=INFO msg="silence cleanup worker started"
```

### Integration Test

```go
package main

import (
    "context"
    "testing"
    "time"

    "github.com/vitaliisemenov/alert-history/internal/core/silencing"
)

func TestSilenceRepositoryIntegration(t *testing.T) {
    // Setup
    repo := setupTestRepository(t)
    ctx := context.Background()

    // Create silence
    silence := &silencing.Silence{
        CreatedBy: "test@example.com",
        Comment:   "Integration test",
        StartsAt:  time.Now(),
        EndsAt:    time.Now().Add(1 * time.Hour),
        Matchers: []silencing.Matcher{
            {Name: "alertname", Value: "TestAlert", Type: silencing.MatcherTypeEqual},
        },
    }

    created, err := repo.CreateSilence(ctx, silence)
    if err != nil {
        t.Fatalf("CreateSilence failed: %v", err)
    }

    // Get silence
    retrieved, err := repo.GetSilenceByID(ctx, created.ID)
    if err != nil {
        t.Fatalf("GetSilenceByID failed: %v", err)
    }

    if retrieved.CreatedBy != "test@example.com" {
        t.Errorf("CreatedBy mismatch: got %s, want test@example.com", retrieved.CreatedBy)
    }

    t.Logf("✅ Integration test passed: silence %s created and retrieved", created.ID)
}
```

## Troubleshooting

### Common Issues

1. **Database connection failed**
   - Check `DATABASE_URL` environment variable
   - Verify PostgreSQL is running: `psql -h localhost -U postgres -d alerthistory`
   - Check network connectivity

2. **Cleanup worker not running**
   - Check logs for "silence cleanup worker started"
   - Verify context is not cancelled prematurely
   - Check `SILENCE_CLEANUP_INTERVAL` configuration

3. **Metrics not exposed**
   - Verify `/metrics` endpoint is registered
   - Check Prometheus scrape configuration
   - Verify `silenceMetrics` is initialized before repository

## Status

- ✅ **Phase 9: Integration Points Documented**
- ⏳ **TN-134: Silence Manager Service** (pending)
- ⏳ **TN-135: Silence API Endpoints** (pending)
- ⏳ **TN-136: Silence Matching Integration** (pending)

## Version

**Version:** 1.0.0
**Status:** Integration-Ready
**Date:** 2025-11-06
