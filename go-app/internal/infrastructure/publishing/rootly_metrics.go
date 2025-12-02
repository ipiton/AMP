package publishing

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// RootlyMetrics holds Prometheus metrics for Rootly publisher
type RootlyMetrics struct {
	// Counter: Total incidents created
	incidentsCreatedTotal *prometheus.CounterVec

	// Counter: Total incidents updated
	incidentsUpdatedTotal *prometheus.CounterVec

	// Counter: Total incidents resolved
	incidentsResolvedTotal prometheus.Counter

	// Counter: Total API requests
	apiRequestsTotal *prometheus.CounterVec

	// Histogram: API request duration
	apiDurationSeconds *prometheus.HistogramVec

	// Counter: API errors
	apiErrorsTotal *prometheus.CounterVec

	// Counter: Rate limit hits
	rateLimitHitsTotal prometheus.Counter

	// Gauge: Active incidents tracked in cache
	activeIncidentsGauge prometheus.Gauge
}

// NewRootlyMetrics creates and registers Rootly metrics
func NewRootlyMetrics() *RootlyMetrics {
	m := &RootlyMetrics{
		incidentsCreatedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rootly_incidents_created_total",
				Help: "Total number of Rootly incidents created",
			},
			[]string{"severity"},
		),
		incidentsUpdatedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rootly_incidents_updated_total",
				Help: "Total number of Rootly incidents updated",
			},
			[]string{"reason"},
		),
		incidentsResolvedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "rootly_incidents_resolved_total",
				Help: "Total number of Rootly incidents resolved",
			},
		),
		apiRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rootly_api_requests_total",
				Help: "Total number of Rootly API requests",
			},
			[]string{"endpoint", "method", "status"},
		),
		apiDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "rootly_api_duration_seconds",
				Help:    "Rootly API request duration in seconds",
				Buckets: prometheus.DefBuckets, // 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10
			},
			[]string{"endpoint", "method"},
		),
		apiErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rootly_api_errors_total",
				Help: "Total number of Rootly API errors",
			},
			[]string{"endpoint", "error_type"},
		),
		rateLimitHitsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "rootly_rate_limit_hits_total",
				Help: "Total number of rate limit hits",
			},
		),
		activeIncidentsGauge: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "rootly_active_incidents_gauge",
				Help: "Number of active incidents tracked in cache",
			},
		),
	}

	// Register all metrics
	prometheus.MustRegister(
		m.incidentsCreatedTotal,
		m.incidentsUpdatedTotal,
		m.incidentsResolvedTotal,
		m.apiRequestsTotal,
		m.apiDurationSeconds,
		m.apiErrorsTotal,
		m.rateLimitHitsTotal,
		m.activeIncidentsGauge,
	)

	return m
}

// RecordIncidentCreated records incident creation
func (m *RootlyMetrics) RecordIncidentCreated(severity string) {
	m.incidentsCreatedTotal.WithLabelValues(severity).Inc()
	m.activeIncidentsGauge.Inc()
}

// RecordIncidentUpdated records incident update
func (m *RootlyMetrics) RecordIncidentUpdated(reason string) {
	m.incidentsUpdatedTotal.WithLabelValues(reason).Inc()
}

// RecordIncidentResolved records incident resolution
func (m *RootlyMetrics) RecordIncidentResolved() {
	m.incidentsResolvedTotal.Inc()
	m.activeIncidentsGauge.Dec()
}

// RecordAPIRequest records API request
func (m *RootlyMetrics) RecordAPIRequest(endpoint, method string, status int, duration time.Duration) {
	m.apiRequestsTotal.WithLabelValues(endpoint, method, fmt.Sprintf("%d", status)).Inc()
	m.apiDurationSeconds.WithLabelValues(endpoint, method).Observe(duration.Seconds())
}

// RecordError records API error
func (m *RootlyMetrics) RecordError(endpoint string, err error) {
	errorType := "unknown"
	if rootlyErr, ok := err.(*RootlyAPIError); ok {
		if rootlyErr.IsRateLimit() {
			errorType = "rate_limit"
		} else if rootlyErr.IsValidation() {
			errorType = "validation"
		} else if rootlyErr.IsAuth() {
			errorType = "auth"
		} else if rootlyErr.IsNotFound() {
			errorType = "not_found"
		} else if rootlyErr.IsConflict() {
			errorType = "conflict"
		} else if rootlyErr.IsServerError() {
			errorType = "server_error"
		} else if rootlyErr.IsClientError() {
			errorType = "client_error"
		}
	}

	m.apiErrorsTotal.WithLabelValues(endpoint, errorType).Inc()
}

// RecordRateLimitHit records rate limit hit
func (m *RootlyMetrics) RecordRateLimitHit() {
	m.rateLimitHitsTotal.Inc()
}

// IncidentIDCache defines interface for incident ID tracking
type IncidentIDCache interface {
	Set(fingerprint, incidentID string)
	Get(fingerprint string) (incidentID string, exists bool)
	Delete(fingerprint string)
	Size() int
}

// cacheEntry holds cached incident ID with expiration
type cacheEntry struct {
	incidentID string
	expiresAt  time.Time
}

// inMemoryIncidentCache implements IncidentIDCache using sync.Map
type inMemoryIncidentCache struct {
	data     sync.Map
	ttl      time.Duration
	ticker   *time.Ticker
	stopChan chan struct{}
}

// NewIncidentIDCache creates a new incident ID cache with TTL
func NewIncidentIDCache(ttl time.Duration) IncidentIDCache {
	cache := &inMemoryIncidentCache{
		data:     sync.Map{},
		ttl:      ttl,
		ticker:   time.NewTicker(1 * time.Hour), // Cleanup every hour
		stopChan: make(chan struct{}),
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// Set stores incident ID for fingerprint
func (c *inMemoryIncidentCache) Set(fingerprint, incidentID string) {
	c.data.Store(fingerprint, cacheEntry{
		incidentID: incidentID,
		expiresAt:  time.Now().Add(c.ttl),
	})
}

// Get retrieves incident ID for fingerprint
func (c *inMemoryIncidentCache) Get(fingerprint string) (string, bool) {
	value, exists := c.data.Load(fingerprint)
	if !exists {
		return "", false
	}

	entry := value.(cacheEntry)

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		c.data.Delete(fingerprint)
		return "", false
	}

	return entry.incidentID, true
}

// Delete removes incident ID for fingerprint
func (c *inMemoryIncidentCache) Delete(fingerprint string) {
	c.data.Delete(fingerprint)
}

// Size returns number of entries in cache
func (c *inMemoryIncidentCache) Size() int {
	count := 0
	c.data.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// cleanup removes expired entries periodically
func (c *inMemoryIncidentCache) cleanup() {
	for {
		select {
		case <-c.ticker.C:
			// Remove expired entries
			now := time.Now()
			c.data.Range(func(key, value interface{}) bool {
				entry := value.(cacheEntry)
				if now.After(entry.expiresAt) {
					c.data.Delete(key)
				}
				return true
			})
		case <-c.stopChan:
			c.ticker.Stop()
			return
		}
	}
}

// Stop stops the cleanup goroutine
func (c *inMemoryIncidentCache) Stop() {
	close(c.stopChan)
}
