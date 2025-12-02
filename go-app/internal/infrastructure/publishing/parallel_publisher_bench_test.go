package publishing

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Benchmark: ParallelPublishResult creation
func BenchmarkParallelPublishResult_Creation(b *testing.B) {
	targetResults := make([]TargetPublishResult, 10)
	for i := 0; i < 10; i++ {
		statusCode := 200
		targetResults[i] = TargetPublishResult{
			TargetName: fmt.Sprintf("target-%d", i),
			TargetType: "webhook",
			Success:    i < 8, // 8 success, 2 failures
			StatusCode: &statusCode,
			Duration:   10 * time.Millisecond,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &ParallelPublishResult{
			TotalTargets: 10,
			SuccessCount: 8,
			FailureCount: 2,
			SkippedCount: 0,
			Results:      targetResults,
			Duration:     100 * time.Millisecond,
		}
	}
}

// Benchmark: ParallelPublishResult.SuccessRate calculation
func BenchmarkParallelPublishResult_SuccessRate(b *testing.B) {
	result := &ParallelPublishResult{
		TotalTargets: 100,
		SuccessCount: 75,
		FailureCount: 25,
		SkippedCount: 0,
		Results:      []TargetPublishResult{},
		Duration:     100 * time.Millisecond,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = result.SuccessRate()
	}
}

// Benchmark: ParallelPublishOptions validation
func BenchmarkParallelPublishOptions_Validate(b *testing.B) {
	opts := DefaultParallelPublishOptions()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = opts.Validate()
	}
}

// Benchmark: Mock target filtering (simulates filterHealthyTargets)
func BenchmarkFilterTargets(b *testing.B) {
	targets := make([]*core.PublishingTarget, 100)
	for i := 0; i < 100; i++ {
		targets[i] = &core.PublishingTarget{
			Name:    fmt.Sprintf("target-%d", i),
			Type:    "webhook",
			Enabled: i%10 != 0, // 90% enabled
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filtered := make([]*core.PublishingTarget, 0, len(targets))
		for _, target := range targets {
			if target.Enabled {
				filtered = append(filtered, target)
			}
		}
		_ = filtered
	}
}

// Benchmark: Concurrent target processing (simulates fan-out)
func BenchmarkConcurrentProcessing(b *testing.B) {
	sizes := []int{1, 5, 10, 25, 50}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("targets_%d", size), func(b *testing.B) {
			benchmarkConcurrentProcessing(b, size)
		})
	}
}

func benchmarkConcurrentProcessing(b *testing.B, numTargets int) {
	// Create test targets
	targets := make([]*core.PublishingTarget, numTargets)
	for i := 0; i < numTargets; i++ {
		targets[i] = &core.PublishingTarget{
			Name:    fmt.Sprintf("target-%d", i),
			Type:    "webhook",
			Enabled: true,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		results := make(chan TargetPublishResult, len(targets))

		for _, target := range targets {
			wg.Add(1)
			go func(t *core.PublishingTarget) {
				defer wg.Done()
				// Simulate processing time
				time.Sleep(1 * time.Microsecond)
				statusCode := 200
				results <- TargetPublishResult{
					TargetName: t.Name,
					TargetType: t.Type,
					Success:    true,
					StatusCode: &statusCode,
					Duration:   1 * time.Microsecond,
				}
			}(target)
		}

		go func() {
			wg.Wait()
			close(results)
		}()

		// Collect results
		collected := make([]TargetPublishResult, 0, len(targets))
		for result := range results {
			collected = append(collected, result)
		}
	}
}

// Benchmark: Result aggregation
func BenchmarkResultAggregation(b *testing.B) {
	sizes := []int{1, 5, 10, 25, 50, 100}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("results_%d", size), func(b *testing.B) {
			benchmarkResultAggregation(b, size)
		})
	}
}

func benchmarkResultAggregation(b *testing.B, numResults int) {
	// Create test results
	targetResults := make([]TargetPublishResult, numResults)
	for i := 0; i < numResults; i++ {
		statusCode := 200
		success := true
		if i%10 == 0 {
			statusCode = 500 // 10% failures
			success = false
		}
		targetResults[i] = TargetPublishResult{
			TargetName: fmt.Sprintf("target-%d", i),
			TargetType: "webhook",
			Success:    success,
			StatusCode: &statusCode,
			Duration:   10 * time.Millisecond,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		successCount := 0
		failureCount := 0
		skippedCount := 0

		for _, result := range targetResults {
			if result.Skipped {
				skippedCount++
			} else if result.Error != nil || (result.StatusCode != nil && *result.StatusCode >= 400) {
				failureCount++
			} else {
				successCount++
			}
		}

		_ = &ParallelPublishResult{
			TotalTargets: len(targetResults),
			SuccessCount: successCount,
			FailureCount: failureCount,
			SkippedCount: skippedCount,
			Results:      targetResults,
			Duration:     100 * time.Millisecond,
		}
	}
}

// Benchmark: Context with timeout (simulates PublishToMultiple with timeout)
func BenchmarkContextTimeout(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = ctx
		cancel()
	}
}

// Benchmark: Channel operations (simulates result collection)
func BenchmarkChannelOperations(b *testing.B) {
	sizes := []int{1, 5, 10, 25, 50}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("items_%d", size), func(b *testing.B) {
			benchmarkChannelOperations(b, size)
		})
	}
}

func benchmarkChannelOperations(b *testing.B, numItems int) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results := make(chan TargetPublishResult, numItems)

		// Send results
		go func() {
			for j := 0; j < numItems; j++ {
				statusCode := 200
				results <- TargetPublishResult{
					TargetName: fmt.Sprintf("target-%d", j),
					TargetType: "webhook",
					Success:    true,
					StatusCode: &statusCode,
					Duration:   1 * time.Microsecond,
				}
			}
			close(results)
		}()

		// Collect results
		collected := make([]TargetPublishResult, 0, numItems)
		for result := range results {
			collected = append(collected, result)
		}
	}
}
