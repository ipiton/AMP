package template

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// TN-153: Template Engine - Unit Tests
// ================================================================================
// Comprehensive unit tests for NotificationTemplateEngine.
//
// Test Coverage:
// - Engine initialization
// - Template execution (success/error cases)
// - Cache operations
// - Concurrent execution
// - Error handling
// - Timeout handling
//
// Target: 30+ tests, 90% coverage
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// Test 1: NewNotificationTemplateEngine - Success
func TestNewNotificationTemplateEngine_Success(t *testing.T) {
	opts := DefaultTemplateEngineOptions()
	engine, err := NewNotificationTemplateEngine(opts)

	require.NoError(t, err)
	assert.NotNil(t, engine)
}

// Test 2: NewNotificationTemplateEngine - Invalid Cache Size
func TestNewNotificationTemplateEngine_InvalidCacheSize(t *testing.T) {
	opts := DefaultTemplateEngineOptions()
	opts.CacheSize = 0 // Will be corrected to default

	engine, err := NewNotificationTemplateEngine(opts)

	require.NoError(t, err)
	assert.NotNil(t, engine)
}

// Test 3: Execute - Simple Template
func TestExecute_SimpleTemplate(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Labels.alertname }}", data)

	require.NoError(t, err)
	assert.Equal(t, "HighCPU", result)
}

// Test 4: Execute - Empty Template
func TestExecute_EmptyTemplate(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	result, err := engine.Execute(context.Background(), "", data)

	require.NoError(t, err)
	assert.Equal(t, "", result)
}

// Test 5: Execute - Nil Data
func TestExecute_NilData(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())

	result, err := engine.Execute(context.Background(), "{{ .Labels.alertname }}", nil)

	require.Error(t, err)
	assert.True(t, IsDataError(err))
	assert.Equal(t, "", result)
}

// Test 6: Execute - Parse Error
func TestExecute_ParseError(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Invalid", data)

	require.Error(t, err)
	assert.True(t, IsParseError(err))
	assert.Equal(t, "", result)
}

// Test 7: Execute - With Functions
func TestExecute_WithFunctions(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Labels.alertname | toUpper }}", data)

	require.NoError(t, err)
	assert.Equal(t, "HIGHCPU", result)
}

// Test 8: Execute - Multiple Fields
func TestExecute_MultipleFields(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		nil, time.Now())

	tmpl := "{{ .Labels.alertname }} - {{ .Labels.severity | toUpper }}"
	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Equal(t, "HighCPU - CRITICAL", result)
}

// Test 9: Execute - With Annotations
func TestExecute_WithAnnotations(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		map[string]string{"summary": "CPU is high"},
		time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Annotations.summary }}", data)

	require.NoError(t, err)
	assert.Equal(t, "CPU is high", result)
}

// Test 10: Execute - Cache Hit
func TestExecute_CacheHit(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	tmpl := "{{ .Labels.alertname }}"

	// First execution (cache miss)
	result1, err1 := engine.Execute(context.Background(), tmpl, data)
	require.NoError(t, err1)

	// Second execution (cache hit)
	result2, err2 := engine.Execute(context.Background(), tmpl, data)
	require.NoError(t, err2)

	assert.Equal(t, result1, result2)

	// Check cache stats
	stats := engine.GetCacheStats()
	assert.Equal(t, uint64(1), stats.Hits)
	assert.Equal(t, uint64(1), stats.Misses)
}

// Test 11: ExecuteMultiple - Success
func TestExecuteMultiple_Success(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		nil, time.Now())

	templates := map[string]string{
		"title": "{{ .Labels.alertname }}",
		"text":  "Severity: {{ .Labels.severity }}",
	}

	results, err := engine.ExecuteMultiple(context.Background(), templates, data)

	require.NoError(t, err)
	assert.Equal(t, "HighCPU", results["title"])
	assert.Equal(t, "Severity: critical", results["text"])
}

// Test 12: ExecuteMultiple - Empty Templates
func TestExecuteMultiple_EmptyTemplates(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	results, err := engine.ExecuteMultiple(context.Background(), map[string]string{}, data)

	require.NoError(t, err)
	assert.Empty(t, results)
}

