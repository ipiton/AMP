package investigation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds Prometheus metrics for the investigation pipeline.
type Metrics struct {
	QueueDepth          prometheus.Gauge
	InvestigationsTotal *prometheus.CounterVec
	DroppedTotal        prometheus.Counter
	DeduplicatedTotal   *prometheus.CounterVec
	ProcessingTime      prometheus.Histogram
}

// NewMetrics registers and returns investigation metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	factory := promauto.With(reg)

	return &Metrics{
		QueueDepth: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: "amp",
			Name:      "investigation_queue_depth",
			Help:      "Current number of investigations waiting in queue",
		}),
		InvestigationsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "amp",
			Name:      "investigations_total",
			Help:      "Total investigations processed, by status",
		}, []string{"status"}),
		DroppedTotal: factory.NewCounter(prometheus.CounterOpts{
			Namespace: "amp",
			Name:      "investigations_dropped_total",
			Help:      "Total investigations dropped: queue full or the record could not be created",
		}),
		DeduplicatedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "amp",
			Name:      "investigations_deduplicated_total",
			Help:      "Investigations skipped because the incident (fingerprint + startsAt) was already claimed, by level (local: this replica, shared: Redis)",
		}, []string{"level"}),
		ProcessingTime: factory.NewHistogram(prometheus.HistogramOpts{
			Namespace: "amp",
			Name:      "investigation_processing_seconds",
			Help:      "Duration of investigation processing",
			Buckets:   []float64{.1, .5, 1, 2.5, 5, 10, 30, 60},
		}),
	}
}
