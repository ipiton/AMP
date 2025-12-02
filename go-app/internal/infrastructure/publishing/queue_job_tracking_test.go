package publishing

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Helper to create sample job snapshot for testing
func createSampleJobSnapshot() *JobSnapshot {
	now := time.Now().Unix()
	return &JobSnapshot{
		ID:          uuid.New().String(),
		Fingerprint: "test-fp-123",
		TargetName:  "test-target",
		Priority:    "high",
		State:       "queued",
		SubmittedAt: now,
		RetryCount:  0,
	}
}

// TestLRUJobTrackingStore_Add tests adding jobs to tracking store
func TestLRUJobTrackingStore_Add(t *testing.T) {
	store := NewLRUJobTrackingStore(10)

	job := createSampleDLQJob()
	store.Add(job)

	// Verify job was added
	snapshot := store.Get(job.ID)
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot after Add")
	}

	if snapshot.ID != job.ID {
		t.Errorf("Expected job ID %s, got %s", job.ID, snapshot.ID)
	}
	if snapshot.TargetName != job.Target.Name {
		t.Errorf("Expected target name %s, got %s", job.Target.Name, snapshot.TargetName)
	}
}

// TestLRUJobTrackingStore_Get tests retrieving jobs from store
func TestLRUJobTrackingStore_Get(t *testing.T) {
	store := NewLRUJobTrackingStore(10)

	job := createSampleDLQJob()
	store.Add(job)

	// Get existing job
	snapshot := store.Get(job.ID)
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot for existing job")
	}

	// Get non-existent job
	nonExistent := store.Get("non-existent-id")
	if nonExistent != nil {
		t.Errorf("Expected nil snapshot for non-existent job, got %v", nonExistent)
	}
}

// TestLRUJobTrackingStore_List tests listing jobs with filters
func TestLRUJobTrackingStore_List(t *testing.T) {
	store := NewLRUJobTrackingStore(100)

	// Add multiple jobs
	for i := 0; i < 5; i++ {
		job := createSampleDLQJob()
		job.Priority = PriorityHigh
		job.State = JobStateQueued
		store.Add(job)
	}

	for i := 0; i < 3; i++ {
		job := createSampleDLQJob()
		job.Priority = PriorityLow
		job.State = JobStateFailed
		store.Add(job)
	}

	// List all (default limit 100)
	all := store.List(JobFilters{})
	if len(all) != 8 {
		t.Errorf("Expected 8 total jobs, got %d", len(all))
	}

	// List high priority jobs
	highPriority := store.List(JobFilters{Priority: "high", Limit: 10})
	if len(highPriority) != 5 {
		t.Errorf("Expected 5 high priority jobs, got %d", len(highPriority))
	}

	// List failed jobs
	failed := store.List(JobFilters{State: "failed", Limit: 10})
	if len(failed) != 3 {
		t.Errorf("Expected 3 failed jobs, got %d", len(failed))
	}

	// List with limit
	limited := store.List(JobFilters{Limit: 3})
	if len(limited) != 3 {
		t.Errorf("Expected 3 jobs with limit, got %d", len(limited))
	}
}

// TestLRUJobTrackingStore_Remove tests removing jobs from store
func TestLRUJobTrackingStore_Remove(t *testing.T) {
	store := NewLRUJobTrackingStore(10)

	job := createSampleDLQJob()
	store.Add(job)

	// Verify job exists
	if store.Get(job.ID) == nil {
		t.Fatal("Job should exist before Remove")
	}

	// Remove job
	store.Remove(job.ID)

	// Verify job removed
	if store.Get(job.ID) != nil {
		t.Errorf("Job should be removed, but still exists")
	}
}

// TestLRUJobTrackingStore_Clear tests clearing all jobs
func TestLRUJobTrackingStore_Clear(t *testing.T) {
	store := NewLRUJobTrackingStore(10)

	// Add multiple jobs
	for i := 0; i < 5; i++ {
		job := createSampleDLQJob()
		store.Add(job)
	}

	// Verify jobs exist
	if store.Size() != 5 {
		t.Fatalf("Expected 5 jobs before Clear, got %d", store.Size())
	}

	// Clear store
	store.Clear()

	// Verify empty
	if store.Size() != 0 {
		t.Errorf("Expected 0 jobs after Clear, got %d", store.Size())
	}
}

// TestLRUJobTrackingStore_Size tests size tracking
func TestLRUJobTrackingStore_Size(t *testing.T) {
	store := NewLRUJobTrackingStore(10)

	// Initially empty
	if store.Size() != 0 {
		t.Errorf("Expected size 0 initially, got %d", store.Size())
	}

	// Add jobs
	for i := 0; i < 7; i++ {
		job := createSampleDLQJob()
		store.Add(job)
	}

	// Check size
	if store.Size() != 7 {
		t.Errorf("Expected size 7 after adding jobs, got %d", store.Size())
	}

	// Remove job
	jobs := store.List(JobFilters{Limit: 1})
	if len(jobs) > 0 {
		store.Remove(jobs[0].ID)
	}

	// Check size after removal
	if store.Size() != 6 {
		t.Errorf("Expected size 6 after removal, got %d", store.Size())
	}
}

