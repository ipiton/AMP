package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// DefaultTargetDiscoveryManager is production implementation of TargetDiscoveryManager.
//
// This implementation:
//   - Integrates with K8s client (TN-046) for secret discovery
//   - Parses secrets (base64 + JSON) into PublishingTarget structures
//   - Validates targets (comprehensive rules)
//   - Stores targets in thread-safe in-memory cache (O(1) lookups)
//   - Records Prometheus metrics (6 metrics)
//   - Logs structured events (slog)
//
// Thread Safety:
//   - All public methods are safe for concurrent use
//   - Internal state protected by sync.RWMutex
//   - Cache uses separate RWMutex for hot path optimization
//
// Performance:
//   - Get: <50ns (in-memory O(1))
//   - List: <800ns for 20 targets
//   - DiscoverTargets: <2s for 20 secrets (K8s API latency)
//
// Observability:
//   - 6 Prometheus metrics (targets, duration, errors, lookups)
//   - Structured logging (DEBUG/INFO/WARN/ERROR levels)
//   - Discovery statistics (GetStats)
//
// Example:
//
//	// Create manager
//	k8sClient, _ := k8s.NewK8sClient(k8s.DefaultK8sClientConfig())
//	manager, err := NewTargetDiscoveryManager(
//	    k8sClient,
//	    "production",
//	    "publishing-target=true",
//	    slog.Default(),
//	    metrics.GlobalRegistry,
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Discover targets
//	if err := manager.DiscoverTargets(context.Background()); err != nil {
//	    log.Warn("Discovery failed, using stale cache")
//	}
//
//	// Use targets
//	target, _ := manager.GetTarget("rootly-prod")
//	publish(alert, target)
type DefaultTargetDiscoveryManager struct {
	// K8s client for secret discovery (from TN-046).
	//
	// NIL in config-only mode (NewConfigOnlyTargetDiscoveryManager): a
	// deployment whose targets come exclusively from the config's
	// `receivers:` section needs no cluster access at all, which is what
	// makes the lite profile able to deliver notifications.
	k8sClient k8s.K8sClient

	// Configuration
	namespace     string // K8s namespace to search
	labelSelector string // Label selector (e.g., "publishing-target=true")

	// In-memory cache of K8s-Secret-sourced targets (thread-safe, O(1) Get)
	cache *targetCache

	// configCache holds the config-provisioned targets (FU-RECEIVERS-
	// INTEGRATION, R3): built by BuildConfigTargets from the loaded
	// `receivers:` section and injected via SetConfigTargets. Deliberately a
	// SECOND cache rather than entries in `cache`:
	//
	//   - DiscoverTargets replaces `cache` wholesale on every K8s poll, so
	//     config targets living there would be wiped by the next refresh;
	//   - the two sources have to be told apart for source-labelled stats and
	//     metrics, and for keeping the K8s path's behaviour bit-for-bit
	//     unchanged (R1: "K8s targets keep today's behavior untouched").
	//
	// targetCache.Set builds a fresh map and swaps it under one lock, so a
	// reload never exposes a window with zero config targets.
	configCache *targetCache

	// Statistics (protected by mu)
	stats DiscoveryStats
	mu    sync.RWMutex

	// Observability
	logger  *slog.Logger
	metrics *DiscoveryMetrics
}

// DiscoveryMetrics holds Prometheus metrics for target discovery.
type DiscoveryMetrics struct {
	// TargetsTotal tracks active targets by type, enabled status and source.
	// Labels: type (rootly/pagerduty/slack/webhook/telegram/email),
	// enabled (true/false), source (k8s/config)
	TargetsTotal *prometheus.GaugeVec

	// DurationSeconds tracks operation duration (discover/parse/validate).
	// Labels: operation (discover/parse/validate)
	DurationSeconds *prometheus.HistogramVec

	// ErrorsTotal tracks errors by type (k8s_api/parse/validate).
	// Labels: error_type
	ErrorsTotal *prometheus.CounterVec

	// SecretsTotal tracks processed secrets by status (valid/invalid/skipped).
	// Labels: status
	SecretsTotal *prometheus.CounterVec

	// LookupsTotal tracks cache lookups by operation and status.
	// Labels: operation (get/list/get_by_type), status (hit/miss)
	LookupsTotal *prometheus.CounterVec

	// LastSuccessTimestamp tracks last successful discovery (Unix timestamp).
	// For alerting on stale cache (alert if >10m old).
	LastSuccessTimestamp prometheus.Gauge
}

