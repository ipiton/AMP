package config

import (
	"context"
	"time"
)

// ================================================================================
// Configuration Update Interfaces
// ================================================================================
// This file defines interfaces for configuration update operations (TN-150).
//
// Architecture:
// - ConfigUpdateService: Core business logic for config updates
// - ConfigStorage: Persistence layer for config versions and audit log
// - ConfigValidator: Multi-phase validation pipeline
// - Reloadable: Interface for components that support hot reload
// - ConfigReloader: Orchestrates hot reload across multiple components
// - LockManager: Distributed lock for preventing concurrent updates
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// ================================================================================
// Core Service Interface
// ================================================================================

// ConfigUpdateService handles configuration update operations
//
// Implementation provides:
// - Multi-phase validation (syntax, schema, type, business, cross-field)
// - Atomic config application (all-or-nothing)
// - Hot reload orchestration with rollback on failure
// - Dry-run mode for pre-deployment testing
// - Partial updates (section filtering)
// - Configuration diff calculation
// - Version management and history
// - Audit logging
//
// Usage Example:
//
//	service := NewConfigUpdateService(currentConfig, storage, validator, reloader, lockManager, logger)
//	opts := UpdateOptions{Format: "json", DryRun: false}
//	result, err := service.UpdateConfig(ctx, configMap, opts)
//	if err != nil {
//	    // Handle validation or update errors
//	}
//	fmt.Printf("Updated to version %d\n", result.Version)
type ConfigUpdateService interface {
	// UpdateConfig updates configuration with validation and hot reload
	//
	// Process Flow:
	// 1. Phase 1: Multi-phase validation
	//    - Syntax validation (JSON/YAML parsing)
	//    - Schema validation (struct unmarshaling)
	//    - Type validation (validator tags)
	//    - Business rule validation (Validate() method)
	//    - Cross-field validation
	// 2. Phase 2: Diff calculation
	//    - Deep comparison with current config
	//    - Identify added/modified/deleted fields
	//    - Sanitize secrets in diff
	//    - Identify affected components
	// 3. Phase 3: Atomic application (if !DryRun)
	//    - Acquire distributed lock
	//    - Backup old config
	//    - Write new config to storage
	//    - Increment version counter
	//    - Write audit log
	//    - Release lock
	// 4. Phase 4: Hot reload (if !DryRun)
	//    - Notify affected components
	//    - Parallel reload with timeout
	//    - Collect errors
	//    - Rollback if critical component fails
	//
	// Parameters:
	// - ctx: Context for cancellation and timeout
	// - configMap: New configuration as map[string]interface{}
	// - opts: Update options (format, dry_run, sections)
	//
	// Returns:
	// - UpdateResult: Contains version, diff, applied status, errors
	// - error: Returns ValidationError, ConflictError, or generic error
	//
	// Error Types:
	// - *ValidationError: Validation failed (HTTP 422)
	// - *ConflictError: Concurrent update detected (HTTP 409)
	// - error: Storage, lock, or reload error (HTTP 500)
	//
	// Performance Target:
	// - Validation: < 50ms p95
	// - Full update: < 500ms p95
	// - Dry-run: < 30ms p95
	UpdateConfig(ctx context.Context, configMap map[string]interface{}, opts UpdateOptions) (*UpdateResult, error)

	// RollbackConfig rolls back to a previous configuration version
	//
	// Process:
	// 1. Load old config from storage by version
	// 2. Validate that old config is still valid (schema may have changed)
	// 3. Apply old config (same process as UpdateConfig)
	// 4. Hot reload components with old config
	// 5. Write audit log with action="rollback"
	//
	// Parameters:
	// - ctx: Context for cancellation and timeout
	// - version: Target version to roll back to
	//
	// Returns:
	// - UpdateResult: Contains rollback status and diff
	// - error: If version not found, validation failed, or rollback failed
	//
	// Notes:
	// - Rollback is itself a new version (not a revert to old version number)
	// - Diff shows changes from current to target version
	// - Audit log tracks rollback source version
	RollbackConfig(ctx context.Context, version int64) (*UpdateResult, error)

	// GetHistory returns configuration version history
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - limit: Maximum number of versions to return (0 = all)
	//
	// Returns:
	// - []*ConfigVersion: List of historical versions, sorted by version DESC
	// - error: If storage access failed
	//
	// Usage:
	//	history, err := service.GetHistory(ctx, 10) // Last 10 versions
	GetHistory(ctx context.Context, limit int) ([]*ConfigVersion, error)

	// GetCurrentVersion returns current configuration version
	GetCurrentVersion() int64

	// GetCurrentConfig returns current configuration
	GetCurrentConfig() *Config
}

