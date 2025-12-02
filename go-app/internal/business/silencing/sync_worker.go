package silencing

import (
	"context"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// syncWorker handles periodic synchronization of in-memory cache with PostgreSQL.
//
// The worker runs a single-phase sync:
//  1. Fetch all active silences from the database
//  2. Rebuild the in-memory cache with the fresh data
//
// This ensures that the cache stays consistent with the database, especially:
//   - After pod restarts (cache is empty)
//   - After external database modifications (manual updates)
//   - To recover from cache inconsistencies
//
// Inspired by:
//   - TN-124: Timer Manager RestoreTimers
//   - TN-129: Inhibition State Manager Cleanup Worker
//
// Architecture:
//   - Runs in separate goroutine
//   - Ticker-based execution (default: 1m interval)
//   - Graceful shutdown support
//   - Metrics tracking for observability
//
// Example usage:
//
//	worker := newSyncWorker(repo, cache, 1*time.Minute, logger, metrics)
//	worker.Start(ctx)
//	defer worker.Stop()
type syncWorker struct {
	repo     infrasilencing.SilenceRepository
	cache    *silenceCache
	interval time.Duration // How often to sync (default: 1m)

	logger  *slog.Logger
	metrics *SilenceMetrics

	stopCh chan struct{} // Signal to stop worker
	doneCh chan struct{} // Signal when worker stopped
}

// newSyncWorker creates a new sync worker (not started).
//
// Parameters:
//   - repo: Silence repository for database queries
//   - cache: In-memory cache to rebuild
//   - interval: How often to run sync
//   - logger: Structured logger
//   - metrics: Prometheus metrics (will be implemented in Phase 7)
//
// Returns:
//   - *syncWorker: Initialized worker (call Start() to begin)
//
// Example:
//
//	worker := newSyncWorker(repo, cache, 1*time.Minute, logger, metrics)
func newSyncWorker(
	repo infrasilencing.SilenceRepository,
	cache *silenceCache,
	interval time.Duration,
	logger *slog.Logger,
	metrics *SilenceMetrics,
) *syncWorker {
	return &syncWorker{
		repo:     repo,
		cache:    cache,
		interval: interval,
		logger:   logger,
		metrics:  metrics,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start starts the sync worker in a background goroutine.
//
// The worker will:
//  1. Run sync immediately on startup (critical for pod restart recovery)
//  2. Run sync periodically based on interval
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
func (w *syncWorker) Start(ctx context.Context) {
	go w.run(ctx)
	w.logger.Info("Sync worker started", "interval", w.interval)
}

// run is the main worker loop.
//
// Lifecycle:
//  1. Create ticker for periodic execution
//  2. Run sync immediately (don't wait for first tick) - CRITICAL for pod restart
//  3. Loop: wait for tick, stop signal, or context cancellation
//  4. Cleanup ticker and signal completion on exit
//
// This method runs in a separate goroutine.
func (w *syncWorker) run(ctx context.Context) {
	defer close(w.doneCh)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on startup (critical for pod restart recovery)
	w.runSync(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Sync worker stopped (context cancelled)")
			return

		case <-w.stopCh:
			w.logger.Info("Sync worker stopped (explicit stop)")
			return

		case <-ticker.C:
			w.runSync(ctx)
		}
	}
}

// runSync performs the cache synchronization process.
//
// Algorithm:
//  1. Fetch all active silences from PostgreSQL (status=active, limit=10000)
//  2. Rebuild the in-memory cache with the fresh data
//  3. Track metrics: old size, new size, added, removed
//
// Error handling:
//   - Errors are logged but don't stop the worker
//   - Cache is NOT rebuilt if database query fails (fail-safe)
//
// Performance:
//   - Target: <500ms for 1000 silences
//   - Uses batch query (limit=10000)
func (w *syncWorker) runSync(ctx context.Context) {
	start := time.Now()

	// Step 1: Get current cache stats (before sync)
	oldStats := w.cache.Stats()
	oldSize := oldStats.Size

	// Step 2: Fetch all active silences from database
	filter := infrasilencing.SilenceFilter{
		Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
		Limit:    10000, // Max active silences to fetch
	}

	silences, err := w.repo.ListSilences(ctx, filter)
	if err != nil {
		w.logger.Error("Failed to sync cache from database",
			"error", err,
		)
		// Fail-safe: don't rebuild cache on error
		return
	}

	// Step 3: Rebuild cache with fresh data
	w.cache.Rebuild(silences)
	newSize := len(silences)

	// Step 4: Calculate diff
	added := max(0, newSize-oldSize)
	removed := max(0, oldSize-newSize)

	duration := time.Since(start)

	w.logger.Info("Cache synchronized",
		"old_size", oldSize,
		"new_size", newSize,
		"added", added,
		"removed", removed,
		"duration", duration,
	)

	// Record metrics
	if w.metrics != nil {
		w.metrics.RecordSyncRun()
		w.metrics.UpdateActiveSilencesGauge(newSize, 0, 0) // TODO: Track by status
	}
}

// Stop gracefully stops the sync worker.
//
// This method:
//  1. Sends stop signal to worker goroutine
//  2. Waits for worker to complete current sync (if running)
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
func (w *syncWorker) Stop() {
	w.logger.Debug("Stopping sync worker")
	close(w.stopCh)
	<-w.doneCh // Block until worker stopped
	w.logger.Debug("Sync worker stopped")
}

// max returns the maximum of two integers.
// TODO: Use built-in max() when upgrading to Go 1.21+
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
