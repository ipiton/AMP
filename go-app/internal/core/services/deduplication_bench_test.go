package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// BenchmarkProcessAlert_CreateNew benchmarks creating a new alert
func BenchmarkProcessAlert_CreateNew(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alert := &core.Alert{
			AlertName: fmt.Sprintf("Alert%d", i),
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname": fmt.Sprintf("Alert%d", i),
				"severity":  "critical",
			},
			StartsAt: time.Now(),
		}

		_, _ = service.ProcessAlert(ctx, alert)
	}
}

// BenchmarkProcessAlert_UpdateExisting benchmarks updating an existing alert
func BenchmarkProcessAlert_UpdateExisting(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	// Create initial alert
	initialAlert := &core.Alert{
		AlertName: "TestAlert",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "TestAlert",
		},
		StartsAt: time.Now(),
	}

	result, _ := service.ProcessAlert(ctx, initialAlert)

	// Benchmark updates
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updateAlert := &core.Alert{
			AlertName:   "TestAlert",
			Status:      core.StatusResolved, // Changed
			Labels:      initialAlert.Labels,
			StartsAt:    initialAlert.StartsAt,
			Fingerprint: result.Alert.Fingerprint,
		}
		endsAt := time.Now()
		updateAlert.EndsAt = &endsAt

		_, _ = service.ProcessAlert(ctx, updateAlert)

		// Reset status for next iteration
		initialAlert.Status = core.StatusFiring
	}
}

// BenchmarkProcessAlert_IgnoreDuplicate benchmarks ignoring exact duplicates
func BenchmarkProcessAlert_IgnoreDuplicate(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	// Create initial alert
	initialAlert := &core.Alert{
		AlertName: "TestAlert",
		Status:    core.StatusFiring,
		Labels: map[string]string{
			"alertname": "TestAlert",
		},
		StartsAt: time.Now(),
	}

	result, _ := service.ProcessAlert(ctx, initialAlert)

	// Benchmark duplicates
	duplicateAlert := &core.Alert{
		AlertName:   initialAlert.AlertName,
		Status:      initialAlert.Status,
		Labels:      initialAlert.Labels,
		StartsAt:    initialAlert.StartsAt,
		Fingerprint: result.Alert.Fingerprint,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.ProcessAlert(ctx, duplicateAlert)
	}
}

// BenchmarkProcessAlert_WithFingerprintGeneration benchmarks with fingerprint generation
func BenchmarkProcessAlert_WithFingerprintGeneration(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alert := &core.Alert{
			AlertName: fmt.Sprintf("Alert%d", i),
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname": fmt.Sprintf("Alert%d", i),
				"severity":  "critical",
				"instance":  "server-1",
			},
			StartsAt: time.Now(),
			// No fingerprint - will be generated
		}

		_, _ = service.ProcessAlert(ctx, alert)
	}
}

// BenchmarkProcessAlert_SmallAlert benchmarks processing small alerts (minimal labels)
func BenchmarkProcessAlert_SmallAlert(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alert := &core.Alert{
			AlertName: fmt.Sprintf("Alert%d", i),
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname": fmt.Sprintf("Alert%d", i),
			},
			StartsAt: time.Now(),
		}

		_, _ = service.ProcessAlert(ctx, alert)
	}
}

// BenchmarkProcessAlert_LargeAlert benchmarks processing large alerts (many labels)
func BenchmarkProcessAlert_LargeAlert(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alert := &core.Alert{
			AlertName: fmt.Sprintf("Alert%d", i),
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname":   fmt.Sprintf("Alert%d", i),
				"severity":    "critical",
				"instance":    "server-1.example.com:9090",
				"namespace":   "production",
				"cluster":     "us-west-1",
				"team":        "platform",
				"env":         "prod",
				"region":      "us-west",
				"datacenter":  "dc1",
				"application": "api-gateway",
			},
			Annotations: map[string]string{
				"summary":     "High CPU usage detected",
				"description": "CPU usage is above 90% for more than 5 minutes",
				"runbook":     "https://runbook.example.com/high-cpu",
			},
			StartsAt: time.Now(),
		}

		_, _ = service.ProcessAlert(ctx, alert)
	}
}