// ================================================================================
// Storage Interface
// ================================================================================

// ConfigStorage handles configuration persistence and version management
//
// Implementation Requirements:
// - Atomic operations (save/load must be transactional)
// - Version monotonicity (versions always increment)
// - Durability (survive process crashes)
// - Performance (save < 100ms p95, load < 50ms p95)
//
// Storage Options:
// 1. PostgreSQL (recommended for production):
//   - Tables: config_versions, config_audit_log
//   - ACID transactions
//   - Retention policies via triggers
//
// 2. Filesystem (fallback for development):
//   - Files: config/versions/v{version}.json
//   - Atomic writes via temp file + rename
//   - Limited concurrency support
type ConfigStorage interface {
	// Save persists configuration and returns new version number
	//
	// Process:
	// 1. Begin transaction
	// 2. Get current max version
	// 3. Increment version counter
	// 4. Calculate SHA256 hash
	// 5. Insert into config_versions table
	// 6. Commit transaction
	//
	// Parameters:
	// - ctx: Context for cancellation and timeout
	// - cfg: Configuration to save
	//
	// Returns:
	// - version: New version number (monotonically increasing)
	// - error: If save failed or transaction rolled back
	//
	// Performance Target: < 100ms p95
	//
	// Error Handling:
	// - Returns error if transaction fails
	// - Ensures version monotonicity even under concurrent saves
	Save(ctx context.Context, cfg *Config) (version int64, err error)

	// Load retrieves configuration by version number
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - version: Version number to load (use GetLatestVersion() for current)
	//
	// Returns:
	// - *Config: Loaded configuration
	// - error: If version not found or load failed
	//
	// Performance Target: < 50ms p95
	//
	// Notes:
	// - Returns error if version doesn't exist
	// - Config is deep-copied to prevent mutations
	Load(ctx context.Context, version int64) (*Config, error)

	// GetLatestVersion returns the most recent version number
	//
	// Returns:
	// - version: Latest version number
	// - error: If query failed
	//
	// Performance Target: < 5ms p95
	//
	// Notes:
	// - Returns 0 if no versions exist (initial state)
	// - Used for optimistic locking and conflict detection
	GetLatestVersion(ctx context.Context) (int64, error)

	// Backup creates a backup of configuration before applying changes
	//
	// Purpose:
	// - Safety: Allows manual recovery if automated rollback fails
	// - Audit: Provides forensic evidence for investigations
	// - Compliance: May be required for regulatory reasons
	//
	// Implementation:
	// - For PostgreSQL: INSERT into config_backups table
	// - For Filesystem: Copy to backups/ directory with timestamp
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - cfg: Configuration to backup
	//
	// Returns:
	// - error: If backup failed (non-fatal, logged as warning)
	//
	// Notes:
	// - Backup failure should NOT fail the update operation
	// - Old backups should be cleaned up periodically (retention: 90 days)
	Backup(ctx context.Context, cfg *Config) error

	// GetHistory returns configuration version history
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - limit: Maximum number of versions (0 = unlimited)
	//
	// Returns:
	// - []*ConfigVersion: Historical versions, sorted by version DESC
	// - error: If query failed
	//
	// Performance Target: < 100ms p95
	//
	// Notes:
	// - Results are paginated if limit > 0
	// - Secrets in config are sanitized before returning
	GetHistory(ctx context.Context, limit int) ([]*ConfigVersion, error)

	// SaveAuditLog writes an audit log entry
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - entry: Audit log entry to write
	//
	// Returns:
	// - error: If write failed
	//
	// Notes:
	// - Audit log writes should never fail the update operation
	// - Write failures are logged as warnings
	// - Retention: 90 days minimum (configurable)
	SaveAuditLog(ctx context.Context, entry *AuditLogEntry) error
}

// ================================================================================
// Validation Interface
// ================================================================================

