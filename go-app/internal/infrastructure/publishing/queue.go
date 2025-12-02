package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ipiton/AMP/internal/core"
)

// Priority levels for job processing order
type Priority int

const (
	PriorityHigh   Priority = 0 // Critical alerts (severity=critical)
	PriorityMedium Priority = 1 // Warning alerts (default)
	PriorityLow    Priority = 2 // Info alerts, resolved alerts
)

func (p Priority) String() string {
	switch p {
	case PriorityHigh:
		return "high"
	case PriorityMedium:
		return "medium"
	case PriorityLow:
		return "low"
	default:
		return "unknown"
	}
}

// JobState represents the current state of a job
type JobState int

const (
	JobStateQueued     JobState = iota // Job submitted to queue
	JobStateProcessing                  // Worker picked up job
	JobStateRetrying                    // Job failed, retrying
	JobStateSucceeded                   // Job completed successfully
	JobStateFailed                      // Job failed (permanent error)
	JobStateDLQ                         // Job sent to DLQ after max retries
)

func (s JobState) String() string {
	switch s {
	case JobStateQueued:
		return "queued"
	case JobStateProcessing:
		return "processing"
	case JobStateRetrying:
		return "retrying"
	case JobStateSucceeded:
		return "succeeded"
	case JobStateFailed:
		return "failed"
	case JobStateDLQ:
		return "dlq"
	default:
		return "unknown"
	}
}

// QueueErrorType classifies errors for retry logic
type QueueErrorType int

const (
	QueueErrorTypeUnknown    QueueErrorType = iota // Default, retry with caution
	QueueErrorTypeTransient                        // Network timeout, rate limit, 502/503/504 → RETRY
	QueueErrorTypePermanent                        // 400 bad request, 401 unauthorized, 404 → NO RETRY
)

