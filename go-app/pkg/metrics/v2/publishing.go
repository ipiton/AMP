package v2

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Subsystem name for publishing metrics.
const publishingSubsystem = "publishing"

// Provider constants for consistent labeling.
const (
	ProviderSlack     = "slack"
	ProviderPagerDuty = "pagerduty"
	ProviderRootly    = "rootly"
	ProviderWebhook   = "webhook"
)

// PublishingMetrics provides consolidated metrics for all publishing operations.
//
// This struct consolidates metrics from:
//   - internal/infrastructure/publishing/slack_metrics.go (SlackMetrics)
//   - internal/infrastructure/publishing/pagerduty_metrics.go (PagerDutyMetrics)
//   - internal/infrastructure/publishing/rootly_metrics.go (RootlyMetrics)
//   - internal/infrastructure/publishing/webhook_metrics.go (WebhookMetrics)
//   - internal/infrastructure/publishing/queue_metrics.go (PublishingMetrics)
//   - internal/infrastructure/publishing/parallel_publish_metrics.go (ParallelPublishMetrics)
//   - internal/business/publishing/health_metrics.go (HealthMetrics)
//   - internal/business/publishing/refresh_metrics.go (RefreshMetrics)
//
// Migration from old metrics:
//
//	Old: slackMetrics.MessagesPosted.WithLabelValues("success").Inc()
//	New: registry.Publishing.RecordMessage(v2.ProviderSlack, "success")
//
//	Old: rootlyMetrics.RecordAPIRequest("incidents", "POST", 200, duration)
//	New: registry.Publishing.RecordAPIRequest("rootly", "incidents", "POST", 200, duration)
type PublishingMetrics struct {
	// ========================================================================
	// Core Publishing Metrics (unified across all providers)
	// ========================================================================

	// messagesTotal counts total messages/events sent by provider and status.
	// Labels: provider (slack/pagerduty/rootly/webhook), status (success/error)
	messagesTotal *prometheus.CounterVec

	// apiRequestsTotal counts total API requests by provider, endpoint, method, and status code.
	// Labels: provider, endpoint, method, status_code
	apiRequestsTotal *prometheus.CounterVec

	// apiDurationSeconds measures API request duration by provider, endpoint, and method.
	// Labels: provider, endpoint, method
	apiDurationSeconds *prometheus.HistogramVec

	// apiErrorsTotal counts API errors by provider, endpoint, and error type.
	// Labels: provider, endpoint, error_type (rate_limit/auth/server/network/timeout/client/unknown)
	apiErrorsTotal *prometheus.CounterVec

	// rateLimitHitsTotal counts rate limit hits by provider.
	// Labels: provider
	rateLimitHitsTotal *prometheus.CounterVec

	// payloadSizeBytes measures payload size by provider.
	// Labels: provider
	payloadSizeBytes *prometheus.HistogramVec

	// ========================================================================
	// Provider-Specific Metrics
	// ========================================================================

	// Slack-specific
	threadRepliesTotal *prometheus.CounterVec // Labels: status

	// Rootly-specific
	incidentsCreatedTotal  *prometheus.CounterVec // Labels: severity
	incidentsUpdatedTotal  *prometheus.CounterVec // Labels: reason
	incidentsResolvedTotal prometheus.Counter
	activeIncidentsGauge   prometheus.Gauge

	// PagerDuty-specific
	eventsTriggeredTotal    *prometheus.CounterVec // Labels: severity
	eventsAcknowledgedTotal prometheus.Counter
	eventsResolvedTotal     prometheus.Counter

	// ========================================================================
	// Queue/Processing Metrics
	// ========================================================================

	// queueSize tracks current queue depth by priority.
	// Labels: priority (high/medium/low)
	queueSize *prometheus.GaugeVec

	// queueCapacityUtil tracks queue utilization (0-1) by priority.
	// Labels: priority
	queueCapacityUtil *prometheus.GaugeVec

	// jobsProcessedTotal counts jobs by target and status.
	// Labels: target, status (succeeded/failed/dlq)
	jobsProcessedTotal *prometheus.CounterVec

	// jobDurationSeconds measures job processing duration.
	// Labels: target, priority
	jobDurationSeconds *prometheus.HistogramVec

	// retryAttemptsTotal counts retry attempts by target and error type.
	// Labels: target, error_type
	retryAttemptsTotal *prometheus.CounterVec

	// workersActive tracks active workers.
	workersActive prometheus.Gauge

	// workersIdle tracks idle workers.
	workersIdle prometheus.Gauge

	// dlqSize tracks DLQ size by target.
	// Labels: target
	dlqSize *prometheus.GaugeVec

	// ========================================================================
	// Circuit Breaker Metrics
	// ========================================================================

	// circuitBreakerState tracks CB state by target (0=closed, 1=halfopen, 2=open).
	// Labels: target
	circuitBreakerState *prometheus.GaugeVec

	// circuitBreakerTripsTotal counts CB trips by target.
	// Labels: target
	circuitBreakerTripsTotal *prometheus.CounterVec

	// ========================================================================
	// Health Check Metrics
	// ========================================================================

	// healthChecksTotal counts health checks by target and status.
	// Labels: target, status (success/failure)
	healthChecksTotal *prometheus.CounterVec

	// healthCheckDurationSeconds measures health check duration.
	// Labels: target
	healthCheckDurationSeconds *prometheus.HistogramVec

	// targetHealthStatus tracks target health (0=unknown, 1=healthy, 2=degraded, 3=unhealthy).
	// Labels: target, target_type
	targetHealthStatus *prometheus.GaugeVec

	// targetConsecutiveFailures tracks consecutive failures for a target.
	// Labels: target
	targetConsecutiveFailures *prometheus.GaugeVec

	// targetSuccessRate tracks success rate (0-100%) for a target.
	// Labels: target
	targetSuccessRate *prometheus.GaugeVec

	// ========================================================================
	// Parallel Publish Metrics
	// ========================================================================

	// parallelPublishTotal counts parallel publish operations.
	// Labels: result (success/partial_success/failure)
	parallelPublishTotal *prometheus.CounterVec

	// parallelPublishDurationSeconds measures parallel publish duration.
	// Labels: result
	parallelPublishDurationSeconds *prometheus.HistogramVec

	// ========================================================================
	// Cache Metrics (message ID cache for threading)
	// ========================================================================

	// cacheHitsTotal counts cache hits by provider.
	// Labels: provider
	cacheHitsTotal *prometheus.CounterVec

	// cacheMissesTotal counts cache misses by provider.
	// Labels: provider
	cacheMissesTotal *prometheus.CounterVec

	// cacheSizeGauge tracks cache size by provider.
	// Labels: provider
	cacheSizeGauge *prometheus.GaugeVec

	// ========================================================================
	// Refresh/Discovery Metrics
	// ========================================================================

	// refreshInProgress tracks current refresh operations in progress.
	refreshInProgress prometheus.Gauge

	// refreshTotal counts total refresh operations by source and status.
	// Labels: source (k8s/static/api), status (success/failure)
	refreshTotal *prometheus.CounterVec

	// refreshDuration measures refresh operation duration by source.
	// Labels: source
	refreshDuration *prometheus.HistogramVec

	// refreshErrorsTotal counts refresh errors by source and error type.
	// Labels: source, error_type
	refreshErrorsTotal *prometheus.CounterVec

	// refreshLastSuccess tracks timestamp of last successful refresh by source.
	// Labels: source
	refreshLastSuccess *prometheus.GaugeVec
}