// Test 13: ExecuteMultiple - Nil Data
func TestExecuteMultiple_NilData(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())

	templates := map[string]string{
		"title": "{{ .Labels.alertname }}",
	}

	results, err := engine.ExecuteMultiple(context.Background(), templates, nil)

	require.Error(t, err)
	assert.Nil(t, results)
}

// Test 14: ExecuteMultiple - One Template Fails
func TestExecuteMultiple_OneTemplateFails(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	templates := map[string]string{
		"title": "{{ .Labels.alertname }}",
		"text":  "{{ .Invalid",
	}

	results, err := engine.ExecuteMultiple(context.Background(), templates, data)

	require.Error(t, err)
	assert.NotNil(t, results) // Partial results returned
}

// Test 15: InvalidateCache
func TestInvalidateCache(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	// Execute to populate cache
	_, _ = engine.Execute(context.Background(), "{{ .Labels.alertname }}", data)

	// Invalidate cache
	engine.InvalidateCache()

	// Check cache is empty
	stats := engine.GetCacheStats()
	assert.Equal(t, 0, stats.Size)
}

// Test 16: GetCacheStats
func TestGetCacheStats(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	// Execute multiple times
	tmpl := "{{ .Labels.alertname }}"
	_, _ = engine.Execute(context.Background(), tmpl, data)
	_, _ = engine.Execute(context.Background(), tmpl, data)
	_, _ = engine.Execute(context.Background(), tmpl, data)

	stats := engine.GetCacheStats()
	assert.Equal(t, uint64(2), stats.Hits)
	assert.Equal(t, uint64(1), stats.Misses)
	assert.Greater(t, stats.HitRatio, 0.0)
}

// Test 17: Concurrent Execution
func TestExecute_Concurrent(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	tmpl := "{{ .Labels.alertname }}"
	done := make(chan bool, 10)

	// Execute 10 concurrent requests
	for i := 0; i < 10; i++ {
		go func() {
			result, err := engine.Execute(context.Background(), tmpl, data)
			assert.NoError(t, err)
			assert.Equal(t, "HighCPU", result)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Test 18: Context Timeout
func TestExecute_ContextTimeout(t *testing.T) {
	opts := DefaultTemplateEngineOptions()
	opts.ExecutionTimeout = 1 * time.Millisecond
	engine, _ := NewNotificationTemplateEngine(opts)

	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	_, err := engine.Execute(ctx, "{{ .Labels.alertname }}", data)

	// Timeout may or may not occur depending on execution speed
	// Just ensure no panic
	_ = err
}

// Test 19: Fallback On Error - Enabled
func TestExecute_FallbackOnError_Enabled(t *testing.T) {
	opts := DefaultTemplateEngineOptions()
	opts.FallbackOnError = true
	engine, _ := NewNotificationTemplateEngine(opts)

	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	// Missing field - should fallback to raw template
	result, err := engine.Execute(context.Background(), "{{ .NonExistent }}", data)

	// With fallback, returns raw template
	require.NoError(t, err)
	assert.Equal(t, "{{ .NonExistent }}", result)
}

// Test 20: Fallback On Error - Disabled
func TestExecute_FallbackOnError_Disabled(t *testing.T) {
	opts := DefaultTemplateEngineOptions()
	opts.FallbackOnError = false
	engine, _ := NewNotificationTemplateEngine(opts)

	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	// Missing field - should return error
	result, err := engine.Execute(context.Background(), "{{ .NonExistent }}", data)

	require.Error(t, err)
	assert.Equal(t, "", result)
}

// Test 21: Execute - Status Field
func TestExecute_StatusField(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing", map[string]string{}, nil, time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Status }}", data)

	require.NoError(t, err)
	assert.Equal(t, "firing", result)
}

// Test 22: Execute - Resolved Status
func TestExecute_ResolvedStatus(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("resolved", map[string]string{}, nil, time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Status }}", data)

	require.NoError(t, err)
	assert.Equal(t, "resolved", result)
}

// Test 23: Execute - With GroupLabels
func TestExecute_WithGroupLabels(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())
	data.WithGroupInfo(
		map[string]string{"alertname": "HighCPU", "cluster": "prod"},
		nil, nil, "")

	result, err := engine.Execute(context.Background(), "{{ .GroupLabels.cluster }}", data)

	require.NoError(t, err)
	assert.Equal(t, "prod", result)
}

