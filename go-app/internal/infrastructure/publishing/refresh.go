package publishing

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// RefreshManager handles periodic and manual refresh of publishing targets
type RefreshManager struct {
	discoveryManager TargetDiscoveryManager
	refreshInterval  time.Duration
	logger           *slog.Logger

	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
}

// NewRefreshManager creates a new refresh manager
func NewRefreshManager(discoveryManager TargetDiscoveryManager, refreshInterval time.Duration, logger *slog.Logger) *RefreshManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &RefreshManager{
		discoveryManager: discoveryManager,
		refreshInterval:  refreshInterval,
		logger:           logger,
		stopChan:         make(chan struct{}),
	}
}

// Start begins periodic target refresh
func (r *RefreshManager) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = true
	r.mu.Unlock()

	// Initial discovery
	if err := r.discoveryManager.DiscoverTargets(ctx); err != nil {
		r.logger.Error("Initial target discovery failed", "error", err)
		return err
	}

	// Start background refresh loop
	r.wg.Add(1)
	go r.refreshLoop(ctx)

	r.logger.Info("Refresh manager started", "interval", r.refreshInterval)
	return nil
}

// Stop stops the refresh loop
func (r *RefreshManager) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	r.mu.Unlock()

	close(r.stopChan)
	r.wg.Wait()

	r.logger.Info("Refresh manager stopped")
}

// RefreshNow triggers an immediate manual refresh
func (r *RefreshManager) RefreshNow(ctx context.Context) error {
	r.logger.Info("Manual refresh triggered")

	if err := r.discoveryManager.DiscoverTargets(ctx); err != nil {
		r.logger.Error("Manual refresh failed", "error", err)
		return err
	}

	r.logger.Info("Manual refresh completed", "target_count", r.discoveryManager.GetTargetCount())
	return nil
}

// refreshLoop runs periodic target discovery
func (r *RefreshManager) refreshLoop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := r.discoveryManager.DiscoverTargets(ctx); err != nil {
				r.logger.Error("Periodic refresh failed", "error", err)
			} else {
				r.logger.Info("Periodic refresh completed", "target_count", r.discoveryManager.GetTargetCount())
			}

		case <-r.stopChan:
			return

		case <-ctx.Done():
			r.logger.Info("Refresh loop stopped due to context cancellation")
			return
		}
	}
}
