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
	// CoordinatorConfig.DeliveryConfirmationTimeout). Treating the timeout as
	// "unconfirmed" is deliberate and keeps the at-least-once posture: a
	// success that lands after the waiter gave up left no nflog entry behind
	// and is re-sent on the next fire (duplicate notification), whereas
	// assuming success would silently drop a genuine delivery failure for a
	// whole repeat_interval.
	//
	// Since fix round 1 (review finding I2) giving up also ABANDONS the job:
	// GroupPublishHandle.Abandon cancels the job's own context, so an
	// in-flight HTTP request and any pending retry back-off unwind instead of
	// pinning a worker for the queue's full ~2min retry budget while later
	// ticks submit fresh jobs for the same (group, target).
	ErrDeliveryWaitTimeout = errors.New("publishing: timed out waiting for delivery confirmation")
)

// jobCompletion carries ONE queued job's final delivery outcome back to the
// goroutine that submitted it (task rec). Kept behind a pointer inside
// PublishingJob so PublishingJob itself stays copyable (go vet copylocks:
// the sync.Once lives here, never in the job struct).
//
// ch is buffered (capacity 1) and never closed (fix round 1, review finding
// M1): the buffer already makes the single send non-blocking for an
// abandoned channel, while closing would make every receive AFTER the value
// is drained yield a nil error — i.e. "confirmed delivery", the one direction
// this design must never fail in.
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
	})
}

// perAlertProgress records which alerts of ONE non-batch group job have been
// confirmed accepted by the target (task fu4, alertmanager-parity wave 4).
//
// WHY: a Slack/Telegram/PagerDuty/Email job sends one wire message PER ALERT
// inside a single job (see publishJob). Before this, the job's outcome was
// all-or-nothing — alert 3 of 5 failing marked the whole (group, target) pair
// unconfirmed, so no nflog entry was written and the group's next fire
// re-sent ALL five alerts, duplicating the four that had already landed. The
// keys collected here travel back to the notify chain through
// GroupPublishHandle.DeliveredAlerts, which records them as the target's
// per-alert delivered set so the retry carries only the alerts still owed.
//
// Two independent duplicate sources are closed by the same bookkeeping:
//
//   - WITHIN one job: retryPublish calls publishJob up to MaxAttempts times,
//     and every attempt used to resend every alert. has() now skips the ones
//     already accepted, so a retry re-sends only the failures.
//   - ACROSS fires: snapshot() is published to the job's atomic pointer after
//     every accepted alert, so even a job whose waiter timed out (or that is
//     still retrying when abandoned) hands back the progress it made.
//
// OWNERSHIP: created and mutated ONLY by the worker goroutine running
// processJob for this job — no locking. The single value shared with the
// submitting goroutine is the immutable []string snapshot behind
// PublishingJob.deliveredAlerts (an atomic.Pointer), which is why a waiter
// may read progress from a job that is still running without racing it.
type perAlertProgress struct {
	delivered map[string]struct{}
	order     []string
}

func newPerAlertProgress(capacity int) *perAlertProgress {
	return &perAlertProgress{delivered: make(map[string]struct{}, capacity), order: make([]string, 0, capacity)}
}

func (p *perAlertProgress) has(key string) bool {
	if p == nil || key == "" {
		return false
	}
	_, ok := p.delivered[key]
	return ok
}

// add records one accepted alert. Insertion-ordered and deduplicated: the
// same alert can legitimately appear twice in a job's alert set only through
// a caller bug, but the delivered set must stay a set either way.
func (p *perAlertProgress) add(key string) {
	if p == nil || key == "" {
		return
	}
	if _, ok := p.delivered[key]; ok {
		return
	}
	p.delivered[key] = struct{}{}
	p.order = append(p.order, key)
}

// snapshot returns an immutable copy safe to publish to another goroutine.
func (p *perAlertProgress) snapshot() []string {
	if p == nil || len(p.order) == 0 {
		return nil
	}
	out := make([]string, len(p.order))
	copy(out, p.order)
	return out
}

// AbandonReason tells the queue WHY a caller stopped awaiting a job's delivery
// outcome (task rec fix round 2, review finding R3). The distinction matters
// because only one of these is evidence about the target's health.
type AbandonReason int32