// ConfigValidator validates configuration through multi-phase pipeline
//
// Validation Phases (in order):
// 1. Syntax: JSON/YAML parsing
// 2. Schema: Struct unmarshaling and type checking
// 3. Type: Validator tags (required, min, max, etc.)
// 4. Business Rules: Custom validation logic (e.g., MaxConn >= MinConn)
// 5. Cross-Field: Dependencies between fields (e.g., if LLM.Enabled then LLM.APIKey required)
//
// Performance Target: < 50ms p95 for full config validation
type ConfigValidator interface {
	// Validate performs multi-phase validation
	//
	// Parameters:
	// - cfg: Configuration to validate
	// - sections: If not empty, validate only these sections
	//
	// Returns:
	// - []ValidationErrorDetail: List of validation errors (empty if valid)
	//
	// Performance: < 50ms p95
	//
	// Notes:
	// - Returns all errors (doesn't stop at first error)
	// - Errors include field path, message, code, constraint
	// - Secrets are sanitized in error values
	Validate(cfg *Config, sections []string) []ValidationErrorDetail

	// ValidatePartial validates only specified sections
	//
	// Parameters:
	// - cfg: Full configuration
	// - sections: Sections to validate (e.g., ["server", "database"])
	//
	// Returns:
	// - []ValidationErrorDetail: Validation errors for specified sections
	//
	// Notes:
	// - Still performs cross-field validation if dependencies exist
	// - Example: If validating "llm" and llm.enabled=true, checks llm.api_key
	ValidatePartial(cfg *Config, sections []string) []ValidationErrorDetail

	// ValidateDiff validates that a configuration change is safe
	//
	// Checks:
	// - No critical fields changed without proper warnings
	// - Dependent fields remain consistent
	// - No dangerous downgrades (e.g., max_connections reduced below active connections)
	//
	// Parameters:
	// - oldCfg: Current configuration
	// - newCfg: Proposed configuration
	// - diff: Pre-calculated diff
	//
	// Returns:
	// - []ValidationErrorDetail: Safety validation errors
	//
	// Notes:
	// - This is an additional safety check beyond normal validation
	// - May prevent valid but dangerous changes
	ValidateDiff(oldCfg *Config, newCfg *Config, diff *ConfigDiff) []ValidationErrorDetail
}

// ================================================================================
// Hot Reload Interfaces
// ================================================================================

// Reloadable is implemented by components that support hot configuration reload
//
// Implementation Guidelines:
// - Reload should be graceful (no interruption of active requests)
// - Reload should be fast (< 5s, ideally < 1s)
// - Reload should be atomic (old or new config, never mixed state)
// - Reload should be idempotent (can be called multiple times safely)
//
// Honesty rule (INF-A): a component that CANNOT be hot-swapped safely must
// NOT pretend. Its Reload returns nil after logging a restart-required
// warning (see RestartRequiredWarning / the W6xx codes in
// reloadable_warnings.go) rather than performing a swap that cannot reach the
// component's real consumers. "Reload succeeded" must never mean "the new
// value is not actually in effect anywhere".
//
// Example Implementation:
//
//	func (db *DatabasePool) RelevantSections() []string { return []string{"database"} }
//
//	func (db *DatabasePool) Reload(ctx context.Context, oldCfg, newCfg *config.Config) error {
//	    // Create new connection pool with new config, verify it, swap it in.
//	    newPool, err := createPool(newCfg.Database)
//	    if err != nil {
//	        return fmt.Errorf("failed to create new pool: %w", err)
//	    }
//	    if err := newPool.Ping(ctx); err != nil {
//	        newPool.Close()
//	        return fmt.Errorf("new pool failed verification: %w", err)
//	    }
//	    oldPool := db.swap(newPool)
//
//	    // Close old pool in background (after in-flight queries finish)
//	    go func() {
//	        time.Sleep(5 * time.Second) // Grace period
//	        oldPool.Close()
//	    }()
//	    return nil
//	}
//
//	func (db *DatabasePool) Name() string { return "database" }
//	func (db *DatabasePool) IsCritical() bool { return true }
type Reloadable interface {
	// Reload reloads component with new configuration
	//
	// Implementation Requirements:
	// 1. Validate/verify the new config before applying it (dial, ping, parse)
	// 2. Apply new config atomically
	// 3. Gracefully release old resources
	// 4. Return error if reload failed
	//
	// Parameters:
	// - ctx: Context with timeout (typically 30s)
	// - oldCfg: Previously active configuration. May be nil, meaning "no
	//   previous state known" — treat every relevant section as changed.
	// - newCfg: New configuration (never nil)
	//
	// Returns:
	// - error: If reload failed. ANY component error rejects the reload as a
	//   whole and the coordinator rolls back to oldCfg (IsCritical only
	//   affects logging/metrics severity, not that decision).
	//
	// Performance:
	// - Should complete within ctx timeout (typically 30s)
	// - Should be fast for unchanged config (< 10ms)
	//
	// Concurrency:
	// - Called sequentially by DefaultConfigReloader (deterministic order),
	//   but concurrently with live traffic — must be thread-safe against the
	//   component's own readers.
	Reload(ctx context.Context, oldCfg, newCfg *Config) error

	// RelevantSections returns the top-level config sections this component
	// reads, using the same names as Config's `mapstructure` tags
	// ("database", "redis", "log", "metrics", "llm", ...).
	//
	// The reloader calls Reload only when at least one of these sections
	// actually differs between oldCfg and newCfg (SectionChanged), so an
	// implementation does not need to re-derive "did anything change" — it
	// only needs to decide WHICH fields within its sections it can apply.
	//
	// Returning nil/empty means "always reload me", which is legal but
	// should be rare. Returning an unknown section name is a programming
	// error: Register logs it loudly and the component is treated as
	// always-relevant so the mistake cannot hide as a silent no-reload.
	RelevantSections() []string

	// Name returns component name for logging and metrics
	//
	// Examples: "database", "redis", "llm", "cache", "publisher"
	//
	// Used in:
	// - Structured logging
	// - Prometheus metrics (label)
	// - Error messages
	// - Affected components list in diff
	Name() string

	// IsCritical marks components whose failure is an outage rather than a
	// degradation. It drives log/metric severity and the wording of the
	// rejection, NOT the rollback decision: since INF-A slice 1 ANY
	// component error rejects the reload and restores the previous config,
	// because a partially-applied config is worse than a rejected one.
	//
	// Critical Components (an outage if they break):
	// - database: Cannot function without database
	// - redis: Required for distributed locking and caching
	//
	// Non-Critical Components (a degradation if they break):
	// - llm: Can continue without AI features
	// - metrics: Can continue without metrics export
	// - logger: Can continue at the previous level/format
	IsCritical() bool
}