// NewTargetDiscoveryManager creates new target discovery manager.
//
// Parameters:
//   - k8sClient: K8s client for secret access (from TN-046, required)
//   - namespace: K8s namespace to search (required, e.g., "production", "default")
//   - labelSelector: Label query (optional, default: "publishing-target=true")
//   - logger: Structured logger (optional, default: slog.Default())
//   - metricsRegistry: Prometheus registry (optional, nil = no metrics)
//
// Returns:
//   - TargetDiscoveryManager implementation
//   - error if validation fails (k8sClient nil, namespace empty)
//
// Example:
//
//	// Basic usage (minimal config)
//	client, _ := k8s.NewK8sClient(k8s.DefaultK8sClientConfig())
//	manager, err := NewTargetDiscoveryManager(client, "default", "", nil, nil)
//
//	// Production usage (full config)
//	manager, err := NewTargetDiscoveryManager(
//	    k8sClient,
//	    os.Getenv("K8S_NAMESPACE"),        // from env
//	    "publishing-target=true,env=prod", // multi-label selector
//	    slog.Default(),
//	    v2.NewRegistry(),
//	)
func NewTargetDiscoveryManager(
	k8sClient k8s.K8sClient,
	namespace string,
	labelSelector string,
	logger *slog.Logger,
	metricsRegistry *v2.Registry,
) (TargetDiscoveryManager, error) {
	// Validate required parameters
	if k8sClient == nil {
		return nil, fmt.Errorf("k8sClient is required (cannot be nil)")
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required (cannot be empty)")
	}

	// Apply defaults
	if labelSelector == "" {
		labelSelector = "publishing-target=true" // default
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Initialize metrics (optional)
	var discoveryMetrics *DiscoveryMetrics
	if metricsRegistry != nil {
		discoveryMetrics = registerDiscoveryMetrics(metricsRegistry)
	}

	manager := &DefaultTargetDiscoveryManager{
		k8sClient:     k8sClient,
		namespace:     namespace,
		labelSelector: labelSelector,
		cache:         newTargetCache(),
		configCache:   newTargetCache(),
		logger:        logger,
		metrics:       discoveryMetrics,
	}

	logger.Info("Target discovery manager initialized",
		"namespace", namespace,
		"label_selector", labelSelector,
		"metrics_enabled", discoveryMetrics != nil,
	)

	return manager, nil
}

// NewConfigOnlyTargetDiscoveryManager creates a discovery manager with NO
// Kubernetes access (FU-RECEIVERS-INTEGRATION slice 1, item 3).
//
// Targets come exclusively from SetConfigTargets, i.e. from the config's
// `receivers:` section. DiscoverTargets becomes a no-op success and Health
// always reports healthy, because there is no cluster to talk to and nothing
// to poll — the config targets are rebuilt by the caller on config reload
// instead.
//
// This is what lets a lite-profile deployment (no K8s client, no Secrets)
// deliver notifications from an untouched upstream Alertmanager config.
func NewConfigOnlyTargetDiscoveryManager(
	logger *slog.Logger,
	metricsRegistry *v2.Registry,
) *DefaultTargetDiscoveryManager {
	if logger == nil {
		logger = slog.Default()
	}

	var discoveryMetrics *DiscoveryMetrics
	if metricsRegistry != nil {
		discoveryMetrics = registerDiscoveryMetrics(metricsRegistry)
	}

	logger.Info("Target discovery manager initialized in config-only mode (no Kubernetes access)")

	return &DefaultTargetDiscoveryManager{
		cache:       newTargetCache(),
		configCache: newTargetCache(),
		logger:      logger,
		metrics:     discoveryMetrics,
	}
}

// IsConfigOnly reports whether this manager runs without Kubernetes access.
func (m *DefaultTargetDiscoveryManager) IsConfigOnly() bool {
	return m.k8sClient == nil
}

// SetConfigTargets atomically replaces the config-provisioned target set
// (implements ConfigTargetSink; FU-RECEIVERS-INTEGRATION R3).
//
// Called once at startup and again on every config reload that changed the
// routing fingerprint. The swap is a single targetCache.Set, so readers see
// either the whole old set or the whole new one — never an empty intermediate
// state, which would silently drop notifications for the duration of a reload.
//
// Passing nil/empty clears the set (all receivers' integrations removed from
// the config).
func (m *DefaultTargetDiscoveryManager) SetConfigTargets(targets []*core.PublishingTarget) {
	if m.configCache == nil {
		m.configCache = newTargetCache()
	}
	m.configCache.Set(targets)

	// Count comes from the CACHE, not from len(targets) (slice-1 review finding
	// I3): the cache is keyed by name, so if two targets ever shared a name the
	// stat would over-report what is actually reachable. BuildConfigTargets
	// drops duplicates itself, and the parser rejects the duplicate receiver
	// names that could cause them — reading the cache keeps the number honest
	// regardless of who calls this.
	stored := m.configCache.Len()

	m.mu.Lock()
	m.stats.ConfigTargets = stored
	m.mu.Unlock()

	if m.metrics != nil {
		m.updateTargetsGauge(m.ListTargets())
	}

	// Names only — they are built from receiver names and integration kinds,
	// so unlike the targets' URLs/headers they carry no credentials.
	m.logger.Info("Config-provisioned publishing targets updated",
		"count", len(targets),
		"names", configTargetNames(targets),
	)
}

// configTargets / configTargetsByType / configTarget are the read accessors for
// the config-provisioned set. They tolerate a nil configCache so a manager
// built by struct literal (as some tests do) keeps working with no config
// targets rather than panicking on the union read paths.
func (m *DefaultTargetDiscoveryManager) configTargets() []*core.PublishingTarget {
	if m.configCache == nil {
		return nil
	}
	return m.configCache.List()
}

func (m *DefaultTargetDiscoveryManager) configTargetsByType(targetType string) []*core.PublishingTarget {
	if m.configCache == nil {
		return nil
	}
	return m.configCache.GetByType(targetType)
}

func (m *DefaultTargetDiscoveryManager) configTarget(name string) *core.PublishingTarget {
	if m.configCache == nil {
		return nil
	}
	return m.configCache.Get(name)
}

// configTargetNames renders the target names for the log line above.
func configTargetNames(targets []*core.PublishingTarget) []string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		if target != nil {
			names = append(names, target.Name)
		}
	}
	return names
}