// NewPublishingMetrics creates and registers all publishing metrics.
func NewPublishingMetrics(registerer prometheus.Registerer) *PublishingMetrics {
	m := &PublishingMetrics{}

	// Core Publishing Metrics
	m.messagesTotal = newCounterVec(registerer, publishingSubsystem,
		"messages_total",
		"Total messages/events sent by provider and status",
		[]string{"provider", "status"})

	m.apiRequestsTotal = newCounterVec(registerer, publishingSubsystem,
		"api_requests_total",
		"Total API requests by provider, endpoint, method, and status code",
		[]string{"provider", "endpoint", "method", "status_code"})

	m.apiDurationSeconds = newHistogramVec(registerer, publishingSubsystem,
		"api_duration_seconds",
		"API request duration in seconds",
		APILatencyBuckets,
		[]string{"provider", "endpoint", "method"})

	m.apiErrorsTotal = newCounterVec(registerer, publishingSubsystem,
		"api_errors_total",
		"API errors by provider, endpoint, and error type",
		[]string{"provider", "endpoint", "error_type"})

	m.rateLimitHitsTotal = newCounterVec(registerer, publishingSubsystem,
		"rate_limit_hits_total",
		"Rate limit hits by provider",
		[]string{"provider"})

	m.payloadSizeBytes = newHistogramVec(registerer, publishingSubsystem,
		"payload_size_bytes",
		"Payload size in bytes by provider",
		PayloadSizeBuckets,
		[]string{"provider"})

	// Slack-specific
	m.threadRepliesTotal = newCounterVec(registerer, publishingSubsystem,
		"slack_thread_replies_total",
		"Total Slack thread replies by status",
		[]string{"status"})

	// Rootly-specific
	m.incidentsCreatedTotal = newCounterVec(registerer, publishingSubsystem,
		"rootly_incidents_created_total",
		"Total Rootly incidents created by severity",
		[]string{"severity"})

	m.incidentsUpdatedTotal = newCounterVec(registerer, publishingSubsystem,
		"rootly_incidents_updated_total",
		"Total Rootly incidents updated by reason",
		[]string{"reason"})

	m.incidentsResolvedTotal = newCounter(registerer, publishingSubsystem,
		"rootly_incidents_resolved_total",
		"Total Rootly incidents resolved")

	m.activeIncidentsGauge = newGauge(registerer, publishingSubsystem,
		"rootly_active_incidents",
		"Number of active Rootly incidents")

	// PagerDuty-specific
	m.eventsTriggeredTotal = newCounterVec(registerer, publishingSubsystem,
		"pagerduty_events_triggered_total",
		"Total PagerDuty events triggered by severity",
		[]string{"severity"})

	m.eventsAcknowledgedTotal = newCounter(registerer, publishingSubsystem,
		"pagerduty_events_acknowledged_total",
		"Total PagerDuty events acknowledged")

	m.eventsResolvedTotal = newCounter(registerer, publishingSubsystem,
		"pagerduty_events_resolved_total",
		"Total PagerDuty events resolved")

	// Queue Metrics
	m.queueSize = newGaugeVec(registerer, publishingSubsystem,
		"queue_size",
		"Current queue depth by priority",
		[]string{"priority"})

	m.queueCapacityUtil = newGaugeVec(registerer, publishingSubsystem,
		"queue_capacity_utilization",
		"Queue capacity utilization (0-1) by priority",
		[]string{"priority"})

	m.jobsProcessedTotal = newCounterVec(registerer, publishingSubsystem,
		"jobs_processed_total",
		"Total jobs processed by target and status",
		[]string{"target", "status"})

	m.jobDurationSeconds = newHistogramVec(registerer, publishingSubsystem,
		"job_duration_seconds",
		"Job processing duration by target and priority",
		DurationBuckets,
		[]string{"target", "priority"})

	m.retryAttemptsTotal = newCounterVec(registerer, publishingSubsystem,
		"retry_attempts_total",
		"Retry attempts by target and error type",
		[]string{"target", "error_type"})

	m.workersActive = newGauge(registerer, publishingSubsystem,
		"workers_active",
		"Number of active workers")

	m.workersIdle = newGauge(registerer, publishingSubsystem,
		"workers_idle",
		"Number of idle workers")

	m.dlqSize = newGaugeVec(registerer, publishingSubsystem,
		"dlq_size",
		"Dead letter queue size by target",
		[]string{"target"})

	// Circuit Breaker
	m.circuitBreakerState = newGaugeVec(registerer, publishingSubsystem,
		"circuit_breaker_state",
		"Circuit breaker state (0=closed, 1=halfopen, 2=open) by target",
		[]string{"target"})

	m.circuitBreakerTripsTotal = newCounterVec(registerer, publishingSubsystem,
		"circuit_breaker_trips_total",
		"Circuit breaker trips by target",
		[]string{"target"})

	// Health Checks
	m.healthChecksTotal = newCounterVec(registerer, publishingSubsystem,
		"health_checks_total",
		"Health checks by target and status",
		[]string{"target", "status"})

	m.healthCheckDurationSeconds = newHistogramVec(registerer, publishingSubsystem,
		"health_check_duration_seconds",
		"Health check duration by target",
		DurationBuckets,
		[]string{"target"})

	m.targetHealthStatus = newGaugeVec(registerer, publishingSubsystem,
		"target_health_status",
		"Target health status (0=unknown, 1=healthy, 2=degraded, 3=unhealthy)",
		[]string{"target", "target_type"})

	m.targetConsecutiveFailures = newGaugeVec(registerer, publishingSubsystem,
		"target_consecutive_failures",
		"Consecutive failures count for target",
		[]string{"target"})

	m.targetSuccessRate = newGaugeVec(registerer, publishingSubsystem,
		"target_success_rate",
		"Success rate (0-100%) for target",
		[]string{"target"})

	// Parallel Publish
	m.parallelPublishTotal = newCounterVec(registerer, publishingSubsystem,
		"parallel_publish_total",
		"Parallel publish operations by result",
		[]string{"result"})

	m.parallelPublishDurationSeconds = newHistogramVec(registerer, publishingSubsystem,
		"parallel_publish_duration_seconds",
		"Parallel publish duration by result",
		DurationBuckets,
		[]string{"result"})

	// Cache
	m.cacheHitsTotal = newCounterVec(registerer, publishingSubsystem,
		"cache_hits_total",
		"Cache hits by provider",
		[]string{"provider"})

	m.cacheMissesTotal = newCounterVec(registerer, publishingSubsystem,
		"cache_misses_total",
		"Cache misses by provider",
		[]string{"provider"})

	m.cacheSizeGauge = newGaugeVec(registerer, publishingSubsystem,
		"cache_size",
		"Cache size by provider",
		[]string{"provider"})

	// Refresh/Discovery Metrics
	m.refreshInProgress = newGauge(registerer, publishingSubsystem,
		"refresh_operations_in_progress",
		"Current refresh operations in progress")

	m.refreshTotal = newCounterVec(registerer, publishingSubsystem,
		"refresh_operations_total",
		"Total refresh operations by source and status",
		[]string{"source", "status"})

	m.refreshDuration = newHistogramVec(registerer, publishingSubsystem,
		"refresh_duration_seconds",
		"Refresh operation duration by source",
		[]float64{0.1, 0.5, 1, 2, 5, 10, 30},
		[]string{"source"})

	m.refreshErrorsTotal = newCounterVec(registerer, publishingSubsystem,
		"refresh_errors_total",
		"Refresh errors by source and error type",
		[]string{"source", "error_type"})

	m.refreshLastSuccess = newGaugeVec(registerer, publishingSubsystem,
		"refresh_last_success_timestamp",
		"Timestamp of last successful refresh by source",
		[]string{"source"})

	return m
}

