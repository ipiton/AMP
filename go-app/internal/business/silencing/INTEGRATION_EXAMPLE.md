# SilenceManager Integration Example

This document provides a complete integration example for the Silence Manager Service (TN-134).

## Table of Contents
- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Basic Integration](#basic-integration)
- [main.go Integration](#maingo-integration)
- [AlertProcessor Integration](#alertprocessor-integration)
- [Configuration](#configuration)
- [Kubernetes Deployment](#kubernetes-deployment)

## Overview

The Silence Manager Service provides centralized management of alert silences with:
- CRUD operations via repository
- In-memory caching for fast lookups
- Alert filtering (IsAlertSilenced)
- Background GC and sync workers
- Prometheus metrics

## Prerequisites

1. PostgreSQL database (for silence storage)
2. Existing SilenceMatcher implementation (from TN-132)
3. Alert History application structure

## Basic Integration

```go
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vitaliisemenov/alert-history/internal/business/silencing"
	"github.com/vitaliisemenov/alert-history/internal/core/silencing" as coresilencing
	infrasilencing "github.com/vitaliisemenov/alert-history/internal/infrastructure/silencing"
	"github.com/vitaliisemenov/alert-history/internal/database/postgres"
)

func main() {
	// 1. Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// 2. Initialize PostgreSQL connection pool
	pool, err := postgres.NewPool(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("Failed to create PostgreSQL pool: %v", err)
	}
	defer pool.Close()

	// 3. Initialize SilenceRepository
	silenceRepo := infrasilencing.NewPostgresSilenceRepository(pool, logger)

	// 4. Initialize SilenceMatcher (from TN-132)
	silenceMatcher := coresilencing.NewDefaultSilenceMatcher(logger)

	// 5. Create SilenceManager
	silenceManager := silencing.NewDefaultSilenceManager(
		silenceRepo,
		silenceMatcher,
		logger,
		nil, // Use default config
	)

	// 6. Start SilenceManager (performs initial cache sync + starts workers)
	ctx := context.Background()
	if err := silenceManager.Start(ctx); err != nil {
		log.Fatalf("Failed to start silence manager: %v", err)
	}
	logger.Info("Silence manager started successfully")

	// 7. Use silence manager in your application
	//    (see AlertProcessor Integration section below)

	// 8. Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := silenceManager.Stop(shutdownCtx); err != nil {
		logger.Error("Silence manager shutdown error", "error", err)
	} else {
		logger.Info("Silence manager stopped gracefully")
	}
}
```

## main.go Integration

Add the following to your existing `main.go`:

```go
// After initializing PostgreSQL pool and before starting HTTP server:

// Initialize Silence Manager
silenceRepo := infrasilencing.NewPostgresSilenceRepository(pool, logger)
silenceMatcher := coresilencing.NewDefaultSilenceMatcher(logger)
silenceManager := silencing.NewDefaultSilenceManager(silenceRepo, silenceMatcher, logger, nil)

if err := silenceManager.Start(ctx); err != nil {
	logger.Fatal("Failed to start silence manager", "error", err)
}
logger.Info("Silence manager started")

// Add to graceful shutdown (before pool.Close()):
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := silenceManager.Stop(shutdownCtx); err != nil {
	logger.Warn("Silence manager shutdown timeout", "error", err)
}
```

## AlertProcessor Integration

Integrate silence filtering into your alert processing pipeline:

```go
package processor

import (
	"context"
	"log/slog"

	"github.com/vitaliisemenov/alert-history/internal/business/silencing"
	coresilencing "github.com/vitaliisemenov/alert-history/internal/core/silencing"
)

type AlertProcessor struct {
	silenceManager silencing.SilenceManager
	logger         *slog.Logger
}

func NewAlertProcessor(sm silencing.SilenceManager, logger *slog.Logger) *AlertProcessor {
	return &AlertProcessor{
		silenceManager: sm,
		logger:         logger,
	}
}

// ProcessAlert processes an incoming alert with silence filtering
func (p *AlertProcessor) ProcessAlert(ctx context.Context, alert *Alert) error {
	p.logger.Info("Processing alert",
		"fingerprint", alert.Fingerprint,
		"alertname", alert.Labels["alertname"],
	)

	// Step 1: Convert alert to silencing.Alert format
	silencingAlert := &coresilencing.Alert{
		Labels:      alert.Labels,
		Annotations: alert.Annotations,
	}

	// Step 2: Check if alert is silenced
	silenced, silenceIDs, err := p.silenceManager.IsAlertSilenced(ctx, silencingAlert)
	if err != nil {
		// Fail-safe: log error but continue processing (don't block alerts)
		p.logger.Warn("Failed to check silence status, processing anyway",
			"error", err,
			"fingerprint", alert.Fingerprint,
		)
	}

	if silenced {
		p.logger.Info("Alert is silenced, skipping notification",
			"fingerprint", alert.Fingerprint,
			"silence_ids", silenceIDs,
		)
		// Store alert but skip notification
		return p.storeAlert(ctx, alert, true) // silenced=true
	}

	// Step 3: Process non-silenced alert normally
	p.logger.Info("Alert is not silenced, processing notification",
		"fingerprint", alert.Fingerprint,
	)

	// Store alert
	if err := p.storeAlert(ctx, alert, false); err != nil {
		return err
	}

	// Send notifications (Rootly, PagerDuty, Slack, etc.)
	return p.sendNotifications(ctx, alert)
}

func (p *AlertProcessor) storeAlert(ctx context.Context, alert *Alert, silenced bool) error {
	// Store alert in database with silenced flag
	// ...
	return nil
}

func (p *AlertProcessor) sendNotifications(ctx context.Context, alert *Alert) error {
	// Send notifications to configured targets
	// ...
	return nil
}
```

## Configuration

### Environment Variables

Configure Silence Manager via environment variables (12-factor app):

```bash
# GC Worker Settings
export SILENCE_GC_INTERVAL="5m"           # How often to run GC
export SILENCE_GC_RETENTION="24h"         # Keep expired silences for this long
export SILENCE_GC_BATCH_SIZE="1000"       # Max silences per GC run

# Sync Worker Settings
export SILENCE_SYNC_INTERVAL="1m"         # How often to sync cache

# Shutdown Settings
export SILENCE_SHUTDOWN_TIMEOUT="30s"     # Max time for graceful shutdown
```

### Custom Configuration

```go
import "github.com/vitaliisemenov/alert-history/internal/business/silencing"

// Create custom config
config := silencing.SilenceManagerConfig{
	GCInterval:      10 * time.Minute,  // Run GC every 10 minutes
	GCRetention:     48 * time.Hour,    // Keep expired for 48 hours
	GCBatchSize:     2000,              // Process 2000 silences per run
	SyncInterval:    2 * time.Minute,   // Sync cache every 2 minutes
	ShutdownTimeout: 60 * time.Second,  // 60s shutdown timeout
}

// Create manager with custom config
manager := silencing.NewDefaultSilenceManager(repo, matcher, logger, &config)
```

## Kubernetes Deployment

### Deployment YAML

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: alert-history
  namespace: monitoring
spec:
  replicas: 3
  selector:
    matchLabels:
      app: alert-history
  template:
    metadata:
      labels:
        app: alert-history
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      containers:
      - name: alert-history
        image: alert-history:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: connection-url
        - name: SILENCE_GC_INTERVAL
          value: "5m"
        - name: SILENCE_GC_RETENTION
          value: "24h"
        - name: SILENCE_SYNC_INTERVAL
          value: "1m"
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
```

### Service & Ingress

```yaml
apiVersion: v1
kind: Service
metadata:
  name: alert-history
  namespace: monitoring
spec:
  selector:
    app: alert-history
  ports:
  - port: 80
    targetPort: 8080
    name: http
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: alert-history
  namespace: monitoring
spec:
  rules:
  - host: alert-history.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: alert-history
            port:
              number: 80
```

## Monitoring

### Prometheus Metrics

The Silence Manager exports 8 Prometheus metrics:

1. `alert_history_business_silence_manager_operations_total{operation,status}`
2. `alert_history_business_silence_manager_operation_duration_seconds{operation}`
3. `alert_history_business_silence_manager_errors_total{operation,type}`
4. `alert_history_business_silence_manager_active_silences{status}`
5. `alert_history_business_silence_manager_cache_operations_total{type,operation}`
6. `alert_history_business_silence_manager_gc_runs_total{phase}`
7. `alert_history_business_silence_manager_gc_cleaned_total{phase}`
8. `alert_history_business_silence_manager_sync_runs_total`

### Example PromQL Queries

```promql
# Total silence operations per second
rate(alert_history_business_silence_manager_operations_total[5m])

# Average operation duration
rate(alert_history_business_silence_manager_operation_duration_seconds_sum[5m]) /
rate(alert_history_business_silence_manager_operation_duration_seconds_count[5m])

# Cache hit rate
rate(alert_history_business_silence_manager_cache_operations_total{type="hit"}[5m]) /
rate(alert_history_business_silence_manager_cache_operations_total[5m])

# Active silences gauge
alert_history_business_silence_manager_active_silences{status="active"}

# GC efficiency (silences cleaned per run)
rate(alert_history_business_silence_manager_gc_cleaned_total[5m]) /
rate(alert_history_business_silence_manager_gc_runs_total[5m])
```

## API Usage Examples

### Create Silence

```go
silence := &coresilencing.Silence{
	CreatedBy: "ops@example.com",
	Comment:   "Maintenance window for API servers",
	StartsAt:  time.Now(),
	EndsAt:    time.Now().Add(2 * time.Hour),
	Matchers: []coresilencing.Matcher{
		{Name: "alertname", Value: "HighCPU", Type: "="},
		{Name: "job", Value: "api-server", Type: "="},
	},
}

created, err := silenceManager.CreateSilence(ctx, silence)
if err != nil {
	log.Printf("Failed to create silence: %v", err)
} else {
	log.Printf("Created silence: %s", created.ID)
}
```

### List Active Silences

```go
silences, err := silenceManager.GetActiveSilences(ctx)
if err != nil {
	log.Printf("Failed to list silences: %v", err)
} else {
	log.Printf("Active silences: %d", len(silences))
	for _, s := range silences {
		log.Printf("  - %s: %s (by %s)", s.ID, s.Comment, s.CreatedBy)
	}
}
```

### Check if Alert is Silenced

```go
alert := &coresilencing.Alert{
	Labels: map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"instance":  "api-01",
	},
}

silenced, silenceIDs, err := silenceManager.IsAlertSilenced(ctx, alert)
if err != nil {
	log.Printf("Error checking silence: %v", err)
} else if silenced {
	log.Printf("Alert is silenced by: %v", silenceIDs)
} else {
	log.Printf("Alert is NOT silenced")
}
```

### Get Manager Stats

```go
stats, err := silenceManager.GetStats(ctx)
if err != nil {
	log.Printf("Failed to get stats: %v", err)
} else {
	log.Printf("Silence Manager Stats:")
	log.Printf("  Cache size: %d", stats.CacheSize)
	log.Printf("  Active silences: %d", stats.ActiveSilences)
	log.Printf("  GC total runs: %d", stats.GCTotalRuns)
	log.Printf("  Sync total runs: %d", stats.SyncTotalRuns)
}
```

## Troubleshooting

### Common Issues

1. **"initial cache sync failed"**
   - Check PostgreSQL connectivity
   - Verify `silences` table exists (run migrations)
   - Check database permissions

2. **"silence manager already started"**
   - Don't call `Start()` multiple times
   - Check if manager was already initialized

3. **High memory usage**
   - Reduce `GCRetention` (default: 24h)
   - Reduce `GCBatchSize` if processing too many silences
   - Check for silence leaks (expired silences not cleaned)

4. **Slow alert processing**
   - Check cache hit rate (should be >95%)
   - Verify sync worker is running (check metrics)
   - Reduce number of active silences

### Debug Logging

Enable debug logging to troubleshoot issues:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelDebug, // Enable debug logs
}))
```

## Performance Tuning

### Recommendations

1. **Cache Size**: Keep active silences <10,000 for optimal performance
2. **GC Interval**: Default 5m is good for most cases
3. **Sync Interval**: 1m ensures cache freshness without overhead
4. **Database Indexes**: Ensure TN-133 indexes are created

### Expected Performance

| Operation | Target | Typical |
|-----------|--------|---------|
| CreateSilence | <15ms | ~3-4ms |
| GetSilence (cached) | <100µs | ~50ns |
| GetSilence (uncached) | <5ms | ~1-1.5ms |
| IsAlertSilenced (100 silences) | <500µs | ~100-200µs |
| GC Cleanup (1000 silences) | <2s | ~40-90ms |
| Sync (1000 silences) | <500ms | ~100-200ms |

## Production Checklist

- [ ] PostgreSQL indexes created (from TN-133)
- [ ] Silence Manager started in main.go
- [ ] AlertProcessor integrated with IsAlertSilenced
- [ ] Prometheus metrics exported
- [ ] Graceful shutdown configured
- [ ] Environment variables set
- [ ] Kubernetes deployment configured
- [ ] Health checks configured
- [ ] Monitoring dashboard created
- [ ] Alerts configured (cache hit rate, error rate, etc.)

## Support

For issues or questions:
- See TN-134 requirements.md and design.md
- Check COMPLETION_REPORT.md for implementation details
- Review TN-131 (Data Models), TN-132 (Matcher), TN-133 (Storage) docs
