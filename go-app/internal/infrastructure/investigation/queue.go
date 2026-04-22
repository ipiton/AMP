// Package investigation implements the Phase 2 async investigation pipeline.
package investigation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ipiton/AMP/internal/core"
	"github.com/prometheus/client_golang/prometheus"
)

// InvestigationLLMClient is the minimal interface the worker needs from the LLM layer.
type InvestigationLLMClient interface {
	InvestigateAlert(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) (*core.InvestigationResult, error)
}

// QueueConfig holds configuration for InvestigationQueue.
type QueueConfig struct {
	QueueSize     int
	WorkerCount   int
	MaxRetries    int
	RetryInterval time.Duration
	LLMTimeout    time.Duration
}

// DefaultQueueConfig returns sensible defaults.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		QueueSize:     500,
		WorkerCount:   3,
		MaxRetries:    3,
		RetryInterval: 5 * time.Second,
		LLMTimeout:    60 * time.Second,
	}
}

// InvestigationQueue is a fire-and-forget async queue for alert investigation jobs.
type InvestigationQueue struct {
	jobs    chan *core.InvestigationJob
	config  QueueConfig
	repo    core.InvestigationRepository
	llm     InvestigationLLMClient
	metrics *Metrics
	logger  *slog.Logger
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewInvestigationQueue creates a new investigation queue.
func NewInvestigationQueue(
	repo core.InvestigationRepository,
	llm InvestigationLLMClient,
	config QueueConfig,
	logger *slog.Logger,
	reg prometheus.Registerer,
) *InvestigationQueue {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &InvestigationQueue{
		jobs:    make(chan *core.InvestigationJob, config.QueueSize),
		config:  config,
		repo:    repo,
		llm:     llm,
		metrics: NewMetrics(reg),
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start launches the worker goroutines.
func (q *InvestigationQueue) Start() {
	q.logger.Info("Starting investigation queue", "workers", q.config.WorkerCount)
	for i := 0; i < q.config.WorkerCount; i++ {
		q.wg.Add(1)
		go q.runWorker(i)
	}
}

// Stop gracefully drains in-flight jobs and shuts down workers.
func (q *InvestigationQueue) Stop(timeout time.Duration) error {
	q.logger.Info("Stopping investigation queue")
	close(q.jobs)

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		q.logger.Info("Investigation queue stopped gracefully")
		return nil
	case <-time.After(timeout):
		q.cancel()
		return fmt.Errorf("investigation queue stop timeout after %v", timeout)
	}
}

// Submit enqueues an investigation job. It is non-blocking: if the queue is full,
// the job is dropped and the metric is incremented.
func (q *InvestigationQueue) Submit(alert *core.Alert, classification *core.ClassificationResult) {
	job := &core.InvestigationJob{
		ID:             uuid.NewString(),
		Alert:          alert,
		Classification: classification,
		SubmittedAt:    time.Now(),
	}

	// Persist the queued record immediately so the status is visible.
	inv := &core.Investigation{
		ID:          job.ID,
		Fingerprint: alert.Fingerprint,
		Status:      core.InvestigationQueued,
		QueuedAt:    job.SubmittedAt,
	}
	if err := q.repo.Create(q.ctx, inv); err != nil {
		q.logger.Warn("Failed to persist investigation job, dropping",
			"fingerprint", alert.Fingerprint,
			"error", err,
		)
		q.metrics.DroppedTotal.Inc()
		return
	}

	select {
	case q.jobs <- job:
		q.metrics.QueueDepth.Inc()
	default:
		q.logger.Warn("Investigation queue full, dropping job",
			"fingerprint", alert.Fingerprint,
			"queue_size", q.config.QueueSize,
		)
		q.metrics.DroppedTotal.Inc()
		// Mark as failed in DB so the status is not stuck at queued.
		_ = q.repo.SaveError(q.ctx, job.ID, "queue full at submit time", core.InvestigationErrorPermanent)
	}
}

// QueueDepth returns the current number of pending jobs.
func (q *InvestigationQueue) QueueDepth() int {
	return len(q.jobs)
}

// runWorker processes jobs until the jobs channel is closed.
func (q *InvestigationQueue) runWorker(id int) {
	defer q.wg.Done()
	q.logger.Debug("Investigation worker started", "worker_id", id)

	for job := range q.jobs {
		q.metrics.QueueDepth.Dec()
		q.processJob(job)
	}

	q.logger.Debug("Investigation worker stopped", "worker_id", id)
}

// processJob executes a single investigation with retry logic.
func (q *InvestigationQueue) processJob(job *core.InvestigationJob) {
	start := time.Now()

	// Mark as processing.
	if err := q.repo.UpdateStatus(q.ctx, job.ID, core.InvestigationProcessing); err != nil {
		q.logger.Warn("Failed to update investigation status to processing",
			"id", job.ID,
			"error", err,
		)
	}

	var (
		result  *core.InvestigationResult
		lastErr error
	)

	for attempt := 0; attempt <= q.config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := q.backoff(attempt)
			q.logger.Info("Retrying investigation",
				"id", job.ID,
				"attempt", attempt,
				"backoff", backoff,
			)
			select {
			case <-q.ctx.Done():
				return
			case <-time.After(backoff):
			}
		}

		llmCtx, cancel := context.WithTimeout(q.ctx, q.config.LLMTimeout)
		result, lastErr = q.llm.InvestigateAlert(llmCtx, job.Alert, job.Classification)
		cancel()

		if lastErr == nil {
			break
		}

		errType := classifyError(lastErr)
		q.logger.Warn("Investigation LLM call failed",
			"id", job.ID,
			"attempt", attempt,
			"error_type", errType,
			"error", lastErr,
		)

		if errType == core.InvestigationErrorPermanent {
			break
		}
	}

	if lastErr != nil {
		errType := classifyError(lastErr)
		if saveErr := q.repo.SaveError(q.ctx, job.ID, lastErr.Error(), errType); saveErr != nil {
			q.logger.Error("Failed to save investigation error", "id", job.ID, "error", saveErr)
		}
		if errType != core.InvestigationErrorPermanent {
			_ = q.repo.MoveToDLQ(q.ctx, job.ID)
			q.metrics.InvestigationsTotal.WithLabelValues("dlq").Inc()
		} else {
			q.metrics.InvestigationsTotal.WithLabelValues("failed").Inc()
		}
		return
	}

	if err := q.repo.SaveResult(q.ctx, job.ID, result); err != nil {
		q.logger.Error("Failed to save investigation result", "id", job.ID, "error", err)
		q.metrics.InvestigationsTotal.WithLabelValues("failed").Inc()
		return
	}

	q.metrics.InvestigationsTotal.WithLabelValues("completed").Inc()
	q.metrics.ProcessingTime.Observe(time.Since(start).Seconds())

	q.logger.Info("Investigation completed",
		"id", job.ID,
		"fingerprint", job.Alert.Fingerprint,
		"confidence", result.Confidence,
		"duration", time.Since(start),
	)
}

// backoff returns exponential backoff duration capped at 60 seconds.
func (q *InvestigationQueue) backoff(attempt int) time.Duration {
	d := q.config.RetryInterval * (1 << uint(attempt-1))
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// classifyError determines whether an error is transient or permanent.
func classifyError(err error) core.InvestigationErrorType {
	if err == nil {
		return core.InvestigationErrorUnknown
	}
	if err == context.DeadlineExceeded || err == context.Canceled {
		return core.InvestigationErrorTransient
	}
	msg := err.Error()
	// Permanent client errors (4xx except 429)
	for _, perm := range []string{"HTTP 400", "HTTP 401", "HTTP 403", "HTTP 404"} {
		if strings.Contains(msg, perm) {
			return core.InvestigationErrorPermanent
		}
	}
	// Transient: rate limit, server errors, timeouts, network issues
	for _, trans := range []string{"HTTP 429", "HTTP 5", "timeout", "deadline", "connection"} {
		if strings.Contains(msg, trans) {
			return core.InvestigationErrorTransient
		}
	}
	return core.InvestigationErrorTransient
}