// ============================================================================
// Core Publishing Methods
// ============================================================================

// RecordMessage records a message/event sent to a provider.
func (m *PublishingMetrics) RecordMessage(provider, status string) {
	m.messagesTotal.WithLabelValues(provider, status).Inc()
}

// RecordAPIRequest records an API request with all details.
func (m *PublishingMetrics) RecordAPIRequest(provider, endpoint, method string, statusCode int, duration time.Duration) {
	m.apiRequestsTotal.WithLabelValues(provider, endpoint, method, fmt.Sprintf("%d", statusCode)).Inc()
	m.apiDurationSeconds.WithLabelValues(provider, endpoint, method).Observe(duration.Seconds())
}

// RecordAPIError records an API error.
func (m *PublishingMetrics) RecordAPIError(provider, endpoint, errorType string) {
	m.apiErrorsTotal.WithLabelValues(provider, endpoint, errorType).Inc()
}

// RecordRateLimitHit records a rate limit hit.
func (m *PublishingMetrics) RecordRateLimitHit(provider string) {
	m.rateLimitHitsTotal.WithLabelValues(provider).Inc()
}

// RecordPayloadSize records the payload size.
func (m *PublishingMetrics) RecordPayloadSize(provider string, bytes int) {
	m.payloadSizeBytes.WithLabelValues(provider).Observe(float64(bytes))
}

