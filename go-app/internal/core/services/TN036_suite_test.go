package services

// TN036_suite_test.go
// Dedicated test suite for TN-036 Alert Deduplication & Fingerprinting
// This file ensures 80%+ test coverage for deduplication.go and fingerprint.go

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// TestTN036_Suite_ProcessAlert_Comprehensive tests all ProcessAlert code paths
func TestTN036_Suite_ProcessAlert_Comprehensive(t *testing.T) {
	businessMetrics := metrics.NewBusinessMetrics("alert_history")

	tests := []struct {
		name           string
		setup          func(*mockAlertStorage)
		alert          *core.Alert
		wantAction     ProcessAction
		wantIsUpdate   bool
		wantIsDuplicate bool
		wantErr        bool
	}{
		{
			name: "create_new_alert",
			setup: func(storage *mockAlertStorage) {
				// Empty storage
			},
			alert: &core.Alert{
				AlertName: "TestAlert",
				Status:    core.StatusFiring,
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "critical",
				},
				StartsAt: time.Now(),
			},
			wantAction:      ProcessActionCreated,
			wantIsUpdate:    false,
			wantIsDuplicate: false,
			wantErr:         false,
		},
		{
			name: "update_existing_alert_status_change",
			setup: func(storage *mockAlertStorage) {
				// Pre-populate with existing alert
				existing := &core.Alert{
					AlertName: "TestAlert",
					Status:    core.StatusFiring,
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "critical",
					},
					Fingerprint: "test-fingerprint",
					StartsAt:    time.Now(),
				}
				storage.alerts[existing.Fingerprint] = existing
			},
			alert: &core.Alert{
				AlertName: "TestAlert",
				Status:    core.StatusResolved, // Changed status
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "critical",
				},
				Fingerprint: "test-fingerprint",
				StartsAt:    time.Now(),
			},
			wantAction:      ProcessActionUpdated,
			wantIsUpdate:    true,
			wantIsDuplicate: false,
			wantErr:         false,
		},
		{
			name: "ignore_duplicate_alert",
			setup: func(storage *mockAlertStorage) {
				// Pre-populate with existing alert
				existing := &core.Alert{
					AlertName: "TestAlert",
					Status:    core.StatusFiring,
					Labels: map[string]string{
						"alertname": "TestAlert",
						"severity":  "critical",
					},
					Fingerprint: "test-fingerprint",
					StartsAt:    time.Now(),
				}
				storage.alerts[existing.Fingerprint] = existing
			},
			alert: &core.Alert{
				AlertName: "TestAlert",
				Status:    core.StatusFiring, // Same status
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "critical",
				},
				Fingerprint: "test-fingerprint",
				StartsAt:    time.Now(),
			},
			wantAction:      ProcessActionIgnored,
			wantIsUpdate:    false,
			wantIsDuplicate: true,
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := newMockAlertStorage()
			tt.setup(storage)

			service, err := NewDeduplicationService(&DeduplicationConfig{
				Storage:         storage,
				BusinessMetrics: businessMetrics,
			})
			require.NoError(t, err)

			result, err := service.ProcessAlert(context.Background(), tt.alert)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantAction, result.Action)
			assert.Equal(t, tt.wantIsUpdate, result.IsUpdate)
			assert.Equal(t, tt.wantIsDuplicate, result.IsDuplicate)
			assert.Greater(t, result.ProcessingTime, time.Duration(0))
		})
	}
}

// TestTN036_Suite_GetDuplicateStats tests statistics gathering
func TestTN036_Suite_GetDuplicateStats(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create 3 alerts
	for i := 0; i < 3; i++ {
		alert := &core.Alert{
			AlertName: "TestAlert",
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname": "TestAlert",
				"instance":  string(rune('A' + i)),
			},
			StartsAt: time.Now(),
		}
		_, err := service.ProcessAlert(ctx, alert)
		require.NoError(t, err)
	}

	// Get stats
	stats, err := service.GetDuplicateStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.TotalProcessed)
	assert.Equal(t, int64(3), stats.Created)
	assert.Equal(t, int64(0), stats.Updated)
	assert.Equal(t, int64(0), stats.Ignored)
}

