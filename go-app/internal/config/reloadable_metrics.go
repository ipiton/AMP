package config

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ================================================================================
// MetricsReloadable (INF-A slice 1)
// ================================================================================

// metricsReloadPriority: right after the logger. Flipping an atomic bool is
// the cheapest reload in the registry and cannot fail, so doing it early keeps
// the expensive components last.
const metricsReloadPriority = 20

// MetricsReloadable hot-reloads the metrics exposition toggle.
//
// What is real: `metrics.enabled` opens/closes the /metrics endpoint through a
// metricsv2.ExpositionGate, atomically, immediately.
//
// What is NOT real, and says so (W603): `metrics.path` and `metrics.port`.
// AMP serves Prometheus metrics at a FIXED /metrics on the main HTTP server
// (internal/application/router.go); neither field is read anywhere in the
// process, so honouring them is a feature that does not exist rather than a
// value awaiting a restart. The warning says exactly that instead of implying
// a restart would help — and it is re-raised on every reload attempt while the
// config keeps asking for them (fix-round I2).
//
// Collection itself is not toggleable: the collectors are promauto-registered
// at package init and increment unconditionally. See ExpositionGate's doc for
// why exposition is the honest granularity, and note the consequence — closing
// the gate does not create a counter gap, it hides one.
type MetricsReloadable struct {
	gate     *metricsv2.ExpositionGate
	logger   *slog.Logger
	warnings *RestartWarnings

	// mu guards applied — the metrics config actually in effect. Enabled moves
	// when the gate is flipped; path/port never move at all, which is why
	// comparing against this keeps W603 alive for as long as the config asks
	// for something the process cannot do.
	mu      sync.Mutex
	applied MetricsConfig
}

// NewMetricsReloadable wires a MetricsReloadable over the process's exposition
// gate. A nil gate is legal (a process that mounted /metrics unguarded); in
// that case a metrics.enabled change is reported as restart-required.
//
// bootCfg is the config the process started with — what the gate was built
// from.
func NewMetricsReloadable(
	gate *metricsv2.ExpositionGate,
	bootCfg *Config,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *MetricsReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	reloadable := &MetricsReloadable{gate: gate, logger: logger, warnings: warnings}
	if bootCfg != nil {
		reloadable.applied = bootCfg.Metrics
	}
	return reloadable
}

// Name implements Reloadable.
func (m *MetricsReloadable) Name() string { return "metrics" }

// RelevantSections implements Reloadable.
func (m *MetricsReloadable) RelevantSections() []string { return []string{"metrics"} }

// IsCritical implements Reloadable: losing metrics is a degradation.
func (m *MetricsReloadable) IsCritical() bool { return false }

// ReloadPriority implements OrderedReloadable.
func (m *MetricsReloadable) ReloadPriority() int { return metricsReloadPriority }

// NeedsResync implements ResyncReloadable: true while the requested metrics
// config differs from what is in effect.
func (m *MetricsReloadable) NeedsResync(newCfg *Config) bool {
	if newCfg == nil {
		return false
	}
	return len(m.drift(newCfg.Metrics)) > 0
}

// drift returns the field paths where the requested config differs from what
// is in effect.
func (m *MetricsReloadable) drift(requested MetricsConfig) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return changedFields("metrics", m.applied, requested)
}

// Reload implements Reloadable.
func (m *MetricsReloadable) Reload(_ context.Context, _, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("metrics reload: nil config")
	}

	fields := m.drift(newCfg.Metrics)
	if len(fields) == 0 {
		m.warnings.Resolve(WarnMetricsRestartRequired, m.Name())
		return nil
	}

	// Apply the half that CAN be applied.
	if m.gate != nil && contains(fields, "metrics.enabled") {
		m.gate.SetEnabled(newCfg.Metrics.Enabled)

		m.mu.Lock()
		m.applied.Enabled = newCfg.Metrics.Enabled
		m.mu.Unlock()

		m.logger.Info("metrics exposition toggled from config",
			"enabled", newCfg.Metrics.Enabled,
			"note", "collection is unaffected; only the /metrics endpoint is gated",
		)
	}

	remaining := m.drift(newCfg.Metrics)
	if len(remaining) == 0 {
		m.warnings.Resolve(WarnMetricsRestartRequired, m.Name())
		return nil
	}

	// ONE warning per component per attempt — see LoggerReloadable.Reload.
	reason := "AMP always serves Prometheus metrics at /metrics on the main HTTP server port; metrics.path and metrics.port are not read by the process, so a restart will not apply them either"
	if contains(remaining, "metrics.enabled") {
		reason = "this process mounted /metrics without an exposition gate; restart to apply metrics.enabled. " + reason
	}

	warnRestartRequired(m.logger, m.warnings, RestartRequiredWarning{
		Code:      WarnMetricsRestartRequired,
		Component: m.Name(),
		Fields:    remaining,
		Reason:    reason,
	})
	return nil
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*MetricsReloadable)(nil)
	_ OrderedReloadable = (*MetricsReloadable)(nil)
	_ ResyncReloadable  = (*MetricsReloadable)(nil)
)