// DiscoverTargets lists K8s secrets and refreshes in-memory cache.
func (m *DefaultTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	startTime := time.Now()

	// Config-only mode: no cluster to list, and NOTHING to clear — the
	// config-provisioned set is owned by SetConfigTargets. Returning early
	// (rather than falling through to a nil-client call) also keeps the
	// periodic RefreshManager and the manual refresh endpoint harmless.
	if m.k8sClient == nil {
		m.mu.Lock()
		m.stats.LastDiscovery = time.Now()
		m.mu.Unlock()
		m.logger.Debug("Target discovery skipped (config-only mode, no Kubernetes client)")
		return nil
	}

	m.logger.Info("Starting target discovery",
		"namespace", m.namespace,
		"label_selector", m.labelSelector,
	)

	// List secrets from K8s API
	secrets, err := m.k8sClient.ListSecrets(ctx, m.namespace, m.labelSelector)
	if err != nil {
		// K8s API unavailable - keep old cache (graceful degradation)
		m.logger.Error("Failed to list K8s secrets",
			"namespace", m.namespace,
			"error", err,
		)

		// Update error statistics
		m.mu.Lock()
		m.stats.DiscoveryErrors++
		m.mu.Unlock()

		// Record error metric
		if m.metrics != nil {
			m.metrics.ErrorsTotal.WithLabelValues("k8s_api").Inc()
		}

		return NewDiscoveryFailedError(m.namespace, err)
	}

	m.logger.Debug("K8s secrets listed",
		"count", len(secrets),
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	// Parse and validate secrets
	validTargets, invalidCount := m.parseAndValidateSecrets(secrets)

	// Update cache atomically
	m.cache.Set(validTargets)

	// Update statistics
	m.mu.Lock()
	m.stats.TotalTargets = len(secrets)
	m.stats.ValidTargets = len(validTargets)
	m.stats.InvalidTargets = invalidCount
	m.stats.LastDiscovery = time.Now()
	m.mu.Unlock()

	// Record metrics
	if m.metrics != nil {
		// Update targets gauge (by type, enabled and source). The gauge covers
		// the WHOLE view, so the config-provisioned targets must be included
		// here too — otherwise every K8s poll would zero their series.
		m.updateTargetsGauge(m.ListTargets())

		// Record duration
		m.metrics.DurationSeconds.WithLabelValues("discover").Observe(time.Since(startTime).Seconds())

		// Record success timestamp
		m.metrics.LastSuccessTimestamp.Set(float64(time.Now().Unix()))

		// Record secret counts
		m.metrics.SecretsTotal.WithLabelValues("valid").Add(float64(len(validTargets)))
		m.metrics.SecretsTotal.WithLabelValues("invalid").Add(float64(invalidCount))
	}

	m.logger.Info("Target discovery complete",
		"duration_ms", time.Since(startTime).Milliseconds(),
		"total_secrets", len(secrets),
		"valid_targets", len(validTargets),
		"invalid_targets", invalidCount,
	)

	return nil
}

// parseAndValidateSecrets parses and validates secrets, returns valid targets + invalid count.
func (m *DefaultTargetDiscoveryManager) parseAndValidateSecrets(secrets []corev1.Secret) ([]*core.PublishingTarget, int) {
	var validTargets []*core.PublishingTarget
	invalidCount := 0

	for _, secret := range secrets {
		// Parse secret
		target, err := parseSecret(secret)
		if err != nil {
			m.logger.Warn("Skipping secret with parse error",
				"secret_name", secret.Name,
				"error", err,
			)
			invalidCount++
			if m.metrics != nil {
				m.metrics.ErrorsTotal.WithLabelValues("parse").Inc()
			}
			continue
		}

		// Validate target
		validationErrs := validateTarget(target)
		if len(validationErrs) > 0 {
			m.logger.Warn("Skipping secret with validation errors",
				"secret_name", secret.Name,
				"target_name", target.Name,
				"validation_errors", len(validationErrs),
			)
			for _, valErr := range validationErrs {
				// Field + message only. valErr.Value used to be logged too
				// (final review finding 18), but these targets come from
				// Kubernetes Secrets, so the offending value is routinely a
				// credential — a malformed webhook URL, an API key of the wrong
				// length. Debug level is not a safety boundary: it is enabled in
				// staging by default and the line lands in whatever log sink the
				// cluster ships to.
				m.logger.Debug("Validation error detail",
					"field", valErr.Field,
					"message", valErr.Message,
				)
			}
			invalidCount++
			if m.metrics != nil {
				m.metrics.ErrorsTotal.WithLabelValues("validate").Inc()
			}
			continue
		}

		// Valid target - add to list
		validTargets = append(validTargets, target)

		m.logger.Debug("Parsed valid target",
			"target_name", target.Name,
			"type", target.Type,
			"url", target.URL,
			"enabled", target.Enabled,
		)
	}

	return validTargets, invalidCount
}

// gaugeTargetTypes is the label-value space reset before each gauge update.
// telegram/email joined the list with FU-RECEIVERS-INTEGRATION: config
// provisioning is the first code path that can actually produce targets of
// those two types (discovery_validate.isValidTargetType still rejects them for
// K8s Secrets), so without them here a removed telegram receiver would leave a
// stale non-zero series behind.
var gaugeTargetTypes = []string{"rootly", "pagerduty", "slack", "webhook", "telegram", "email", "alertmanager"}

// updateTargetsGauge updates the Prometheus gauge with target counts by type,
// enabled status and SOURCE ("config" vs "k8s" — FU-RECEIVERS-INTEGRATION
// slice 1, item 2), for the union of both sources.
func (m *DefaultTargetDiscoveryManager) updateTargetsGauge(targets []*core.PublishingTarget) {
	// Reset all gauges (to handle deleted targets)
	for _, targetType := range gaugeTargetTypes {
		for _, enabled := range []string{"true", "false"} {
			for _, source := range []string{TargetSourceK8s, TargetSourceConfig} {
				m.metrics.TargetsTotal.WithLabelValues(targetType, enabled, source).Set(0)
			}
		}
	}

	// Count targets by (type, enabled, source)
	type gaugeKey struct {
		targetType string
		enabled    string
		source     string
	}
	counts := make(map[gaugeKey]int, len(targets))
	for _, target := range targets {
		enabledStr := "false"
		if target.Enabled {
			enabledStr = "true"
		}
		counts[gaugeKey{target.Type, enabledStr, TargetSource(target)}]++
	}

	// Update gauges
	for key, count := range counts {
		m.metrics.TargetsTotal.WithLabelValues(key.targetType, key.enabled, key.source).Set(float64(count))
	}
}

// GetTarget returns target by name (O(1) lookup) from the UNION of both
// sources (R3). The two name spaces are disjoint by construction — config
// targets are prefixed "cfg:", which validateTarget rejects for K8s Secrets —
// so the lookup order carries no precedence semantics.
func (m *DefaultTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	target := m.cache.Get(name)
	if target == nil {
		target = m.configTarget(name)
	}

	// Record lookup metric
	if m.metrics != nil {
		status := "hit"
		if target == nil {
			status = "miss"
		}
		m.metrics.LookupsTotal.WithLabelValues("get", status).Inc()
	}

	if target == nil {
		m.logger.Debug("Target not found in cache", "name", name)
		return nil, NewTargetNotFoundError(name)
	}

	m.logger.Debug("Target found in cache", "name", name, "type", target.Type)
	return target, nil
}

// ListTargets returns all active targets from BOTH sources (R3): the K8s
// Secret-sourced ones first (unchanged order semantics for existing callers),
// then the config-provisioned ones.
func (m *DefaultTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	targets := append(m.cache.List(), m.configTargets()...)

	// Record lookup metric
	if m.metrics != nil {
		m.metrics.LookupsTotal.WithLabelValues("list", "hit").Inc()
	}

	m.logger.Debug("Listed targets", "count", len(targets))
	return targets
}

// GetTargetsByType filters the union of both sources by type (R3).
func (m *DefaultTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	targets := append(m.cache.GetByType(targetType), m.configTargetsByType(targetType)...)

	// Record lookup metric
	if m.metrics != nil {
		m.metrics.LookupsTotal.WithLabelValues("get_by_type", "hit").Inc()
	}

	m.logger.Debug("Filtered targets by type",
		"type", targetType,
		"count", len(targets),
	)
	return targets
}

// GetStats returns discovery statistics.
func (m *DefaultTargetDiscoveryManager) GetStats() DiscoveryStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return copy (thread-safe)
	return m.stats
}

