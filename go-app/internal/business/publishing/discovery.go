// Package publishing provides target discovery and publishing functionality.
//
// This package implements dynamic discovery of publishing targets from Kubernetes
// Secrets, providing a simplified interface for the publishing pipeline.
//
// Key Components:
//   - TargetDiscoveryManager: Interface for target discovery and management
//   - DefaultTargetDiscoveryManager: Production implementation with K8s integration
//   - targetCache: Thread-safe in-memory cache for fast O(1) lookups
//   - Secret parsing: Base64 decode + JSON unmarshal + validation
//   - Validation engine: Comprehensive rules for target configuration
//
// Example Usage:
//
//	// Initialize K8s client
//	k8sClient, err := k8s.NewK8sClient(k8s.DefaultK8sClientConfig())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer k8sClient.Close()
//
//	// Create target discovery manager
//	manager, err := publishing.NewTargetDiscoveryManager(
//	    k8sClient,
//	    "production",                  // namespace
//	    "publishing-target=true",      // label selector
//	    slog.Default(),
//	    metrics.GlobalRegistry,
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Discover targets
//	if err := manager.DiscoverTargets(context.Background()); err != nil {
//	    log.Warn("Discovery failed, using stale cache", "error", err)
//	}
//
//	// Get target for publishing
//	target, err := manager.GetTarget("rootly-prod")
//	if err != nil {
//	    log.Error("Target not found", "name", "rootly-prod")
//	    return
//	}
//
//	// Publish alert to target
//	publish(alert, target)
//
// See TN-047 for detailed design documentation.
package publishing

