package publishing

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// slack_metrics.go - Prometheus metrics for Slack publisher
// 8 metrics: messages_posted, thread_replies, errors, duration, cache_hits/misses, cache_size, rate_limit_hits

// SlackMetrics holds Prometheus metrics for Slack publisher
type SlackMetrics struct {
	// MessagesPosted tracks total messages posted (by status: success/error)
	MessagesPosted *prometheus.CounterVec

	// ThreadReplies tracks total thread replies
	ThreadReplies prometheus.Counter

	// MessageErrors tracks message publishing errors (by error_type)
	MessageErrors *prometheus.CounterVec

	// APIDuration tracks Slack API request duration in seconds (by method, status)
	APIDuration *prometheus.HistogramVec

	// CacheHits tracks cache hits (found existing message for threading)
	CacheHits prometheus.Counter

	// CacheMisses tracks cache misses (no existing message)
	CacheMisses prometheus.Counter

	// CacheSize tracks current cache size (number of entries)
	CacheSize prometheus.Gauge

	// RateLimitHits tracks rate limit hits (429 errors)
	RateLimitHits prometheus.Counter
}

var (
	slackMetricsInstance *SlackMetrics
	slackMetricsOnce     sync.Once
)

// NewSlackMetrics creates a new SlackMetrics instance
// Registers all metrics with Prometheus registry (promauto)
// Uses sync.Once to prevent duplicate registration
func NewSlackMetrics() *SlackMetrics {
	slackMetricsOnce.Do(func() {
		slackMetricsInstance = &SlackMetrics{
			MessagesPosted: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_messages_posted_total",
				Help:      "Total number of Slack messages posted (by status)",
			},
			[]string{"status"}, // success/error
		),

		ThreadReplies: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_thread_replies_total",
				Help:      "Total number of Slack thread replies posted",
			},
		),

		MessageErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_message_errors_total",
				Help:      "Total number of Slack message publishing errors (by error_type)",
			},
			[]string{"error_type"}, // rate_limit/auth_error/bad_request/server_error/network_error/format_error
		),

		APIDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_api_request_duration_seconds",
				Help:      "Slack API request duration in seconds (by method, status)",
				Buckets:   prometheus.DefBuckets, // [.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10]
			},
			[]string{"method", "status"}, // post_message/thread_reply, success/error
		),

		CacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_cache_hits_total",
				Help:      "Total number of cache hits (found existing message for threading)",
			},
		),

		CacheMisses: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_cache_misses_total",
				Help:      "Total number of cache misses (no existing message)",
			},
		),

		CacheSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "slack_cache_size",
				Help:      "Current size of message ID cache (number of entries)",
			},
		),

			RateLimitHits: promauto.NewCounter(
				prometheus.CounterOpts{
					Namespace: "alert_history",
					Subsystem: "publishing",
					Name:      "slack_rate_limit_hits_total",
					Help:      "Total number of Slack rate limit hits (429 errors)",
				},
			),
		}
	})
	return slackMetricsInstance
}

// RecordCacheSize updates cache size gauge
// Should be called periodically (e.g., in cleanup worker)
func (m *SlackMetrics) RecordCacheSize(size int) {
	m.CacheSize.Set(float64(size))
}