// Health checks target discovery manager + K8s client health.
func (m *DefaultTargetDiscoveryManager) Health(ctx context.Context) error {
	// Config-only mode: nothing external to be unhealthy about. The targets
	// are in memory and their own reachability is the health monitor's job.
	if m.k8sClient == nil {
		return nil
	}

	// Check K8s client health
	if err := m.k8sClient.Health(ctx); err != nil {
		m.logger.Warn("K8s client unhealthy", "error", err)
		return fmt.Errorf("K8s client unhealthy: %w", err)
	}

	m.logger.Debug("Target discovery manager healthy")
	return nil
}

// registerDiscoveryMetrics registers Prometheus metrics for target discovery.
func registerDiscoveryMetrics(reg *v2.Registry) *DiscoveryMetrics {
	return &DiscoveryMetrics{
		TargetsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing_discovery",
				Name:      "targets_total",
				Help:      "Total discovered targets by type, enabled status and source (k8s/config)",
			},
			[]string{"type", "enabled", "source"},
		),
		DurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing_discovery",
				Name:      "duration_seconds",
				Help:      "Target discovery operation duration in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0},
			},
			[]string{"operation"},
		),
		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing_discovery",
				Name:      "errors_total",
				Help:      "Total discovery errors by type",
			},
			[]string{"error_type"},
		),
		SecretsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing_discovery",
				Name:      "secrets_total",
				Help:      "Total processed secrets by status",
			},
			[]string{"status"},
		),
		LookupsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "target_lookups_total",
				Help:      "Total target cache lookups by operation and status",
			},
			[]string{"operation", "status"},
		),
		LastSuccessTimestamp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing_discovery",
				Name:      "last_success_timestamp",
				Help:      "Unix timestamp of last successful target discovery",
			},
		),
	}
}
