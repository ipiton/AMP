package v2

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestActiveJobsTracker_StartEndJob(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Start a job
	jobID := tracker.StartJob("slack-prod", "high")
	assert.NotEmpty(t, jobID, "Job ID should not be empty")

	// Check active job count
	count := tracker.GetActiveJobCount()
	assert.Equal(t, 1, count, "Should have 1 active job")

	// End the job
	tracker.EndJob(jobID)

	// Check active job count after ending
	count = tracker.GetActiveJobCount()
	assert.Equal(t, 0, count, "Should have 0 active jobs after ending")
}

func TestActiveJobsTracker_MultipleJobs(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Start multiple jobs
	jobID1 := tracker.StartJob("slack-prod", "high")
	jobID2 := tracker.StartJob("pagerduty-prod", "medium")
	jobID3 := tracker.StartJob("slack-prod", "low")

	// Check active job count
	count := tracker.GetActiveJobCount()
	assert.Equal(t, 3, count, "Should have 3 active jobs")

	// Get active jobs
	jobs := tracker.GetActiveJobs()
	assert.Len(t, jobs, 3, "Should return 3 active jobs")

	// End one job
	tracker.EndJob(jobID2)

	// Check active job count after ending one
	count = tracker.GetActiveJobCount()
	assert.Equal(t, 2, count, "Should have 2 active jobs after ending one")

	// End remaining jobs
	tracker.EndJob(jobID1)
	tracker.EndJob(jobID3)

	// Check final count
	count = tracker.GetActiveJobCount()
	assert.Equal(t, 0, count, "Should have 0 active jobs after ending all")
}

func TestActiveJobsTracker_GetActiveJobsByTarget(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Start jobs for different targets
	tracker.StartJob("slack-prod", "high")
	tracker.StartJob("slack-prod", "medium")
	tracker.StartJob("pagerduty-prod", "high")
	tracker.StartJob("rootly-prod", "low")

	// Get jobs for slack-prod
	slackJobs := tracker.GetActiveJobsByTarget("slack-prod")
	assert.Len(t, slackJobs, 2, "Should have 2 slack-prod jobs")

	// Get jobs for pagerduty-prod
	pdJobs := tracker.GetActiveJobsByTarget("pagerduty-prod")
	assert.Len(t, pdJobs, 1, "Should have 1 pagerduty-prod job")

	// Get jobs for non-existent target
	nonExistentJobs := tracker.GetActiveJobsByTarget("non-existent")
	assert.Len(t, nonExistentJobs, 0, "Should have 0 jobs for non-existent target")
}

func TestActiveJobsTracker_JobDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Start a job
	jobID := tracker.StartJob("slack-prod", "high")

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// End the job
	tracker.EndJob(jobID)

	// Metrics should be recorded (we can't easily test histogram values,
	// but we can verify no panics occurred)
	assert.Equal(t, 0, tracker.GetActiveJobCount(), "Should have 0 active jobs")
}

func TestActiveJobsTracker_EndNonExistentJob(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Try to end a job that doesn't exist (should not panic)
	tracker.EndJob("non-existent-job-id")

	// Should still have 0 active jobs
	count := tracker.GetActiveJobCount()
	assert.Equal(t, 0, count, "Should have 0 active jobs")
}

func TestActiveJobsTracker_ConcurrentAccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Start multiple jobs concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			jobID := tracker.StartJob("slack-prod", "high")
			time.Sleep(10 * time.Millisecond)
			tracker.EndJob(jobID)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 0 active jobs after all complete
	count := tracker.GetActiveJobCount()
	assert.Equal(t, 0, count, "Should have 0 active jobs after concurrent access")
}

func TestActiveJobsTracker_GetActiveJobs_Copy(t *testing.T) {
	reg := prometheus.NewRegistry()
	tracker := NewActiveJobsTracker(reg)

	// Start a job
	jobID := tracker.StartJob("slack-prod", "high")

	// Get active jobs
	jobs := tracker.GetActiveJobs()
	assert.Len(t, jobs, 1, "Should have 1 active job")

	// Modify the returned job (should not affect internal state)
	jobs[0].Target = "modified"

	// Get active jobs again
	jobs2 := tracker.GetActiveJobs()
	assert.Equal(t, "slack-prod", jobs2[0].Target, "Internal state should not be modified")

	// Clean up
	tracker.EndJob(jobID)
}
