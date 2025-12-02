package services

import (
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

// BenchmarkFingerprintGenerator_FNV1a benchmarks FNV-1a fingerprint generation
func BenchmarkFingerprintGenerator_FNV1a(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
		"namespace": "production",
		"cluster":   "us-west-1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateFromLabels(labels)
	}
}

// BenchmarkFingerprintGenerator_SHA256 benchmarks SHA-256 fingerprint generation
func BenchmarkFingerprintGenerator_SHA256(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmSHA256})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
		"namespace": "production",
		"cluster":   "us-west-1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateFromLabels(labels)
	}
}

// BenchmarkFingerprintGenerator_FNV1a_SmallLabels benchmarks FNV-1a with minimal labels
func BenchmarkFingerprintGenerator_FNV1a_SmallLabels(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname": "TestAlert",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateFromLabels(labels)
	}
}

// BenchmarkFingerprintGenerator_FNV1a_LargeLabels benchmarks FNV-1a with many labels
func BenchmarkFingerprintGenerator_FNV1a_LargeLabels(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname":   "HighCPU",
		"severity":    "critical",
		"instance":    "server-1.example.com:9090",
		"namespace":   "production",
		"cluster":     "us-west-1",
		"team":        "platform",
		"env":         "prod",
		"region":      "us-west",
		"datacenter":  "dc1",
		"application": "api-gateway",
		"version":     "v1.2.3",
		"job":         "monitoring",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateFromLabels(labels)
	}
}

// BenchmarkFingerprintGenerator_Generate_Alert benchmarks full alert fingerprinting
func BenchmarkFingerprintGenerator_Generate_Alert(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	alert := &core.Alert{
		AlertName: "HighCPU",
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "server-1",
			"namespace": "production",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.Generate(alert)
	}
}

// BenchmarkFingerprintGenerator_Parallel benchmarks concurrent fingerprint generation
func BenchmarkFingerprintGenerator_Parallel(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
		"namespace": "production",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = generator.GenerateFromLabels(labels)
		}
	})
}

// BenchmarkValidateFingerprint_FNV1a benchmarks fingerprint validation
func BenchmarkValidateFingerprint_FNV1a(b *testing.B) {
	fingerprint := "a1b2c3d4e5f60708"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateFingerprint(fingerprint, AlgorithmFNV1a)
	}
}

// BenchmarkValidateFingerprint_SHA256 benchmarks SHA-256 fingerprint validation
func BenchmarkValidateFingerprint_SHA256(b *testing.B) {
	fingerprint := "a1b2c3d4e5f607089a1b2c3d4e5f607089a1b2c3d4e5f607089a1b2c3d4e5f60"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateFingerprint(fingerprint, AlgorithmSHA256)
	}
}

// BenchmarkGenerateWithAlgorithm_FNV1a benchmarks algorithm-specific generation
func BenchmarkGenerateWithAlgorithm_FNV1a(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateWithAlgorithm(labels, AlgorithmFNV1a)
	}
}

// BenchmarkGenerateWithAlgorithm_SHA256 benchmarks SHA-256 algorithm-specific generation
func BenchmarkGenerateWithAlgorithm_SHA256(b *testing.B) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmSHA256})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateWithAlgorithm(labels, AlgorithmSHA256)
	}
}

// BenchmarkFingerprintGenerator_Comparison benchmarks algorithm comparison
func BenchmarkFingerprintGenerator_Comparison(b *testing.B) {
	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
		"namespace": "production",
	}

	b.Run("FNV-1a", func(b *testing.B) {
		generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = generator.GenerateFromLabels(labels)
		}
	})

	b.Run("SHA-256", func(b *testing.B) {
		generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmSHA256})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = generator.GenerateFromLabels(labels)
		}
	})
}
