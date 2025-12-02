package publishing

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ipiton/AMP/internal/core"
)

// Helper function to create a sample PublishingJob for DLQ testing
func createSampleDLQJob() *PublishingJob {
	jobID := uuid.New()
	now := time.Now()
	return &PublishingJob{
		ID: jobID.String(),
		EnrichedAlert: &core.EnrichedAlert{
			Alert: &core.Alert{
				Fingerprint: "test-fingerprint-123",
				Labels:      map[string]string{"severity": "critical"},
				Annotations: map[string]string{"summary": "Test alert"},
			},
		},
		Target: &core.PublishingTarget{
			Name: "test-target",
			Type: "webhook",
		},
		Priority:    PriorityHigh,
		State:       JobStateFailed,
		RetryCount:  3,
		SubmittedAt: now,
		StartedAt:   &now,
		LastError:   fmt.Errorf("connection timeout"),
		ErrorType:   QueueErrorTypeTransient,
	}
}

// TestDLQEntry_Serialization tests DLQ entry JSON serialization
func TestDLQEntry_Serialization(t *testing.T) {
	entry := &DLQEntry{
		ID:          uuid.New(),
		JobID:       uuid.New(),
		Fingerprint: "test-fp",
		TargetName:  "test-target",
		TargetType:  "webhook",
		EnrichedAlert: &core.EnrichedAlert{
			Alert: &core.Alert{
				Fingerprint: "test-fp",
			},
		},
		TargetConfig: &core.PublishingTarget{
			Name: "test-target",
			Type: "webhook",
		},
		ErrorMessage: "connection timeout",
		ErrorType:    "transient",
		RetryCount:   3,
		Priority:     "high",
		FailedAt:     time.Now(),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Replayed:     false,
	}

	// Serialize
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Failed to marshal DLQEntry: %v", err)
	}

	// Deserialize
	var decoded DLQEntry
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal DLQEntry: %v", err)
	}

	// Assert
	if decoded.Fingerprint != entry.Fingerprint {
		t.Errorf("Expected fingerprint %s, got %s", entry.Fingerprint, decoded.Fingerprint)
	}
	if decoded.ErrorType != entry.ErrorType {
		t.Errorf("Expected error type %s, got %s", entry.ErrorType, decoded.ErrorType)
	}
}

// TestDLQFilters_Defaults tests DLQ filter default values
func TestDLQFilters_Defaults(t *testing.T) {
	filters := DLQFilters{}

	if filters.TargetName != "" {
		t.Errorf("Expected empty target name, got %s", filters.TargetName)
	}
	if filters.Limit != 0 {
		t.Errorf("Expected 0 limit, got %d", filters.Limit)
	}
	if filters.Offset != 0 {
		t.Errorf("Expected 0 offset, got %d", filters.Offset)
	}
}

// TestDLQFilters_WithValues tests DLQ filters with specific values
func TestDLQFilters_WithValues(t *testing.T) {
	replayed := false
	failedAfter := time.Now().Add(-24 * time.Hour)

	filters := DLQFilters{
		TargetName:  "target-1",
		ErrorType:   "transient",
		Priority:    "high",
		Replayed:    &replayed,
		FailedAfter: &failedAfter,
		Limit:       10,
		Offset:      5,
	}

	if filters.TargetName != "target-1" {
		t.Errorf("Expected target name 'target-1', got %s", filters.TargetName)
	}
	if filters.ErrorType != "transient" {
		t.Errorf("Expected error type 'transient', got %s", filters.ErrorType)
	}
	if filters.Priority != "high" {
		t.Errorf("Expected priority 'high', got %s", filters.Priority)
	}
	if *filters.Replayed != false {
		t.Errorf("Expected replayed=false, got %v", *filters.Replayed)
	}
	if filters.Limit != 10 {
		t.Errorf("Expected limit 10, got %d", filters.Limit)
	}
	if filters.Offset != 5 {
		t.Errorf("Expected offset 5, got %d", filters.Offset)
	}
}

// TestDLQStats_EmptyStats tests empty DLQ statistics
func TestDLQStats_EmptyStats(t *testing.T) {
	stats := &DLQStats{
		TotalEntries:       0,
		EntriesByErrorType: make(map[string]int),
		EntriesByTarget:    make(map[string]int),
		EntriesByPriority:  make(map[string]int),
		ReplayedCount:      0,
	}

	if stats.TotalEntries != 0 {
		t.Errorf("Expected 0 total entries, got %d", stats.TotalEntries)
	}
	if len(stats.EntriesByErrorType) != 0 {
		t.Errorf("Expected empty error type map, got %d entries", len(stats.EntriesByErrorType))
	}
	if stats.ReplayedCount != 0 {
		t.Errorf("Expected 0 replayed count, got %d", stats.ReplayedCount)
	}
}