const (
	// AbandonReasonSettled means the outcome already arrived and the handle is
	// just being released. Nothing to judge: whatever the job reported was
	// already accounted for by processJob. Also what an implicit cancellation
	// (queue shutdown, no Abandon call) reads as — see AbandonReasonShutdown.
	AbandonReasonSettled AbandonReason = iota

	// AbandonReasonUnconfirmed means the waiter gave up before the job
	// reported anything: its delivery-confirmation timeout elapsed, or the
	// caller's context died.
	//
	// THIS IS EVIDENCE THE TARGET IS UNHEALTHY — it was handed a notification
	// and did not answer within the window, which is precisely what the
	// circuit breaker exists to notice. Fix round 1 skipped RecordFailure for
	// every abandoned job, so a target that HANGS (rather than failing fast)
	// could never open its breaker: each fire abandoned its job at the
	// timeout, the breaker saw nothing, and every subsequent fire paid the
	// full wait again. Fix round 2 (review finding R3) counts this case.
	AbandonReasonUnconfirmed

	// AbandonReasonShutdown means the queue itself is going away, so nothing
	// can be concluded about the target: never counted against the breaker,
	// never written to the DLQ (the notify chain re-publishes after restart,
	// and a DLQ replay would double-deliver). PublishingQueue.Stop's force
	// cancel reaches jobs through q.ctx without any Abandon call, so it
	// arrives as the AbandonReasonSettled zero value — which is treated the
	// same way. This constant exists for callers that abandon explicitly
	// during a shutdown sequence.
	AbandonReasonShutdown
)

func (r AbandonReason) String() string {
	switch r {
	case AbandonReasonSettled:
		return "settled"
	case AbandonReasonUnconfirmed:
		return "unconfirmed"
	case AbandonReasonShutdown:
		return "shutdown"
	default:
		return "unknown"
	}
}

// GroupPublishHandle is what SubmitGroupWithConfirmation hands back: the
// single-outcome channel for one (group, target) job plus the ability to
// abandon that job (task rec fix round 1, review finding I2).
type GroupPublishHandle struct {
	done    <-chan error
	abandon context.CancelFunc
	job     *PublishingJob
}

// Done yields exactly one value: the job's final delivery outcome (nil ==
// confirmed delivery). Never closed — see jobCompletion.
func (h *GroupPublishHandle) Done() <-chan error {
	if h == nil {
		return nil
	}
	return h.done
}

// DeliveredAlerts reports the core.Alert.DeliveryKey of every alert this job
// has confirmed accepted so far (task fu4, alertmanager-parity wave 4). Nil
// for a batch-capable target — one POST covers the whole set, so there is no
// per-alert notion to report — and nil for a non-batch job that got nowhere.
//
// Meaningful on BOTH outcomes:
//
//   - Done() reported failure: these alerts still landed, and the notify chain
//     records them so the retry fire skips them (the whole point of the task).
//   - Done() never fired (the waiter's confirmation timeout elapsed): the
//     snapshot is whatever the still-running job had delivered when it was
//     read, which is strictly better than assuming nothing arrived.
//
// Safe to call at any time from the submitting goroutine, including while the
// worker is still publishing: the value read is an immutable slice published
// through an atomic pointer, never the worker's own mutable tracker (see
// perAlertProgress). A read that races an in-flight alert may therefore MISS
// a delivery that just succeeded, which is the safe direction — an unrecorded
// delivery costs one duplicate on the next fire, whereas a wrongly-recorded
// one would silently drop the notification for a whole repeat_interval.
func (h *GroupPublishHandle) DeliveredAlerts() []string {
	if h == nil || h.job == nil {
		return nil
	}
	return h.job.deliveredSnapshot()
}