import (
	"context"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// TargetDiscoveryManager manages dynamic discovery of publishing targets from K8s Secrets.
//
// This interface provides methods for:
//   - Discovering targets from K8s Secrets (with label selectors)
//   - Retrieving targets by name (O(1) lookup)
//   - Listing all active targets
//   - Filtering targets by type (rootly/pagerduty/slack/webhook)
//   - Getting discovery statistics
//   - Health checking K8s client connectivity
//
// Thread Safety:
//   - All methods are safe for concurrent use
//   - Read operations (Get/List) can run in parallel
//   - Write operations (DiscoverTargets) are serialized
//
// Error Handling:
//   - Discovery errors are returned but don't crash (graceful degradation)
//   - Invalid secrets are logged and skipped (partial success)
//   - K8s API unavailable → old cache retained (stale OK)
//
// Performance:
//   - Get: O(1), <100ns (in-memory cache)
//   - List: O(n), <1µs for 20 targets
//   - DiscoverTargets: <2s for 20 secrets (K8s API latency)
//
// Example:
//
//	// Create manager
//	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
//
//	// Discover targets (initial discovery)
//	if err := manager.DiscoverTargets(ctx); err != nil {
//	    log.Error("Discovery failed", "error", err)
//	}
//
//	// Get target by name
//	target, err := manager.GetTarget("rootly-prod")
//	if err != nil {
//	    return fmt.Errorf("target not found: %w", err)
//	}
//
//	// List all targets
//	targets := manager.ListTargets()
//	log.Info("Active targets", "count", len(targets))
//
//	// Filter by type
//	slackTargets := manager.GetTargetsByType("slack")
//	for _, target := range slackTargets {
//	    log.Info("Slack target", "name", target.Name, "url", target.URL)
//	}
//
//	// Get statistics
//	stats := manager.GetStats()
//	log.Info("Discovery stats",
//	    "total", stats.TotalTargets,
//	    "valid", stats.ValidTargets,
//	    "invalid", stats.InvalidTargets,
//	    "last_discovery", stats.LastDiscovery)
//
//	// Health check
//	if err := manager.Health(ctx); err != nil {
//	    log.Error("Target discovery unhealthy", "error", err)
//	}
type TargetDiscoveryManager interface {
	// DiscoverTargets lists K8s secrets matching label selector and refreshes in-memory cache.
	//
	// This method:
	//   1. Calls k8sClient.ListSecrets(namespace, labelSelector)
	//   2. Parses each secret (base64 decode + JSON unmarshal)
	//   3. Validates parsed targets (required fields, URL format)
	//   4. Updates in-memory cache atomically (all-or-nothing)
	//   5. Records statistics (discovered/valid/invalid counts)
	//   6. Updates Prometheus metrics
	//
	// Error Handling:
	//   - K8s API unavailable → returns error, keeps old cache (stale OK)
	//   - Some secrets invalid → logs warnings, continues with valid (partial success)
	//   - Zero secrets found → empties cache, returns nil (not an error)
	//
	// Performance:
	//   - Target: <2s for 20 secrets (includes K8s API latency)
	//   - Goal (150%): <1s for 20 secrets
	//
	// Example:
	//   ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	//   defer cancel()
	//
	//   if err := manager.DiscoverTargets(ctx); err != nil {
	//       log.Error("Discovery failed", "error", err)
	//       // Publishing continues with stale cache
	//   }
	DiscoverTargets(ctx context.Context) error

	// GetTarget returns target by name (O(1) lookup in cache).
	//
	// Returns:
	//   - PublishingTarget + nil on success
	//   - nil + ErrTargetNotFound if target doesn't exist
	//
	// Performance:
	//   - Target: <500ns
	//   - Goal (150%): <100ns (in-memory map lookup)
	//
	// Thread-Safe: Yes (RLock during lookup)
	//
	// Example:
	//   target, err := manager.GetTarget("rootly-prod")
	//   if err != nil {
	//       var notFoundErr *ErrTargetNotFound
	//       if errors.As(err, &notFoundErr) {
	//           log.Warn("Target not configured", "name", "rootly-prod")
	//       }
	//       return
	//   }
	//   log.Info("Publishing to target", "name", target.Name, "url", target.URL)
	GetTarget(name string) (*core.PublishingTarget, error)

	// ListTargets returns all active targets in cache.
	//
	// Returns:
	//   - Slice of all targets (shallow copy, safe to iterate)
	//   - Empty slice if no targets discovered
	//
	// Performance:
	//   - Target: <5µs for 20 targets
	//   - Goal (150%): <1µs for 20 targets
	//
	// Thread-Safe: Yes (RLock during iteration)
	//
	// Example:
	//   targets := manager.ListTargets()
	//   log.Info("Active targets", "count", len(targets))
	//   for _, target := range targets {
	//       if target.Enabled {
	//           publish(alert, target)
	//       }
	//   }
	ListTargets() []*core.PublishingTarget

	// GetTargetsByType filters targets by type (rootly/pagerduty/slack/webhook).
	//
	// Parameters:
	//   - targetType: Target type to filter (rootly, pagerduty, slack, webhook)
	//
	// Returns:
	//   - Slice of matching targets
	//   - Empty slice if no targets match type
	//
	// Performance:
	//   - Target: <10µs for 20 targets (O(n) scan)
	//   - Goal (150%): <2µs for 20 targets
	//
	// Thread-Safe: Yes (RLock during iteration)
	//
	// Example:
	//   slackTargets := manager.GetTargetsByType("slack")
	//   log.Info("Slack targets", "count", len(slackTargets))
	//   for _, target := range slackTargets {
	//       sendSlackAlert(alert, target)
	//   }
	GetTargetsByType(targetType string) []*core.PublishingTarget

	// GetStats returns discovery statistics for monitoring/debugging.
	//
	// Returns:
	//   - DiscoveryStats with current statistics
	//   - Thread-safe (copy of internal stats)
	//
	// Example:
	//   stats := manager.GetStats()
	//   log.Info("Discovery stats",
	//       "total_targets", stats.TotalTargets,
	//       "valid_targets", stats.ValidTargets,
	//       "invalid_targets", stats.InvalidTargets,
	//       "last_discovery", stats.LastDiscovery,
	//       "discovery_errors", stats.DiscoveryErrors)
	//
	//   // Check if cache is stale
	//   if time.Since(stats.LastDiscovery) > 10*time.Minute {
	//       log.Warn("Discovery cache is stale", "age", time.Since(stats.LastDiscovery))
	//   }
	GetStats() DiscoveryStats

	// Health checks target discovery manager + K8s client health.
	//
	// Returns:
	//   - nil if healthy (K8s API accessible)
	//   - error if unhealthy (K8s API unreachable)
	//
	// Performance:
	//   - Target: <100ms (K8s API ping)
	//   - Timeout: 5s (fail-fast)
	//
	// Use Case: Kubernetes liveness/readiness probes
	//
	// Example:
	//   func healthHandler(w http.ResponseWriter, r *http.Request) {
	//       ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	//       defer cancel()
	//
	//       if err := manager.Health(ctx); err != nil {
	//           http.Error(w, "Target discovery unhealthy", 503)
	//           return
	//       }
	//       w.WriteHeader(200)
	//       w.Write([]byte("OK"))
	//   }
	Health(ctx context.Context) error
}

// DiscoveryStats tracks target discovery statistics for monitoring and debugging.
//
// Fields:
//   - TotalTargets: Total secrets discovered from K8s API (valid + invalid)
//   - ValidTargets: Number of valid targets currently in cache
//   - InvalidTargets: Number of invalid/skipped secrets (parse/validation errors)
//   - LastDiscovery: Timestamp of last successful discovery (for staleness check)
//   - DiscoveryErrors: Cumulative count of discovery errors (K8s API failures)
//
// Thread-Safety:
//   - Read via GetStats() (returns copy, safe)
//   - Write via internal manager methods (protected by RWMutex)
//
// Example:
//
//	stats := manager.GetStats()
//
//	// Check cache health
//	if stats.ValidTargets == 0 {
//	    log.Warn("No valid targets discovered")
//	}
//
//	// Check cache staleness (for alerting)
//	if time.Since(stats.LastDiscovery) > 10*time.Minute {
//	    log.Error("Discovery cache is stale", "age", time.Since(stats.LastDiscovery))
//	}
//
//	// Check error rate (for debugging)
//	if stats.InvalidTargets > 0 {
//	    invalidRate := float64(stats.InvalidTargets) / float64(stats.TotalTargets)
//	    if invalidRate > 0.2 {
//	        log.Warn("High invalid secret rate", "rate", invalidRate)
//	    }
//	}
//
//	// Prometheus metrics
//	targetsGauge.Set(float64(stats.ValidTargets))
//	invalidSecretsCounter.Add(float64(stats.InvalidTargets))
//	lastSuccessTimestamp.Set(float64(stats.LastDiscovery.Unix()))
type DiscoveryStats struct {
	// TotalTargets is total number of secrets discovered from K8s API.
	// This includes both valid and invalid secrets.
	TotalTargets int

	// ValidTargets is number of valid targets currently in cache.
	// These are secrets that passed parsing + validation.
	ValidTargets int

	// InvalidTargets is number of invalid/skipped secrets.
	// Reasons: parse errors (bad base64/JSON), validation failures.
	InvalidTargets int

	// LastDiscovery is timestamp of last successful discovery.
	// Used for cache staleness detection (alert if >10m old).
	LastDiscovery time.Time

	// DiscoveryErrors is cumulative count of discovery errors.
	// Incremented on K8s API failures (network, auth, timeout).
	DiscoveryErrors int
}