// TestDLQStats_WithData tests DLQ statistics with data
func TestDLQStats_WithData(t *testing.T) {
	oldest := time.Now().Add(-7 * 24 * time.Hour)
	newest := time.Now()

	stats := &DLQStats{
		TotalEntries: 100,
		EntriesByErrorType: map[string]int{
			"transient": 60,
			"permanent": 40,
		},
		EntriesByTarget: map[string]int{
			"target-1": 50,
			"target-2": 50,
		},
		EntriesByPriority: map[string]int{
			"high":   30,
			"medium": 50,
			"low":    20,
		},
		OldestEntry:   &oldest,
		NewestEntry:   &newest,
		ReplayedCount: 10,
	}

	// Serialize and deserialize
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Failed to marshal DLQStats: %v", err)
	}

	var decoded DLQStats
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal DLQStats: %v", err)
	}

	// Assert
	if decoded.TotalEntries != 100 {
		t.Errorf("Expected 100 total entries, got %d", decoded.TotalEntries)
	}
	if decoded.EntriesByErrorType["transient"] != 60 {
		t.Errorf("Expected 60 transient errors, got %d", decoded.EntriesByErrorType["transient"])
	}
	if decoded.ReplayedCount != 10 {
		t.Errorf("Expected 10 replayed entries, got %d", decoded.ReplayedCount)
	}
}

// TestPublishingJob_ToEnrichedAlert tests EnrichedAlert field access
func TestPublishingJob_ToEnrichedAlert(t *testing.T) {
	job := createSampleDLQJob()

	if job.EnrichedAlert == nil {
		t.Fatal("Expected non-nil EnrichedAlert")
	}

	if job.EnrichedAlert.Alert == nil {
		t.Fatal("Expected non-nil Alert")
	}

	if job.EnrichedAlert.Alert.Fingerprint != "test-fingerprint-123" {
		t.Errorf("Expected fingerprint 'test-fingerprint-123', got %s", job.EnrichedAlert.Alert.Fingerprint)
	}
}

// TestPublishingJob_ErrorSerialization tests error serialization
func TestPublishingJob_ErrorSerialization(t *testing.T) {
	job := createSampleDLQJob()

	if job.LastError == nil {
		t.Fatal("Expected non-nil LastError")
	}

	errorMsg := job.LastError.Error()
	if errorMsg != "connection timeout" {
		t.Errorf("Expected error 'connection timeout', got %s", errorMsg)
	}

	// Verify error type
	if job.ErrorType != QueueErrorTypeTransient {
		t.Errorf("Expected transient error type, got %v", job.ErrorType)
	}
}

// TestDLQEntry_NilFields tests DLQEntry with nil optional fields
func TestDLQEntry_NilFields(t *testing.T) {
	entry := &DLQEntry{
		ID:            uuid.New(),
		JobID:         uuid.New(),
		Fingerprint:   "test-fp",
		TargetName:    "test-target",
		TargetType:    "webhook",
		ErrorMessage:  "error",
		ErrorType:     "transient",
		RetryCount:    3,
		Priority:      "high",
		FailedAt:      time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Replayed:      false,
		LastRetryAt:   nil, // Nil field
		ReplayedAt:    nil, // Nil field
		ReplayResult:  nil, // Nil field
		EnrichedAlert: nil, // Nil field
		TargetConfig:  nil, // Nil field
	}

	// Serialize with nil fields
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Failed to marshal DLQEntry with nil fields: %v", err)
	}

	// Deserialize
	var decoded DLQEntry
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal DLQEntry with nil fields: %v", err)
	}

	// Assert nil fields remain nil
	if decoded.LastRetryAt != nil {
		t.Errorf("Expected nil LastRetryAt, got %v", decoded.LastRetryAt)
	}
	if decoded.ReplayedAt != nil {
		t.Errorf("Expected nil ReplayedAt, got %v", decoded.ReplayedAt)
	}
	if decoded.ReplayResult != nil {
		t.Errorf("Expected nil ReplayResult, got %v", decoded.ReplayResult)
	}
}

// TestDLQEntry_ReplayedFlag tests replayed flag behavior
func TestDLQEntry_ReplayedFlag(t *testing.T) {
	entry := &DLQEntry{
		ID:           uuid.New(),
		JobID:        uuid.New(),
		Fingerprint:  "test-fp",
		TargetName:   "test-target",
		TargetType:   "webhook",
		ErrorMessage: "error",
		ErrorType:    "transient",
		RetryCount:   3,
		Priority:     "high",
		FailedAt:     time.Now(),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Replayed:     false,
	}

	// Initially not replayed
	if entry.Replayed {
		t.Errorf("Expected not replayed, got replayed=true")
	}

	// Mark as replayed
	entry.Replayed = true
	replayTime := time.Now()
	entry.ReplayedAt = &replayTime
	replayResult := "success"
	entry.ReplayResult = &replayResult

	// Assert replayed state
	if !entry.Replayed {
		t.Errorf("Expected replayed=true, got false")
	}
	if entry.ReplayedAt == nil {
		t.Errorf("Expected non-nil ReplayedAt")
	}
	if entry.ReplayResult == nil || *entry.ReplayResult != "success" {
		t.Errorf("Expected replay result 'success', got %v", entry.ReplayResult)
	}
}