func (e QueueErrorType) String() string {
	switch e {
	case QueueErrorTypeTransient:
		return "transient"
	case QueueErrorTypePermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// PublishingJob represents a single publishing task
type PublishingJob struct {
	// Core fields
	EnrichedAlert *core.EnrichedAlert
	Target        *core.PublishingTarget
	RetryCount    int
	SubmittedAt   time.Time

	// Extended fields for 150% quality
	ID          string         // UUID v4
	Priority    Priority       // HIGH/MEDIUM/LOW
	State       JobState       // queued/processing/retrying/succeeded/failed/dlq
	StartedAt   *time.Time     // When processing began
	CompletedAt *time.Time     // When processing completed
	LastError   error          // Most recent error
	ErrorType   QueueErrorType // transient/permanent/unknown
}

// PublishingQueue manages async publishing with worker pool and retry logic
type PublishingQueue struct {
	// Priority queues (3 tiers)
	highPriorityJobs   chan *PublishingJob
	mediumPriorityJobs chan *PublishingJob
	lowPriorityJobs    chan *PublishingJob

	factory           *PublisherFactory
	dlqRepository     DLQRepository     // Dead Letter Queue for failed jobs
	jobTrackingStore  JobTrackingStore  // LRU cache for job status tracking
	modeManager       ModeManager       // TN-060: Mode manager for metrics-only fallback
	maxRetries        int
	retryInterval     time.Duration
	workerCount       int
	logger            *slog.Logger
	metrics           *PublishingMetrics
	wg                sync.WaitGroup
	ctx               context.Context
	cancel            context.CancelFunc
	circuitBreakers   map[string]*CircuitBreaker
	mu                sync.RWMutex
}

// PublishingQueueConfig holds configuration for publishing queue
type PublishingQueueConfig struct {
	WorkerCount             int
	HighPriorityQueueSize   int
	MediumPriorityQueueSize int
	LowPriorityQueueSize    int
	MaxRetries              int
	RetryInterval           time.Duration
	CircuitTimeout          time.Duration
}

// DefaultPublishingQueueConfig returns default configuration
func DefaultPublishingQueueConfig() PublishingQueueConfig {
	return PublishingQueueConfig{
		WorkerCount:             10,
		HighPriorityQueueSize:   500,
		MediumPriorityQueueSize: 1000,
		LowPriorityQueueSize:    500,
		MaxRetries:              3,
		RetryInterval:           2 * time.Second,
		CircuitTimeout:          30 * time.Second,
	}
}

// NewPublishingQueue creates a new publishing queue
func NewPublishingQueue(factory *PublisherFactory, dlqRepository DLQRepository, jobTrackingStore JobTrackingStore, config PublishingQueueConfig, metrics *PublishingMetrics, modeManager ModeManager, logger *slog.Logger) *PublishingQueue {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	queue := &PublishingQueue{
		highPriorityJobs:   make(chan *PublishingJob, config.HighPriorityQueueSize),
		mediumPriorityJobs: make(chan *PublishingJob, config.MediumPriorityQueueSize),
		lowPriorityJobs:    make(chan *PublishingJob, config.LowPriorityQueueSize),
		factory:            factory,
		dlqRepository:      dlqRepository,
		jobTrackingStore:   jobTrackingStore,
		modeManager:        modeManager,
		maxRetries:         config.MaxRetries,
		retryInterval:      config.RetryInterval,
		workerCount:        config.WorkerCount,
		logger:             logger,
		metrics:            metrics,
		ctx:                ctx,
		cancel:             cancel,
		circuitBreakers:    make(map[string]*CircuitBreaker),
	}

	// Initialize worker metrics
	if metrics != nil {
		metrics.InitializeWorkerMetrics(config.WorkerCount)
		metrics.UpdateQueueSize("high", 0, config.HighPriorityQueueSize)
		metrics.UpdateQueueSize("medium", 0, config.MediumPriorityQueueSize)
		metrics.UpdateQueueSize("low", 0, config.LowPriorityQueueSize)
	}

	return queue
}

// Start starts the worker pool
func (q *PublishingQueue) Start() {
	q.logger.Info("Starting publishing queue", "workers", q.workerCount)

	for i := 0; i < q.workerCount; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
}

// Stop gracefully stops the publishing queue
func (q *PublishingQueue) Stop(timeout time.Duration) error {
	q.logger.Info("Stopping publishing queue", "timeout", timeout)

	// Close all priority job channels to signal workers
	close(q.highPriorityJobs)
	close(q.mediumPriorityJobs)
	close(q.lowPriorityJobs)

	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		q.logger.Info("Publishing queue stopped gracefully")
		return nil
	case <-time.After(timeout):
		q.cancel() // Force cancel remaining jobs
		return fmt.Errorf("publishing queue stop timeout after %v", timeout)
	}
}

// Submit submits a job to the publishing queue
func (q *PublishingQueue) Submit(enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	// Generate job ID
	jobID := uuid.NewString()

	// Determine priority
	priority := determinePriority(enrichedAlert)

	// Create job
	job := &PublishingJob{
		EnrichedAlert: enrichedAlert,
		Target:        target,
		RetryCount:    0,
		SubmittedAt:   time.Now(),
		ID:            jobID,
		Priority:      priority,
		State:         JobStateQueued,
	}

	// Select appropriate queue
	var targetQueue chan *PublishingJob
	switch priority {
	case PriorityHigh:
		targetQueue = q.highPriorityJobs
	case PriorityMedium:
		targetQueue = q.mediumPriorityJobs
	case PriorityLow:
		targetQueue = q.lowPriorityJobs
	default:
		targetQueue = q.mediumPriorityJobs
	}

	// Submit to queue
	select {
	case targetQueue <- job:
		// Update metrics
		if q.metrics != nil {
			q.metrics.RecordQueueSubmission(priority.String(), true)
			q.metrics.UpdateQueueSize(priority.String(), len(targetQueue), cap(targetQueue))
		}

		// Track job
		if q.jobTrackingStore != nil {
			q.jobTrackingStore.Add(job)
		}

		q.logger.Debug("Job submitted",
			"job_id", jobID,
			"priority", priority,
			"target", target.Name,
			"fingerprint", enrichedAlert.Alert.Fingerprint,
		)
		return nil
	case <-q.ctx.Done():
		if q.metrics != nil {
			q.metrics.RecordQueueSubmission(priority.String(), false)
		}
		return fmt.Errorf("publishing queue is shutting down")
	default:
		if q.metrics != nil {
			q.metrics.RecordQueueSubmission(priority.String(), false)
		}
		return fmt.Errorf("queue full (priority=%s, capacity=%d)", priority, cap(targetQueue))
	}
}

// worker processes jobs from the queue with priority-based selection
func (q *PublishingQueue) worker(id int) {
	defer q.wg.Done()

	q.logger.Debug("Worker started", "worker_id", id)

	for {
		var job *PublishingJob
		var priority Priority

		// Priority-based select (HIGH > MEDIUM > LOW)
		select {
		case job = <-q.highPriorityJobs:
			if job == nil {
				// High priority channel closed
				return
			}
			priority = PriorityHigh
		case <-q.ctx.Done():
			return
		default:
			// Check medium, then low
			select {
			case job = <-q.mediumPriorityJobs:
				if job == nil {
					// Medium priority channel closed
					return
				}
				priority = PriorityMedium
			case <-q.ctx.Done():
				return
			default:
				// Check low
				select {
				case job = <-q.lowPriorityJobs:
					if job == nil {
						// Low priority channel closed
						return
					}
					priority = PriorityLow
				case <-q.ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					// Idle timeout, loop back to check high priority
					continue
				}
			}
		}

		if job != nil {
			// TN-060: Check mode before processing (metrics-only mode fallback)
			if q.modeManager != nil && q.modeManager.IsMetricsOnly() {
				q.logger.Debug("Job skipped (metrics-only mode)",
					"job_id", job.ID,
					"target", job.Target.Name,
					"worker_id", id,
				)
				// Skip processing, continue to next job
				continue
			}

			// Update worker metrics
			if q.metrics != nil {
				q.metrics.RecordWorkerActive(id, true)
			}

			// Process job
			q.processJob(job)

			// Update worker metrics
			if q.metrics != nil {
				q.metrics.RecordWorkerActive(id, false)
			}

			// Update queue size metric
			if q.metrics != nil {
				switch priority {
				case PriorityHigh:
					q.metrics.UpdateQueueSize("high", len(q.highPriorityJobs), cap(q.highPriorityJobs))
				case PriorityMedium:
					q.metrics.UpdateQueueSize("medium", len(q.mediumPriorityJobs), cap(q.mediumPriorityJobs))
				case PriorityLow:
					q.metrics.UpdateQueueSize("low", len(q.lowPriorityJobs), cap(q.lowPriorityJobs))
				}
			}
		}
	}
}

// processJob processes a single publishing job with retry logic
func (q *PublishingQueue) processJob(job *PublishingJob) {
	// Update job state to Processing
	job.State = JobStateProcessing
	now := time.Now()
	job.StartedAt = &now

	// Track job state change
	if q.jobTrackingStore != nil {
		q.jobTrackingStore.Add(job)
	}

	// Check circuit breaker
	cb := q.getCircuitBreaker(job.Target.Name)
	if !cb.CanAttempt() {
		q.logger.Warn("Circuit breaker open, skipping publish",
			"target", job.Target.Name,
			"state", cb.State(),
		)
		return
	}

	// Create publisher
	publisher, err := q.factory.CreatePublisher(job.Target.Type)
	if err != nil {
		q.logger.Error("Failed to create publisher",
			"target", job.Target.Name,
			"type", job.Target.Type,
			"error", err,
		)
		cb.RecordFailure()
		return
	}

	// Attempt publish with retry
	startTime := time.Now()
	err = q.retryPublish(publisher, job)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		q.logger.Error("Failed to publish after retries",
			"job_id", job.ID,
			"target", job.Target.Name,
			"fingerprint", job.EnrichedAlert.Alert.Fingerprint,
			"error", err,
		)
		cb.RecordFailure()
		if q.metrics != nil {
			q.metrics.RecordJobFailure(job.Target.Name, job.Priority.String(), "retry_exhausted")
		}

		// Send to Dead Letter Queue
		if q.dlqRepository != nil {
			job.State = JobStateDLQ
			dlqErr := q.dlqRepository.Write(q.ctx, job)
			if dlqErr != nil {
				q.logger.Error("Failed to write to DLQ",
					"job_id", job.ID,
					"target", job.Target.Name,
					"error", dlqErr,
				)
			} else {
				q.logger.Info("Job sent to DLQ",
					"job_id", job.ID,
					"target", job.Target.Name,
					"error_type", job.ErrorType,
				)
			}

			// Track DLQ state
			if q.jobTrackingStore != nil {
				q.jobTrackingStore.Add(job)
			}
		}
	} else {
		q.logger.Info("Alert published successfully",
			"job_id", job.ID,
			"target", job.Target.Name,
			"fingerprint", job.EnrichedAlert.Alert.Fingerprint,
			"queue_time", time.Since(job.SubmittedAt),
		)
		cb.RecordSuccess()
		if q.metrics != nil {
			q.metrics.RecordJobSuccess(job.Target.Name, job.Priority.String(), duration)
		}

		// Track success state (updated in retryPublish)
		if q.jobTrackingStore != nil {
			q.jobTrackingStore.Add(job)
		}
	}
}

