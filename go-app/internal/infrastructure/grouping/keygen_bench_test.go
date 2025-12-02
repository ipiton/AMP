package grouping

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// BenchmarkGenerateKey_Simple benchmarks simple key generation (2 labels)
//
// Target: <50μs (150% goal)
// Expected: ~20-30μs
func BenchmarkGenerateKey_Simple(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkGenerateKey_Complex benchmarks complex key generation (10 labels)
//
// Target: <100μs
// Expected: ~50-70μs
func BenchmarkGenerateKey_Complex(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname":   "HighCPU",
		"cluster":     "production",
		"environment": "us-east-1",
		"instance":    "server-12345",
		"namespace":   "monitoring",
		"pod":         "alertmanager-0",
		"service":     "alertmanager",
		"severity":    "critical",
		"team":        "platform",
		"zone":        "us-east-1a",
	}
	groupBy := []string{
		"alertname", "cluster", "environment", "instance", "namespace",
		"pod", "service", "severity", "team", "zone",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkGenerateKey_SpecialGrouping benchmarks special grouping ("...")
//
// Target: <100μs
// Expected: ~40-60μs
func BenchmarkGenerateKey_SpecialGrouping(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
		"namespace": "default",
		"pod":       "app-0",
	}
	groupBy := []string{"..."}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkGenerateKey_GlobalGrouping benchmarks global grouping ([])
//
// Target: <10μs (very fast)
// Expected: ~1-5μs
func BenchmarkGenerateKey_GlobalGrouping(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkGenerateKey_MissingLabels benchmarks key generation with missing labels
//
// Target: <50μs
// Expected: ~25-35μs
func BenchmarkGenerateKey_MissingLabels(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		// Missing: cluster, instance
	}
	groupBy := []string{"alertname", "cluster", "instance"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkGenerateKey_URLEncoding benchmarks key generation with special characters
//
// Target: <50μs
// Expected: ~30-40μs (slightly slower due to encoding)
func BenchmarkGenerateKey_URLEncoding(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "High CPU Usage",
		"message":   "CPU usage is high, please investigate",
		"team":      "platform-engineering",
	}
	groupBy := []string{"alertname", "message", "team"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkGenerateHash benchmarks hash generation
//
// Target: <10μs
// Expected: ~5-8μs
func BenchmarkGenerateHash(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateHash(labels, groupBy)
	}
}

// BenchmarkHashFNV1a benchmarks raw FNV-1a hashing
//
// Target: <1μs (very fast)
// Expected: ~100-200ns
func BenchmarkHashFNV1a(b *testing.B) {
	key := "alertname=HighCPU,cluster=prod,instance=server1"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = hashFNV1a(key)
	}
}

// BenchmarkHashFromKey benchmarks HashFromKey convenience function
//
// Target: <1μs
// Expected: ~100-200ns
func BenchmarkHashFromKey(b *testing.B) {
	key := GroupKey("alertname=HighCPU,cluster=prod")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = HashFromKey(key)
	}
}

// BenchmarkGroupKey_IsSpecial benchmarks IsSpecial method
//
// Target: <100ns (very fast)
// Expected: ~10-50ns
func BenchmarkGroupKey_IsSpecial(b *testing.B) {
	keys := []GroupKey{
		GlobalGroupKey,
		EmptyGroupKey,
		GroupKey("{hash:a1b2c3d4e5f60708}"),
		GroupKey("alertname=HighCPU"),
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, key := range keys {
			_ = key.IsSpecial()
		}
	}
}

// BenchmarkGroupKey_Matches benchmarks Matches method
//
// Target: <50μs
// Expected: ~25-35μs
func BenchmarkGroupKey_Matches(b *testing.B) {
	gen := NewGroupKeyGenerator()
	key := GroupKey("alertname=HighCPU,cluster=prod")
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
	}
	groupBy := []string{"alertname", "cluster"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = key.Matches(labels, groupBy, gen)
	}
}

// BenchmarkConcurrent benchmarks concurrent key generation
//
// Target: >20K ops/sec (150% goal)
// Expected: ~50-100K ops/sec
func BenchmarkConcurrent(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = gen.GenerateKey(labels, groupBy)
		}
	})
}