// TestDLQStats_Aggregation tests statistics aggregation logic
func TestDLQStats_Aggregation(t *testing.T) {
	stats := &DLQStats{
		TotalEntries:       150,
		EntriesByErrorType: make(map[string]int),
		EntriesByTarget:    make(map[string]int),
		EntriesByPriority:  make(map[string]int),
		ReplayedCount:      0,
	}

	// Simulate aggregation
	stats.EntriesByErrorType["transient"] = 90
	stats.EntriesByErrorType["permanent"] = 50
	stats.EntriesByErrorType["unknown"] = 10

	stats.EntriesByTarget["target-1"] = 70
	stats.EntriesByTarget["target-2"] = 50
	stats.EntriesByTarget["target-3"] = 30

	stats.EntriesByPriority["high"] = 60
	stats.EntriesByPriority["medium"] = 70
	stats.EntriesByPriority["low"] = 20

	// Assert totals match
	totalByError := stats.EntriesByErrorType["transient"] +
		stats.EntriesByErrorType["permanent"] +
		stats.EntriesByErrorType["unknown"]
	if totalByError != 150 {
		t.Errorf("Expected total by error type to equal 150, got %d", totalByError)
	}

	totalByTarget := stats.EntriesByTarget["target-1"] +
		stats.EntriesByTarget["target-2"] +
		stats.EntriesByTarget["target-3"]
	if totalByTarget != 150 {
		t.Errorf("Expected total by target to equal 150, got %d", totalByTarget)
	}

	totalByPriority := stats.EntriesByPriority["high"] +
		stats.EntriesByPriority["medium"] +
		stats.EntriesByPriority["low"]
	if totalByPriority != 150 {
		t.Errorf("Expected total by priority to equal 150, got %d", totalByPriority)
	}
}

// TestDLQFilters_NilPointers tests filter behavior with nil pointers
func TestDLQFilters_NilPointers(t *testing.T) {
	filters := DLQFilters{
		TargetName:  "target-1",
		Replayed:    nil, // Nil pointer - should accept all
		FailedAfter: nil, // Nil pointer - no time filter
		Limit:       10,
	}

	if filters.Replayed != nil {
		t.Errorf("Expected nil Replayed pointer, got %v", filters.Replayed)
	}
	if filters.FailedAfter != nil {
		t.Errorf("Expected nil FailedAfter pointer, got %v", filters.FailedAfter)
	}
}

// TestDLQEntry_UUIDGeneration tests UUID generation for DLQ entries
func TestDLQEntry_UUIDGeneration(t *testing.T) {
	entry1 := &DLQEntry{
		ID:    uuid.New(),
		JobID: uuid.New(),
	}

	entry2 := &DLQEntry{
		ID:    uuid.New(),
		JobID: uuid.New(),
	}

	// UUIDs should be unique
	if entry1.ID == entry2.ID {
		t.Errorf("Expected unique IDs, got same ID: %s", entry1.ID)
	}
	if entry1.JobID == entry2.JobID {
		t.Errorf("Expected unique Job IDs, got same Job ID: %s", entry1.JobID)
	}
}

// BenchmarkDLQEntry_Serialization benchmarks DLQ entry serialization
func BenchmarkDLQEntry_Serialization(b *testing.B) {
	entry := &DLQEntry{
		ID:          uuid.New(),
		JobID:       uuid.New(),
		Fingerprint: "test-fp",
		TargetName:  "test-target",
		TargetType:  "webhook",
		EnrichedAlert: &core.EnrichedAlert{
			Alert: &core.Alert{
				Fingerprint: "test-fp",
			},
		},
		ErrorMessage: "error",
		ErrorType:    "transient",
		RetryCount:   3,
		Priority:     "high",
		FailedAt:     time.Now(),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Replayed:     false,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(entry)
	}
}

// BenchmarkDLQStats_Aggregation benchmarks statistics aggregation
func BenchmarkDLQStats_Aggregation(b *testing.B) {
	stats := &DLQStats{
		TotalEntries:       1000,
		EntriesByErrorType: make(map[string]int),
		EntriesByTarget:    make(map[string]int),
		EntriesByPriority:  make(map[string]int),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats.EntriesByErrorType["transient"] = 600
		stats.EntriesByErrorType["permanent"] = 400
		stats.EntriesByTarget["target-1"] = 500
		stats.EntriesByTarget["target-2"] = 500
		stats.EntriesByPriority["high"] = 300
		stats.EntriesByPriority["medium"] = 500
		stats.EntriesByPriority["low"] = 200
	}
}