// getCircuitBreaker gets or creates circuit breaker for target
func (q *PublishingQueue) getCircuitBreaker(targetName string) *CircuitBreaker {
	q.mu.RLock()
	cb, exists := q.circuitBreakers[targetName]
	q.mu.RUnlock()

	if exists {
		return cb
	}

	// Create new circuit breaker
	q.mu.Lock()
	defer q.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists := q.circuitBreakers[targetName]; exists {
		return cb
	}

	cb = NewCircuitBreakerWithMetrics(
		CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		},
		targetName,
		q.metrics,
	)

	q.circuitBreakers[targetName] = cb
	q.logger.Debug("Created circuit breaker", "target", targetName)

	return cb
}

// GetQueueSize returns total current queue size (all priorities)
func (q *PublishingQueue) GetQueueSize() int {
	return len(q.highPriorityJobs) + len(q.mediumPriorityJobs) + len(q.lowPriorityJobs)
}

// GetQueueCapacity returns total queue capacity (all priorities)
func (q *PublishingQueue) GetQueueCapacity() int {
	return cap(q.highPriorityJobs) + cap(q.mediumPriorityJobs) + cap(q.lowPriorityJobs)
}

// GetQueueSizeByPriority returns queue size for specific priority
func (q *PublishingQueue) GetQueueSizeByPriority(priority Priority) int {
	switch priority {
	case PriorityHigh:
		return len(q.highPriorityJobs)
	case PriorityMedium:
		return len(q.mediumPriorityJobs)
	case PriorityLow:
		return len(q.lowPriorityJobs)
	default:
		return 0
	}
}