// ============================================================================
// Slack-specific Methods
// ============================================================================

// RecordSlackThreadReply records a Slack thread reply.
func (m *PublishingMetrics) RecordSlackThreadReply(status string) {
	m.threadRepliesTotal.WithLabelValues(status).Inc()
}

// ============================================================================
// Rootly-specific Methods
// ============================================================================

// RecordRootlyIncidentCreated records a Rootly incident creation.
func (m *PublishingMetrics) RecordRootlyIncidentCreated(severity string) {
	m.incidentsCreatedTotal.WithLabelValues(severity).Inc()
	m.activeIncidentsGauge.Inc()
}

// RecordRootlyIncidentUpdated records a Rootly incident update.
func (m *PublishingMetrics) RecordRootlyIncidentUpdated(reason string) {
	m.incidentsUpdatedTotal.WithLabelValues(reason).Inc()
}

// RecordRootlyIncidentResolved records a Rootly incident resolution.
func (m *PublishingMetrics) RecordRootlyIncidentResolved() {
	m.incidentsResolvedTotal.Inc()
	m.activeIncidentsGauge.Dec()
}

// SetRootlyActiveIncidents sets the number of active Rootly incidents.
func (m *PublishingMetrics) SetRootlyActiveIncidents(count int) {
	m.activeIncidentsGauge.Set(float64(count))
}

