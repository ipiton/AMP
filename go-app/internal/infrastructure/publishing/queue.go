package publishing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/ipiton/AMP/pkg/retry"
)

// Delivery-confirmation sentinels (task rec, alertmanager-parity wave 3:
// RecordSent on CONFIRMED delivery). Both are reported through a job's
// delivery-confirmation channel — see PublishingJob.completion,
// PublishingQueue.SubmitGroupWithConfirmation and
// PublishingCoordinator.PublishGroupToTargets — and both mean "this target
// did NOT receive the notification", so the notify chain must not write an
// nflog entry for it and must retry it on the group's next scheduled fire.
var (
	// ErrDeliveryNotAttempted means the queue deliberately never ran a
	// publish attempt for this job: metrics-only mode (the worker skips the
	// job entirely), an open circuit breaker for the target, or a publisher
	// that could not be constructed. Distinct from a failed HTTP attempt
	// only for diagnostics — the notify-chain consequence is identical
	// (no delivery ⇒ no nflog entry ⇒ retry next tick).
	ErrDeliveryNotAttempted = errors.New("publishing: delivery not attempted")

	// ErrDeliveryWaitTimeout means the caller stopped waiting for this
	// job's final outcome before a worker reported one (see
	// CoordinatorConfig.DeliveryConfirmationTimeout). The job itself is NOT
	// cancelled — it keeps running on the queue's worker pool and may still
	// succeed. Treating the timeout as "unconfirmed" is deliberate and
	// keeps the at-least-once posture: a late success that already left no
	// nflog entry behind is re-sent on the next fire (duplicate
	// notification), whereas assuming success would silently drop a genuine
	// delivery failure for a whole repeat_interval.
	ErrDeliveryWaitTimeout = errors.New("publishing: timed out waiting for delivery confirmation")
)

// jobCompletion carries ONE queued job's final delivery outcome back to the
// goroutine that submitted it (task rec). Kept behind a pointer inside
// PublishingJob so PublishingJob itself stays copyable (go vet copylocks:
// the sync.Once lives here, never in the job struct).
//
// ch is buffered (capacity 1) and closed right after the single send, so
// signal never blocks even when nobody is waiting any more (the submitter
// already gave up on ErrDeliveryWaitTimeout) and a waiter that arrives late
// still observes the outcome.
type jobCompletion struct {
	once sync.Once
	ch   chan error
}

func newJobCompletion() *jobCompletion {
	return &jobCompletion{ch: make(chan error, 1)}
}