// QueueStats represents queue statistics
type QueueStats struct {
	TotalSize      int
	HighPriority   int
	MedPriority    int
	LowPriority    int
	Capacity       int
	WorkerCount    int
	ActiveJobs     int
	TotalSubmitted int64
	TotalCompleted int64
	TotalFailed    int64
}

// GetStats returns detailed queue statistics
func (q *PublishingQueue) GetStats() QueueStats {
	stats := QueueStats{
		TotalSize:    q.GetQueueSize(),
		HighPriority: q.GetQueueSizeByPriority(PriorityHigh),
		MedPriority:  q.GetQueueSizeByPriority(PriorityMedium),
		LowPriority:  q.GetQueueSizeByPriority(PriorityLow),
		Capacity:     q.GetQueueCapacity(),
		WorkerCount:  q.workerCount,
		ActiveJobs:   0, // TODO: track active jobs in progress
	}

	// Get metrics if available
	if q.metrics != nil {
		// Prometheus metrics can't be read directly, so we return 0s
		// These would need to be tracked separately if needed
		stats.TotalSubmitted = 0
		stats.TotalCompleted = 0
		stats.TotalFailed = 0
	}

	return stats
}

// retryPublish attempts to publish with exponential backoff retry and error classification
func (q *PublishingQueue) retryPublish(publisher AlertPublisher, job *PublishingJob) error {
	var lastErr error

	for attempt := 0; attempt <= q.maxRetries; attempt++ {
		// Try publish
		err := publisher.Publish(q.ctx, job.EnrichedAlert, job.Target)
		if err == nil {
			// Success
			job.State = JobStateSucceeded
			now := time.Now()
			job.CompletedAt = &now
			return nil
		}

		// Classify error
		errorType := classifyPublishingError(err)
		lastErr = err
		job.LastError = err
		job.ErrorType = errorType

		// Update job state
		if attempt < q.maxRetries {
			job.State = JobStateRetrying
		}

		// Record retry attempt in metrics
		willRetry := errorType != QueueErrorTypePermanent && attempt < q.maxRetries
		if q.metrics != nil {
			q.metrics.RecordRetryAttempt(job.Target.Name, errorType.String(), willRetry)
		}

		q.logger.Warn("Publish failed",
			"job_id", job.ID,
			"attempt", attempt+1,
			"max_retries", q.maxRetries,
			"error", err,
			"error_type", errorType,
			"target", job.Target.Name,
		)

		// Check if error is permanent (no retry)
		if errorType == QueueErrorTypePermanent {
			job.State = JobStateFailed
			q.logger.Error("Permanent error detected, skipping retries",
				"job_id", job.ID,
				"target", job.Target.Name,
				"error", err,
			)
			return fmt.Errorf("permanent error (no retry): %w", err)
		}

		// Don't sleep after last attempt
		if attempt < q.maxRetries {
			// Exponential backoff with jitter: interval * 2^attempt + random(0-1s)
			baseBackoff := time.Duration(math.Pow(2, float64(attempt))) * q.retryInterval
			if baseBackoff > 30*time.Second {
				baseBackoff = 30 * time.Second
			}

			// Add jitter (0-1000ms) to prevent thundering herd
			jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
			backoff := baseBackoff + jitter

			q.logger.Debug("Retrying publish",
				"job_id", job.ID,
				"attempt", attempt+1,
				"max_retries", q.maxRetries,
				"backoff", backoff,
				"error_type", errorType,
			)

			select {
			case <-time.After(backoff):
				// Continue to next attempt
			case <-q.ctx.Done():
				return q.ctx.Err()
			}
		}
	}

	// Max retries exhausted
	job.State = JobStateFailed
	now := time.Now()
	job.CompletedAt = &now

	return fmt.Errorf("failed after %d retries (error_type=%s): %w", q.maxRetries, job.ErrorType, lastErr)
}
