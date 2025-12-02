package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// mockAlertStorage implements core.AlertStorage for testing
type mockAlertStorage struct {
	alerts                     map[string]*core.Alert // fingerprint -> alert
	saveErr, getErr, updateErr error
	mu                         sync.RWMutex // Thread-safe access
}

func newMockAlertStorage() *mockAlertStorage {
	return &mockAlertStorage{
		alerts: make(map[string]*core.Alert),
	}
}

func (m *mockAlertStorage) SaveAlert(ctx context.Context, alert *core.Alert) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts[alert.Fingerprint] = alert
	return nil
}

func (m *mockAlertStorage) GetAlertByFingerprint(ctx context.Context, fingerprint string) (*core.Alert, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	alert, ok := m.alerts[fingerprint]
	if !ok {
		return nil, core.ErrAlertNotFound
	}
	return alert, nil
}

func (m *mockAlertStorage) UpdateAlert(ctx context.Context, alert *core.Alert) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts[alert.Fingerprint] = alert
	return nil
}

func (m *mockAlertStorage) DeleteAlert(ctx context.Context, fingerprint string) error {
	delete(m.alerts, fingerprint)
	return nil
}

func (m *mockAlertStorage) ListAlerts(ctx context.Context, filters *core.AlertFilters) (*core.AlertList, error) {
	return &core.AlertList{}, nil
}

func (m *mockAlertStorage) GetAlertStats(ctx context.Context) (*core.AlertStats, error) {
	return &core.AlertStats{}, nil
}

func (m *mockAlertStorage) CleanupOldAlerts(ctx context.Context, retentionDays int) (int, error) {
	return 0, nil
}

// TestNewDeduplicationService tests service creation
func TestNewDeduplicationService(t *testing.T) {
	tests := []struct {
		name    string
		config  *DeduplicationConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
			errMsg:  "config is required",
		},
		{
			name: "nil storage",
			config: &DeduplicationConfig{
				Storage: nil,
			},
			wantErr: true,
			errMsg:  "storage is required",
		},
		{
			name: "valid config with defaults",
			config: &DeduplicationConfig{
				Storage: newMockAlertStorage(),
			},
			wantErr: false,
		},
		{
			name: "valid config with custom fingerprint",
			config: &DeduplicationConfig{
				Storage:     newMockAlertStorage(),
				Fingerprint: NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a}),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewDeduplicationService(tt.config)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, service)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, service)
			}
		})
	}
}

// TestProcessAlert_CreateNewAlert tests creating a new alert
func TestProcessAlert_CreateNewAlert(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	alert := &core.Alert{
		AlertName: "HighCPU",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "server-1",
		},
		StartsAt: time.Now(),
	}

	ctx := context.Background()
	result, err := service.ProcessAlert(ctx, alert)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result
	assert.Equal(t, ProcessActionCreated, result.Action)
	assert.NotNil(t, result.Alert)
	assert.NotEmpty(t, result.Alert.Fingerprint, "fingerprint should be generated")
	assert.False(t, result.IsUpdate)
	assert.False(t, result.IsDuplicate)
	assert.Nil(t, result.ExistingID)
	assert.Greater(t, result.ProcessingTime, time.Duration(0))

	// Verify alert was saved
	saved, err := storage.GetAlertByFingerprint(ctx, result.Alert.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, alert.AlertName, saved.AlertName)
	assert.Equal(t, alert.Status, saved.Status)
}

