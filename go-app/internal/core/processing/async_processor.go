package processing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// AlertHandler defines the interface for processing alerts.
// This is the same interface used by UniversalWebhookHandler.
type AlertHandler interface {
	ProcessAlert(ctx context.Context, alert *core.Alert) error
}

// AsyncWebhookProcessor provides asynchronous webhook processing with a worker pool.
//
// Features:
//   - Bounded job queue to prevent memory exhaustion
//   - Configurable number of workers
//   - Graceful shutdown with timeout
//   - Metrics for queue size and active workers
//   - Context cancellation support
type AsyncWebhookProcessor struct {
	handler   AlertHandler
	metrics   *metrics.WebhookMetrics
	logger    *slog.Logger
	workers   int
	queueSize int
	jobQueue  chan *WebhookJob
	stopChan  chan struct{}
	wg        sync.WaitGroup
	mu        sync.RWMutex
	running   bool
}

// WebhookJob represents a single webhook processing job.
type WebhookJob struct {
	ID        string
	Alerts    []*core.Alert
	CreatedAt time.Time
}

// AsyncProcessorConfig holds configuration for AsyncWebhookProcessor.
type AsyncProcessorConfig struct {
	Handler   AlertHandler
	Metrics   *metrics.WebhookMetrics
	Logger    *slog.Logger
	Workers   int // Number of worker goroutines (default: 10)
	QueueSize int // Maximum queue size (default: 1000)
}

// NewAsyncWebhookProcessor creates a new async webhook processor.
//
// Parameters:
//   - config: Configuration with handler, metrics, and worker settings
//
// Returns:
//   - *AsyncWebhookProcessor: Initialized processor (not started yet)
//   - error: Configuration validation error
func NewAsyncWebhookProcessor(config AsyncProcessorConfig) (*AsyncWebhookProcessor, error) {
	if config.Handler == nil {
		return nil, fmt.Errorf("handler is required")
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	if config.Workers <= 0 {
		config.Workers = 10 // Default: 10 workers
	}

	if config.QueueSize <= 0 {
		config.QueueSize = 1000 // Default: 1000 jobs
	}

	if config.Metrics == nil {
		config.Metrics = metrics.NewWebhookMetrics()
	}

	return &AsyncWebhookProcessor{
		handler:   config.Handler,
		metrics:   config.Metrics,
		logger:    config.Logger,
		workers:   config.Workers,
		queueSize: config.QueueSize,
		jobQueue:  make(chan *WebhookJob, config.QueueSize),
		stopChan:  make(chan struct{}),
	}, nil
}

// Start starts the worker pool.
//
// This method spawns worker goroutines that will process jobs from the queue.
// It's safe to call Start multiple times (subsequent calls are no-ops).
//
// Parameters:
//   - ctx: Context for cancellation (workers will stop when context is cancelled)
//
// Returns:
//   - error: Start error (e.g., already running)
func (p *AsyncWebhookProcessor) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return fmt.Errorf("processor already running")
	}

	p.running = true

	// Start worker goroutines
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}

	// Start queue size monitor
	p.wg.Add(1)
	go p.queueMonitor(ctx)

	p.logger.Info("Async webhook processor started",
		"workers", p.workers,
		"queue_size", p.queueSize)

	return nil
}

// Stop gracefully stops the worker pool.
//
// This method:
//  1. Closes the job queue (no new jobs accepted)
//  2. Waits for all workers to finish current jobs
//  3. Times out after 30 seconds
//
// Returns:
//   - error: Stop error (e.g., timeout)
func (p *AsyncWebhookProcessor) Stop() error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return fmt.Errorf("processor not running")
	}
	p.running = false
	p.mu.Unlock()

	p.logger.Info("Stopping async webhook processor...")

	// Signal workers to stop
	close(p.stopChan)

	// Close job queue (no new jobs)
	close(p.jobQueue)

	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("Async webhook processor stopped gracefully")
		return nil
	case <-time.After(30 * time.Second):
		p.logger.Warn("Async webhook processor stop timeout (some jobs may be lost)")
		return fmt.Errorf("stop timeout after 30 seconds")
	}
}