// BenchmarkGetDuplicateStats benchmarks statistics retrieval
func BenchmarkGetDuplicateStats(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	// Process some alerts
	for i := 0; i < 100; i++ {
		alert := &core.Alert{
			AlertName: fmt.Sprintf("Alert%d", i),
			Status:    core.StatusFiring,
			Labels: map[string]string{
				"alertname": fmt.Sprintf("Alert%d", i),
			},
			StartsAt: time.Now(),
		}
		_, _ = service.ProcessAlert(ctx, alert)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetDuplicateStats(ctx)
	}
}

// BenchmarkProcessAlert_Parallel benchmarks concurrent alert processing
func BenchmarkProcessAlert_Parallel(b *testing.B) {
	storage := newMockAlertStorage()
	service, _ := NewDeduplicationService(&DeduplicationConfig{
		Storage: storage,
	})

	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			alert := &core.Alert{
				AlertName: fmt.Sprintf("Alert%d", i),
				Status:    core.StatusFiring,
				Labels: map[string]string{
					"alertname": fmt.Sprintf("Alert%d", i),
					"severity":  "critical",
				},
				StartsAt: time.Now(),
			}

			_, _ = service.ProcessAlert(ctx, alert)
			i++
		}
	})
}

// BenchmarkProcessAlert_Comparison benchmarks all three actions
func BenchmarkProcessAlert_Comparison(b *testing.B) {
	b.Run("Create", func(b *testing.B) {
		storage := newMockAlertStorage()
		service, _ := NewDeduplicationService(&DeduplicationConfig{
			Storage: storage,
		})
		ctx := context.Background()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			alert := &core.Alert{
				AlertName: fmt.Sprintf("Alert%d", i),
				Status:    core.StatusFiring,
				Labels:    map[string]string{"alertname": fmt.Sprintf("Alert%d", i)},
				StartsAt:  time.Now(),
			}
			_, _ = service.ProcessAlert(ctx, alert)
		}
	})

	b.Run("Update", func(b *testing.B) {
		storage := newMockAlertStorage()
		service, _ := NewDeduplicationService(&DeduplicationConfig{
			Storage: storage,
		})
		ctx := context.Background()

		// Create initial alert
		alert := &core.Alert{
			AlertName: "TestAlert",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "TestAlert"},
			StartsAt:  time.Now(),
		}
		result, _ := service.ProcessAlert(ctx, alert)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			updateAlert := &core.Alert{
				AlertName:   "TestAlert",
				Status:      core.StatusResolved,
				Labels:      alert.Labels,
				StartsAt:    alert.StartsAt,
				Fingerprint: result.Alert.Fingerprint,
			}
			endsAt := time.Now()
			updateAlert.EndsAt = &endsAt

			_, _ = service.ProcessAlert(ctx, updateAlert)
			alert.Status = core.StatusFiring // Reset
		}
	})

	b.Run("Ignore", func(b *testing.B) {
		storage := newMockAlertStorage()
		service, _ := NewDeduplicationService(&DeduplicationConfig{
			Storage: storage,
		})
		ctx := context.Background()

		// Create initial alert
		alert := &core.Alert{
			AlertName: "TestAlert",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "TestAlert"},
			StartsAt:  time.Now(),
		}
		result, _ := service.ProcessAlert(ctx, alert)

		duplicateAlert := &core.Alert{
			AlertName:   alert.AlertName,
			Status:      alert.Status,
			Labels:      alert.Labels,
			StartsAt:    alert.StartsAt,
			Fingerprint: result.Alert.Fingerprint,
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = service.ProcessAlert(ctx, duplicateAlert)
		}
	})
}