// TestProcessAlert_UpdateExistingAlert tests updating an existing alert
func TestProcessAlert_UpdateExistingAlert(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create initial alert
	alert1 := &core.Alert{
		AlertName: "HighCPU",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		StartsAt: time.Now(),
	}

	result1, err := service.ProcessAlert(ctx, alert1)
	require.NoError(t, err)
	assert.Equal(t, ProcessActionCreated, result1.Action)

	// Update alert (change status to resolved)
	alert2 := &core.Alert{
		AlertName:   "HighCPU",
		Status:      core.StatusResolved, // Changed
		Labels:      alert1.Labels,       // Same labels -> same fingerprint
		StartsAt:    alert1.StartsAt,
		Fingerprint: result1.Alert.Fingerprint, // Reuse fingerprint
	}

	endsAt := time.Now().Add(5 * time.Minute)
	alert2.EndsAt = &endsAt

	result2, err := service.ProcessAlert(ctx, alert2)

	require.NoError(t, err)
	require.NotNil(t, result2)

	// Verify result
	assert.Equal(t, ProcessActionUpdated, result2.Action)
	assert.True(t, result2.IsUpdate)
	assert.False(t, result2.IsDuplicate)
	assert.NotNil(t, result2.ExistingID)
	assert.Equal(t, result1.Alert.Fingerprint, *result2.ExistingID)

	// Verify alert was updated
	updated, err := storage.GetAlertByFingerprint(ctx, result1.Alert.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, core.StatusResolved, updated.Status)
	assert.NotNil(t, updated.EndsAt)
}

// TestProcessAlert_IgnoreDuplicate tests ignoring exact duplicates
func TestProcessAlert_IgnoreDuplicate(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create initial alert
	alert1 := &core.Alert{
		AlertName: "HighCPU",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		StartsAt: time.Now(),
	}

	result1, err := service.ProcessAlert(ctx, alert1)
	require.NoError(t, err)
	assert.Equal(t, ProcessActionCreated, result1.Action)

	// Send exact duplicate
	alert2 := &core.Alert{
		AlertName:   alert1.AlertName,
		Status:      alert1.Status, // Same
		Labels:      alert1.Labels,
		StartsAt:    alert1.StartsAt,
		Fingerprint: result1.Alert.Fingerprint,
	}

	result2, err := service.ProcessAlert(ctx, alert2)

	require.NoError(t, err)
	require.NotNil(t, result2)

	// Verify result
	assert.Equal(t, ProcessActionIgnored, result2.Action)
	assert.False(t, result2.IsUpdate)
	assert.True(t, result2.IsDuplicate)
	assert.NotNil(t, result2.ExistingID)
	assert.Equal(t, result1.Alert.Fingerprint, *result2.ExistingID)

	// Verify alert was not modified
	existing, err := storage.GetAlertByFingerprint(ctx, result1.Alert.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, alert1.Status, existing.Status)
}

// TestProcessAlert_NilAlert tests nil alert handling
func TestProcessAlert_NilAlert(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()
	result, err := service.ProcessAlert(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "alert is nil")
}

// TestProcessAlert_EmptyFingerprint tests alert with empty labels (no fingerprint)
func TestProcessAlert_EmptyFingerprint(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()
	alert := &core.Alert{
		AlertName: "TestAlert",
		Status:    core.StatusFiring,
		Labels:    map[string]string{}, // Empty labels
		StartsAt:  time.Now(),
	}

	result, err := service.ProcessAlert(ctx, alert)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to generate fingerprint")
}

