package silencing

import (
	"context"
	"log/slog"
	"time"

	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// gcWorker handles periodic garbage collection of expired silences.
//
// The worker runs two phases:
//  1. Expire Phase: Changes status from 'active' to 'expired' for silences past EndsAt
//  2. Delete Phase: Permanently deletes expired silences older than retention period
//
// Inspired by:
//   - TN-124: Timer Manager TTL Cleanup Worker
//   - TN-129: Inhibition State Manager Cleanup Worker
//
// Architecture:
//   - Runs in separate goroutine
//   - Ticker-based execution (default: 5m interval)
//   - Graceful shutdown support
//   - Metrics tracking for observability
//
// Example usage:
//
//	worker := newGCWorker(repo, cache, 5*time.Minute, 24*time.Hour, 1000, logger, metrics)
//	worker.Start(ctx)
//	defer worker.Stop()
type gcWorker struct {
	repo      infrasilencing.SilenceRepository
	cache     *silenceCache
	interval  time.Duration // How often to run cleanup (default: 5m)
	retention time.Duration // Keep expired for this long (default: 24h)
	batchSize int           // Max silences per run (default: 1000)

	logger  *slog.Logger
	metrics *SilenceMetrics

	stopCh chan struct{} // Signal to stop worker
	doneCh chan struct{} // Signal when worker stopped
}

// newGCWorker creates a new GC worker (not started).
//
// Parameters:
//   - repo: Silence repository for database operations
//   - cache: In-memory cache (for potential future use)
//   - interval: How often to run cleanup
//   - retention: Keep expired silences for this long before deletion
//   - batchSize: Max silences to process per run
//   - logger: Structured logger
//   - metrics: Prometheus metrics (will be implemented in Phase 7)
//
// Returns:
//   - *gcWorker: Initialized worker (call Start() to begin)
//
// Example:
//
//	worker := newGCWorker(repo, cache, 5*time.Minute, 24*time.Hour, 1000, logger, metrics)
func newGCWorker(
	repo infrasilencing.SilenceRepository,
	cache *silenceCache,
	interval, retention time.Duration,
	batchSize int,
	logger *slog.Logger,
	metrics *SilenceMetrics,
) *gcWorker {
	return &gcWorker{
		repo:      repo,
		cache:     cache,
		interval:  interval,
		retention: retention,
		batchSize: batchSize,
		logger:    logger,
		metrics:   metrics,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start starts the GC worker in a background goroutine.
//
// The worker will:
//  1. Run cleanup immediately on startup
//  2. Run cleanup periodically based on interval
//  3. Stop gracefully when context is cancelled or Stop() is called
//
// This method is non-blocking (goroutine spawned).
//
// Parameters:
//   - ctx: Context for cancellation
//
// Example:
//
//	worker.Start(ctx)
//	defer worker.Stop()
func (w *gcWorker) Start(ctx context.Context) {
	go w.run(ctx)
	w.logger.Info("GC worker started",
		"interval", w.interval,
		"retention", w.retention,
		"batch_size", w.batchSize,
	)
}

// run is the main worker loop.
//
// Lifecycle:
//  1. Create ticker for periodic execution
//  2. Run cleanup immediately (don't wait for first tick)
//  3. Loop: wait for tick, stop signal, or context cancellation
//  4. Cleanup ticker and signal completion on exit
//
// This method runs in a separate goroutine.
func (w *gcWorker) run(ctx context.Context) {
	defer close(w.doneCh)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on startup (don't wait for first tick)
	w.runCleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("GC worker stopped (context cancelled)")
			return

		case <-w.stopCh:
			w.logger.Info("GC worker stopped (explicit stop)")
			return

		case <-ticker.C:
			w.runCleanup(ctx)
		}
	}
}

// runCleanup runs the two-phase cleanup process.
//
// Phase 1: Expire active silences (status update)
//   - Find silences where ends_at < NOW AND status = 'active'
//   - UPDATE status = 'expired'
//
// Phase 2: Delete old expired silences (hard delete)
//   - Find silences where ends_at < NOW-retention AND status = 'expired'
//   - DELETE FROM silences
//
// Error handling:
//   - Errors are logged but don't stop the worker
//   - Each phase is independent (phase 1 failure doesn't prevent phase 2)
//
// Performance:
//   - Both phases use batch limits (default: 1000)
//   - Total cleanup time target: <2s for 1000 silences
func (w *gcWorker) runCleanup(ctx context.Context) {
	start := time.Now()

	// Phase 1: Expire active silences
	expiredCount, err := w.expireActiveSilences(ctx)
	if err != nil {
		w.logger.Error("Failed to expire silences", "error", err)
	} else {
		w.logger.Info("Phase 1 complete (expire)",
			"expired_count", expiredCount,
		)
	}

	// Phase 2: Delete old expired silences
	deletedCount, err := w.deleteOldExpired(ctx)
	if err != nil {
		w.logger.Error("Failed to delete old silences", "error", err)
	} else {
		w.logger.Info("Phase 2 complete (delete)",
			"deleted_count", deletedCount,
		)
	}

	duration := time.Since(start)
	w.logger.Info("GC cleanup complete",
		"expired", expiredCount,
		"deleted", deletedCount,
		"duration", duration,
	)
}

// expireActiveSilences changes status from 'active' to 'expired' for silences past EndsAt.
//
// This is Phase 1 of the cleanup process.
//
// SQL equivalent:
//
//	UPDATE silences
//	SET status = 'expired', updated_at = NOW()
//	WHERE ends_at < NOW() AND status = 'active'
//	LIMIT 1000
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - int64: Number of silences expired
//   - error: Database error, or nil on success
//
// Performance target: <500ms for 1000 silences
func (w *gcWorker) expireActiveSilences(ctx context.Context) (int64, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		w.logger.Debug("expireActiveSilences completed", "duration_seconds", duration)
	}()

	// Call repository ExpireSilences (deleteExpired=false for status update)
	count, err := w.repo.ExpireSilences(ctx, time.Now(), false)
	if err != nil {
		w.logger.Error("ExpireSilences failed", "error", err)
		return 0, err
	}

	// Record metrics
	if w.metrics != nil {
		w.metrics.RecordGCRun("expire", count)
	}

	return count, nil
}

// deleteOldExpired permanently deletes expired silences older than retention period.
//
// This is Phase 2 of the cleanup process.
//
// SQL equivalent:
//
//	DELETE FROM silences
//	WHERE ends_at < NOW() - retention AND status = 'expired'
//	LIMIT 1000
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - int64: Number of silences deleted
//   - error: Database error, or nil on success
//
// Performance target: <1.5s for 1000 silences
func (w *gcWorker) deleteOldExpired(ctx context.Context) (int64, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		w.logger.Debug("deleteOldExpired completed", "duration_seconds", duration)
	}()

	// Calculate cutoff time (NOW - retention)
	before := time.Now().Add(-w.retention)

	// Call repository ExpireSilences (deleteExpired=true for hard delete)
	count, err := w.repo.ExpireSilences(ctx, before, true)
	if err != nil {
		w.logger.Error("DeleteOldExpired failed", "error", err)
		return 0, err
	}

	// Record metrics
	if w.metrics != nil {
		w.metrics.RecordGCRun("delete", count)
	}

	return count, nil
}

// Stop gracefully stops the GC worker.
//
// This method:
//  1. Sends stop signal to worker goroutine
//  2. Waits for worker to complete current cleanup (if running)
//  3. Blocks until worker fully stopped
//
// Timeout: No timeout (caller should use context.WithTimeout if needed)
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	worker.Stop()
//
// Note: Safe to call multiple times (no-op after first call).
func (w *gcWorker) Stop() {
	w.logger.Debug("Stopping GC worker")
	close(w.stopCh)
	<-w.doneCh // Block until worker stopped
	w.logger.Debug("GC worker stopped")
}
