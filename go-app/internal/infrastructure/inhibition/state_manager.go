package inhibition

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics"
)

// InhibitionState represents the state of an inhibition relationship between alerts.
// It tracks which target alert is being inhibited by which source alert and under which rule.
type InhibitionState struct {
	// TargetFingerprint is the fingerprint of the inhibited (suppressed) alert
	TargetFingerprint string `json:"target_fingerprint"`

	// SourceFingerprint is the fingerprint of the source alert causing the inhibition
	SourceFingerprint string `json:"source_fingerprint"`

	// RuleName is the name of the inhibition rule that caused this inhibition
	RuleName string `json:"rule_name"`

	// InhibitedAt is when the inhibition relationship was established
	InhibitedAt time.Time `json:"inhibited_at"`

	// ExpiresAt is when this inhibition expires (optional)
	// If nil, the inhibition lasts until the source alert resolves
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// InhibitionStateManager manages the state of active inhibition relationships.
// It tracks which alerts are currently inhibited and by whom, providing both
// in-memory access and Redis persistence for high availability.
type InhibitionStateManager interface {
	// RecordInhibition records a new inhibition relationship.
	// This is called when an alert is determined to be inhibited by another alert.
	RecordInhibition(ctx context.Context, state *InhibitionState) error

	// RemoveInhibition removes an inhibition relationship.
	// This is called when the source alert resolves or the inhibition expires.
	RemoveInhibition(ctx context.Context, targetFingerprint string) error

	// GetActiveInhibitions returns all currently active inhibition relationships.
	GetActiveInhibitions(ctx context.Context) ([]*InhibitionState, error)

	// GetInhibitedAlerts returns a list of all currently inhibited alert fingerprints.
	// This is a convenience method for quick lookups.
	GetInhibitedAlerts(ctx context.Context) ([]string, error)

	// IsInhibited checks if a specific alert is currently inhibited.
	IsInhibited(ctx context.Context, targetFingerprint string) (bool, error)

	// GetInhibitionState retrieves the inhibition state for a specific target alert.
	// Returns nil if the alert is not inhibited.
	GetInhibitionState(ctx context.Context, targetFingerprint string) (*InhibitionState, error)
}

// DefaultStateManager implements InhibitionStateManager with in-memory storage
// and optional Redis persistence for high availability.
type DefaultStateManager struct {
	// In-memory state (fingerprint -> InhibitionState)
	states sync.Map

	// Redis store for persistence (optional)
	redisStore cache.Cache

	// Redis key prefix
	redisPrefix string

	// Redis TTL for state entries
	redisTTL time.Duration

	// Logger
	logger *slog.Logger

	// Metrics for observability (TN-129)
	metrics *metrics.BusinessMetrics

	// Cleanup worker control (TN-129 Phase 6)
	cleanupInterval time.Duration
	cleanupStop     chan struct{}
	cleanupDone     sync.WaitGroup
}

// NewDefaultStateManager creates a new DefaultStateManager instance.
//
// Parameters:
//   - redisStore: Optional Redis cache for persistence. If nil, only in-memory storage is used.
//   - logger: Logger instance for debug/error logging.
//   - metrics: BusinessMetrics instance for observability. If nil, metrics are not recorded.
//
// Returns:
//   - *DefaultStateManager: Initialized state manager.
//
// Note: Call StartCleanupWorker(ctx) after creation to enable automatic cleanup of expired states.
func NewDefaultStateManager(redisStore cache.Cache, logger *slog.Logger, metrics *metrics.BusinessMetrics) *DefaultStateManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &DefaultStateManager{
		states:          sync.Map{},
		redisStore:      redisStore,
		redisPrefix:     "inhibition:state:",
		redisTTL:        24 * time.Hour,
		logger:          logger,
		metrics:         metrics,
		cleanupInterval: 1 * time.Minute, // Default: cleanup every minute
		cleanupStop:     make(chan struct{}),
	}
}