// ============================================================================
// PagerDuty-specific Methods
// ============================================================================

// RecordPagerDutyEventTriggered records a PagerDuty event trigger.
func (m *PublishingMetrics) RecordPagerDutyEventTriggered(severity string) {
	m.eventsTriggeredTotal.WithLabelValues(severity).Inc()
}

// RecordPagerDutyEventAcknowledged records a PagerDuty event acknowledgment.
func (m *PublishingMetrics) RecordPagerDutyEventAcknowledged() {
	m.eventsAcknowledgedTotal.Inc()
}

// RecordPagerDutyEventResolved records a PagerDuty event resolution.
func (m *PublishingMetrics) RecordPagerDutyEventResolved() {
	m.eventsResolvedTotal.Inc()
}

// ============================================================================
// Queue Methods
// ============================================================================

// UpdateQueueSize updates queue size and capacity utilization.
func (m *PublishingMetrics) UpdateQueueSize(priority string, currentSize, capacity int) {
	m.queueSize.WithLabelValues(priority).Set(float64(currentSize))

	utilization := 0.0
	if capacity > 0 {
		utilization = float64(currentSize) / float64(capacity)
	}
	m.queueCapacityUtil.WithLabelValues(priority).Set(utilization)
}

// RecordJobSuccess records a successful job.
func (m *PublishingMetrics) RecordJobSuccess(target, priority string, duration time.Duration) {
	m.jobsProcessedTotal.WithLabelValues(target, "succeeded").Inc()
	m.jobDurationSeconds.WithLabelValues(target, priority).Observe(duration.Seconds())
}

// RecordJobFailure records a failed job.
func (m *PublishingMetrics) RecordJobFailure(target string) {
	m.jobsProcessedTotal.WithLabelValues(target, "failed").Inc()
}

// RecordJobDLQ records a job sent to DLQ.
func (m *PublishingMetrics) RecordJobDLQ(target string) {
	m.jobsProcessedTotal.WithLabelValues(target, "dlq").Inc()
}

// RecordRetryAttempt records a retry attempt.
func (m *PublishingMetrics) RecordRetryAttempt(target, errorType string) {
	m.retryAttemptsTotal.WithLabelValues(target, errorType).Inc()
}

// SetWorkerCounts sets worker counts.
func (m *PublishingMetrics) SetWorkerCounts(active, idle int) {
	m.workersActive.Set(float64(active))
	m.workersIdle.Set(float64(idle))
}

// UpdateDLQSize updates DLQ size.
func (m *PublishingMetrics) UpdateDLQSize(target string, size int) {
	m.dlqSize.WithLabelValues(target).Set(float64(size))
}