// OrderedReloadable is an optional add-on to Reloadable: components that
// implement it are reloaded in ascending ReloadPriority order.
//
// Components that do NOT implement it get defaultReloadPriority (100), and
// ties keep registration order (stable sort), so ordering is always
// deterministic — a hard requirement for the logger, which must be reloaded
// FIRST so that every later component's reload is logged at the new level and
// in the new format.
type OrderedReloadable interface {
	ReloadPriority() int
}

// ConfigReloader orchestrates hot reload across multiple Reloadable components
//
// Responsibilities:
// - Maintain registry of Reloadable components
// - Trigger reload on config updates
// - Execute reloads in parallel (with timeout)
// - Collect and aggregate errors
// - Decide whether to rollback based on critical failures
//
// Performance:
// - Parallel execution reduces reload time
// - Timeout prevents slow components from blocking
// - Target: < 300ms for typical reload (5-10 components)
type ConfigReloader interface {
	// Register registers a component for hot reload
	//
	// Parameters:
	// - component: Component implementing Reloadable interface
	//
	// Notes:
	// - Components should be registered during initialization
	// - Reload order is decided by OrderedReloadable.ReloadPriority, not by
	//   registration order (registration order only breaks ties)
	// - Registering the same component name twice is a no-op (idempotent)
	Register(component Reloadable)

	// Unregister removes a component from hot reload registry
	//
	// Parameters:
	// - componentName: Name of component to unregister
	//
	// Notes:
	// - Used during graceful shutdown
	// - No-op if component not registered
	Unregister(componentName string)

	// ReloadAll reloads the applicable registered components, sequentially,
	// in deterministic ReloadPriority order.
	//
	// Process:
	// 1. Select components: name in affectedComponents (when non-empty) AND
	//    at least one RelevantSections entry differs between oldCfg/newCfg
	// 2. Call Reload(ctx, oldCfg, newCfg) on each, in priority order
	// 3. Stop at the FIRST error (fail-fast: do not keep applying changes to
	//    further components once the reload is known to be rejected)
	//
	// Sequential rather than parallel (changed in INF-A slice 1): the logger
	// must be swapped before anything else logs, and a deterministic,
	// reproducible order is worth more here than the few milliseconds
	// parallelism saved across a handful of components — every component's
	// own Reload is either a no-op or a single verified swap.
	//
	// Parameters:
	// - ctx: Context with timeout (typically 30s)
	// - oldCfg: Previously active configuration (nil = "reload everything")
	// - newCfg: New configuration
	// - affectedComponents: Component names to consider (nil/empty = all)
	//
	// Returns:
	// - []ReloadError: reload errors; at most one, since selection is
	//   fail-fast. Empty when every applicable component succeeded.
	//
	// Performance Target: < 300ms p95
	ReloadAll(ctx context.Context, oldCfg, newCfg *Config, affectedComponents []string) []ReloadError

	// GetRegisteredComponents returns registered component names in reload
	// order.
	GetRegisteredComponents() []string

	// SelectComponents returns the names of the components ReloadAll would
	// reload for this config change, in reload order. The coordinator uses it
	// to report per-component results without having to duplicate the
	// selection rules.
	SelectComponents(oldCfg, newCfg *Config, affectedComponents []string) []string
}