// signal reports the job's FINAL outcome exactly once (nil == confirmed
// delivery). Extra calls — a retry loop plus the defer in processJob, say —
// are dropped, and a nil receiver (a job submitted through the
// fire-and-forget Submit/SubmitGroup path, which has no completion channel)
// is a no-op.
func (c *jobCompletion) signal(err error) {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.ch <- err
		close(c.ch)
	})
}

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
	JobStateProcessing                 // Worker picked up job
	JobStateRetrying                   // Job failed, retrying
	JobStateSucceeded                  // Job completed successfully
	JobStateFailed                     // Job failed (permanent error)
	JobStateDLQ                        // Job sent to DLQ after max retries
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
	QueueErrorTypeUnknown   QueueErrorType = iota // Default, retry with caution
	QueueErrorTypeTransient                       // Network timeout, rate limit, 502/503/504 → RETRY
	QueueErrorTypePermanent                       // 400 bad request, 401 unauthorized, 404 → NO RETRY
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

	// Alerts, GroupKey and Receiver are set (Alerts non-empty) for a GROUP
	// job (task fwb: wire-level group batching) — one job per (group,
	// target), submitted by PublishingCoordinator.PublishGroupToTargets,
	// carrying every alert of the group instead of the pre-fwb one-job-per-
	// (alert, target) shape. EnrichedAlert above is still populated for a
	// group job (wrapping Alerts[0]) purely so existing fingerprint-keyed
	// logging/DLQ/job-tracking code paths that assume it's always non-nil
	// keep working unchanged — Alerts is the authoritative payload for a
	// group job; EnrichedAlert.Alert is representative only. See
	// PublishingQueue.publishJob for how a group job is actually
	// dispatched (one wire batch, or a per-alert iteration loop, depending
	// on whether the target's publisher implements BatchAlertPublisher).
	Alerts      []*core.Alert
	GroupKey    string
	Receiver    string
	GroupLabels map[string]string

	// completion, when non-nil, is signalled exactly once with this job's
	// FINAL delivery outcome — nil for a confirmed publish, non-nil for a
	// failed/never-attempted one (task rec: RecordSent on confirmed
	// delivery). Only jobs submitted via SubmitGroupWithConfirmation carry
	// one; Submit/SubmitGroup stay fire-and-forget and leave it nil, which
	// makes every signal call a no-op for them.
	//
	// Unexported on purpose: it is queue-internal plumbing, must never be
	// serialized into the DLQ, and callers interact with it only through
	// the receive-only channel SubmitGroupWithConfirmation hands back.
	completion *jobCompletion

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

	factory          *PublisherFactory
	dlqRepository    DLQRepository    // Dead Letter Queue for failed jobs
	jobTrackingStore JobTrackingStore // LRU cache for job status tracking
	modeManager      ModeManager      // TN-060: Mode manager for metrics-only fallback
	maxRetries       int
	retryInterval    time.Duration
	workerCount      int
	logger           *slog.Logger
	metrics          *v2.PublishingMetrics // v2 metrics for queue operations
	wg               sync.WaitGroup
	ctx              context.Context
	cancel           context.CancelFunc
	circuitBreakers  map[string]*CircuitBreaker
	mu               sync.RWMutex
	totalSubmitted   atomic.Int64
	totalCompleted   atomic.Int64
	totalFailed      atomic.Int64
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
	Metrics                 *v2.PublishingMetrics // v2 metrics (optional, will create if nil)
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
func NewPublishingQueue(factory *PublisherFactory, dlqRepository DLQRepository, jobTrackingStore JobTrackingStore, config PublishingQueueConfig, modeManager ModeManager, logger *slog.Logger) *PublishingQueue {
	// Use v2.PublishingMetrics from config (no stub needed)
	metrics := config.Metrics
	if metrics == nil {
		// Fallback: create default metrics if not provided
		metrics = v2.NewRegistry().Publishing
	}

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
		// Force cancel remaining jobs. Task rec: jobs still sitting in a
		// channel at this point are never picked up, so their
		// delivery-confirmation channels are never signalled — a caller
		// waiting on one falls back to its own
		// DeliveryConfirmationTimeout and reports the target as
		// unconfirmed, which is the correct answer for a queue that was
		// force-stopped mid-flight (no nflog entry, retry after restart).
		q.cancel()
		return fmt.Errorf("publishing queue stop timeout after %v", timeout)
	}
}

// Submit submits a job to the publishing queue
func (q *PublishingQueue) Submit(enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	priority := determinePriority(enrichedAlert)

	job := &PublishingJob{
		EnrichedAlert: enrichedAlert,
		Target:        target,
		RetryCount:    0,
		SubmittedAt:   time.Now(),
		ID:            uuid.NewString(),
		Priority:      priority,
		State:         JobStateQueued,
	}

	return q.submitJob(job, priority, enrichedAlert.Alert.Fingerprint)
}

