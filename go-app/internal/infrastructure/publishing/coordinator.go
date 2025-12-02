package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ipiton/AMP/internal/core"
)

// PublishingResult represents the result of publishing to a single target
type PublishingResult struct {
	Target  *core.PublishingTarget
	Success bool
	Error   error
}

// PublishingCoordinator manages concurrent publishing to multiple targets
type PublishingCoordinator struct {
	queue           *PublishingQueue
	discoveryManager TargetDiscoveryManager
	modeManager     ModeManager // TN-060: Mode manager for metrics-only fallback
	semaphore       chan struct{}
	logger          *slog.Logger
}

// CoordinatorConfig holds configuration for publishing coordinator
type CoordinatorConfig struct {
	MaxConcurrent int // Maximum concurrent publishing operations
}

// DefaultCoordinatorConfig returns default configuration
func DefaultCoordinatorConfig() CoordinatorConfig {
	return CoordinatorConfig{
		MaxConcurrent: 5, // Publish to max 5 targets concurrently
	}
}

// NewPublishingCoordinator creates a new publishing coordinator
func NewPublishingCoordinator(
	queue *PublishingQueue,
	discoveryManager TargetDiscoveryManager,
	modeManager ModeManager,
	config CoordinatorConfig,
	logger *slog.Logger,
) *PublishingCoordinator {
	if logger == nil {
		logger = slog.Default()
	}

	return &PublishingCoordinator{
		queue:           queue,
		discoveryManager: discoveryManager,
		modeManager:     modeManager,
		semaphore:       make(chan struct{}, config.MaxConcurrent),
		logger:          logger,
	}
}

// PublishToAll publishes alert to all enabled targets concurrently
func (c *PublishingCoordinator) PublishToAll(ctx context.Context, enrichedAlert *core.EnrichedAlert) ([]*PublishingResult, error) {
	// TN-060: Check mode before publishing (metrics-only mode fallback)
	if c.modeManager != nil && c.modeManager.IsMetricsOnly() {
		c.logger.Info("Publishing skipped (metrics-only mode)",
			"fingerprint", enrichedAlert.Alert.Fingerprint,
		)
		// Return empty results (no publishing attempts)
		return []*PublishingResult{}, nil
	}

	// Get all enabled targets
	targets := c.discoveryManager.ListTargets()
	if len(targets) == 0 {
		c.logger.Warn("No publishing targets available")
		return nil, fmt.Errorf("no publishing targets available")
	}

	// Filter enabled targets
	enabledTargets := make([]*core.PublishingTarget, 0, len(targets))
	for _, t := range targets {
		if t.Enabled {
			enabledTargets = append(enabledTargets, t)
		}
	}

	if len(enabledTargets) == 0 {
		c.logger.Warn("No enabled publishing targets")
		return nil, fmt.Errorf("no enabled publishing targets")
	}

	c.logger.Info("Publishing to multiple targets",
		"total_targets", len(enabledTargets),
		"fingerprint", enrichedAlert.Alert.Fingerprint,
	)

	// Publish to all targets concurrently
	results := make([]*PublishingResult, len(enabledTargets))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, target := range enabledTargets {
		wg.Add(1)

		go func(idx int, t *core.PublishingTarget) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case c.semaphore <- struct{}{}:
				defer func() { <-c.semaphore }()
			case <-ctx.Done():
				mu.Lock()
				results[idx] = &PublishingResult{
					Target:  t,
					Success: false,
					Error:   ctx.Err(),
				}
				mu.Unlock()
				return
			}

			// Submit to queue
			err := c.queue.Submit(enrichedAlert, t)

			mu.Lock()
			results[idx] = &PublishingResult{
				Target:  t,
				Success: err == nil,
				Error:   err,
			}
			mu.Unlock()
		}(i, target)
	}

	// Wait for all publishing operations to complete
	wg.Wait()

	// Count successes
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	c.logger.Info("Parallel publishing completed",
		"total", len(results),
		"successful", successCount,
		"failed", len(results)-successCount,
	)

	return results, nil
}

// PublishToTargets publishes alert to specific targets by name
func (c *PublishingCoordinator) PublishToTargets(ctx context.Context, enrichedAlert *core.EnrichedAlert, targetNames []string) ([]*PublishingResult, error) {
	// TN-060: Check mode before publishing (metrics-only mode fallback)
	if c.modeManager != nil && c.modeManager.IsMetricsOnly() {
		c.logger.Info("Publishing skipped (metrics-only mode)",
			"fingerprint", enrichedAlert.Alert.Fingerprint,
			"targets", targetNames,
		)
		// Return empty results (no publishing attempts)
		return []*PublishingResult{}, nil
	}

	if len(targetNames) == 0 {
		return nil, fmt.Errorf("no target names provided")
	}

	// Resolve targets
	targets := make([]*core.PublishingTarget, 0, len(targetNames))
	for _, name := range targetNames {
		target, err := c.discoveryManager.GetTarget(name)
		if err != nil {
			c.logger.Warn("Target not found", "name", name)
			continue
		}
		if target.Enabled {
			targets = append(targets, target)
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("no valid targets found")
	}

	c.logger.Info("Publishing to specific targets",
		"requested", len(targetNames),
		"found", len(targets),
		"fingerprint", enrichedAlert.Alert.Fingerprint,
	)

	// Publish concurrently
	results := make([]*PublishingResult, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, target := range targets {
		wg.Add(1)

		go func(idx int, t *core.PublishingTarget) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case c.semaphore <- struct{}{}:
				defer func() { <-c.semaphore }()
			case <-ctx.Done():
				mu.Lock()
				results[idx] = &PublishingResult{
					Target:  t,
					Success: false,
					Error:   ctx.Err(),
				}
				mu.Unlock()
				return
			}

			// Submit to queue
			err := c.queue.Submit(enrichedAlert, t)

			mu.Lock()
			results[idx] = &PublishingResult{
				Target:  t,
				Success: err == nil,
				Error:   err,
			}
			mu.Unlock()
		}(i, target)
	}

	wg.Wait()

	return results, nil
}
