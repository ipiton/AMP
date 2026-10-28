// Package investigation implements the Phase 2 async investigation pipeline.
package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ipiton/AMP/internal/core"
	coreinv "github.com/ipiton/AMP/internal/core/investigation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
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
	// OnlyFiring: when true, Submit drops resolved alerts without enqueuing.
	OnlyFiring bool
}

// storeTimeout bounds the worker's dedup claim and record creation, so a slow
// Redis or database cannot park a worker indefinitely.
const storeTimeout = 5 * time.Second

// DefaultQueueConfig returns sensible defaults.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		QueueSize:     500,
		WorkerCount:   3,
		MaxRetries:    3,
		RetryInterval: 5 * time.Second,
		LLMTimeout:    60 * time.Second,
		OnlyFiring:    true,
	}
}

// InvestigationQueue is a fire-and-forget async queue for alert investigation jobs.
type InvestigationQueue struct {
	jobs      chan *core.InvestigationJob
	config    QueueConfig
	repo      core.InvestigationRepository
	llm       InvestigationLLMClient
	agentLoop *coreinv.AgentLoop // Phase 5B: optional agentic loop
	dedup     *dedup
	metrics   *Metrics
	logger    *slog.Logger
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
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
		dedup:   newDedup(),
		metrics: NewMetrics(reg),
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SetAgentLoop wires the Phase 5B agentic loop. Must be called before Start().
func (q *InvestigationQueue) SetAgentLoop(loop *coreinv.AgentLoop) {
	q.agentLoop = loop
}

// SetSharedDedup makes the per-incident dedup claim across replicas through
// Redis (SETNX). Without it the claim is per replica. Must be called before
// Start().
func (q *InvestigationQueue) SetSharedDedup(rdb redis.Cmdable) {
	q.dedup.rdb = rdb
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

// Submit enqueues an investigation job. It is non-blocking and does no IO: it
// runs on the alert ingest goroutine (INVESTIGATION-ASYNC-DEDUP). A resend of
// an incident this replica already claimed is skipped; if the queue is full,
// the job is dropped, the metric is incremented and the claim released so a
// later resend may try again. The record is created by the worker.
func (q *InvestigationQueue) Submit(alert *core.Alert, classification *core.ClassificationResult) {
	if q.config.OnlyFiring && alert.Status != core.StatusFiring {
		return
	}
	key := dedupKey(alert)
	if !q.dedup.claimLocal(key) {
		q.metrics.DeduplicatedTotal.WithLabelValues("local").Inc()
		return
	}
	job := &core.InvestigationJob{
		ID:             uuid.NewString(),
		Alert:          alert,
		Classification: classification,
		SubmittedAt:    time.Now(),
	}

	select {
	case q.jobs <- job:
		q.metrics.QueueDepth.Inc()
	default:
		q.dedup.releaseLocal(key)
		q.logger.Warn("Investigation queue full, dropping job",
			"fingerprint", alert.Fingerprint,
			"queue_size", q.config.QueueSize,
		)
		q.metrics.DroppedTotal.Inc()
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

	if !q.claimAndCreate(job) {
		return
	}

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

	// Phase 5B: if agentic loop is wired, run full tool-calling cycle.
	if q.agentLoop != nil {
		q.processJobWithAgent(start, job, result)
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

// claimAndCreate makes the cross-replica dedup claim and persists the queued
// record. It returns false when the job must not run: another replica owns
// the incident, or the record could not be created.
func (q *InvestigationQueue) claimAndCreate(job *core.InvestigationJob) bool {
	ctx, cancel := context.WithTimeout(q.ctx, storeTimeout)
	defer cancel()

	claimed, err := q.dedup.claimShared(ctx, dedupKey(job.Alert))
	if err != nil {
		// Fail-open: a Redis outage costs a possible duplicate, not the
		// investigation.
		q.logger.Warn("Investigation dedup claim failed, investigating anyway",
			"fingerprint", job.Alert.Fingerprint,
			"error", err,
		)
	}
	if !claimed {
		q.metrics.DeduplicatedTotal.WithLabelValues("shared").Inc()
		return false
	}

	inv := &core.Investigation{
		ID:          job.ID,
		Fingerprint: job.Alert.Fingerprint,
		Status:      core.InvestigationQueued,
		QueuedAt:    job.SubmittedAt,
	}
	if err := q.repo.Create(ctx, inv); err != nil {
		q.logger.Warn("Failed to persist investigation job, dropping",
			"fingerprint", job.Alert.Fingerprint,
			"error", err,
		)
		q.metrics.DroppedTotal.Inc()
		return false
	}
	return true
}

// processJobWithAgent runs the Phase 5B agentic loop and persists the full trace.
func (q *InvestigationQueue) processJobWithAgent(start time.Time, job *core.InvestigationJob, classResult *core.InvestigationResult) {
	agentRun, err := q.agentLoop.Run(q.ctx, job.Alert, job.Classification)
	if err != nil {
		q.logger.Warn("Agent loop returned error",
			"id", job.ID,
			"termination", agentRun.TerminationKind,
			"runbooks", agentRun.RunbooksUsed,
			"error", err,
		)
	}

	// If the loop produced no final answer, record a failure.
	if agentRun.TerminationKind != "final_answer" || agentRun.Result == nil {
		stepsJSON, _ := json.Marshal(agentRun.Steps)
		_ = q.repo.SaveAgentResult(q.ctx, job.ID, classResult, &core.AgentRunSummary{
			StepsJSON:       stepsJSON,
			IterationsCount: agentRun.IterationsUsed,
			ToolCallsCount:  agentRun.ToolCallsCount,
			TerminationKind: agentRun.TerminationKind,
		})
		q.metrics.InvestigationsTotal.WithLabelValues("failed").Inc()
		return
	}

	stepsJSON, _ := json.Marshal(agentRun.Steps)
	summary := &core.AgentRunSummary{
		StepsJSON:       stepsJSON,
		IterationsCount: agentRun.IterationsUsed,
		ToolCallsCount:  agentRun.ToolCallsCount,
		TerminationKind: agentRun.TerminationKind,
	}
	if err := q.repo.SaveAgentResult(q.ctx, job.ID, agentRun.Result, summary); err != nil {
		q.logger.Error("Failed to save agent result", "id", job.ID, "error", err)
		q.metrics.InvestigationsTotal.WithLabelValues("failed").Inc()
		return
	}

	q.metrics.InvestigationsTotal.WithLabelValues("completed").Inc()
	q.metrics.ProcessingTime.Observe(time.Since(start).Seconds())

	q.logger.Info("Agent investigation completed",
		"id", job.ID,
		"fingerprint", job.Alert.Fingerprint,
		"termination", agentRun.TerminationKind,
		"iterations", agentRun.IterationsUsed,
		"tool_calls", agentRun.ToolCallsCount,
		"runbooks", agentRun.RunbooksUsed,
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