// SubmitGroup submits ONE job for a whole alert group destined to a single
// target (task fwb: wire-level group batching) — the coordinator calls this
// once per target instead of Submit once per (alert, target) pair. See
// PublishingJob's doc comment on Alerts/GroupKey/Receiver and
// PublishingQueue.publishJob for how the job is dispatched once a worker
// picks it up.
//
// alerts must be non-empty (the caller — PublishingCoordinator.
// PublishGroupToTargets — never calls this otherwise). priority is derived
// from the HIGHEST-priority alert in the set (PriorityHigh has the lowest
// numeric value): a group containing even one critical firing alert must
// not be queued behind an unrelated low-priority job.
func (q *PublishingQueue) SubmitGroup(alerts []*core.Alert, target *core.PublishingTarget, groupKey string, receiver string, groupLabels map[string]string) error {
	_, err := q.submitGroupJob(alerts, target, groupKey, receiver, groupLabels, false)
	return err
}

// SubmitGroupWithConfirmation is SubmitGroup plus a delivery-confirmation
// channel (task rec, alertmanager-parity wave 3): the returned channel
// yields exactly one value — the job's FINAL outcome once a worker is done
// with it — where nil means the wire-level publish for this (group, target)
// pair actually succeeded, and non-nil means it did not (HTTP error after
// all retries, publisher construction failure, open circuit breaker, or
// metrics-only mode; see ErrDeliveryNotAttempted).
//
// WHY THIS EXISTS: PublishingCoordinator.PublishGroupToTargets used to
// report SubmitGroup's own return — "job enqueued" — as the target's
// publish outcome, and DefaultGroupManager.publishGroupAlerts wrote an
// nflog entry per enqueued target. A webhook returning 500 half a second
// later was therefore recorded as "sent" and the group stayed deduped for a
// full repeat_interval (hours), with no retry. Waiting for this channel is
// what makes RecordSent mean "delivered".
//
// The channel is buffered and closed after its single send, so a caller
// that gives up waiting (CoordinatorConfig.DeliveryConfirmationTimeout)
// leaks nothing and the worker never blocks. A non-nil error return means
// the job was never enqueued at all (queue full / shutting down) and the
// channel is nil — there is nothing to wait for.
func (q *PublishingQueue) SubmitGroupWithConfirmation(alerts []*core.Alert, target *core.PublishingTarget, groupKey string, receiver string, groupLabels map[string]string) (<-chan error, error) {
	return q.submitGroupJob(alerts, target, groupKey, receiver, groupLabels, true)
}

// submitGroupJob is the shared body of SubmitGroup (withConfirmation ==
// false, fire-and-forget) and SubmitGroupWithConfirmation (true).
func (q *PublishingQueue) submitGroupJob(alerts []*core.Alert, target *core.PublishingTarget, groupKey string, receiver string, groupLabels map[string]string, withConfirmation bool) (<-chan error, error) {
	if len(alerts) == 0 {
		return nil, fmt.Errorf("cannot submit a group job with no alerts")
	}

	now := time.Now().UTC()
	priority := PriorityLow
	for _, a := range alerts {
		if p := determinePriority(&core.EnrichedAlert{Alert: a, ProcessingTimestamp: &now}); p < priority {
			priority = p
		}
	}

	job := &PublishingJob{
		// Representative alert for logging/DLQ/tracking code that assumes
		// EnrichedAlert is always non-nil — see PublishingJob's doc comment.
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0], ProcessingTimestamp: &now},
		Target:        target,
		RetryCount:    0,
		SubmittedAt:   time.Now(),
		ID:            uuid.NewString(),
		Priority:      priority,
		State:         JobStateQueued,
		Alerts:        alerts,
		GroupKey:      groupKey,
		Receiver:      receiver,
		GroupLabels:   groupLabels,
	}

	if withConfirmation {
		job.completion = newJobCompletion()
	}

	if err := q.submitJob(job, priority, fmt.Sprintf("group:%s alerts=%d", groupKey, len(alerts))); err != nil {
		// Never enqueued ⇒ no worker will ever signal this job's
		// completion, so hand back a nil channel rather than one that would
		// block until the caller's timeout.
		return nil, err
	}

	if job.completion == nil {
		return nil, nil
	}
	return job.completion.ch, nil
}

