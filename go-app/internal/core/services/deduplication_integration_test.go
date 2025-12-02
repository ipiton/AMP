package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure"
)

// TestDeduplicationIntegration_RealPostgres tests deduplication with real PostgreSQL database.
//
// This test requires a running PostgreSQL instance. Set environment variable:
//
//	TEST_DATABASE_DSN=postgres://user:password@localhost:5432/testdb?sslmode=disable
//
// To run:
//
//	TEST_DATABASE_DSN="..." go test -v -tags=integration ./internal/core/services/
func TestDeduplicationIntegration_RealPostgres(t *testing.T) {
	// Skip if not running integration tests
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("Skipping integration test: TEST_DATABASE_DSN not set")
	}

	// Setup: Initialize PostgreSQL connection
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	config := &infrastructure.Config{
		DSN:             dsn,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
		Logger:          logger,
	}

	storage, err := infrastructure.NewPostgresDatabase(config)
	require.NoError(t, err, "Failed to connect to PostgreSQL")
	// Note: PostgresDatabase doesn't expose Close() - connection is managed internally

	// Ensure alerts table exists (migrations should be run beforehand)
	// For testing, we'll assume the schema is already created

	// Initialize deduplication service
	fingerprintGen := NewFingerprintGenerator(nil) // Default FNV-1a
	dedupConfig := &DeduplicationConfig{
		Storage:     storage,
		Fingerprint: fingerprintGen,
		Logger:      logger,
	}

	dedupService, err := NewDeduplicationService(dedupConfig)
	require.NoError(t, err, "Failed to create deduplication service")

	// Test 1: Create new alert
	t.Run("CreateNewAlert", func(t *testing.T) {
		alert := &core.Alert{
			AlertName:   "TestAlert_Integration",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"env": "test", "severity": "critical"},
			Annotations: map[string]string{"summary": "Test alert"},
			StartsAt:    time.Now(),
		}

		result, err := dedupService.ProcessAlert(ctx, alert)
		require.NoError(t, err)
		assert.Equal(t, ProcessActionCreated, result.Action)
		assert.NotNil(t, result.Alert)
		assert.NotEmpty(t, result.Alert.Fingerprint, "Fingerprint should be generated")
		assert.False(t, result.IsDuplicate)

		// Cleanup
		if result.Alert != nil && result.Alert.Fingerprint != "" {
			_ = storage.DeleteAlert(ctx, result.Alert.Fingerprint)
		}
	})

	// Test 2: Duplicate detection (same alert twice)
	t.Run("DetectDuplicate", func(t *testing.T) {
		alert := &core.Alert{
			AlertName:   "DuplicateAlert",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"app": "test", "instance": "1"},
			Annotations: map[string]string{"description": "Duplicate test"},
			StartsAt:    time.Now(),
		}

		// First: create
		result1, err := dedupService.ProcessAlert(ctx, alert)
		require.NoError(t, err)
		assert.Equal(t, ProcessActionCreated, result1.Action)
		fingerprint := result1.Alert.Fingerprint

		// Second: duplicate (should be ignored)
		result2, err := dedupService.ProcessAlert(ctx, alert)
		require.NoError(t, err)
		assert.Equal(t, ProcessActionIgnored, result2.Action)
		assert.True(t, result2.IsDuplicate)
		assert.Equal(t, fingerprint, result2.Alert.Fingerprint)

		// Cleanup
		_ = storage.DeleteAlert(ctx, fingerprint)
	})

	// Test 3: Update existing alert (status change)
	t.Run("UpdateExistingAlert", func(t *testing.T) {
		alert := &core.Alert{
			AlertName:   "UpdateAlert",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"service": "api", "env": "prod"},
			Annotations: map[string]string{"info": "Update test"},
			StartsAt:    time.Now(),
		}

		// First: create firing alert
		result1, err := dedupService.ProcessAlert(ctx, alert)
		require.NoError(t, err)
		assert.Equal(t, ProcessActionCreated, result1.Action)
		fingerprint := result1.Alert.Fingerprint

		// Second: resolve alert (status change)
		time.Sleep(100 * time.Millisecond) // Ensure time difference
		resolvedAlert := &core.Alert{
			AlertName:   "UpdateAlert",
			Status:      core.StatusResolved,
			Labels:      map[string]string{"service": "api", "env": "prod"},
			Annotations: map[string]string{"info": "Update test"},
			StartsAt:    alert.StartsAt,
			EndsAt:      timePtr(time.Now()),
		}

		result2, err := dedupService.ProcessAlert(ctx, resolvedAlert)
		require.NoError(t, err)
		assert.Equal(t, ProcessActionUpdated, result2.Action)
		assert.True(t, result2.IsUpdate)
		assert.Equal(t, fingerprint, result2.Alert.Fingerprint)
		assert.Equal(t, core.StatusResolved, result2.Alert.Status)

		// Cleanup
		_ = storage.DeleteAlert(ctx, fingerprint)
	})

	// Test 4: Concurrent processing (100 alerts)
	t.Run("ConcurrentProcessing", func(t *testing.T) {
		const numGoroutines = 100
		alerts := make([]*core.Alert, numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			alerts[i] = &core.Alert{
				AlertName:   fmt.Sprintf("ConcurrentAlert_%d", i),
				Status:      core.StatusFiring,
				Labels:      map[string]string{"id": fmt.Sprintf("%d", i)},
				Annotations: map[string]string{"test": "concurrent"},
				StartsAt:    time.Now(),
			}
		}

		// Process all alerts concurrently
		results := make(chan *ProcessResult, numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func(alert *core.Alert) {
				result, err := dedupService.ProcessAlert(ctx, alert)
				if err != nil {
					t.Logf("Error processing alert: %v", err)
					results <- nil
					return
				}
				results <- result
			}(alerts[i])
		}

		// Collect results
		successCount := 0
		fingerprints := make([]string, 0, numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			result := <-results
			if result != nil && result.Action == ProcessActionCreated {
				successCount++
				fingerprints = append(fingerprints, result.Alert.Fingerprint)
			}
		}
		close(results)

		assert.Equal(t, numGoroutines, successCount, "All alerts should be created successfully")

		// Cleanup
		for _, fp := range fingerprints {
			_ = storage.DeleteAlert(ctx, fp)
		}
	})

	// Test 5: Fingerprint consistency (same labels = same fingerprint)
	t.Run("FingerprintConsistency", func(t *testing.T) {
		labels := map[string]string{"region": "us-west", "cluster": "prod"}

		alert1 := &core.Alert{
			AlertName:   "FingerprintTest",
			Status:      core.StatusFiring,
			Labels:      labels,
			Annotations: map[string]string{"test": "1"},
			StartsAt:    time.Now(),
		}

		result1, err := dedupService.ProcessAlert(ctx, alert1)
		require.NoError(t, err)
		fp1 := result1.Alert.Fingerprint

		// Same labels, different annotations (fingerprint should be identical)
		alert2 := &core.Alert{
			AlertName:   "FingerprintTest",
			Status:      core.StatusFiring,
			Labels:      labels,
			Annotations: map[string]string{"test": "2", "extra": "field"},
			StartsAt:    time.Now().Add(1 * time.Minute),
		}

		result2, err := dedupService.ProcessAlert(ctx, alert2)
		require.NoError(t, err)

		assert.Equal(t, fp1, result2.Alert.Fingerprint, "Fingerprints should match for same labels")
		assert.Equal(t, ProcessActionIgnored, result2.Action, "Second alert should be ignored as duplicate")

		// Cleanup
		_ = storage.DeleteAlert(ctx, fp1)
	})

	// Test 6: Retrieve deduplication stats
	t.Run("GetStats", func(t *testing.T) {
		stats, err := dedupService.GetDuplicateStats(ctx)
		require.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Greater(t, stats.TotalProcessed, int64(0), "Should have processed alerts")
		t.Logf("Deduplication stats: %+v", stats)
	})
}

// timePtr is a helper to create *time.Time
func timePtr(t time.Time) *time.Time {
	return &t
}
