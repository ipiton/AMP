package publishing

import (
	"context"
	"log/slog"
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

// BenchmarkGetCurrentMode measures the performance of GetCurrentMode (should be <100ns)
func BenchmarkGetCurrentMode(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)
	modeManager.CheckModeTransition()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = modeManager.GetCurrentMode()
	}
}

// BenchmarkIsMetricsOnly measures the performance of IsMetricsOnly (should be <100ns)
func BenchmarkIsMetricsOnly(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)
	modeManager.CheckModeTransition()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = modeManager.IsMetricsOnly()
	}
}

// BenchmarkCheckModeTransition measures the performance of mode checking
func BenchmarkCheckModeTransition(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = modeManager.CheckModeTransition()
	}
}

// BenchmarkGetModeMetrics measures the performance of GetModeMetrics
func BenchmarkGetModeMetrics(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)
	modeManager.CheckModeTransition()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = modeManager.GetModeMetrics()
	}
}

// BenchmarkConcurrentGetCurrentMode measures concurrent read performance
func BenchmarkConcurrentGetCurrentMode(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)
	modeManager.CheckModeTransition()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = modeManager.GetCurrentMode()
		}
	})
}

// BenchmarkConcurrentIsMetricsOnly measures concurrent IsMetricsOnly performance
func BenchmarkConcurrentIsMetricsOnly(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)
	modeManager.CheckModeTransition()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = modeManager.IsMetricsOnly()
		}
	})
}

// BenchmarkModeManagerWithCaching measures caching effectiveness
func BenchmarkModeManagerWithCaching(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	stubDiscovery.AddTarget(&core.PublishingTarget{
		Name:    "test-target",
		Type:    "webhook",
		Enabled: true,
	})

	modeManager := NewModeManager(stubDiscovery, logger, nil)
	modeManager.CheckModeTransition()

	b.Run("Sequential", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = modeManager.GetCurrentMode()
		}
	})

	b.Run("Concurrent", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = modeManager.GetCurrentMode()
			}
		})
	})
}

// BenchmarkStartStop measures lifecycle overhead
func BenchmarkStartStop(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		modeManager := NewModeManager(stubDiscovery, logger, nil)
		ctx := context.Background()
		_ = modeManager.Start(ctx)
		_ = modeManager.Stop()
	}
}

// BenchmarkSubscribe measures subscription overhead
func BenchmarkSubscribe(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	callback := func(from, to Mode, reason string) {
		// No-op callback
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		unsubscribe := modeManager.Subscribe(callback)
		unsubscribe()
	}
}

// BenchmarkModeTransition measures full transition overhead
func BenchmarkModeTransition(b *testing.B) {
	logger := slog.Default()
	stubDiscovery := NewStubTargetDiscoveryManager(logger)
	modeManager := NewModeManager(stubDiscovery, logger, nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			// Add target (transition to normal)
			stubDiscovery.AddTarget(&core.PublishingTarget{
				Name:    "test-target",
				Type:    "webhook",
				Enabled: true,
			})
		} else {
			// Remove target (transition to metrics-only)
			stubDiscovery.ClearTargets()
		}
		_, _, _ = modeManager.CheckModeTransition()
	}
}