// submitJob is the shared enqueue path for Submit and SubmitGroup: picks the
// priority-tiered channel, updates metrics/tracking, and reports back
// queue-full/shutting-down as errors exactly as the pre-fwb Submit did.
// logField is whatever identifies the job in the debug log line (a single
// alert's fingerprint for Submit, a group summary for SubmitGroup).
func (q *PublishingQueue) submitJob(job *PublishingJob, priority Priority, logField string) error {
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

	// Track the job BEFORE handing it to the channel (data race fix): the
	// instant the send below completes, a worker may already be inside
	// processJob writing job.State/job.StartedAt, while JobTrackingStore.Add
	// READS exactly those fields to build its snapshot. Doing it first puts
	// the read strictly before the channel send, which happens-before the
	// worker's receive. The race is pre-existing (any Submit with a live
	// worker pool hits it) but was only surfaced by task rec's tests, which
	// are the first to submit and process concurrently under -race.
	//
	// A job that then fails to enqueue is removed again below, so a
	// queue-full/shutting-down submission leaves no phantom "queued" entry
	// behind — same observable tracking state as before this reordering.
	if q.jobTrackingStore != nil {
		q.jobTrackingStore.Add(job)
	}

	select {
	case targetQueue <- job:
		q.totalSubmitted.Add(1)

		if q.metrics != nil {
			q.metrics.RecordQueueSubmission(priority.String(), true)
			q.metrics.UpdateQueueSize(priority.String(), len(targetQueue), cap(targetQueue))
		}

		if q.logger.Enabled(q.ctx, slog.LevelDebug) {
			q.logger.Debug("Job submitted",
				"job_id", job.ID,
				"priority", priority,
				"target", job.Target.Name,
				"fingerprint", logField,
			)
		}
		return nil
	case <-q.ctx.Done():
		if q.metrics != nil {
			q.metrics.RecordQueueSubmission(priority.String(), false)
		}
		if q.jobTrackingStore != nil {
			q.jobTrackingStore.Remove(job.ID)
		}
		return fmt.Errorf("publishing queue is shutting down")
	default:
		if q.metrics != nil {
			q.metrics.RecordQueueSubmission(priority.String(), false)
		}
		if q.jobTrackingStore != nil {
			q.jobTrackingStore.Remove(job.ID)
		}
		return fmt.Errorf("queue full (priority=%s, capacity=%d)", priority, cap(targetQueue))
	}
}