// TestTN036_Suite_ResetStats tests statistics reset
func TestTN036_Suite_ResetStats(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Process an alert
	alert := &core.Alert{
		AlertName: "TestAlert",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "TestAlert",
		},
		StartsAt: time.Now(),
	}
	_, err = service.ProcessAlert(ctx, alert)
	require.NoError(t, err)

	// Verify stats before reset
	stats, err := service.GetDuplicateStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalProcessed)

	// Reset stats
	err = service.ResetStats(ctx)
	require.NoError(t, err)

	// Verify stats after reset
	stats, err = service.GetDuplicateStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.TotalProcessed)
	assert.Equal(t, int64(0), stats.Created)
}

// TestTN036_Suite_String tests ProcessAction.String()
func TestTN036_Suite_String(t *testing.T) {
	assert.Equal(t, "created", ProcessActionCreated.String())
	assert.Equal(t, "updated", ProcessActionUpdated.String())
	assert.Equal(t, "ignored", ProcessActionIgnored.String())
}

// TestTN036_Suite_Fingerprint_Algorithms tests both FNV-1a and SHA-256
func TestTN036_Suite_Fingerprint_Algorithms(t *testing.T) {
	labels := map[string]string{
		"alertname": "TestAlert",
		"severity":  "critical",
	}

	tests := []struct {
		name        string
		algorithm   FingerprintAlgorithm
		wantLength  int
		wantEmpty   bool
	}{
		{
			name:       "FNV-1a algorithm",
			algorithm:  AlgorithmFNV1a,
			wantLength: 16,
			wantEmpty:  false,
		},
		{
			name:       "SHA-256 algorithm",
			algorithm:  AlgorithmSHA256,
			wantLength: 64,
			wantEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := NewFingerprintGenerator(&FingerprintConfig{
				Algorithm: tt.algorithm,
			})

			fingerprint := generator.GenerateFromLabels(labels)

			if tt.wantEmpty {
				assert.Empty(t, fingerprint)
			} else {
				assert.Len(t, fingerprint, tt.wantLength)
				assert.True(t, ValidateFingerprint(fingerprint, tt.algorithm))
			}
		})
	}
}

// TestTN036_Suite_Fingerprint_EdgeCases tests edge cases
func TestTN036_Suite_Fingerprint_EdgeCases(t *testing.T) {
	generator := NewFingerprintGenerator(nil)

	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "nil_labels",
			labels: nil,
			want:   "",
		},
		{
			name:   "empty_labels",
			labels: map[string]string{},
			want:   "",
		},
		{
			name: "single_label",
			labels: map[string]string{
				"alertname": "Test",
			},
			want: "", // Non-empty fingerprint expected
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fingerprint := generator.GenerateFromLabels(tt.labels)
			if tt.want == "" && len(tt.labels) > 0 {
				// For non-empty labels, expect non-empty fingerprint
				assert.NotEmpty(t, fingerprint)
			} else {
				assert.Equal(t, tt.want, fingerprint)
			}
		})
	}
}

// TestTN036_Suite_Alert_NeedsUpdate tests update detection
func TestTN036_Suite_Alert_NeedsUpdate(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create initial alert
	initial := &core.Alert{
		AlertName: "TestAlert",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "TestAlert",
		},
		StartsAt: time.Now(),
	}

	result1, err := service.ProcessAlert(ctx, initial)
	require.NoError(t, err)
	assert.Equal(t, ProcessActionCreated, result1.Action)

	// Test EndsAt change
	endsAt := time.Now().Add(1 * time.Hour)
	updated := &core.Alert{
		AlertName:   "TestAlert",
		Status:      core.StatusFiring, // Same
		Labels:      initial.Labels,
		StartsAt:    initial.StartsAt,
		EndsAt:      &endsAt, // Changed
		Fingerprint: result1.Alert.Fingerprint,
	}

	result2, err := service.ProcessAlert(ctx, updated)
	require.NoError(t, err)
	assert.Equal(t, ProcessActionUpdated, result2.Action)
	assert.True(t, result2.IsUpdate)

	// Test annotations update
	endsAt2 := time.Now().Add(2 * time.Hour)
	updatedWithAnnotations := &core.Alert{
		AlertName: "TestAlert",
		Status:    core.StatusFiring,
		Labels:    initial.Labels,
		Annotations: map[string]string{
			"summary": "Test summary",
			"runbook": "http://runbook.example.com",
		},
		EndsAt:      &endsAt2,
		Fingerprint: result1.Alert.Fingerprint,
	}

	result3, err := service.ProcessAlert(ctx, updatedWithAnnotations)
	require.NoError(t, err)
	assert.Equal(t, ProcessActionUpdated, result3.Action)
	assert.True(t, result3.IsUpdate)
	assert.NotNil(t, result3.Alert.Annotations)
	assert.Equal(t, "Test summary", result3.Alert.Annotations["summary"])
}