// Abandon cancels the job's context: an in-flight HTTP request is aborted and
// any pending retry back-off unwinds immediately, freeing the worker.
//
// Idempotent and safe to call after the job already finished (then it is just
// the context cleanup `go vet -lostcancel` wants), so callers should
// `defer handle.Abandon(reason)` unconditionally rather than only on timeout —
// AbandonReasonSettled once an outcome has been read, AbandonReasonUnconfirmed
// when the wait was given up on. reason is stored on the job BEFORE the
// cancellation, so the worker observing the cancelled context can classify it
// (see processJob's abandon branch).
//
// WHY ABANDONING IS THE RIGHT DEFAULT: nobody is left who could act on this
// job's outcome — its waiter already reported the target as unconfirmed, and
// the notify chain will re-publish to it on the group's next fire. Letting it
// run would burn a worker for up to the queue's full retry budget and push
// healthy targets' jobs behind it, turning one hanging endpoint into
// unconfirmed (⇒ duplicated) notifications for endpoints that are fine.
func (h *GroupPublishHandle) Abandon(reason AbandonReason) {
	if h == nil || h.abandon == nil {
		return
	}
	if h.job != nil {
		h.job.abandonReason.Store(int32(reason))
	}
	h.abandon()
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

	// ctx, when non-nil, scopes THIS job's publish attempts and retry
	// back-off (task rec fix round 1, review finding I2). It is derived from
	// the queue's own context, so a queue shutdown still cancels it, and it
	// is cancelled early by GroupPublishHandle.Abandon when the submitter
	// stops waiting for the outcome. Nil (Submit/SubmitGroup) means "use the
	// queue context", i.e. the pre-fix-round behaviour.
	ctx context.Context

	// progress tracks which alerts of a NON-BATCH group job have been accepted
	// so far (task fu4). Worker-owned: created lazily by publishJob and
	// mutated only by the single worker goroutine running this job, so it
	// carries no lock — see perAlertProgress.
	progress *perAlertProgress

	// deliveredAlerts publishes progress's immutable snapshot to the
	// submitting goroutine (task fu4). Atomic because the waiter may read it
	// while the worker is still publishing — a job whose confirmation wait
	// expired is abandoned, not joined, so there is no happens-before edge to
	// lean on there. Read through deliveredSnapshot / exposed by
	// GroupPublishHandle.DeliveredAlerts.
	deliveredAlerts atomic.Pointer[[]string]

	// abandonReason carries the AbandonReason the waiter set when it gave up,
	// read by the worker once it sees ctx cancelled (task rec fix round 2,
	// review finding R3). Zero value is AbandonReasonSettled, which is also
	// the right reading for "cancelled without any Abandon call", i.e. a queue
	// shutdown — see the abandon branch in processJob. Atomic because it is
	// written by the submitting goroutine and read by the worker.
	abandonReason atomic.Int32

	// Extended fields for 150% quality
	ID          string         // UUID v4
	Priority    Priority       // HIGH/MEDIUM/LOW
	State       JobState       // queued/processing/retrying/succeeded/failed/dlq
	StartedAt   *time.Time     // When processing began
	CompletedAt *time.Time     // When processing completed
	LastError   error          // Most recent error
	ErrorType   QueueErrorType // transient/permanent/unknown
}

// deliveredSnapshot loads the last published per-alert delivery snapshot for
// this job (task fu4), or nil when the job has confirmed nothing (or is a
// batch/single-alert job, which never records per-alert progress).
func (j *PublishingJob) deliveredSnapshot() []string {
	if j == nil {
		return nil
	}
	if snapshot := j.deliveredAlerts.Load(); snapshot != nil {
		return *snapshot
	}
	return nil
}