// worker processes jobs from the queue with priority-based selection
func (q *PublishingQueue) worker(id int) {
	defer q.wg.Done()

	// Level guard: avoid expensive logging in production
	if q.logger.Enabled(q.ctx, slog.LevelDebug) {
		q.logger.Debug("Worker started", "worker_id", id)
	}

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
				// Level guard: avoid expensive logging in production
				if q.logger.Enabled(q.ctx, slog.LevelDebug) {
					q.logger.Debug("Job skipped (metrics-only mode)",
						"job_id", job.ID,
						"target", job.Target.Name,
						"worker_id", id,
					)
				}
				// Task rec: a skipped job must still report an outcome, or
				// a caller waiting for delivery confirmation would block
				// until its timeout for something that was decided
				// instantly. Non-nil on purpose: metrics-only mode delivers
				// nothing, so no nflog entry may be written for it (same
				// reasoning as grouping.ErrDeliveryNotConfirmed).
				job.completion.signal(fmt.Errorf("%w: metrics-only mode (target %q)", ErrDeliveryNotAttempted, job.Target.Name))
				// Skip processing, continue to next job
				continue
			}

			// Update worker metrics (v2 API uses Inc/Dec pattern)
			if q.metrics != nil {
				q.metrics.RecordWorkerActive()
			}

			// Process job
			q.processJob(job)

			// Update worker metrics (v2 API uses Inc/Dec pattern)
			if q.metrics != nil {
				q.metrics.RecordWorkerIdle()
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

// createPublisherForJob builds the publisher for one queued job.
//
// WHY THIS ISN'T JUST CreatePublisher (final review finding 6): this queue is
// the ONLY live publish path in the process — everything eventually funnels
// through PublishingQueue.Submit -> processJob. It called
// CreatePublisher(job.Target.Type), which is type-only and therefore cannot
// build any publisher that needs per-target credentials or settings. The
// consequence for Telegram was total: NewTelegramPublisher is a bare generic
// HTTPPublisher, so EnhancedTelegramPublisher (bot token, chat_id,
// message_thread_id, disable_notifications, Telegram Bot API sendMessage) was
// unreachable at runtime — its only non-test caller was
// CreatePublisherForTarget, whose only caller was parallel_publisher.go, which
// has no non-test constructor. The same held for the enhanced Slack, PagerDuty
// and Rootly publishers. The docs claimed runtime support for all of them.
//
// Integration types are therefore routed through CreatePublisherForTarget,
// which reads job.Target.Headers. Note that each createEnhanced*Publisher
// already falls back to the basic HTTP publisher (with a Warn) when the target
// lacks the credentials it needs, so a misconfigured target degrades exactly as
// before rather than failing the job.
//
// TargetTypeWebhook/TargetTypeAlertmanager deliberately stay on
// CreatePublisher: EnhancedWebhookPublisher additionally runs
// WebhookValidator.ValidateTarget (URL scheme/host and header rules) against
// every target, which would reject target configurations the basic publisher
// accepts today. Turning that validation on is a separate, deliberate behaviour
// change, not something to bundle into a wiring fix.
func (q *PublishingQueue) createPublisherForJob(job *PublishingJob) (AlertPublisher, error) {
	switch TargetType(job.Target.Type) {
	case TargetTypeRootly, TargetTypePagerDuty, TargetTypeSlack, TargetTypeTelegram, TargetTypeEmail:
		return q.factory.CreatePublisherForTarget(job.Target)
	default:
		return q.factory.CreatePublisher(job.Target.Type)
	}
}

// processJob processes a single publishing job with retry logic.
//
// Delivery confirmation (task rec): every exit path reports an outcome
// through job.completion — nil ONLY on a confirmed publish. deliveryOutcome
// starts non-nil so any path that forgets to set it (or an unforeseen early
// return added later) fails safe as "not delivered": the notify chain then
// skips RecordSent and retries the target next tick, which costs at most a
// duplicate notification, whereas a false "delivered" silently drops the
// notification for a whole repeat_interval.
func (q *PublishingQueue) processJob(job *PublishingJob) {
	deliveryOutcome := fmt.Errorf("%w: job did not reach a publish attempt (target %q)", ErrDeliveryNotAttempted, job.Target.Name)
	defer func() { job.completion.signal(deliveryOutcome) }()

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
		deliveryOutcome = fmt.Errorf("%w: circuit breaker %s for target %q", ErrDeliveryNotAttempted, cb.State(), job.Target.Name)
		return
	}

	// Create publisher
	publisher, err := q.createPublisherForJob(job)
	if err != nil {
		job.State = JobStateFailed
		now := time.Now()
		job.CompletedAt = &now
		job.LastError = err
		q.totalFailed.Add(1)
		deliveryOutcome = fmt.Errorf("%w: publisher construction failed for target %q: %w", ErrDeliveryNotAttempted, job.Target.Name, err)

		q.logger.Error("Failed to create publisher",
			"target", job.Target.Name,
			"type", job.Target.Type,
			"error", err,
		)
		cb.RecordFailure()
		if q.metrics != nil {
			q.metrics.RecordJobFailure(job.Target.Name)
		}
		if q.jobTrackingStore != nil {
			q.jobTrackingStore.Add(job)
		}
		return
	}

	// Attempt publish with retry
	startTime := time.Now()
	err = q.retryPublish(publisher, job)
	duration := time.Since(startTime).Seconds()

	// Task rec: this is the authoritative delivery outcome — retryPublish
	// returns nil only when a publish attempt actually succeeded, and every
	// in-queue retry of this job counts as ONE delivery attempt cycle, so a
	// final failure here reports "not delivered" exactly once.
	deliveryOutcome = err

	if err != nil {
		q.totalFailed.Add(1)

		q.logger.Error("Failed to publish after retries",
			"job_id", job.ID,
			"target", job.Target.Name,
			"fingerprint", job.EnrichedAlert.Alert.Fingerprint,
			"error", err,
		)
		cb.RecordFailure()
		if q.metrics != nil {
			// v2 API: RecordJobFailure(target string)
			q.metrics.RecordJobFailure(job.Target.Name)
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
		q.totalCompleted.Add(1)

		q.logger.Info("Alert published successfully",
			"job_id", job.ID,
			"target", job.Target.Name,
			"fingerprint", job.EnrichedAlert.Alert.Fingerprint,
			"queue_time", time.Since(job.SubmittedAt),
		)
		cb.RecordSuccess()
		if q.metrics != nil {
			// v2 API: RecordJobSuccess(target, priority string, duration time.Duration)
			q.metrics.RecordJobSuccess(job.Target.Name, job.Priority.String(), time.Duration(duration*float64(time.Second)))
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

	cb = NewCircuitBreakerWithName(
		CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		},
		targetName,
	)

	q.circuitBreakers[targetName] = cb

	// Level guard: avoid expensive logging in production
	if q.logger.Enabled(q.ctx, slog.LevelDebug) {
		q.logger.Debug("Created circuit breaker", "target", targetName)
	}

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
	// Count active jobs from job tracking store
	activeJobs := 0
	if q.jobTrackingStore != nil {
		// Count jobs in "processing" or "retrying" state
		processingJobs := q.jobTrackingStore.List(JobFilters{State: "processing", Limit: 10000})
		retryingJobs := q.jobTrackingStore.List(JobFilters{State: "retrying", Limit: 10000})
		activeJobs = len(processingJobs) + len(retryingJobs)
	}

	stats := QueueStats{
		TotalSize:      q.GetQueueSize(),
		HighPriority:   q.GetQueueSizeByPriority(PriorityHigh),
		MedPriority:    q.GetQueueSizeByPriority(PriorityMedium),
		LowPriority:    q.GetQueueSizeByPriority(PriorityLow),
		Capacity:       q.GetQueueCapacity(),
		WorkerCount:    q.workerCount,
		ActiveJobs:     activeJobs, // Now tracked via JobTrackingStore
		TotalSubmitted: q.totalSubmitted.Load(),
		TotalCompleted: q.totalCompleted.Load(),
		TotalFailed:    q.totalFailed.Load(),
	}

	return stats
}

// retryPublish attempts to publish with exponential backoff retry and error classification
// retryPublish executes publisher with unified retry strategy from pkg/retry.
//
// This replaces the old 87-line custom retry implementation with a standardized approach.
// Benefits:
//   - Consistent retry behavior across the application
//   - Optimized backoff calculation (bit shift instead of math.Pow)
//   - Better jitter algorithm (±15% instead of hardcoded 0-1000ms)
//   - Configurable via Strategy pattern
//
// Migration note: This is part of Sprint 5 (Retry Unification).
// See: tasks/code-quality-refactoring/ACTION_ITEMS.md#1
// publishJob dispatches one publish attempt for job (task fwb: wire-level
// group batching). Three cases:
//
//   - Not a group job (len(job.Alerts) == 0): unchanged pre-fwb behavior —
//     one Publish call for job.EnrichedAlert.
//   - Group job, publisher implements BatchAlertPublisher (webhook/
//     alertmanager formats): ONE PublishBatch call carrying every alert —
//     the actual wire-level batching this task adds.
//   - Group job, publisher does NOT implement BatchAlertPublisher (Slack,
//     Telegram, PagerDuty, Email — inherently one-message-per-alert
//     integrations): iterate Publish once per alert WITHIN this single
//     call/attempt, so retries and rate-limiting stay scoped to one job per
//     (group, target) rather than fragmenting back into one job per alert.
//     Best-effort: every alert is attempted even if an earlier one fails,
//     and the first error (if any) is what the retry strategy above sees —
//     a retry of this job resends the whole alert set again, including any
//     that already succeeded (accepted trade-off, documented in the task
//     spec: there is no wire-level partial-success concept for a per-
//     message loop the way there is for a single batched HTTP request).
func (q *PublishingQueue) publishJob(publisher AlertPublisher, job *PublishingJob) error {
	if len(job.Alerts) == 0 {
		return publisher.Publish(q.ctx, job.EnrichedAlert, job.Target)
	}

	if batchPublisher, ok := publisher.(BatchAlertPublisher); ok {
		return batchPublisher.PublishBatch(q.ctx, job.Alerts, job.GroupKey, job.Receiver, job.GroupLabels, job.Target)
	}

	now := time.Now().UTC()
	var firstErr error
	for _, alert := range job.Alerts {
		enrichedAlert := &core.EnrichedAlert{Alert: alert, ProcessingTimestamp: &now}
		if err := publisher.Publish(q.ctx, enrichedAlert, job.Target); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (q *PublishingQueue) retryPublish(publisher AlertPublisher, job *PublishingJob) error {
	// Create retry strategy with queue configuration
	// Note: Uses queue-specific config (maxRetries, retryInterval) which can be
	// overridden by global retry config if needed
	strategy := retry.Strategy{
		MaxAttempts:     q.maxRetries + 1, // maxRetries is retry count, not total attempts
		BaseDelay:       q.retryInterval,
		MaxDelay:        30 * time.Second, // TODO: Make configurable via config.Retry.MaxDelay
		Multiplier:      2.0,              // TODO: Make configurable via config.Retry.Multiplier
		JitterRatio:     0.15,             // TODO: Make configurable via config.Retry.JitterRatio
		ErrorClassifier: &PublishingErrorClassifier{},
		Logger:          q.logger,
		OperationName:   fmt.Sprintf("publish_%s", job.Target.Name),
	}

	// Track attempt count for job state updates
	attemptCount := 0

	// Execute publish with retry
	err := retry.DoSimple(q.ctx, strategy, func() error {
		attemptCount++

		// Try publish
		publishErr := q.publishJob(publisher, job)

		if publishErr != nil {
			// Classify error for job tracking
			errorType := classifyPublishingError(publishErr)
			job.LastError = publishErr
			job.ErrorType = errorType

			// Update job state
			if attemptCount < strategy.MaxAttempts {
				job.State = JobStateRetrying
			}

			// Record metrics
			if q.metrics != nil {
				// v2 API: RecordRetryAttempt(target, errorType string)
				q.metrics.RecordRetryAttempt(job.Target.Name, errorType.String())
			}

			return publishErr
		}

		// Success!
		job.State = JobStateSucceeded
		now := time.Now()
		job.CompletedAt = &now
		return nil
	})

	// Handle final result
	if err != nil {
		job.State = JobStateFailed
		now := time.Now()
		job.CompletedAt = &now
		return fmt.Errorf("publish failed after %d attempts: %w", attemptCount, err)
	}

	return nil
}

// PublishingErrorClassifier classifies publishing errors for retry decisions.
// This implements retry.ErrorClassifier interface.
type PublishingErrorClassifier struct{}

// IsRetryable determines if a publishing error should trigger a retry.
func (c *PublishingErrorClassifier) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Use queue's error classification
	errorType := classifyPublishingError(err)

	// Only retry transient errors (not permanent)
	return errorType == QueueErrorTypeTransient
}