// RecordInhibition records a new inhibition relationship in both memory and Redis.
func (sm *DefaultStateManager) RecordInhibition(ctx context.Context, state *InhibitionState) error {
// 	start := time.Now()

	if state == nil {
		return fmt.Errorf("state cannot be nil")
	}

	if state.TargetFingerprint == "" {
		return fmt.Errorf("target fingerprint cannot be empty")
	}

	if state.SourceFingerprint == "" {
		return fmt.Errorf("source fingerprint cannot be empty")
	}

	// Store in memory
	sm.states.Store(state.TargetFingerprint, state)

	sm.logger.Debug("Recorded inhibition",
		"target", state.TargetFingerprint,
		"source", state.SourceFingerprint,
		"rule", state.RuleName,
	)

	// Persist to Redis if available
	if sm.redisStore != nil {
		if err := sm.persistToRedis(ctx, state); err != nil {
			sm.logger.Warn("Failed to persist inhibition state to Redis",
				"error", err,
				"target", state.TargetFingerprint,
			)
			// Record Redis error metric
			if sm.metrics != nil {
				sm.metrics.RecordInhibitionStateRedisError("persist")
			}
			// Non-critical: in-memory state is still valid
		}
	}

	// Record metrics
// 	duration := time.Since(start)
	if sm.metrics != nil {
		sm.metrics.RecordInhibitionStateRecord(state.RuleName)
		// Update active gauge
		count := sm.countActiveStates()
		sm.metrics.SetInhibitionStateActive(float64(count))
	}

	return nil
}

// RemoveInhibition removes an inhibition relationship from both memory and Redis.
func (sm *DefaultStateManager) RemoveInhibition(ctx context.Context, targetFingerprint string) error {
// 	start := time.Now()

	if targetFingerprint == "" {
		return fmt.Errorf("target fingerprint cannot be empty")
	}

	// Remove from memory
	sm.states.Delete(targetFingerprint)

	sm.logger.Debug("Removed inhibition",
		"target", targetFingerprint,
	)

	// Remove from Redis if available
	if sm.redisStore != nil {
		key := sm.redisPrefix + targetFingerprint
		if err := sm.redisStore.Delete(ctx, key); err != nil {
			sm.logger.Warn("Failed to remove inhibition state from Redis",
				"error", err,
				"target", targetFingerprint,
			)
			// Record Redis error metric
			if sm.metrics != nil {
				sm.metrics.RecordInhibitionStateRedisError("delete")
			}
			// Non-critical: in-memory state is already removed
		}
	}

	// Record metrics
// 	duration := time.Since(start)
	if sm.metrics != nil {
		sm.metrics.RecordInhibitionStateRemoval("manual")
		// Update active gauge
		count := sm.countActiveStates()
		sm.metrics.SetInhibitionStateActive(float64(count))
	}

	return nil
}

// GetActiveInhibitions returns all currently active inhibition relationships.
func (sm *DefaultStateManager) GetActiveInhibitions(ctx context.Context) ([]*InhibitionState, error) {
// 	start := time.Now()
	states := make([]*InhibitionState, 0)

	sm.states.Range(func(key, value interface{}) bool {
		if state, ok := value.(*InhibitionState); ok {
			// Filter out expired inhibitions
			if state.ExpiresAt == nil || time.Now().Before(*state.ExpiresAt) {
				states = append(states, state)
			} else {
				// Clean up expired state
				sm.states.Delete(key)
			}
		}
		return true
	})

	// Record metrics
	if sm.metrics != nil {
// 		duration := time.Since(start)
		sm.metrics.RecordInhibitionStateOperation("get", "success")
	}

	return states, nil
}