// TestTN036_Suite_Alert_NeedsUpdate_EdgeCases tests edge cases for update detection
func TestTN036_Suite_Alert_NeedsUpdate_EdgeCases(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name         string
		initial      *core.Alert
		updated      *core.Alert
		wantAction   ProcessAction
		wantIsUpdate bool
	}{
		{
			name: "endsAt_nil_to_non_nil",
			initial: &core.Alert{
				AlertName: "Test1",
				Status:    core.StatusFiring,
				Labels:    map[string]string{"alertname": "Test1"},
				EndsAt:    nil,
			},
			updated: func() *core.Alert {
				t := time.Now()
				return &core.Alert{
					AlertName: "Test1",
					Status:    core.StatusFiring,
					Labels:    map[string]string{"alertname": "Test1"},
					EndsAt:    &t,
				}
			}(),
			wantAction:   ProcessActionUpdated,
			wantIsUpdate: true,
		},
		{
			name: "endsAt_non_nil_to_nil",
			initial: func() *core.Alert {
				t := time.Now()
				return &core.Alert{
					AlertName: "Test2",
					Status:    core.StatusFiring,
					Labels:    map[string]string{"alertname": "Test2"},
					EndsAt:    &t,
				}
			}(),
			updated: &core.Alert{
				AlertName: "Test2",
				Status:    core.StatusFiring,
				Labels:    map[string]string{"alertname": "Test2"},
				EndsAt:    nil,
			},
			wantAction:   ProcessActionUpdated,
			wantIsUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset storage for each test
			storage = newMockAlertStorage()
			service, err = NewDeduplicationService(&DeduplicationConfig{
				Storage: storage,
			})
			require.NoError(t, err)

			// Create initial
			result1, err := service.ProcessAlert(ctx, tt.initial)
			require.NoError(t, err)
			assert.Equal(t, ProcessActionCreated, result1.Action)

			// Update with modified alert
			tt.updated.Fingerprint = result1.Alert.Fingerprint
			result2, err := service.ProcessAlert(ctx, tt.updated)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAction, result2.Action)
			assert.Equal(t, tt.wantIsUpdate, result2.IsUpdate)
		})
	}
}

// TestTN036_Suite_Fingerprint_AlgorithmSwitch tests runtime algorithm selection
func TestTN036_Suite_Fingerprint_AlgorithmSwitch(t *testing.T) {
	labels := map[string]string{
		"alertname": "TestAlert",
		"severity":  "critical",
	}

	// Test default algorithm (FNV-1a)
	generatorDefault := NewFingerprintGenerator(nil)
	fpDefault := generatorDefault.GenerateWithAlgorithm(labels, AlgorithmFNV1a)
	assert.Len(t, fpDefault, 16)

	// Test SHA-256
	fpSHA := generatorDefault.GenerateWithAlgorithm(labels, AlgorithmSHA256)
	assert.Len(t, fpSHA, 64)

	// Test unknown algorithm (should fallback to FNV-1a)
	fpUnknown := generatorDefault.GenerateWithAlgorithm(labels, FingerprintAlgorithm("unknown"))
	assert.Len(t, fpUnknown, 16) // Should fallback to FNV-1a
	assert.Equal(t, fpDefault, fpUnknown)
}
