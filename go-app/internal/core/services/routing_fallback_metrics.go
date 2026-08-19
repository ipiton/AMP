package services

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Outcomes recorded by RoutingFallbackMetrics.RecordUnavailable.
const (
	// RoutingFallbackDefaultReceiver: a route tree is configured but produced
	// no decision for this alert, and the tree's root receiver was used
	// instead.
	RoutingFallbackDefaultReceiver = "default_receiver"

	// RoutingFallbackFailed: same situation, but no root receiver was
	// resolvable, so the alert failed instead of being published unscoped.
	RoutingFallbackFailed = "failed"
)

// RoutingFallbackMetrics counts the "route tree configured but no decision"
// situations that FU-RECEIVERS-INTEGRATION's slice-1 re-review (finding R1)
// made load-bearing.
//
// WHY THIS EXISTS: an alert with no routing decision used to publish with
// receiver "" , which PublishingCoordinator.targetMatchesReceiver reads as
// "every enabled target". With config-provisioned targets that is a silent
// cross-receiver fan-out, and it is reachable while the process looks healthy:
// initializeRouting's failure is non-fatal (startup continues, targets are still
// provisioned), and Evaluate can fail per alert. Neither had any signal at all —
// hence a counter, not just a log line.
//
// Metrics are prefixed "alert_history_routing_".
type RoutingFallbackMetrics struct {
	// UnavailableTotal counts alerts whose routing decision was unavailable
	// despite a configured route tree, by outcome.
	// Labels: outcome (default_receiver/failed)
	UnavailableTotal *prometheus.CounterVec
}

// NewRoutingFallbackMetrics registers the metrics against the default
// Prometheus registerer.
//
// Call AT MOST ONCE per process (promauto panics on double registration) —
// application wiring goes through a sync.OnceValue wrapper, mirroring the
// routing matcher/evaluator metrics.
func NewRoutingFallbackMetrics() *RoutingFallbackMetrics {
	return NewRoutingFallbackMetricsWithRegisterer(prometheus.DefaultRegisterer)
}

// NewRoutingFallbackMetricsWithRegisterer registers against a caller-provided
// registerer; tests pass a fresh prometheus.NewRegistry() for isolation.
func NewRoutingFallbackMetricsWithRegisterer(reg prometheus.Registerer) *RoutingFallbackMetrics {
	factory := promauto.With(reg)
	return &RoutingFallbackMetrics{
		UnavailableTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "decision_unavailable_total",
				Help:      "Alerts with a configured route tree but no routing decision, by fallback outcome",
			},
			[]string{"outcome"},
		),
	}
}

// RecordUnavailable counts one such alert. Nil-safe: metrics are optional.
func (m *RoutingFallbackMetrics) RecordUnavailable(outcome string) {
	if m == nil || m.UnavailableTotal == nil {
		return
	}
	m.UnavailableTotal.WithLabelValues(outcome).Inc()
}