// ============================================================================
// Circuit Breaker Methods
// ============================================================================

// CircuitBreakerState represents circuit breaker states.
type CircuitBreakerState int

const (
	CircuitBreakerClosed   CircuitBreakerState = 0
	CircuitBreakerHalfOpen CircuitBreakerState = 1
	CircuitBreakerOpen     CircuitBreakerState = 2
)

// SetCircuitBreakerState sets the circuit breaker state.
func (m *PublishingMetrics) SetCircuitBreakerState(target string, state CircuitBreakerState) {
	m.circuitBreakerState.WithLabelValues(target).Set(float64(state))
}

// RecordCircuitBreakerTrip records a circuit breaker trip.
func (m *PublishingMetrics) RecordCircuitBreakerTrip(target string) {
	m.circuitBreakerTripsTotal.WithLabelValues(target).Inc()
}

// ============================================================================
// Health Check Methods
// ============================================================================

// RecordHealthCheck records a health check.
func (m *PublishingMetrics) RecordHealthCheck(target string, success bool, duration time.Duration) {
	status := "failure"
	if success {
		status = "success"
	}
	m.healthChecksTotal.WithLabelValues(target, status).Inc()
	m.healthCheckDurationSeconds.WithLabelValues(target).Observe(duration.Seconds())
}

// HealthStatus represents target health status.
type HealthStatus int

const (
	HealthStatusUnknown   HealthStatus = 0
	HealthStatusHealthy   HealthStatus = 1
	HealthStatusDegraded  HealthStatus = 2
	HealthStatusUnhealthy HealthStatus = 3
)

// SetTargetHealthStatus sets the health status for a target.
func (m *PublishingMetrics) SetTargetHealthStatus(target, targetType string, status HealthStatus) {
	m.targetHealthStatus.WithLabelValues(target, targetType).Set(float64(status))
}

// SetConsecutiveFailures sets the consecutive failures count for a target.
func (m *PublishingMetrics) SetConsecutiveFailures(target string, count int) {
	m.targetConsecutiveFailures.WithLabelValues(target).Set(float64(count))
}

// SetSuccessRate sets the success rate (0-100%) for a target.
func (m *PublishingMetrics) SetSuccessRate(target string, rate float64) {
	m.targetSuccessRate.WithLabelValues(target).Set(rate)
}

// ============================================================================
// Refresh/Discovery Methods
// ============================================================================

// RecordRefreshStart marks the beginning of a refresh operation.
func (m *PublishingMetrics) RecordRefreshStart() {
	m.refreshInProgress.Inc()
}

// RecordRefreshComplete marks the completion of a refresh operation.
// Parameters:
//   - source: Source of refresh (e.g., "k8s", "static", "api")
//   - status: Status of operation ("success" or "failure")
//   - duration: Time taken for the operation
func (m *PublishingMetrics) RecordRefreshComplete(source, status string, duration time.Duration) {
	m.refreshInProgress.Dec()
	m.refreshTotal.WithLabelValues(source, status).Inc()
	m.refreshDuration.WithLabelValues(source).Observe(duration.Seconds())

	if status == "success" {
		m.refreshLastSuccess.WithLabelValues(source).SetToCurrentTime()
	}
}

// RecordRefreshError records a refresh error.
// Parameters:
//   - source: Source of refresh (e.g., "k8s", "static", "api")
//   - errorType: Type of error (e.g., "discovery_failed", "validation_error")
func (m *PublishingMetrics) RecordRefreshError(source, errorType string) {
	m.refreshErrorsTotal.WithLabelValues(source, errorType).Inc()
}

// ============================================================================
// Parallel Publish Methods
// ============================================================================

// RecordParallelPublish records a parallel publish operation.
func (m *PublishingMetrics) RecordParallelPublish(result string, duration time.Duration) {
	m.parallelPublishTotal.WithLabelValues(result).Inc()
	m.parallelPublishDurationSeconds.WithLabelValues(result).Observe(duration.Seconds())
}

// ============================================================================
// Cache Methods
// ============================================================================

