package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Reload outcome labels for reloadsTotal.
const (
	resultSuccess    = "success"    // triggered, confirmed, application healthy
	resultRejected   = "rejected"   // triggered and confirmed, but the app rejected the config
	resultFailed     = "failed"     // the trigger itself failed to deliver
	resultUnverified = "unverified" // triggered, but the app never confirmed it
)

// reloaderMetrics is the sidecar's Prometheus surface, served on --metrics-addr
// (default :9091).
type reloaderMetrics struct {
	reloadsTotal   *prometheus.CounterVec
	lastReloadTime prometheus.Gauge
	lastSuccessful prometheus.Gauge
	configHash     prometheus.Gauge
	configInfo     *prometheus.GaugeVec
	watchErrors    prometheus.Counter
}

func newReloaderMetrics(registry prometheus.Registerer) *reloaderMetrics {
	metrics := &reloaderMetrics{
		reloadsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "amp_config_reloader_reloads_total",
			Help: "Reload attempts triggered by this sidecar, by outcome " +
				"(success, rejected: the app refused the config, " +
				"failed: the trigger did not deliver, " +
				"unverified: the app never confirmed the attempt).",
		}, []string{"result"}),

		lastReloadTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "amp_config_reloader_last_reload_timestamp_seconds",
			Help: "Unix timestamp of the last reload this sidecar triggered, whatever its outcome.",
		}),

		lastSuccessful: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "amp_config_reloader_last_reload_successful",
			Help: "1 if the last triggered reload was confirmed healthy by the application, 0 otherwise. " +
				"This is the alertable series: a stuck ConfigMap edit sits at 0.",
		}),

		configHash: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "amp_config_reloader_config_hash",
			Help: "Leading 52 bits of the SHA256 of the watched config file, as a float. " +
				"Use it to SEE a change; the readable value is the hash label on " +
				"amp_config_reloader_config_info. Never compare for equality.",
		}),

		configInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "amp_config_reloader_config_info",
			Help: "Always 1, labelled with the short SHA256 of the watched config file. " +
				"Reset on every change, so exactly one series exists at a time.",
		}, []string{"hash"}),

		watchErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "amp_config_reloader_watch_errors_total",
			Help: "Failures to read or hash the watched config file (missing mount, permissions).",
		}),
	}

	registry.MustRegister(
		metrics.reloadsTotal,
		metrics.lastReloadTime,
		metrics.lastSuccessful,
		metrics.configHash,
		metrics.configInfo,
		metrics.watchErrors,
	)

	// Initialise every result series so a dashboard shows 0 instead of "no
	// data" before the first config change.
	for _, result := range []string{resultSuccess, resultRejected, resultFailed, resultUnverified} {
		metrics.reloadsTotal.WithLabelValues(result)
	}

	return metrics
}

// observeConfig records the currently observed config hash.
func (m *reloaderMetrics) observeConfig(hash string) {
	m.configHash.Set(hashToFloat(hash))

	// Reset first: the label value changes on every config edit, so without
	// this the sidecar would accumulate one stale series per historical hash.
	m.configInfo.Reset()
	m.configInfo.WithLabelValues(shortHash(hash)).Set(1)
}

// observeReload records the outcome of one triggered reload.
func (m *reloaderMetrics) observeReload(result string, at float64) {
	m.reloadsTotal.WithLabelValues(result).Inc()
	m.lastReloadTime.Set(at)

	if result == resultSuccess {
		m.lastSuccessful.Set(1)
		return
	}
	m.lastSuccessful.Set(0)
}
