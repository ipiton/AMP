package publishing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// BenchmarkFormatAlert benchmarks the formatter with pooled maps
func BenchmarkFormatAlert(b *testing.B) {
	formatter := NewAlertFormatter()
	ctx := context.Background()

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			AlertName:   "TestAlert",
			Fingerprint: "abc123",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"severity":  "critical",
				"namespace": "production",
				"service":   "api",
			},
			Annotations: map[string]string{
				"summary":     "Test alert summary",
				"description": "Test alert description with some details",
			},
			StartsAt: time.Now(),
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityCritical,
			Confidence: 0.95,
			Reasoning:  "This is a critical alert because the API service is down in production",
			Recommendations: []string{
				"Check API service logs",
				"Verify database connectivity",
				"Review recent deployments",
			},
		},
	}

	formats := []core.PublishingFormat{
		core.FormatAlertmanager,
		core.FormatRootly,
		core.FormatPagerDuty,
		core.FormatSlack,
		core.FormatWebhook,
	}

	for _, format := range formats {
		b.Run(string(format), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				result, err := formatter.FormatAlert(ctx, enrichedAlert, format)
				if err != nil {
					b.Fatal(err)
				}
				// Simulate usage: access a field
				_ = result["status"]
			}
		})
	}
}

// BenchmarkFormatAlertParallel benchmarks the formatter under concurrent load
func BenchmarkFormatAlertParallel(b *testing.B) {
	formatter := NewAlertFormatter()
	ctx := context.Background()

	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			AlertName:   "TestAlert",
			Fingerprint: "abc123",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"severity":  "critical",
				"namespace": "production",
			},
			Annotations: map[string]string{
				"summary": "Test alert",
			},
			StartsAt: time.Now(),
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityCritical,
			Confidence: 0.95,
			Reasoning:  "Critical issue detected",
			Recommendations: []string{
				"Check logs",
				"Verify connectivity",
			},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			result, err := formatter.FormatAlert(ctx, enrichedAlert, core.FormatWebhook)
			if err != nil {
				b.Fatal(err)
			}
			_ = result["status"]
		}
	})
}

// BenchmarkStringBuilderPool benchmarks the string builder pool
func BenchmarkStringBuilderPool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			builder := getBuilder()
			builder.WriteString("Alert: TestAlert\n")
			builder.WriteString("Status: firing\n")
			builder.WriteString("Namespace: production\n")
			_ = builder.String()
			putBuilder(builder)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			var builder strings.Builder
			builder.WriteString("Alert: TestAlert\n")
			builder.WriteString("Status: firing\n")
			builder.WriteString("Namespace: production\n")
			_ = builder.String()
		}
	})
}

// BenchmarkFormatterResultPool benchmarks the formatter result pool
func BenchmarkFormatterResultPool(b *testing.B) {
	b.Run("WithPool", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			result := getFormatterResult()
			result["alert_name"] = "TestAlert"
			result["fingerprint"] = "abc123"
			result["status"] = "firing"
			result["labels"] = map[string]string{"severity": "critical"}
			result["annotations"] = map[string]string{"summary": "Test"}
			releaseFormatterResult(result)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			result := make(map[string]any, 30)
			result["alert_name"] = "TestAlert"
			result["fingerprint"] = "abc123"
			result["status"] = "firing"
			result["labels"] = map[string]string{"severity": "critical"}
			result["annotations"] = map[string]string{"summary": "Test"}
		}
	})
}
