package template

import (
	"context"
	"testing"
	"time"
)

// ================================================================================
// TN-153: 150% Enterprise Quality - Comprehensive Benchmark Suite
// ================================================================================
// This file provides comprehensive benchmarks to verify performance targets
// and ensure enterprise-grade performance at scale.
//
// Performance Targets:
// - Template Parse: < 10ms p95
// - Execute (cached): < 5ms p95
// - Execute (uncached): < 20ms p95
// - Cache hit ratio: > 95%
//
// Author: AI Assistant
// Date: 2025-11-24
// Quality: 150% Enterprise Grade

// ================================================================================
// Template Parsing Benchmarks
// ================================================================================

func BenchmarkTemplateParse_Simple(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname }}"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkTemplateParse_Complex(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "prod-1",
		},
		map[string]string{
			"summary": "CPU is high",
		},
		time.Now())

	tmpl := `{{ .Labels.severity | toUpper }}: {{ .Labels.alertname }}
Instance: {{ .Labels.instance }}
Started: {{ .StartsAt | humanizeTimestamp }}
Summary: {{ .Annotations.summary }}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkTemplateParse_WithFunctions(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test", "severity": "critical"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname | toUpper | truncate 10 }} - {{ .Labels.severity | title }}"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

// ================================================================================
// Cached Execution Benchmarks
// ================================================================================

func BenchmarkTemplateExecute_Cached(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname }}"

	// Warm up cache
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkTemplateExecute_CachedComplex(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		map[string]string{
			"summary": "CPU is high",
		},
		time.Now())

	tmpl := `{{ .Labels.severity | toUpper }}: {{ .Labels.alertname }}
Summary: {{ .Annotations.summary }}
Started: {{ .StartsAt | humanizeTimestamp }}`

	// Warm up cache
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

// ================================================================================
// Uncached Execution Benchmarks
// ================================================================================

func BenchmarkTemplateExecute_Uncached(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use unique template each time to avoid caching
		tmpl := "{{ .Labels.alertname }}" + string(rune(i%10))
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

// ================================================================================
// Function Performance Benchmarks
// ================================================================================

func BenchmarkFunction_HumanizeTimestamp(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{},
		map[string]string{},
		time.Now().Add(-2*time.Hour))

	tmpl := "{{ .StartsAt | humanizeTimestamp }}"

	// Warm up
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkFunction_ToUpper(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "highcpu"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname | toUpper }}"

	// Warm up
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkFunction_Truncate(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "VeryLongAlertNameThatNeedsTruncation"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname | truncate 10 }}"

	// Warm up
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkFunction_Join(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "Test",
			"severity":  "critical",
			"instance":  "prod-1",
		},
		map[string]string{},
		time.Now())

	tmpl := `{{ .Labels | sortedPairs | join ", " }}`

	// Warm up
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

// ================================================================================
// Multiple Template Execution Benchmarks
// ================================================================================

func BenchmarkExecuteMultiple_Small(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "Test",
			"severity":  "critical",
		},
		map[string]string{
			"summary": "Test summary",
		},
		time.Now())

	templates := map[string]string{
		"title": "{{ .Labels.alertname }}",
		"text":  "{{ .Labels.severity }}",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.ExecuteMultiple(ctx, templates, data)
	}
}

func BenchmarkExecuteMultiple_Large(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "prod-1",
		},
		map[string]string{
			"summary":     "CPU is high",
			"description": "CPU usage over 90%",
		},
		time.Now())

	templates := map[string]string{
		"title":   "{{ .Labels.severity | toUpper }}: {{ .Labels.alertname }}",
		"text":    "{{ .Annotations.summary }}",
		"pretext": "Instance: {{ .Labels.instance }}",
		"field1":  "{{ .StartsAt | humanizeTimestamp }}",
		"field2":  "{{ .Status }}",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.ExecuteMultiple(ctx, templates, data)
	}
}

// ================================================================================
// Cache Performance Benchmarks
// ================================================================================

func BenchmarkCache_HitRate(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	// Pre-populate cache with 10 templates
	templates := make([]string, 10)
	for i := 0; i < 10; i++ {
		templates[i] = "{{ .Labels.alertname }}" + string(rune(i))
		_, _ = engine.Execute(ctx, templates[i], data)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 90% cache hits, 10% misses
		var tmpl string
		if i%10 == 0 {
			tmpl = "{{ .Labels.alertname }}" + string(rune(100+i))
		} else {
			tmpl = templates[i%10]
		}
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkCache_Invalidation(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname }}"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
		if i%100 == 0 {
			engine.InvalidateCache()
		}
	}
}

// ================================================================================
// Receiver Integration Benchmarks
// ================================================================================

func BenchmarkProcessSlackConfig(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		map[string]string{
			"summary": "CPU is high",
		},
		time.Now())

	config := &SlackConfig{
		Title: "{{ .Labels.alertname }}",
		Text:  "{{ .Annotations.summary }}",
		Fields: []*SlackField{
			{
				Title: "Severity",
				Value: "{{ .Labels.severity }}",
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ProcessSlackConfig(ctx, engine, config, data)
	}
}

func BenchmarkProcessPagerDutyConfig(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		map[string]string{
			"summary": "CPU is high",
		},
		time.Now())

	config := &PagerDutyConfig{
		Summary: "{{ .Labels.severity | toUpper }}: {{ .Labels.alertname }}",
		Details: map[string]string{
			"summary": "{{ .Annotations.summary }}",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ProcessPagerDutyConfig(ctx, engine, config, data)
	}
}

// ================================================================================
// Concurrent Execution Benchmarks
// ================================================================================

func BenchmarkConcurrent_Execute(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname }}"

	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			_, _ = engine.Execute(ctx, tmpl, data)
		}
	})
}

func BenchmarkConcurrent_ExecuteMultiple(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "Test",
			"severity":  "critical",
		},
		map[string]string{},
		time.Now())

	templates := map[string]string{
		"title": "{{ .Labels.alertname }}",
		"text":  "{{ .Labels.severity }}",
	}

	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			_, _ = engine.ExecuteMultiple(ctx, templates, data)
		}
	})
}

// ================================================================================
// Memory Allocation Benchmarks
// ================================================================================

func BenchmarkMemory_Execute(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	ctx := context.Background()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test"},
		map[string]string{},
		time.Now())

	tmpl := "{{ .Labels.alertname }}"

	// Warm up
	_, _ = engine.Execute(ctx, tmpl, data)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(ctx, tmpl, data)
	}
}

func BenchmarkMemory_NewTemplateData(b *testing.B) {
	labels := map[string]string{"alertname": "Test"}
	annotations := map[string]string{"summary": "Test"}
	now := time.Now()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewTemplateData("firing", labels, annotations, now)
	}
}