// TestProcessAlert_StorageErrors tests storage error handling
func TestProcessAlert_StorageErrors(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(*mockAlertStorage)
		wantErr   string
	}{
		{
			name: "storage error on get",
			setupFunc: func(m *mockAlertStorage) {
				m.getErr = errors.New("database connection error")
			},
			wantErr: "storage error",
		},
		{
			name: "storage error on save",
			setupFunc: func(m *mockAlertStorage) {
				m.saveErr = errors.New("disk full")
			},
			wantErr: "failed to create alert",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := newMockAlertStorage()
			tt.setupFunc(storage)

			service, err := NewDeduplicationService(&DeduplicationConfig{
				Storage: storage,
			})
			require.NoError(t, err)

			ctx := context.Background()
			alert := &core.Alert{
				AlertName: "TestAlert",
				Status:    core.StatusFiring,
				Labels: map[string]string{
					"alertname": "TestAlert",
				},
				StartsAt: time.Now(),
			}

			result, err := service.ProcessAlert(ctx, alert)

			assert.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestGetDuplicateStats tests statistics retrieval
func TestGetDuplicateStats(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Process multiple alerts
	alerts := []*core.Alert{
		// Alert 1: Created
		{
			AlertName: "Alert1",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "Alert1"},
			StartsAt:  time.Now(),
		},
		// Alert 2: Created
		{
			AlertName: "Alert2",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "Alert2"},
			StartsAt:  time.Now(),
		},
		// Alert 1: Duplicate (ignored)
		{
			AlertName: "Alert1",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "Alert1"},
			StartsAt:  time.Now(),
		},
	}

	var fingerprint1 string
	for i, alert := range alerts {
		result, err := service.ProcessAlert(ctx, alert)
		require.NoError(t, err, "alert %d failed", i)

		if i == 0 {
			fingerprint1 = result.Alert.Fingerprint
		} else if i == 2 {
			// Reuse fingerprint for duplicate
			alert.Fingerprint = fingerprint1
		}
	}

	// Update Alert 1 (status change)
	updateAlert := &core.Alert{
		AlertName:   "Alert1",
		Status:      core.StatusResolved, // Changed
		Labels:      map[string]string{"alertname": "Alert1"},
		StartsAt:    time.Now(),
		Fingerprint: fingerprint1,
	}
	endsAt := time.Now()
	updateAlert.EndsAt = &endsAt

	_, err = service.ProcessAlert(ctx, updateAlert)
	require.NoError(t, err)

	// Get stats
	stats, err := service.GetDuplicateStats(ctx)

	require.NoError(t, err)
	require.NotNil(t, stats)

	// Verify stats
	assert.Equal(t, int64(4), stats.TotalProcessed, "should have processed 4 alerts")
	assert.Equal(t, int64(2), stats.Created, "should have created 2 alerts")
	assert.Equal(t, int64(1), stats.Updated, "should have updated 1 alert")
	assert.Equal(t, int64(1), stats.Ignored, "should have ignored 1 alert")

	// Verify rates
	assert.InDelta(t, 50.0, stats.DeduplicationRate, 0.1, "deduplication rate should be 50%")
	assert.InDelta(t, 25.0, stats.UpdateRate, 0.1, "update rate should be 25%")
	assert.InDelta(t, 25.0, stats.IgnoreRate, 0.1, "ignore rate should be 25%")

	// Verify average processing time
	assert.Greater(t, stats.AverageProcessingTime, time.Duration(0))
}

// TestResetStats tests statistics reset
func TestResetStats(t *testing.T) {
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
		Labels:    map[string]string{"alertname": "TestAlert"},
		StartsAt:  time.Now(),
	}

	_, err = service.ProcessAlert(ctx, alert)
	require.NoError(t, err)

	// Verify stats exist
	statsBefore, err := service.GetDuplicateStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), statsBefore.TotalProcessed)

	// Reset stats
	err = service.ResetStats(ctx)
	require.NoError(t, err)

	// Verify stats are reset
	statsAfter, err := service.GetDuplicateStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), statsAfter.TotalProcessed)
	assert.Equal(t, int64(0), statsAfter.Created)
	assert.Equal(t, int64(0), statsAfter.Updated)
	assert.Equal(t, int64(0), statsAfter.Ignored)
}

// TestProcessAction_String tests ProcessAction string representation
func TestProcessAction_String(t *testing.T) {
	tests := []struct {
		action   ProcessAction
		expected string
	}{
		{ProcessActionCreated, "created"},
		{ProcessActionUpdated, "updated"},
		{ProcessActionIgnored, "ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.action.String())
		})
	}
}

// TestProcessAlert_ConcurrentProcessing tests concurrent alert processing
func TestProcessAlert_ConcurrentProcessing(t *testing.T) {
	storage := newMockAlertStorage()
	service, err := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})
	require.NoError(t, err)

	ctx := context.Background()
	numAlerts := 100

	// Process many alerts concurrently
	done := make(chan bool, numAlerts)
	for i := 0; i < numAlerts; i++ {
		go func(idx int) {
			alert := &core.Alert{
				AlertName: "Alert",
				Status:    core.StatusFiring,
				Labels: map[string]string{
					"alertname": "Alert",
					"instance":  string(rune(idx)), // Different instance per alert
				},
				StartsAt: time.Now(),
			}

			_, err := service.ProcessAlert(ctx, alert)
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all to complete
	for i := 0; i < numAlerts; i++ {
		<-done
	}

	// Verify stats
	stats, err := service.GetDuplicateStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(numAlerts), stats.TotalProcessed)
}
