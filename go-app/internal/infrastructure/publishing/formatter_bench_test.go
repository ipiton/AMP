package publishing

import (
	"context"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Benchmark all format functions to verify <500μs p50 target

// BenchmarkFormatAlertmanager benchmarks Alertmanager v4 format
// Target: <400μs per operation
func BenchmarkFormatAlertmanager(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatAlertmanager)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatRootly benchmarks Rootly incident format
// Target: <500μs per operation
func BenchmarkFormatRootly(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatRootly)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatPagerDuty benchmarks PagerDuty Events API v2 format
// Target: <300μs per operation
func BenchmarkFormatPagerDuty(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatPagerDuty)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatSlack benchmarks Slack Blocks API format
// Target: <600μs per operation (most complex)
func BenchmarkFormatSlack(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatSlack)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatWebhook benchmarks generic webhook format
// Target: <200μs per operation (simplest)
func BenchmarkFormatWebhook(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatWebhook)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatAlert_AllFormats benchmarks all formats sequentially
// Useful for comparing relative performance
func BenchmarkFormatAlert_AllFormats(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	formats := []core.PublishingFormat{
		core.FormatAlertmanager,
		core.FormatRootly,
		core.FormatPagerDuty,
		core.FormatSlack,
		core.FormatWebhook,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, format := range formats {
			_, err := formatter.FormatAlert(ctx, alert, format)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkFormatAlert_WithoutClassification benchmarks formatting without LLM data
// Measures overhead of classification injection
func BenchmarkFormatAlert_WithoutClassification(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlertNoClassification()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatAlertmanager)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatAlert_WithLongClassification benchmarks with long reasoning/recommendations
// Tests truncation performance
func BenchmarkFormatAlert_WithLongClassification(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlertLongClassification()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := formatter.FormatAlert(ctx, alert, core.FormatRootly)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormatAlert_Parallel benchmarks concurrent formatting
// Tests thread-safety and scalability
func BenchmarkFormatAlert_Parallel(b *testing.B) {
	formatter := NewAlertFormatter()
	alert := createBenchmarkAlert()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := formatter.FormatAlert(ctx, alert, core.FormatAlertmanager)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkNewAlertFormatter benchmarks formatter construction
// Target: <1μs (should be negligible)
func BenchmarkNewAlertFormatter(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewAlertFormatter()
	}
}

// BenchmarkTruncateString benchmarks string truncation helper
// Target: <1μs per operation
func BenchmarkTruncateString(b *testing.B) {
	longString := generateLongString(1000)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = truncateString(longString, 500)
	}
}

// Helper functions for benchmarks

// createBenchmarkAlert creates a realistic alert for benchmarking
func createBenchmarkAlert() *core.EnrichedAlert {
	now := time.Now()
	generatorURL := "http://prometheus:9090/graph?g0.expr=up%7Bjob%3D%22node%22%7D+%3D%3D+0&g0.tab=1"

	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "abc123def456ghi789jkl012mno345pqr678stu901vwx234yz",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"alertname": "HighCPUUsage",
				"severity":  "warning",
				"namespace": "production",
				"cluster":   "us-east-1",
				"instance":  "node-01.example.com:9100",
				"job":       "node-exporter",
				"datacenter": "aws-us-east-1a",
				"team":      "platform",
			},
			Annotations: map[string]string{
				"summary":     "High CPU usage detected on node-01",
				"description": "CPU usage is above 80% for more than 5 minutes on node-01.example.com",
				"runbook_url": "https://wiki.example.com/runbooks/high-cpu",
				"dashboard":   "https://grafana.example.com/d/node-exporter",
			},
			StartsAt:     now,
			EndsAt:       nil,
			GeneratorURL: &generatorURL,
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityWarning,
			Confidence: 0.87,
			Reasoning:  "This alert indicates sustained high CPU usage which could lead to performance degradation. The issue has persisted for 5+ minutes, suggesting it's not a transient spike.",
			Recommendations: []string{
				"Check for runaway processes using 'top' or 'htop'",
				"Review recent deployments that might have caused increased load",
				"Verify auto-scaling is functioning correctly",
				"Check if this is expected load (e.g., batch job, backup)",
				"Consider scaling horizontally if sustained high load",
			},
		},
		EnrichmentMetadata: map[string]any{
			"enriched_at": now.Format(time.RFC3339),
			"version":     "1.0",
		},
	}
}

// createBenchmarkAlertNoClassification creates alert without classification
func createBenchmarkAlertNoClassification() *core.EnrichedAlert {
	alert := createBenchmarkAlert()
	alert.Classification = nil
	return alert
}

// createBenchmarkAlertLongClassification creates alert with very long reasoning
func createBenchmarkAlertLongClassification() *core.EnrichedAlert {
	alert := createBenchmarkAlert()
	alert.Classification.Reasoning = generateLongString(2000)
	alert.Classification.Recommendations = []string{
		"Recommendation 1: " + generateLongString(100),
		"Recommendation 2: " + generateLongString(100),
		"Recommendation 3: " + generateLongString(100),
		"Recommendation 4: " + generateLongString(100),
		"Recommendation 5: " + generateLongString(100),
		"Recommendation 6: " + generateLongString(100),
		"Recommendation 7: " + generateLongString(100),
		"Recommendation 8: " + generateLongString(100),
	}
	return alert
}

// generateLongString generates a string of specified length for testing
func generateLongString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[i%len(charset)]
	}
	return string(result)
}
