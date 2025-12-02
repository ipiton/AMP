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
//    - Tables: config_versions, config_audit_log
//    - ACID transactions
//    - Retention policies via triggers
// 2. Filesystem (fallback for development):
//    - Files: config/versions/v{version}.json
//    - Atomic writes via temp file + rename
//    - Limited concurrency support
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
// Example Implementation:
//
//	type DatabasePool struct {
//	    pool *pgxpool.Pool
//	    config *config.Config
//	    logger *slog.Logger
//	}
//
//	func (db *DatabasePool) Reload(ctx context.Context, cfg *config.Config) error {
//	    // Check if database config actually changed
//	    if reflect.DeepEqual(db.config.Database, cfg.Database) {
//	        db.logger.Info("database config unchanged, skipping reload")
//	        return nil // No-op if config unchanged
//	    }
//
//	    // Create new connection pool with new config
//	    newPool, err := createPool(cfg.Database)
//	    if err != nil {
//	        return fmt.Errorf("failed to create new pool: %w", err)
//	    }
//
//	    // Gracefully close old pool
//	    oldPool := db.pool
//	    db.pool = newPool
//	    db.config = cfg
//
//	    // Close old pool in background (after active connections finish)
//	    go func() {
//	        time.Sleep(5 * time.Second) // Grace period
//	        oldPool.Close()
//	    }()
//
//	    db.logger.Info("database pool reloaded successfully")
//	    return nil
//	}
//
//	func (db *DatabasePool) Name() string { return "database" }
//	func (db *DatabasePool) IsCritical() bool { return true }
type Reloadable interface {
	// Reload reloads component with new configuration
	//
	// Implementation Requirements:
	// 1. Check if config actually changed (optimization)
	// 2. Validate new config before applying
	// 3. Apply new config atomically
	// 4. Gracefully close old resources
	// 5. Return error if reload failed
	//
	// Parameters:
	// - ctx: Context with timeout (typically 30s)
	// - cfg: New configuration
	//
	// Returns:
	// - error: If reload failed (triggers rollback if IsCritical)
	//
	// Performance:
	// - Should complete within ctx timeout (typically 30s)
	// - Should be fast for unchanged config (< 10ms)
	//
	// Concurrency:
	// - May be called concurrently with other Reloadable instances
	// - Must be thread-safe
	Reload(ctx context.Context, cfg *Config) error

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

	// IsCritical returns true if reload failure should trigger rollback
	//
	// Critical Components (must succeed):
	// - database: Cannot function without database
	// - redis: Required for distributed locking and caching
	//
	// Non-Critical Components (can fail gracefully):
	// - llm: Can continue without AI features
	// - metrics: Can continue without metrics export
	// - cache: Can fallback to no caching
	//
	// Rollback Policy:
	// - If ANY critical component fails: Rollback entire config
	// - If ONLY non-critical components fail: Continue with warning
	IsCritical() bool
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
	// - Registration order doesn't matter (parallel execution)
	// - Can register same component multiple times (idempotent)
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

	// ReloadAll reloads all registered components in parallel
	//
	// Process:
	// 1. Filter to affected components (if specified)
	// 2. Create goroutine for each component
	// 3. Call Reload() with timeout (30s)
	// 4. Collect results
	// 5. Check for critical failures
	//
	// Parameters:
	// - ctx: Context with timeout (typically 30s)
	// - cfg: New configuration
	// - affectedComponents: Component names to reload (nil = all)
	//
	// Returns:
	// - []ReloadError: List of reload errors (empty if all succeeded)
	//
	// Performance Target: < 300ms p95
	//
	// Rollback Decision:
	// - Returns error if ANY critical component fails
	// - Returns error list if ONLY non-critical components fail
	ReloadAll(ctx context.Context, cfg *Config, affectedComponents []string) []ReloadError

	// GetRegisteredComponents returns list of registered component names
	GetRegisteredComponents() []string
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