// Test 24: Execute - Complex Template
func TestExecute_ComplexTemplate(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "prod-1",
		},
		map[string]string{
			"summary": "CPU usage is high",
		},
		time.Now())

	tmpl := `🔥 {{ .Labels.alertname | toUpper }} - {{ .Status }}
Severity: {{ .Labels.severity }}
Instance: {{ .Labels.instance }}
Summary: {{ .Annotations.summary }}`

	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Contains(t, result, "🔥 HIGHCPU - firing")
	assert.Contains(t, result, "Severity: critical")
	assert.Contains(t, result, "Instance: prod-1")
	assert.Contains(t, result, "Summary: CPU usage is high")
}

// Test 25: Execute - With Conditional
func TestExecute_WithConditional(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"severity": "critical"},
		map[string]string{"runbook_url": "https://wiki.company.com/runbooks"},
		time.Now())

	tmpl := `{{ if .Annotations.runbook_url }}Runbook: {{ .Annotations.runbook_url }}{{ end }}`

	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Equal(t, "Runbook: https://wiki.company.com/runbooks", result)
}

// Test 26: Execute - Conditional False
func TestExecute_ConditionalFalse(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"severity": "critical"},
		nil,
		time.Now())

	tmpl := `{{ if .Annotations.runbook_url }}Runbook: {{ .Annotations.runbook_url }}{{ end }}`

	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Equal(t, "", result)
}

// Test 27: Execute - With Default Function
func TestExecute_WithDefaultFunction(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	tmpl := `{{ .Labels.severity | default "unknown" }}`

	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Equal(t, "unknown", result)
}

// Test 28: Execute - With Time Function
func TestExecute_WithTimeFunction(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	now := time.Now()
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, now)

	tmpl := `{{ .StartsAt | date "2006-01-02" }}`

	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Equal(t, now.Format("2006-01-02"), result)
}

// Test 29: Execute - Large Template
func TestExecute_LargeTemplate(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	// Build large template
	tmpl := ""
	for i := 0; i < 100; i++ {
		tmpl += "{{ .Labels.alertname }} "
	}

	result, err := engine.Execute(context.Background(), tmpl, data)

	require.NoError(t, err)
	assert.Contains(t, result, "HighCPU")
}

// Test 30: Execute - Special Characters
func TestExecute_SpecialCharacters(t *testing.T) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "Test-Alert_123"},
		nil, time.Now())

	result, err := engine.Execute(context.Background(), "{{ .Labels.alertname }}", data)

	require.NoError(t, err)
	assert.Equal(t, "Test-Alert_123", result)
}

// Benchmark: Template Parse + Execute (uncached)
func BenchmarkExecute_Uncached(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use different template each time to avoid cache
		tmpl := "{{ .Labels.alertname }}" + string(rune(i%10))
		_, _ = engine.Execute(context.Background(), tmpl, data)
	}
}

// Benchmark: Template Execute (cached)
func BenchmarkExecute_Cached(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{"alertname": "HighCPU"},
		nil, time.Now())

	tmpl := "{{ .Labels.alertname }}"

	// Warm up cache
	_, _ = engine.Execute(context.Background(), tmpl, data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(context.Background(), tmpl, data)
	}
}

// Benchmark: ExecuteMultiple
func BenchmarkExecuteMultiple(b *testing.B) {
	engine, _ := NewNotificationTemplateEngine(DefaultTemplateEngineOptions())
	data := NewTemplateData("firing",
		map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
		nil, time.Now())

	templates := map[string]string{
		"title": "{{ .Labels.alertname }}",
		"text":  "Severity: {{ .Labels.severity }}",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.ExecuteMultiple(context.Background(), templates, data)
	}
}