// TestLRUJobTrackingStore_LRUEviction tests LRU eviction policy
func TestLRUJobTrackingStore_LRUEviction(t *testing.T) {
	store := NewLRUJobTrackingStore(3) // Small capacity

	// Add 4 jobs (exceeds capacity)
	job1 := createSampleDLQJob()
	job2 := createSampleDLQJob()
	job3 := createSampleDLQJob()
	job4 := createSampleDLQJob()

	store.Add(job1)
	store.Add(job2)
	store.Add(job3)

	// Check size (should be 3)
	if store.Size() != 3 {
		t.Fatalf("Expected size 3, got %d", store.Size())
	}

	// Add 4th job (should evict job1 - least recently used)
	store.Add(job4)

	// Check size (still 3)
	if store.Size() != 3 {
		t.Errorf("Expected size 3 after eviction, got %d", store.Size())
	}

	// job1 should be evicted (oldest)
	if store.Get(job1.ID) != nil {
		t.Errorf("Expected job1 to be evicted, but still exists")
	}

	// job2, job3, job4 should still exist
	if store.Get(job2.ID) == nil {
		t.Errorf("Expected job2 to exist")
	}
	if store.Get(job3.ID) == nil {
		t.Errorf("Expected job3 to exist")
	}
	if store.Get(job4.ID) == nil {
		t.Errorf("Expected job4 to exist")
	}
}

// TestLRUJobTrackingStore_UpdateExisting tests updating existing job
func TestLRUJobTrackingStore_UpdateExisting(t *testing.T) {
	store := NewLRUJobTrackingStore(10)

	job := createSampleDLQJob()
	job.State = JobStateQueued
	store.Add(job)

	// Update job state
	job.State = JobStateProcessing
	store.Add(job)

	// Verify updated state
	snapshot := store.Get(job.ID)
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot")
	}

	if snapshot.State != "processing" {
		t.Errorf("Expected state 'processing', got %s", snapshot.State)
	}

	// Size should remain 1 (update, not add)
	if store.Size() != 1 {
		t.Errorf("Expected size 1 after update, got %d", store.Size())
	}
}

// TestJobSnapshot_Serialization tests JobSnapshot fields
func TestJobSnapshot_Serialization(t *testing.T) {
	snapshot := createSampleJobSnapshot()

	// Verify required fields
	if snapshot.ID == "" {
		t.Error("Expected non-empty ID")
	}
	if snapshot.Fingerprint == "" {
		t.Error("Expected non-empty Fingerprint")
	}
	if snapshot.TargetName == "" {
		t.Error("Expected non-empty TargetName")
	}
	if snapshot.Priority == "" {
		t.Error("Expected non-empty Priority")
	}
	if snapshot.State == "" {
		t.Error("Expected non-empty State")
	}
	if snapshot.SubmittedAt == 0 {
		t.Error("Expected non-zero SubmittedAt")
	}
}

// TestLRUJobTrackingStore_Concurrent tests thread-safe operations
func TestLRUJobTrackingStore_Concurrent(t *testing.T) {
	store := NewLRUJobTrackingStore(100)

	// Concurrent Add operations
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			job := createSampleDLQJob()
			job.ID = fmt.Sprintf("job-%d", index)
			store.Add(job)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify size
	if store.Size() != 10 {
		t.Errorf("Expected size 10 after concurrent adds, got %d", store.Size())
	}

	// Concurrent Get operations
	for i := 0; i < 10; i++ {
		go func(index int) {
			jobID := fmt.Sprintf("job-%d", index)
			snapshot := store.Get(jobID)
			if snapshot == nil {
				t.Errorf("Expected job-%d to exist", index)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// BenchmarkJobTrackingStore_Add benchmarks Add operation
func BenchmarkJobTrackingStore_Add(b *testing.B) {
	store := NewLRUJobTrackingStore(10000)
	job := createSampleDLQJob()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job.ID = fmt.Sprintf("job-%d", i)
		store.Add(job)
	}
}

// BenchmarkJobTrackingStore_Get benchmarks Get operation
func BenchmarkJobTrackingStore_Get(b *testing.B) {
	store := NewLRUJobTrackingStore(10000)

	// Populate store
	for i := 0; i < 1000; i++ {
		job := createSampleDLQJob()
		job.ID = fmt.Sprintf("job-%d", i)
		store.Add(job)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jobID := fmt.Sprintf("job-%d", i%1000)
		_ = store.Get(jobID)
	}
}

// BenchmarkJobTrackingStore_List benchmarks List operation
func BenchmarkJobTrackingStore_List(b *testing.B) {
	store := NewLRUJobTrackingStore(10000)

	// Populate store
	for i := 0; i < 1000; i++ {
		job := createSampleDLQJob()
		job.Priority = PriorityHigh
		store.Add(job)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.List(JobFilters{Priority: "high", Limit: 100})
	}
}
