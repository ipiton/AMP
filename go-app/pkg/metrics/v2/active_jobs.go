package v2

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ActiveJobsTracker tracks active jobs in real-time for monitoring and debugging.
//
// Features:
//   - Real-time active job count
//   - Job duration tracking
//   - Per-target job tracking
//   - Thread-safe implementation
//   - Automatic cleanup of completed jobs
//
// Usage:
//
//	tracker := v2.NewActiveJobsTracker(registry)
//	jobID := tracker.StartJob("slack-prod", "high")
//	defer tracker.EndJob(jobID)
type ActiveJobsTracker struct {
	// Metrics
	activeJobsGauge     *prometheus.GaugeVec
	jobDurationHistogram *prometheus.HistogramVec

	// Job tracking
	jobs map[string]*ActiveJob
	mu   sync.RWMutex
}

// ActiveJob represents an active job being tracked.
type ActiveJob struct {
	ID        string
	Target    string
	Priority  string
	StartTime time.Time
}

// NewActiveJobsTracker creates a new active jobs tracker.
//
// Parameters:
//   - registerer: Prometheus registerer for metrics
//
// Returns:
//   - *ActiveJobsTracker: Configured tracker
func NewActiveJobsTracker(registerer prometheus.Registerer) *ActiveJobsTracker {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}

	tracker := &ActiveJobsTracker{
		jobs: make(map[string]*ActiveJob),
		activeJobsGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "publishing",
				Name:      "active_jobs",
				Help:      "Number of active publishing jobs by target and priority",
			},
			[]string{"target", "priority"},
		),
		jobDurationHistogram: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "publishing",
				Name:      "job_duration_seconds",
				Help:      "Duration of publishing jobs in seconds",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
			[]string{"target", "priority"},
		),
	}

	// Register metrics
	registerer.MustRegister(tracker.activeJobsGauge)
	registerer.MustRegister(tracker.jobDurationHistogram)

	return tracker
}

// StartJob starts tracking a new job.
//
// Parameters:
//   - target: Target name (e.g., "slack-prod")
//   - priority: Job priority (e.g., "high", "medium", "low")
//
// Returns:
//   - string: Job ID for later reference
func (t *ActiveJobsTracker) StartJob(target, priority string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Generate job ID (simple counter-based for now)
	jobID := generateJobID()

	// Create job
	job := &ActiveJob{
		ID:        jobID,
		Target:    target,
		Priority:  priority,
		StartTime: time.Now(),
	}

	// Track job
	t.jobs[jobID] = job

	// Update metrics
	t.activeJobsGauge.WithLabelValues(target, priority).Inc()

	return jobID
}

// EndJob stops tracking a job and records its duration.
//
// Parameters:
//   - jobID: Job ID returned by StartJob
func (t *ActiveJobsTracker) EndJob(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Get job
	job, exists := t.jobs[jobID]
	if !exists {
		return
	}

	// Calculate duration
	duration := time.Since(job.StartTime)

	// Update metrics
	t.activeJobsGauge.WithLabelValues(job.Target, job.Priority).Dec()
	t.jobDurationHistogram.WithLabelValues(job.Target, job.Priority).Observe(duration.Seconds())

	// Remove job
	delete(t.jobs, jobID)
}

// GetActiveJobs returns a snapshot of currently active jobs.
//
// Returns:
//   - []*ActiveJob: List of active jobs
func (t *ActiveJobsTracker) GetActiveJobs() []*ActiveJob {
	t.mu.RLock()
	defer t.mu.RUnlock()

	jobs := make([]*ActiveJob, 0, len(t.jobs))
	for _, job := range t.jobs {
		// Create copy to avoid race conditions
		jobCopy := &ActiveJob{
			ID:        job.ID,
			Target:    job.Target,
			Priority:  job.Priority,
			StartTime: job.StartTime,
		}
		jobs = append(jobs, jobCopy)
	}

	return jobs
}

// GetActiveJobCount returns the number of active jobs.
//
// Returns:
//   - int: Number of active jobs
func (t *ActiveJobsTracker) GetActiveJobCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.jobs)
}

// GetActiveJobsByTarget returns active jobs for a specific target.
//
// Parameters:
//   - target: Target name
//
// Returns:
//   - []*ActiveJob: List of active jobs for the target
func (t *ActiveJobsTracker) GetActiveJobsByTarget(target string) []*ActiveJob {
	t.mu.RLock()
	defer t.mu.RUnlock()

	jobs := make([]*ActiveJob, 0)
	for _, job := range t.jobs {
		if job.Target == target {
			// Create copy to avoid race conditions
			jobCopy := &ActiveJob{
				ID:        job.ID,
				Target:    job.Target,
				Priority:  job.Priority,
				StartTime: job.StartTime,
			}
			jobs = append(jobs, jobCopy)
		}
	}

	return jobs
}

// jobIDCounter is a simple counter for generating job IDs
var (
	jobIDCounter uint64
	jobIDMu      sync.Mutex
)

// generateJobID generates a unique job ID
func generateJobID() string {
	jobIDMu.Lock()
	defer jobIDMu.Unlock()

	jobIDCounter++
	return fmt.Sprintf("job-%d-%d", time.Now().Unix(), jobIDCounter)
}