// RecordCacheHit records a cache hit.
func (m *PublishingMetrics) RecordCacheHit(provider string) {
	m.cacheHitsTotal.WithLabelValues(provider).Inc()
}

// RecordCacheMiss records a cache miss.
func (m *PublishingMetrics) RecordCacheMiss(provider string) {
	m.cacheMissesTotal.WithLabelValues(provider).Inc()
}

// SetCacheSize sets the cache size.
func (m *PublishingMetrics) SetCacheSize(provider string, size int) {
	m.cacheSizeGauge.WithLabelValues(provider).Set(float64(size))
}

// ============================================================================
// Convenience Aliases (for cleaner code in publishers)
// ============================================================================

// RecordIncidentCreated is an alias for RecordRootlyIncidentCreated.
func (m *PublishingMetrics) RecordIncidentCreated(severity string) {
	m.RecordRootlyIncidentCreated(severity)
}

// RecordIncidentUpdated is an alias for RecordRootlyIncidentUpdated.
func (m *PublishingMetrics) RecordIncidentUpdated(reason string) {
	m.RecordRootlyIncidentUpdated(reason)
}

// RecordIncidentResolved is an alias for RecordRootlyIncidentResolved.
func (m *PublishingMetrics) RecordIncidentResolved() {
	m.RecordRootlyIncidentResolved()
}

// RecordEventTriggered is an alias for RecordPagerDutyEventTriggered.
func (m *PublishingMetrics) RecordEventTriggered(severity string) {
	m.RecordPagerDutyEventTriggered(severity)
}

// RecordEventAcknowledged is an alias for RecordPagerDutyEventAcknowledged.
func (m *PublishingMetrics) RecordEventAcknowledged() {
	m.RecordPagerDutyEventAcknowledged()
}

// RecordEventResolved is an alias for RecordPagerDutyEventResolved.
func (m *PublishingMetrics) RecordEventResolved() {
	m.RecordPagerDutyEventResolved()
}

// RecordThreadReply is an alias for RecordSlackThreadReply.
func (m *PublishingMetrics) RecordThreadReply(status string) {
	m.RecordSlackThreadReply(status)
}

// RecordAPIDuration records API request duration without full details.
// This is a convenience method for when you only need to record duration.
func (m *PublishingMetrics) RecordAPIDuration(provider, endpoint, method string, duration time.Duration) {
	m.apiDurationSeconds.WithLabelValues(provider, endpoint, method).Observe(duration.Seconds())
}

// ============================================================================
// Legacy Aliases (for backward compatibility during migration)
// ============================================================================

// UpdateCircuitBreakerState is an alias for SetCircuitBreakerState with int conversion.
func (m *PublishingMetrics) UpdateCircuitBreakerState(target string, state CircuitBreakerState) {
	m.SetCircuitBreakerState(target, state)
}

// RecordCircuitBreakerRecovery records a circuit breaker recovery.
func (m *PublishingMetrics) RecordCircuitBreakerRecovery(target string) {
	// Recovery is implicitly recorded when state changes to closed
	// This is a no-op metric kept for compatibility
}

// RecordPublish records a parallel publish operation (legacy name).
func (m *PublishingMetrics) RecordPublish(result string) {
	m.parallelPublishTotal.WithLabelValues(result).Inc()
}

// RecordQueueSubmission records a queue submission.
func (m *PublishingMetrics) RecordQueueSubmission(priority string, accepted bool) {
	result := "accepted"
	if !accepted {
		result = "rejected"
	}
	m.jobsProcessedTotal.WithLabelValues("queue", result).Inc()
}

// RecordWorkerActive records a worker becoming active.
func (m *PublishingMetrics) RecordWorkerActive() {
	m.workersActive.Inc()
	m.workersIdle.Dec()
}

// RecordWorkerIdle records a worker becoming idle.
func (m *PublishingMetrics) RecordWorkerIdle() {
	m.workersActive.Dec()
	m.workersIdle.Inc()
}

// InitializeWorkerMetrics initializes worker count metrics.
func (m *PublishingMetrics) InitializeWorkerMetrics(workerCount int) {
	m.workersActive.Set(0)
	m.workersIdle.Set(float64(workerCount))
}