// SubmitJob submits a webhook processing job to the queue.
//
// This method is non-blocking if the queue has space.
// If the queue is full, it returns an error immediately.
//
// Parameters:
//   - ctx: Context for cancellation
//   - job: Webhook job to process
//
// Returns:
//   - error: Queue full error or processor not running
func (p *AsyncWebhookProcessor) SubmitJob(ctx context.Context, job *WebhookJob) error {
	p.mu.RLock()
	if !p.running {
		p.mu.RUnlock()
		return fmt.Errorf("processor not running")
	}
	p.mu.RUnlock()

	select {
	case p.jobQueue <- job:
		p.logger.Debug("Job submitted to queue",
			"job_id", job.ID,
			"alerts_count", len(job.Alerts))
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Queue is full
		p.logger.Warn("Job queue full, rejecting job",
			"job_id", job.ID,
			"queue_size", p.queueSize)
		return fmt.Errorf("job queue full (capacity: %d)", p.queueSize)
	}
}

// worker processes jobs from the queue.
func (p *AsyncWebhookProcessor) worker(ctx context.Context, id int) {
	defer p.wg.Done()

	p.logger.Debug("Worker started", "worker_id", id)
	p.metrics.IncrementActiveWorkers()
	defer p.metrics.DecrementActiveWorkers()

	for {
		select {
		case <-ctx.Done():
			p.logger.Debug("Worker stopped by context", "worker_id", id)
			return
		case <-p.stopChan:
			p.logger.Debug("Worker stopped by signal", "worker_id", id)
			return
		case job, ok := <-p.jobQueue:
			if !ok {
				p.logger.Debug("Worker stopped (queue closed)", "worker_id", id)
				return
			}

			p.processJob(ctx, job, id)
		}
	}
}

// processJob processes a single webhook job.
func (p *AsyncWebhookProcessor) processJob(ctx context.Context, job *WebhookJob, workerID int) {
	startTime := time.Now()

	p.logger.Debug("Processing job",
		"worker_id", workerID,
		"job_id", job.ID,
		"alerts_count", len(job.Alerts))

	// Process each alert in the job
	successCount := 0
	for i, alert := range job.Alerts {
		if err := p.handler.ProcessAlert(ctx, alert); err != nil {
			p.logger.Error("Failed to process alert in async job",
				"worker_id", workerID,
				"job_id", job.ID,
				"alert_index", i,
				"alert_name", alert.AlertName,
				"error", err)
			continue
		}
		successCount++
	}

	duration := time.Since(startTime).Seconds()

	p.logger.Info("Job processed",
		"worker_id", workerID,
		"job_id", job.ID,
		"alerts_total", len(job.Alerts),
		"alerts_success", successCount,
		"duration", duration)
}

// queueMonitor periodically updates queue size metrics.
func (p *AsyncWebhookProcessor) queueMonitor(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopChan:
			return
		case <-ticker.C:
			queueLen := len(p.jobQueue)
			p.metrics.SetQueueSize(queueLen)

			if queueLen > p.queueSize*8/10 { // 80% full
				p.logger.Warn("Job queue high utilization",
					"current", queueLen,
					"capacity", p.queueSize,
					"utilization_pct", float64(queueLen)/float64(p.queueSize)*100)
			}
		}
	}
}

// Stats returns current processor statistics.
type ProcessorStats struct {
	Running       bool
	Workers       int
	QueueSize     int
	CurrentQueue  int
	QueueCapacity int
}

// GetStats returns current processor statistics.
func (p *AsyncWebhookProcessor) GetStats() *ProcessorStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return &ProcessorStats{
		Running:       p.running,
		Workers:       p.workers,
		QueueSize:     p.queueSize,
		CurrentQueue:  len(p.jobQueue),
		QueueCapacity: cap(p.jobQueue),
	}
}
