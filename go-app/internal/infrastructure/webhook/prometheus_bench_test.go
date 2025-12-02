package webhook

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// =======================================================================================
// TN-146 Phase 7: Performance Benchmarks (8 comprehensive benchmarks)
//
// Performance Targets:
// - DetectPrometheusFormat: < 5µs/op
// - ParseSingleAlert: < 10µs/op
// - Parse100Alerts: < 1ms/op
// - ConvertToDomain: < 5µs per alert
// - GenerateFingerprint: < 1µs/op
// - FlattenGroups: < 100µs/op
// - HandlerE2E: < 100µs/op
// - ConcurrentParsing: Scalability test (linear scaling)
//
// Memory Targets:
// - Hot path: < 10 allocs/op
// - Parse single: < 5 allocs/op
// - Fingerprint: 0 allocs/op (zero allocation goal)
// =======================================================================================

// BenchmarkDetectPrometheusFormat benchmarks format detection speed
// Target: < 5µs/op
func BenchmarkDetectPrometheusFormat(b *testing.B) {
	detector := NewPrometheusFormatDetector()

	// Prometheus v1 payload
	payloadV1 := []byte(`[{
		"labels": {"alertname": "Test"},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"generatorURL": "http://prometheus:9090"
	}]`)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = detector.DetectPrometheusFormat(payloadV1)
	}
}

// BenchmarkParseSingleAlert benchmarks parsing a single Prometheus v1 alert
// Target: < 10µs/op, < 5 allocs/op
func BenchmarkParseSingleAlert(b *testing.B) {
	parser := NewPrometheusParser()

	payload := []byte(`[{
		"labels": {"alertname": "HighCPU", "instance": "server-1", "job": "api", "severity": "warning"},
		"annotations": {"summary": "CPU usage is high"},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"generatorURL": "http://prometheus:9090/graph"
	}]`)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(payload)
	}
}

// BenchmarkParse100Alerts benchmarks parsing 100 Prometheus v1 alerts
// Target: < 1ms/op (< 10µs per alert)
func BenchmarkParse100Alerts(b *testing.B) {
	parser := NewPrometheusParser()

	// Build payload with 100 alerts using fmt.Sprintf
	payloadStr := "["
	for i := 0; i < 100; i++ {
		if i > 0 {
			payloadStr += ","
		}
		payloadStr += fmt.Sprintf(`{
			"labels": {"alertname": "Alert%d", "instance": "server-%d"},
			"state": "firing",
			"activeAt": "2025-11-18T10:00:00Z",
			"generatorURL": "http://prometheus:9090"
		}`, i%10, i%10)
	}
	payloadStr += "]"
	payload := []byte(payloadStr)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(payload)
	}
}

// Note: BenchmarkConvertToDomain and BenchmarkGenerateFingerprint already exist in parser_test.go
// Total benchmarks: 8 (this file) + 3 (parser_test.go) = 11 benchmarks

// BenchmarkFlattenGroups benchmarks flattening Prometheus v2 grouped format
// Target: < 100µs/op
func BenchmarkFlattenGroups(b *testing.B) {
	// Prometheus v2 payload with 3 groups, 10 alerts each (30 total)
	webhook := &PrometheusWebhook{
		Groups: []PrometheusAlertGroup{
			{
				Labels: map[string]string{"job": "api", "severity": "warning"},
				Alerts: make([]PrometheusAlert, 10),
			},
			{
				Labels: map[string]string{"job": "db", "severity": "critical"},
				Alerts: make([]PrometheusAlert, 10),
			},
			{
				Labels: map[string]string{"job": "frontend", "severity": "info"},
				Alerts: make([]PrometheusAlert, 10),
			},
		},
	}

	// Initialize alerts
	now := time.Now()
	for g := range webhook.Groups {
		for i := range webhook.Groups[g].Alerts {
			webhook.Groups[g].Alerts[i] = PrometheusAlert{
				Labels:       map[string]string{"alertname": "Test", "instance": "server-1"},
				State:        "firing",
				ActiveAt:     now,
				GeneratorURL: "http://prometheus:9090",
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = webhook.FlattenAlerts()
	}
}

// BenchmarkHandlerE2E benchmarks end-to-end handler processing (detection → parsing → validation → conversion)
// Target: < 100µs/op
func BenchmarkHandlerE2E(b *testing.B) {
	processor := newMockAlertProcessorWithHealth()
	handler := NewUniversalWebhookHandler(processor, nil)

	payload := []byte(`[{
		"labels": {"alertname": "HighCPU", "instance": "server-1"},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"generatorURL": "http://prometheus:9090"
	}]`)

	req := &HandleWebhookRequest{
		Payload:     payload,
		ContentType: "application/json",
	}

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = handler.HandleWebhook(ctx, req)
	}
}

// BenchmarkConcurrentParsing benchmarks scalability of concurrent parsing
// Measures: Linear scaling with goroutines
func BenchmarkConcurrentParsing(b *testing.B) {
	parser := NewPrometheusParser()

	payload := []byte(`[{
		"labels": {"alertname": "Test"},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"generatorURL": "http://prometheus:9090"
	}]`)

	// Test concurrent parsing with different concurrency levels
	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency=%d", concurrency), func(b *testing.B) {
			b.SetParallelism(concurrency)
			b.ResetTimer()
			b.ReportAllocs()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = parser.Parse(payload)
				}
			})
		})
	}
}

// BenchmarkValidatePrometheus benchmarks Prometheus webhook validation
// Target: < 10µs/op
func BenchmarkValidatePrometheus(b *testing.B) {
	validator := NewWebhookValidator()

	webhook := &PrometheusWebhook{
		Alerts: []PrometheusAlert{
			{
				Labels: map[string]string{
					"alertname": "HighCPU",
					"instance":  "server-1",
					"job":       "api",
				},
				State:        "firing",
				ActiveAt:     time.Now(),
				GeneratorURL: "http://prometheus:9090",
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = validator.ValidatePrometheus(webhook)
	}
}

// BenchmarkDetectorWithPrometheusPayload benchmarks full WebhookDetector.Detect() with Prometheus payload
// Target: < 5µs/op
func BenchmarkDetectorWithPrometheusPayload(b *testing.B) {
	detector := NewWebhookDetector()

	payload := []byte(`[{
		"labels": {"alertname": "Test"},
		"state": "firing",
		"activeAt": "2025-11-18T10:00:00Z",
		"generatorURL": "http://prometheus:9090"
	}]`)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = detector.Detect(payload)
	}
}