// BenchmarkWithHashLongKeys benchmarks automatic hashing of long keys
//
// Target: <100μs
// Expected: ~60-80μs
func BenchmarkWithHashLongKeys(b *testing.B) {
	gen := NewGroupKeyGenerator(
		WithHashLongKeys(true),
		WithMaxKeyLength(50),
	)

	labels := map[string]string{
		"alertname": "VeryLongAlertNameThatExceedsMaxLength",
		"cluster":   "production-us-east-1-cluster",
		"instance":  "server-with-very-long-name-12345",
	}
	groupBy := []string{"alertname", "cluster", "instance"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkWithValidation benchmarks key generation with validation enabled
//
// Target: <50μs
// Expected: ~30-40μs (slightly slower due to validation)
func BenchmarkWithValidation(b *testing.B) {
	gen := NewGroupKeyGenerator(WithValidation(true))
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = gen.GenerateKey(labels, groupBy)
	}
}

// BenchmarkMemoryAllocation benchmarks memory allocation patterns
//
// Target: <500 bytes/op
// Expected: ~200-400 bytes/op
func BenchmarkMemoryAllocation(b *testing.B) {
	gen := NewGroupKeyGenerator()

	b.Run("Simple", func(b *testing.B) {
		labels := map[string]string{
			"alertname": "HighCPU",
			"cluster":   "prod",
		}
		groupBy := []string{"alertname", "cluster"}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, _ = gen.GenerateKey(labels, groupBy)
		}
	})

	b.Run("Complex", func(b *testing.B) {
		labels := map[string]string{
			"alertname":   "HighCPU",
			"cluster":     "prod",
			"environment": "us-east-1",
			"instance":    "server-12345",
			"namespace":   "monitoring",
		}
		groupBy := []string{"alertname", "cluster", "environment", "instance", "namespace"}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, _ = gen.GenerateKey(labels, groupBy)
		}
	})
}

// BenchmarkVaryingLabelCounts benchmarks performance with different label counts
func BenchmarkVaryingLabelCounts(b *testing.B) {
	gen := NewGroupKeyGenerator()

	labelCounts := []int{1, 2, 5, 10, 20, 50}

	for _, count := range labelCounts {
		b.Run(fmt.Sprintf("Labels_%d", count), func(b *testing.B) {
			labels := make(map[string]string, count)
			groupBy := make([]string, count)

			for i := 0; i < count; i++ {
				labelName := fmt.Sprintf("label_%d", i)
				labels[labelName] = fmt.Sprintf("value_%d", i)
				groupBy[i] = labelName
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = gen.GenerateKey(labels, groupBy)
			}
		})
	}
}

// BenchmarkStringBuilder benchmarks string builder performance
//
// This benchmark compares our optimized implementation with naive concatenation
func BenchmarkStringBuilder(b *testing.B) {
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
	}
	labelNames := []string{"alertname", "cluster", "instance"}

	b.Run("Optimized", func(b *testing.B) {
		gen := NewGroupKeyGenerator()

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, _ = gen.buildKey(labels, labelNames)
		}
	})

	b.Run("Naive", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			key := ""
			for i, name := range labelNames {
				if i > 0 {
					key += ","
				}
				key += name + "=" + labels[name]
			}
			_ = key
		}
	})
}

// BenchmarkSyncPool benchmarks sync.Pool effectiveness
func BenchmarkSyncPool(b *testing.B) {
	gen := NewGroupKeyGenerator()
	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	b.Run("WithPool", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			_, _ = gen.GenerateKey(labels, groupBy)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			var builder strings.Builder
			builder.WriteString("alertname=")
			builder.WriteString(labels["alertname"])
			builder.WriteByte(',')
			builder.WriteString("cluster=")
			builder.WriteString(labels["cluster"])
			_ = builder.String()
		}
	})
}

// BenchmarkConcurrentStress benchmarks performance under high concurrent load
//
// Target: No degradation under load
// Expected: Similar performance to single-threaded
func BenchmarkConcurrentStress(b *testing.B) {
	gen := NewGroupKeyGenerator()

	goroutineCounts := []int{1, 10, 100, 1000}

	for _, count := range goroutineCounts {
		b.Run(fmt.Sprintf("Goroutines_%d", count), func(b *testing.B) {
			labels := map[string]string{
				"alertname": "HighCPU",
				"cluster":   "prod",
			}
			groupBy := []string{"alertname", "cluster"}

			b.ResetTimer()
			b.ReportAllocs()

			var wg sync.WaitGroup
			iterations := b.N / count
			if iterations == 0 {
				iterations = 1
			}

			for i := 0; i < count; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < iterations; j++ {
						_, _ = gen.GenerateKey(labels, groupBy)
					}
				}()
			}

			wg.Wait()
		})
	}
}
