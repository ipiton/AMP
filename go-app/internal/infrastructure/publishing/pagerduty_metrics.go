package publishing

import (
	"github.com/prometheus/client_golang/prometheus"
)

// PagerDuty Prometheus Metrics
// Full implementation will be done in Phase 8

// PagerDutyMetrics holds all PagerDuty-specific Prometheus metrics
type PagerDutyMetrics struct {
	// EventsTriggered tracks the total number of PagerDuty events triggered
	EventsTriggered *prometheus.CounterVec

	// EventsAcknowledged tracks the total number of PagerDuty events acknowledged
	EventsAcknowledged *prometheus.CounterVec

	// EventsResolved tracks the total number of PagerDuty events resolved
	EventsResolved *prometheus.CounterVec

	// ChangeEvents tracks the total number of PagerDuty change events sent
	ChangeEvents *prometheus.CounterVec

	// APIRequests tracks the total number of API requests
	APIRequests *prometheus.CounterVec

	// APIErrors tracks the total number of API errors
	APIErrors *prometheus.CounterVec

	// APIDuration tracks the duration of API requests
	APIDuration *prometheus.HistogramVec

	// RateLimitHits tracks the number of rate limit hits
	RateLimitHits prometheus.Counter
}

// NewPagerDutyMetrics creates a new PagerDutyMetrics instance
// This is a temporary stub for Phase 4
// Full implementation will be done in Phase 8
func NewPagerDutyMetrics() *PagerDutyMetrics {
	return &PagerDutyMetrics{
		EventsTriggered: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pagerduty_events_triggered_total",
				Help: "Total number of PagerDuty events triggered",
			},
			[]string{"routing_key", "severity"},
		),
		EventsAcknowledged: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pagerduty_events_acknowledged_total",
				Help: "Total number of PagerDuty events acknowledged",
			},
			[]string{"routing_key"},
		),
		EventsResolved: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pagerduty_events_resolved_total",
				Help: "Total number of PagerDuty events resolved",
			},
			[]string{"routing_key"},
		),
		ChangeEvents: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pagerduty_change_events_total",
				Help: "Total number of PagerDuty change events sent",
			},
			[]string{"routing_key"},
		),
		APIRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pagerduty_api_requests_total",
				Help: "Total number of PagerDuty API requests",
			},
			[]string{"endpoint", "status"},
		),
		APIErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pagerduty_api_errors_total",
				Help: "Total number of PagerDuty API errors",
			},
			[]string{"error_type"},
		),
		APIDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "pagerduty_api_duration_seconds",
				Help:    "Duration of PagerDuty API requests in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint"},
		),
		RateLimitHits: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "pagerduty_rate_limit_hits_total",
				Help: "Total number of PagerDuty rate limit hits",
			},
		),
	}
}
