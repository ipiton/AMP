package services

import (
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // FilterMetrics has no pkg/metrics/v2 equivalent yet; migration tracked separately
)

// DefaultDedupWindow is the default window for Rule 7 (duplicate suppression).
// It is far below any sane Alertmanager repeat_interval, so legitimate repeat
// notifications are never suppressed — only redundant re-deliveries are.
const DefaultDedupWindow = time.Minute

// SimpleFilterEngine is a basic implementation of FilterEngine
type SimpleFilterEngine struct {
	logger  *slog.Logger
	metrics *metrics.FilterMetrics

	// Deduplication state (Rule 7): fingerprint -> last status and when it
	// was seen. Guarded by dedupMu; entries older than dedupWindow are swept
	// lazily.
	dedupWindow time.Duration
	dedupMu     sync.Mutex
	dedupSeen   map[string]dedupEntry
	dedupSweep  time.Time
}

// dedupEntry is the last status Rule 7 let through for a fingerprint.
type dedupEntry struct {
	status core.AlertStatus
	seenAt time.Time
}

// NewSimpleFilterEngine creates a new simple filter engine
func NewSimpleFilterEngine(logger *slog.Logger) *SimpleFilterEngine {
	return NewSimpleFilterEngineWithMetrics(logger, metrics.NewFilterMetrics())
}

// NewSimpleFilterEngineWithMetrics creates a new simple filter engine with custom metrics
// If filterMetrics is nil, metrics collection will be disabled
func NewSimpleFilterEngineWithMetrics(logger *slog.Logger, filterMetrics *metrics.FilterMetrics) *SimpleFilterEngine {
	if logger == nil {
		logger = slog.Default()
	}
	// Don't create metrics if nil is passed - allow disabling metrics
	return &SimpleFilterEngine{
		logger:      logger,
		metrics:     filterMetrics,
		dedupWindow: DefaultDedupWindow,
		dedupSeen:   make(map[string]dedupEntry),
		dedupSweep:  time.Now(),
	}
}

// SetDedupWindow overrides the duplicate-suppression window (Rule 7).
// A non-positive window disables deduplication.
func (f *SimpleFilterEngine) SetDedupWindow(window time.Duration) {
	f.dedupMu.Lock()
	defer f.dedupMu.Unlock()
	f.dedupWindow = window
}

// ShouldBlock determines if an alert should be blocked. Rules, in order:
// LLM severity "noise" and confidence < 0.3 (only with a classification),
// empty alert name, resolved alerts older than 24h, and a repeat of the
// fingerprint's last status within the dedup window. Every block is
// counted in FilterMetrics with its reason.
//
// There are deliberately no rules on alert content (name, environment,
// namespace): upstream Alertmanager drops none, and a route to a receiver
// without integrations or a silence does the same job in config
// (PROD-HARDCODED-FILTER).
func (f *SimpleFilterEngine) ShouldBlock(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
	start := time.Now()

	blocked, reason := f.shouldBlockInternal(alert, classification)

	// Record metrics (only if metrics are enabled)
	if f.metrics != nil {
		duration := time.Since(start).Seconds()
		result := "allowed"
		if blocked {
			result = "blocked"
			f.metrics.RecordBlockedAlert(reason)
		}
		f.metrics.RecordAlertFiltered(result)
		f.metrics.RecordFilterDuration(duration)
	}

	return blocked, reason
}

// shouldBlockInternal contains the actual filtering logic
func (f *SimpleFilterEngine) shouldBlockInternal(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
	// Rules 1 (test alerts) and 4 (namespaces) were removed by
	// PROD-HARDCODED-FILTER; the numbering is kept because "Rule 7" is
	// referenced from tests and planning docs.

	// Rule 2: Block noise alerts (if we have classification)
	if classification != nil && classification.Severity == core.SeverityNoise {
		return true, "noise"
	}

	// Rule 3: Block alerts with very low confidence (< 0.3)
	if classification != nil && classification.Confidence < 0.3 {
		return true, "low_confidence"
	}

	// Rule 5: Block alerts with empty alert name
	if alert.AlertName == "" {
		return true, "empty_alert_name"
	}

	// Rule 6: Block resolved alerts older than 24 hours (cleanup)
	if isOldResolvedAlert(alert) {
		return true, "old_resolved"
	}

	// Rule 7: Block duplicate fingerprints within short time window
	if f.isDuplicate(alert) {
		return true, "duplicate"
	}

	// Default: allow
	return false, ""
}

// isDuplicate implements Rule 7: a repeat of the fingerprint's last status
// within dedupWindow is a redundant re-delivery and gets blocked. It is the
// only notification dedup on the direct-publish path (no route: tree), where
// an HA Prometheus pair would otherwise notify twice.
//
// The key is the fingerprint alone, so a status change always passes and
// becomes the new last status: firing -> resolved -> firing within the window
// delivers all three. Keying on fingerprint+status used to block the second
// firing against the first, leaving the receiver on "resolved" while the
// alert was firing. A blocked repeat does not refresh seenAt.
func (f *SimpleFilterEngine) isDuplicate(alert *core.Alert) bool {
	if alert.Fingerprint == "" {
		return false
	}

	f.dedupMu.Lock()
	defer f.dedupMu.Unlock()

	if f.dedupWindow <= 0 {
		return false
	}

	now := time.Now()

	// Lazy sweep: at most once per window, drop expired entries so the map
	// stays bounded by the number of distinct alerts per window.
	if now.Sub(f.dedupSweep) >= f.dedupWindow {
		for fp, entry := range f.dedupSeen {
			if now.Sub(entry.seenAt) >= f.dedupWindow {
				delete(f.dedupSeen, fp)
			}
		}
		f.dedupSweep = now
	}

	if last, ok := f.dedupSeen[alert.Fingerprint]; ok &&
		last.status == alert.Status && now.Sub(last.seenAt) < f.dedupWindow {
		return true
	}

	f.dedupSeen[alert.Fingerprint] = dedupEntry{status: alert.Status, seenAt: now}
	return false
}

// isOldResolvedAlert checks if alert is resolved and older than 24 hours
func isOldResolvedAlert(alert *core.Alert) bool {
	if alert.Status != core.StatusResolved {
		return false
	}

	// If ends_at is set, check if it's older than 24 hours
	if alert.EndsAt != nil {
		age := time.Since(*alert.EndsAt)
		return age > 24*time.Hour
	}

	// If ends_at is not set but status is resolved, check starts_at
	age := time.Since(alert.StartsAt)
	return age > 48*time.Hour // More lenient for missing ends_at
}