// GetInhibitedAlerts returns a list of all currently inhibited alert fingerprints.
func (sm *DefaultStateManager) GetInhibitedAlerts(ctx context.Context) ([]string, error) {
	fingerprints := make([]string, 0)

	sm.states.Range(func(key, value interface{}) bool {
		if fingerprint, ok := key.(string); ok {
			if state, ok := value.(*InhibitionState); ok {
				// Filter out expired inhibitions
				if state.ExpiresAt == nil || time.Now().Before(*state.ExpiresAt) {
					fingerprints = append(fingerprints, fingerprint)
				}
			}
		}
		return true
	})

	return fingerprints, nil
}

// IsInhibited checks if a specific alert is currently inhibited.
func (sm *DefaultStateManager) IsInhibited(ctx context.Context, targetFingerprint string) (bool, error) {
// 	start := time.Now()

	if targetFingerprint == "" {
		return false, fmt.Errorf("target fingerprint cannot be empty")
	}

	value, ok := sm.states.Load(targetFingerprint)
	if !ok {
		// Record metrics for fast path (not found)
		if sm.metrics != nil {
// 			duration := time.Since(start)
			sm.metrics.RecordInhibitionStateOperation("check", "success")
		}
		return false, nil
	}

	state, ok := value.(*InhibitionState)
	if !ok {
		return false, nil
	}

	// Check if expired
	if state.ExpiresAt != nil && time.Now().After(*state.ExpiresAt) {
		// Clean up expired state
		sm.states.Delete(targetFingerprint)

		// Record metrics
		if sm.metrics != nil {
// 			duration := time.Since(start)
			sm.metrics.RecordInhibitionStateOperation("check", "success")
		}
		return false, nil
	}

	// Record metrics for successful check
	if sm.metrics != nil {
// 		duration := time.Since(start)
		sm.metrics.RecordInhibitionStateOperation("check", "success")
	}

	return true, nil
}

// GetInhibitionState retrieves the inhibition state for a specific target alert.
func (sm *DefaultStateManager) GetInhibitionState(ctx context.Context, targetFingerprint string) (*InhibitionState, error) {
	if targetFingerprint == "" {
		return nil, fmt.Errorf("target fingerprint cannot be empty")
	}

	value, ok := sm.states.Load(targetFingerprint)
	if !ok {
		// Try Redis fallback if available
		if sm.redisStore != nil {
			state, err := sm.loadFromRedis(ctx, targetFingerprint)
			if err != nil {
				return nil, nil // Not found
			}
			// Repopulate memory cache
			sm.states.Store(targetFingerprint, state)
			return state, nil
		}
		return nil, nil
	}

	state, ok := value.(*InhibitionState)
	if !ok {
		return nil, nil
	}

	// Check if expired
	if state.ExpiresAt != nil && time.Now().After(*state.ExpiresAt) {
		// Clean up expired state
		sm.states.Delete(targetFingerprint)
		return nil, nil
	}

	return state, nil
}

// persistToRedis persists an inhibition state to Redis.
func (sm *DefaultStateManager) persistToRedis(ctx context.Context, state *InhibitionState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	key := sm.redisPrefix + state.TargetFingerprint
	if err := sm.redisStore.Set(ctx, key, string(data), sm.redisTTL); err != nil {
		return fmt.Errorf("failed to set Redis key: %w", err)
	}

	return nil
}