// ================================================================================
// Lock Management Interface
// ================================================================================

// LockManager provides distributed locking for preventing concurrent config updates
//
// Requirements:
// - Prevents concurrent updates from multiple API instances
// - Timeout-based: Lock auto-expires if holder crashes
// - Renewable: Lock holder can extend TTL (heartbeat)
// - Fair: FIFO ordering (if supported by backend)
//
// Backends:
// - Redis (recommended): RedLock algorithm
// - etcd (alternative): Native locking support
// - PostgreSQL (fallback): Advisory locks
type LockManager interface {
	// Acquire acquires a distributed lock
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - key: Lock key (typically "config:update")
	// - ttl: Lock TTL (auto-release after this duration)
	//
	// Returns:
	// - Lock: Lock handle for renewal and release
	// - error: If lock acquisition failed or timed out
	//
	// Performance:
	// - Should return quickly if lock available (< 50ms)
	// - Blocks (up to ctx timeout) if lock held by another process
	//
	// Notes:
	// - Caller MUST call Lock.Release() when done (use defer)
	// - Lock auto-expires after TTL even if not explicitly released
	Acquire(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}

// Lock represents an acquired distributed lock
type Lock interface {
	// Release releases the lock
	//
	// Notes:
	// - Should always succeed (idempotent)
	// - Safe to call multiple times
	// - Logs warning if release fails (lock will auto-expire)
	Release(ctx context.Context) error

	// Renew extends lock TTL (heartbeat)
	//
	// Parameters:
	// - ctx: Context for cancellation
	// - ttl: New TTL duration
	//
	// Returns:
	// - error: If renewal failed (lock may have expired)
	//
	// Notes:
	// - Used for long-running updates
	// - Should be called periodically (every TTL/2)
	Renew(ctx context.Context, ttl time.Duration) error

	// IsHeld checks if lock is still held
	//
	// Returns:
	// - bool: True if lock is still held
	//
	// Notes:
	// - May return false if lock expired or released
	// - Does NOT renew the lock
	IsHeld() bool
}

// ================================================================================
// Helper Interfaces
// ================================================================================

// ConfigComparator compares configurations and calculates diffs
type ConfigComparator interface {
	// Compare calculates diff between two configurations
	//
	// Parameters:
	// - oldCfg: Current configuration
	// - newCfg: Proposed configuration
	// - sections: If specified, diff only these sections
	//
	// Returns:
	// - *ConfigDiff: Structured diff (added, modified, deleted)
	// - error: If comparison failed
	//
	// Performance Target: < 20ms p95
	//
	// Features:
	// - Deep comparison (handles nested structs)
	// - Secret sanitization in diff values
	// - Affected component detection
	// - Critical change detection
	Compare(oldCfg *Config, newCfg *Config, sections []string) (*ConfigDiff, error)

	// IdentifyAffectedComponents returns components affected by diff
	//
	// Parameters:
	// - diff: Configuration diff
	//
	// Returns:
	// - []string: Component names that need reload
	//
	// Logic:
	// - "database" if database.* changed
	// - "redis" if redis.* changed
	// - "llm" if llm.* changed
	// - etc.
	IdentifyAffectedComponents(diff *ConfigDiff) []string

	// IsCriticalChange checks if diff contains critical changes
	//
	// Critical Changes:
	// - database.host or database.port (connection loss)
	// - redis.addr (connection loss)
	// - authentication.enabled (security impact)
	// - server.port (requires restart)
	//
	// Parameters:
	// - diff: Configuration diff
	//
	// Returns:
	// - bool: True if diff contains critical changes
	IsCriticalChange(diff *ConfigDiff) bool
}
