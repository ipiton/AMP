// Package silencing implements the Silence Manager Service for managing alert silences.
//
// The Silence Manager coordinates the lifecycle of silence rules, providing:
//   - CRUD operations for silences
//   - In-memory caching for fast lookups
//   - Alert filtering (IsAlertSilenced)
//   - Background garbage collection
//   - Automatic cache synchronization
//
// Example usage:
//
//	repo := silencing.NewPostgresSilenceRepository(pool, logger)
//	matcher := silencing.NewDefaultSilenceMatcher(logger)
//	manager := NewDefaultSilenceManager(repo, matcher, logger, nil)
//
//	if err := manager.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer manager.Stop(ctx)
//
//	// Create silence
//	silence := &silencing.Silence{
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Maintenance window",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers: []silencing.Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: "="},
//	    },
//	}
//	created, err := manager.CreateSilence(ctx, silence)
//
//	// Check if alert is silenced
//	alert := &silencing.Alert{
//	    Labels: map[string]string{"alertname": "HighCPU"},
//	}
//	silenced, ids, err := manager.IsAlertSilenced(ctx, alert)
//
// Thread-safety: All methods are safe for concurrent use.
// Lifecycle: Must call Start() before operations, Stop() for graceful shutdown.
//
// TN-134: Silence Manager Service
// Target Quality: 150% (Enterprise-Grade)
// Date: 2025-11-06
package silencing