// publishProgress records one accepted alert and republishes the snapshot the
// waiter reads (task fu4). Called by the worker only.
//
// The store happens after every accepted alert rather than once at the end so
// a job that is abandoned mid-flight — its waiter's confirmation timeout
// elapsed while alert 4 of 5 was in flight — still hands back the alerts that
// did land. Skipped entirely for jobs with no completion channel
// (Submit/SubmitGroup, fire-and-forget): nobody can read the snapshot, so
// maintaining it would be pure allocation.
func (j *PublishingJob) publishProgress(key string) {
	if j == nil || j.progress == nil {
		return
	}
	j.progress.add(key)
	if j.completion == nil {
		return
	}
	snapshot := j.progress.snapshot()
	j.deliveredAlerts.Store(&snapshot)
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
		// Fallback: reuse the process-wide singleton registry (sync.Once
		// under the hood, see pkg/metrics/v2.Global) rather than calling
		// v2.NewRegistry() here. NewRegistry() with no explicit registerer
		// defaults to prometheus.DefaultRegisterer and re-registers every
		// PublishingMetrics collector on every call — a second
		// NewPublishingQueue in the same process (multiple instances, or
		// repeated construction across tests in one binary) then panics
		// with "duplicate metrics collector registration". v2.Global()
		// registers exactly once no matter how many times it's called.
		metrics = v2.Global().Publishing
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
// IN-FLIGHT DEDUP — why there is none (fix round 1, review finding I2): a
// second job for the same (groupKey, target) cannot be submitted while the
// first is still being awaited, because the notify chain serializes fires per
// group key (grouping.groupPublishLocks, held across the whole publish) and
// across replicas (GroupNotifyLog.TryClaim). The only overlap window is the
// tail of an ABANDONED job — cancelled, unwinding — which is bounded by how
// fast the HTTP client honours cancellation, not by the retry budget. A
// (group, target) in-flight registry would therefore add shared mutable state
// to guard a window the locking already bounds.
//
// The handle's channel is buffered, so a caller that gives up waiting
// (CoordinatorConfig.DeliveryConfirmationTimeout) leaks nothing and the
// worker never blocks. Such a caller MUST call GroupPublishHandle.Abandon to
// release the job's context; `defer handle.Abandon()` right after a
// successful submit is the intended shape. A non-nil error return means the
// job was never enqueued at all (queue full / shutting down) and the handle
// is nil — there is nothing to wait for and nothing to abandon.
func (q *PublishingQueue) SubmitGroupWithConfirmation(alerts []*core.Alert, target *core.PublishingTarget, groupKey string, receiver string, groupLabels map[string]string) (*GroupPublishHandle, error) {
	return q.submitGroupJob(alerts, target, groupKey, receiver, groupLabels, true)
}

// submitGroupJob is the shared body of SubmitGroup (withConfirmation ==
// false, fire-and-forget, nil handle) and SubmitGroupWithConfirmation (true).
func (q *PublishingQueue) submitGroupJob(alerts []*core.Alert, target *core.PublishingTarget, groupKey string, receiver string, groupLabels map[string]string, withConfirmation bool) (*GroupPublishHandle, error) {
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

	if !withConfirmation {
		if err := q.submitJob(job, priority, fmt.Sprintf("group:%s alerts=%d", groupKey, len(alerts))); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Confirmed-delivery submission: the job gets its own cancellable context
	// (rooted in the queue's, so shutdown still reaches it) plus a completion
	// channel. Cancelling that context is how a caller that stops waiting
	// frees the worker — see GroupPublishHandle.Abandon.
	jobCtx, abandon := context.WithCancel(q.ctx)
	job.ctx = jobCtx
	job.completion = newJobCompletion()

	if err := q.submitJob(job, priority, fmt.Sprintf("group:%s alerts=%d", groupKey, len(alerts))); err != nil {
		// Never enqueued ⇒ no worker will ever signal this job's completion,
		// so hand back a nil handle rather than one that would block until
		// the caller's timeout, and release the context here.
		abandon()
		return nil, err
	}

	return &GroupPublishHandle{done: job.completion.ch, abandon: abandon, job: job}, nil
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

				// Close the job out properly (fix round 1, review finding M3):
				// before this, a skipped job kept its "queued" tracking
				// snapshot forever, so JobTrackingStore/GetStats reported jobs
				// that no worker would ever touch again as still pending.
				job.State = JobStateFailed
				skippedAt := time.Now()
				job.CompletedAt = &skippedAt
				if q.jobTrackingStore != nil {
					q.jobTrackingStore.Add(job)
				}

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
		// CreateBasicPublisherForTarget, not CreatePublisher: same basic
		// publisher, but honouring the target's `http_config`
		// (FU-HTTP-CONFIG). Webhook targets stay on the BASIC publisher for the
		// validation reason above, and they are the most likely users of
		// http_config — a corp-proxied or mTLS internal endpoint — so ignoring
		// it here would make the feature dead on the queue path.
		return q.factory.CreateBasicPublisherForTarget(job.Target)
	}
}

// jobWasAbandoned reports whether job's delivery attempt was genuinely
// abandoned — cancelled before it produced any real outcome — as opposed to
// one that SETTLED (successfully, or with a real failure) even though
// job.ctx happens to already be cancelled by the time this runs.
//
// Review finding M-c: a job whose final HTTP attempt fails (500, refused) at
// essentially the same instant the waiter's confirmation-wait timeout fires
// can have job.ctx already cancelled by handle.Abandon by the time processJob
// checks it, even though that cancellation had nothing to do with the
// attempt's own, already-decided outcome. job.ctx is a plain
// context.WithCancel, so its Err() is always exactly context.Canceled once
// set — checking that the RETURNED error itself wraps that cancellation (the
// attempt was actually aborted mid-flight, not merely coincident with a
// now-cancelled context) is what tells "never settled" apart from "settled,
// but raced". A settled job must keep its normal failure path (breaker + DLQ
// decision), never lose its DLQ entry to the abandon branch.
func jobWasAbandoned(job *PublishingJob, err error) bool {
	return err != nil && job.ctx != nil && job.ctx.Err() != nil && errors.Is(err, context.Canceled)
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

	// Abandoned job (task rec fix round 1, review finding I2; classified in
	// round 2 per finding R3): its context was cancelled BEFORE IT FINISHED —
	// i.e. cancellation is what ended the attempt, not a coincidence. Never a
	// DLQ entry — the notify chain re-publishes to any target it could not
	// confirm on the group's next fire, so a DLQ record here is a duplicate
	// waiting to be replayed.
	//
	// errors.Is(err, context.Canceled) (rather than just job.ctx.Err() != nil)
	// is the fix for review finding M-c: a job whose final HTTP attempt
	// genuinely SETTLES (success, or a real 500/refused failure) at
	// essentially the same instant the waiter's confirmation-wait timeout
	// fires can have job.ctx already cancelled by handle.Abandon by the time
	// this runs, even though that cancellation had nothing to do with the
	// attempt's own outcome. job.ctx is a plain context.WithCancel, so its
	// Err() is always exactly context.Canceled once set — checking that the
	// RETURNED error itself is a cancellation (the request was actually
	// aborted mid-flight) distinguishes "never settled" from "settled with a
	// real failure that happened to race the ctx cancellation", which must
	// keep its normal failure path (breaker + DLQ decision) below instead of
	// silently losing its DLQ entry.
	//
	// The circuit breaker, though, depends on WHY:
	//
	//   - AbandonReasonUnconfirmed (the waiter's delivery-confirmation timeout
	//     elapsed): the target was given a notification and did not answer in
	//     time. That IS a failure and must count, otherwise a HANGING target —
	//     unlike one that 500s or refuses the connection, which fails fast and
	//     completes normally — never opens its breaker and every fire keeps
	//     paying the full wait.
	//   - anything else (queue shutdown, or a handle released after its
	//     outcome was already read): says nothing about the target, so the
	//     breaker is left alone.
	if jobWasAbandoned(job, err) {
		reason := AbandonReason(job.abandonReason.Load())

		q.totalFailed.Add(1)
		job.State = JobStateFailed
		completedAt := time.Now()
		job.CompletedAt = &completedAt
		if q.jobTrackingStore != nil {
			q.jobTrackingStore.Add(job)
		}

		if reason == AbandonReasonUnconfirmed {
			cb.RecordFailure()
		}
		if q.metrics != nil {
			// Observability for exactly the hanging-endpoint case above, which
			// otherwise shows up on no dashboard at all (review finding R5).
			q.metrics.RecordJobAbandoned(job.Target.Name, reason.String())
		}

		q.logger.Warn("Publish abandoned (delivery confirmation no longer awaited)",
			"job_id", job.ID,
			"target", job.Target.Name,
			"fingerprint", job.EnrichedAlert.Alert.Fingerprint,
			"reason", reason,
			"counted_against_circuit_breaker", reason == AbandonReasonUnconfirmed,
			"error", err,
		)
		return
	}

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
//     Best-effort: every alert still owed is attempted even if an earlier one
//     fails, and the first error (if any) is what the retry strategy above
//     sees.
//
// PER-ALERT OUTCOMES (task fu4, alertmanager-parity wave 4) apply to that
// third case only. Each accepted alert is recorded in job.progress, and the
// loop SKIPS alerts already recorded, which fixes two distinct duplicate
// sources:
//
//   - a retry of this job (retryPublish calls this function again) no longer
//     resends the alerts that already landed on the previous attempt — the
//     trade-off the pre-fu4 comment documented as accepted;
//   - the accumulated keys travel back to the notify chain (see
//     GroupPublishHandle.DeliveredAlerts), which records them as the target's
//     delivered set so the group's NEXT fire sends only the alerts still owed
//     instead of the whole set.
//
// The batch branch deliberately records nothing: one POST either delivers the
// whole set or none of it, so per-target confirmation is already exact and
// wave-3 semantics stay untouched.
func (q *PublishingQueue) publishJob(publisher AlertPublisher, job *PublishingJob) error {
	ctx := q.jobContext(job)

	if len(job.Alerts) == 0 {
		return publisher.Publish(ctx, job.EnrichedAlert, job.Target)
	}

	if batchPublisher, ok := publisher.(BatchAlertPublisher); ok {
		return batchPublisher.PublishBatch(ctx, job.Alerts, job.GroupKey, job.Receiver, job.GroupLabels, job.Target)
	}

	if job.progress == nil {
		job.progress = newPerAlertProgress(len(job.Alerts))
	}

	now := time.Now().UTC()
	var firstErr error
	for _, alert := range job.Alerts {
		// An alert with no fingerprint cannot be tracked: every such alert would
		// share the key ":<status>" and the second one would be skipped as
		// "already delivered" (review round 1, finding m5). Unreachable through
		// the grouping path — groups index their alerts BY fingerprint — so this
		// is a guard, not a fix: an untrackable alert is always sent and never
		// recorded, i.e. it falls back to at-least-once.
		key := ""
		if alert != nil && alert.Fingerprint != "" {
			key = alert.DeliveryKey()
		}

		if job.progress.has(key) {
			// Already accepted on an earlier attempt of THIS job — re-sending
			// it would be a duplicate wire message, not a retry.
			continue
		}

		enrichedAlert := &core.EnrichedAlert{Alert: alert, ProcessingTimestamp: &now}
		if err := publisher.Publish(ctx, enrichedAlert, job.Target); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		job.publishProgress(key)
	}
	return firstErr
}

// jobContext is the context a job's publish attempts run under: the job's own
// cancellable context when it has one (task rec fix round 1, review finding
// I2 — cancelled by GroupPublishHandle.Abandon so an abandoned delivery stops
// occupying a worker), otherwise the queue-wide context, which is the
// pre-fix-round behaviour for Submit/SubmitGroup jobs.
func (q *PublishingQueue) jobContext(job *PublishingJob) context.Context {
	ctx := q.ctx
	if job.ctx != nil {
		ctx = job.ctx
	}

	// TEMPLATES-EPIC slice 2: carry the group's identity/labels/receiver so the
	// template renderer can build upstream's Data with a populated
	// `.GroupLabels` and `.Receiver`. Only attached when the job actually came
	// from the group path; see notification_context.go for why it travels on the
	// context instead of through AlertPublisher's signature.
	if job.GroupKey != "" || job.Receiver != "" || len(job.GroupLabels) > 0 {
		// The labels are COPIED, not aliased (slice-2 review minor 6). The group
		// is sealed before dispatch today, so sharing the map would be safe — but
		// it is read during template rendering on a worker goroutine while the
		// job object stays reachable from the caller, and one small allocation
		// per job is a cheaper guarantee than that reasoning.
		groupLabels := make(map[string]string, len(job.GroupLabels))
		for key, value := range job.GroupLabels {
			groupLabels[key] = value
		}

		ctx = withGroupNotificationContext(ctx, GroupNotificationContext{
			GroupKey:    job.GroupKey,
			Receiver:    job.Receiver,
			GroupLabels: groupLabels,
		})
	}
	return ctx
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

	// Execute publish with retry, on the JOB's context (fix round 1, finding
	// I2): abandoning a job must also unwind its pending retry back-off, not
	// just cancel the HTTP request in flight.
	err := retry.DoSimple(q.jobContext(job), strategy, func() error {
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