// loadFromRedis loads an inhibition state from Redis.
func (sm *DefaultStateManager) loadFromRedis(ctx context.Context, targetFingerprint string) (*InhibitionState, error) {
	key := sm.redisPrefix + targetFingerprint

	var data string
	if err := sm.redisStore.Get(ctx, key, &data); err != nil {
		// Record Redis error metric
		if sm.metrics != nil {
			sm.metrics.RecordInhibitionStateRedisError("load")
		}
		return nil, fmt.Errorf("failed to get Redis key: %w", err)
	}

	var state InhibitionState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// countActiveStates counts the number of currently active (non-expired) inhibition states.
// This is a helper method used for updating the InhibitionStateActiveGauge metric.
func (sm *DefaultStateManager) countActiveStates() int {
	count := 0
	now := time.Now()

	sm.states.Range(func(key, value interface{}) bool {
		if state, ok := value.(*InhibitionState); ok {
			// Only count non-expired states
			if state.ExpiresAt == nil || now.Before(*state.ExpiresAt) {
				count++
			}
		}
		return true
	})

	return count
}

// ==================== Cleanup Worker (TN-129 Phase 6) ====================

// StartCleanupWorker starts the background cleanup worker that periodically removes expired inhibition states.
// The worker runs every cleanupInterval (default: 1 minute) and removes states where ExpiresAt has passed.
//
// This method is safe to call multiple times - only one worker will be active.
//
// Parameters:
//   - ctx: Context for cancellation. When ctx is cancelled, the worker stops gracefully.
//
// Example:
//
//	sm := NewDefaultStateManager(redis, logger, metrics)
//	sm.StartCleanupWorker(ctx)
//	defer sm.StopCleanupWorker()
func (sm *DefaultStateManager) StartCleanupWorker(ctx context.Context) {
	sm.cleanupDone.Add(1)
	go sm.cleanupWorker(ctx)

	sm.logger.Info("Inhibition state cleanup worker started",
		"interval", sm.cleanupInterval,
	)
}

// cleanupWorker is the main loop for the cleanup worker.
// It runs periodically and removes expired inhibition states.
func (sm *DefaultStateManager) cleanupWorker(ctx context.Context) {
	defer sm.cleanupDone.Done()

	ticker := time.NewTicker(sm.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sm.logger.Info("Cleanup worker stopped (context cancelled)")
			return

		case <-sm.cleanupStop:
			sm.logger.Info("Cleanup worker stopped (explicit stop)")
			return

		case <-ticker.C:
			sm.cleanupExpiredStates(ctx)
		}
	}
}

// cleanupExpiredStates removes all expired inhibition states from memory.
// This is called periodically by the cleanup worker.
func (sm *DefaultStateManager) cleanupExpiredStates(ctx context.Context) {
// 	start := time.Now()
	cleanedCount := 0
	now := time.Now()

	// Collect expired fingerprints
	expiredFingerprints := make([]string, 0)

	sm.states.Range(func(key, value interface{}) bool {
		fingerprint, ok := key.(string)
		if !ok {
			return true
		}

		state, ok := value.(*InhibitionState)
		if !ok {
			return true
		}

		// Check if expired
		if state.ExpiresAt != nil && now.After(*state.ExpiresAt) {
			expiredFingerprints = append(expiredFingerprints, fingerprint)
		}

		return true
	})

start := time.Now()
	// Delete expired states
	for _, fp := range expiredFingerprints {
		sm.states.Delete(fp)
		cleanedCount++

		// Record metrics
		if sm.metrics != nil {
			sm.metrics.RecordInhibitionStateExpired("expired")
		}

		sm.logger.Debug("Cleaned up expired inhibition state",
			"target_fingerprint", fp,
		)
	}

	// Record cleanup duration
	if sm.metrics != nil {
// 		duration := time.Since(start)
		sm.metrics.RecordInhibitionStateOperation("cleanup", "success")

		// Update active gauge
		if cleanedCount > 0 {
			count := sm.countActiveStates()
			sm.metrics.SetInhibitionStateActive(float64(count))
		}
	}

	if cleanedCount > 0 {
		sm.logger.Info("Cleanup completed",
			"expired_states_removed", cleanedCount,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

// StopCleanupWorker gracefully stops the cleanup worker.
// This method blocks until the worker has fully stopped.
//
// It's safe to call this multiple times.
//
// Example:
//
//	sm.StartCleanupWorker(ctx)
//	defer sm.StopCleanupWorker()
func (sm *DefaultStateManager) StopCleanupWorker() {
	select {
	case <-sm.cleanupStop:
		// Already stopped
		return
	default:
		close(sm.cleanupStop)
	}

	sm.cleanupDone.Wait()

	sm.logger.Info("Cleanup worker stopped gracefully")
}