import (
	"context"
	"time"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// SilenceManager coordinates silence lifecycle and alert filtering.
//
// The manager acts as a central coordinator between:
//   - Storage layer (SilenceRepository)
//   - Matching layer (SilenceMatcher)
//   - In-memory cache (fast lookups)
//   - Background workers (GC, sync)
//
// Responsibilities:
//   - Manage silence CRUD operations
//   - Maintain in-memory cache of active silences
//   - Filter alerts against active silences
//   - Run background GC for expired silences
//   - Synchronize cache with database
//
// Lifecycle:
//  1. Create manager with NewDefaultSilenceManager()
//  2. Call Start(ctx) to initialize cache and workers
//  3. Use CRUD and filtering methods
//  4. Call Stop(ctx) for graceful shutdown
//
// Example:
//
//	manager := NewDefaultSilenceManager(repo, matcher, logger, nil)
//	if err := manager.Start(ctx); err != nil {
//	    return err
//	}
//	defer manager.Stop(context.WithTimeout(context.Background(), 30*time.Second))
type SilenceManager interface {
	// ==================== Core CRUD Operations ====================

	// CreateSilence creates a new silence in the database and cache.
	//
	// The method:
	//  1. Validates the silence via silence.Validate()
	//  2. Saves to database via repository
	//  3. Adds to in-memory cache if status == "active"
	//  4. Records Prometheus metrics
	//
	// Returns:
	//   - *silencing.Silence: Created silence with ID, CreatedAt populated
	//   - error: ErrManagerNotStarted, ErrManagerShutdown, validation errors, repository errors
	//
	// Performance target: <15ms
	//
	// Example:
	//
	//	silence := &silencing.Silence{
	//	    CreatedBy: "ops@example.com",
	//	    Comment:   "Maintenance window",
	//	    StartsAt:  time.Now(),
	//	    EndsAt:    time.Now().Add(2 * time.Hour),
	//	    Matchers: []silencing.Matcher{
	//	        {Name: "alertname", Value: "HighCPU", Type: "="},
	//	    },
	//	}
	//	created, err := manager.CreateSilence(ctx, silence)
	//	if err != nil {
	//	    return err
	//	}
	//	fmt.Printf("Created silence: %s\n", created.ID)
	CreateSilence(ctx context.Context, silence *silencing.Silence) (*silencing.Silence, error)

	// GetSilence retrieves a silence by ID (cache-first strategy).
	//
	// The method:
	//  1. Checks in-memory cache first (O(1) lookup)
	//  2. Falls back to database if cache miss
	//  3. Updates cache if silence is active
	//
	// Returns:
	//   - *silencing.Silence: The silence object
	//   - error: ErrSilenceNotFound, ErrInvalidUUID, repository errors
	//
	// Performance target: <100µs (cached), <5ms (uncached)
	//
	// Example:
	//
	//	silence, err := manager.GetSilence(ctx, "550e8400-e29b-41d4-a716-446655440000")
	//	if err == infrasilencing.ErrSilenceNotFound {
	//	    fmt.Println("Silence not found")
	//	    return
	//	}
	GetSilence(ctx context.Context, id string) (*silencing.Silence, error)

	// UpdateSilence updates an existing silence.
	//
	// The method:
	//  1. Validates the silence
	//  2. Updates in database via repository
	//  3. Invalidates cache entry
	//  4. Re-adds to cache if new status == "active"
	//
	// Returns:
	//   - error: ErrSilenceNotFound, ErrSilenceConflict (optimistic locking), validation errors
	//
	// Performance target: <20ms
	//
	// Example:
	//
	//	silence.Comment = "Extended maintenance window"
	//	silence.EndsAt = time.Now().Add(4 * time.Hour)
	//	err := manager.UpdateSilence(ctx, silence)
	UpdateSilence(ctx context.Context, silence *silencing.Silence) error

	// DeleteSilence deletes a silence by ID.
	//
	// The method:
	//  1. Deletes from database (hard delete)
	//  2. Removes from cache
	//  3. Records metrics
	//
	// Returns:
	//   - error: ErrSilenceNotFound, repository errors
	//
	// Performance target: <10ms
	//
	// Example:
	//
	//	err := manager.DeleteSilence(ctx, "550e8400-e29b-41d4-a716-446655440000")
	DeleteSilence(ctx context.Context, id string) error

	// ListSilences retrieves silences matching the filter.
	//
	// The method:
	//  - Fast path: If filter is only "status=active", returns from cache
	//  - Slow path: Complex filters query database
	//
	// Returns:
	//   - []*silencing.Silence: List of matching silences (empty if no matches)
	//   - error: ErrInvalidFilter, repository errors
	//
	// Performance target: <10ms (cache), <50ms (database)
	//
	// Example:
	//
	//	filter := infrasilencing.SilenceFilter{
	//	    Statuses: []silencing.SilenceStatus{"active"},
	//	    Limit:    100,
	//	}
	//	silences, err := manager.ListSilences(ctx, filter)
	ListSilences(ctx context.Context, filter infrasilencing.SilenceFilter) ([]*silencing.Silence, error)

	// ==================== Alert Filtering Integration ====================

	// IsAlertSilenced checks if an alert matches any active silence.
	//
	// The method:
	//  1. Retrieves active silences from cache
	//  2. Iterates through silences (early exit on first match)
	//  3. Uses SilenceMatcher to check if alert matches each silence
	//  4. Returns list of matched silence IDs
	//
	// Returns:
	//   - bool: true if alert is silenced, false otherwise
	//   - []string: List of silence IDs that matched the alert
	//   - error: ErrInvalidAlert, matcher errors (graceful degradation)
	//
	// Performance target: <500µs for 100 active silences
	//
	// Fail-safe: On errors, returns (false, nil, nil) to prevent blocking alerts
	//
	// Example:
	//
	//	alert := &silencing.Alert{
	//	    Labels: map[string]string{
	//	        "alertname": "HighCPU",
	//	        "job":       "api-server",
	//	    },
	//	}
	//	silenced, silenceIDs, err := manager.IsAlertSilenced(ctx, alert)
	//	if silenced {
	//	    fmt.Printf("Alert silenced by: %v\n", silenceIDs)
	//	    return // Skip publishing
	//	}
	IsAlertSilenced(ctx context.Context, alert *silencing.Alert) (bool, []string, error)

	// GetActiveSilences retrieves all currently active silences.
	//
	// The method returns silences from cache (fast path).
	// Falls back to database query if cache is empty.
	//
	// Returns:
	//   - []*silencing.Silence: List of active silences
	//   - error: Repository errors
	//
	// Performance target: <100µs (cached)
	//
	// Example:
	//
	//	silences, err := manager.GetActiveSilences(ctx)
	//	fmt.Printf("Active silences: %d\n", len(silences))
	GetActiveSilences(ctx context.Context) ([]*silencing.Silence, error)

	// ==================== Lifecycle Management ====================

	// Start initializes the manager and starts background workers.
	//
	// The method:
	//  1. Performs initial cache sync (loads active silences from database)
	//  2. Starts GC worker (garbage collection of expired silences)
	//  3. Starts sync worker (periodic cache refresh)
	//  4. Sets started flag
	//
	// Returns:
	//   - error: If already started, or initial cache sync fails
	//
	// Note: This method must be called before any CRUD/filtering operations.
	//
	// Example:
	//
	//	if err := manager.Start(ctx); err != nil {
	//	    log.Fatal("Failed to start silence manager:", err)
	//	}
	Start(ctx context.Context) error

	// Stop gracefully shuts down the manager.
	//
	// The method:
	//  1. Sets shutdown flag (rejects new operations)
	//  2. Stops background workers (GC, sync)
	//  3. Waits for in-flight operations to complete
	//  4. Returns within timeout or error
	//
	// Parameters:
	//   - ctx: Context with timeout (recommended: 30s)
	//
	// Returns:
	//   - error: If shutdown didn't complete within timeout
	//
	// Note: After Stop(), the manager cannot be reused.
	//
	// Example:
	//
	//	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	//	defer cancel()
	//	if err := manager.Stop(shutdownCtx); err != nil {
	//	    log.Warn("Shutdown timeout:", err)
	//	}
	Stop(ctx context.Context) error

	// ==================== Status & Monitoring ====================

	// GetStats returns current manager statistics.
	//
	// Returns:
	//   - *SilenceManagerStats: Statistics including cache size, repository counts, worker status
	//   - error: Repository errors
	//
	// Example:
	//
	//	stats, err := manager.GetStats(ctx)
	//	fmt.Printf("Active silences: %d (cache: %d)\n", stats.ActiveSilences, stats.CacheSize)
	GetStats(ctx context.Context) (*SilenceManagerStats, error)
}

// SilenceManagerConfig holds configuration for DefaultSilenceManager.
//
// All durations support environment variable overrides via 12-factor app principles.
//
// Environment variables:
//   - SILENCE_GC_INTERVAL: GC worker interval (default: 5m)
//   - SILENCE_GC_RETENTION: Keep expired silences for this long (default: 24h)
//   - SILENCE_GC_BATCH_SIZE: Max silences per GC run (default: 1000)
//   - SILENCE_SYNC_INTERVAL: Sync worker interval (default: 1m)
//   - SILENCE_CACHE_ENABLED: Enable in-memory cache (default: true)
//   - SILENCE_SHUTDOWN_TIMEOUT: Max time for graceful shutdown (default: 30s)
type SilenceManagerConfig struct {
	// GC Worker Settings
	GCInterval  time.Duration // How often to run GC (default: 5m)
	GCRetention time.Duration // Keep expired for this long (default: 24h)
	GCBatchSize int           // Max silences per GC run (default: 1000)

	// Sync Worker Settings
	SyncInterval time.Duration // How often to sync cache (default: 1m)

	// Cache Settings
	CacheEnabled bool          // Enable in-memory cache (default: true)
	CacheTTL     time.Duration // Not used (cache always fresh)

	// Shutdown Settings
	ShutdownTimeout time.Duration // Max time for graceful shutdown (default: 30s)
}

// DefaultSilenceManagerConfig returns default configuration.
//
// Defaults:
//   - GC runs every 5 minutes
//   - Expired silences kept for 24 hours before deletion
//   - GC processes up to 1000 silences per run
//   - Cache syncs every 1 minute
//   - Cache enabled
//   - Shutdown timeout: 30 seconds
func DefaultSilenceManagerConfig() SilenceManagerConfig {
	return SilenceManagerConfig{
		GCInterval:      5 * time.Minute,
		GCRetention:     24 * time.Hour,
		GCBatchSize:     1000,
		SyncInterval:    1 * time.Minute,
		CacheEnabled:    true,
		CacheTTL:        5 * time.Minute, // Not used
		ShutdownTimeout: 30 * time.Second,
	}
}

// SilenceManagerStats holds statistics about the silence manager.
//
// These stats are useful for monitoring, dashboards, and alerting.
//
// Example usage:
//
//	stats, _ := manager.GetStats(ctx)
//	if stats.CacheSize > 10000 {
//	    log.Warn("High cache size detected")
//	}
type SilenceManagerStats struct {
	// Cache Statistics
	CacheSize     int                                           // Number of silences in cache
	CacheLastSync time.Time                                     // Last cache synchronization time
	CacheByStatus map[silencing.SilenceStatus]int               // Count by status (pending/active/expired)

	// Repository Statistics
	TotalSilences   int64 // Total silences in database
	ActiveSilences  int64 // Active silences (StartsAt <= now < EndsAt)
	PendingSilences int64 // Pending silences (now < StartsAt)
	ExpiredSilences int64 // Expired silences (now >= EndsAt)

	// Worker Statistics
	GCLastRun      time.Time // Last GC run time
	GCTotalRuns    int64     // Total number of GC runs
	GCTotalCleaned int64     // Total silences cleaned by GC
	SyncLastRun    time.Time // Last sync run time
	SyncTotalRuns  int64     // Total number of sync runs
}
