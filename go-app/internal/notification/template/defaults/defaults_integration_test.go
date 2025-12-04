package defaults

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/notification/template"
)

// ================================================================================
// TN-154: Default Templates - Integration Tests with TN-153 Template Engine
// ================================================================================
// Phase 2 of 150% Quality Enhancement
//
// Tests all default templates with actual TN-153 template engine execution.
// Critical for 150% quality - validates templates work end-to-end.
//
// Added: 2025-11-24
// Tests: 12+ comprehensive integration tests

// createTestTemplateEngine creates a template engine for testing
func createTestTemplateEngine(t *testing.T) template.NotificationTemplateEngine {
	opts := template.DefaultTemplateEngineOptions()
	engine, err := template.NewNotificationTemplateEngine(opts)
	require.NoError(t, err, "Failed to create template engine")
	return engine
}

// createTestTemplateData creates sample template data for testing
func createTestTemplateData(status string, alertCount int) *template.TemplateData {
	labels := map[string]string{
		"alertname":   "HighCPU",
		"severity":    "critical",
		"environment": "production",
		"cluster":     "us-east-1",
		"instance":    "web-01.example.com",
	}

	annotations := map[string]string{
		"summary":        "CPU usage is 95%",
		"description":    "CPU usage has been above 90% for 5 minutes",
		"runbook_url":    "https://runbook.example.com/cpu",
		"dashboard_url":  "https://grafana.example.com/d/cpu",
	}

	data := template.NewTemplateData(status, labels, annotations, time.Now())
	data.ExternalURL = "https://alertmanager.example.com"
	data.GeneratorURL = "https://prometheus.example.com"
	data.Receiver = "team-platform"
	data.GroupLabels = map[string]string{"alertname": "HighCPU"}
	data.CommonLabels = labels
	data.CommonAnnotations = annotations

	// Create Alerts array for multiple alert scenarios
	data.Alerts = make([]template.Alert, alertCount)
	for i := 0; i < alertCount; i++ {
		data.Alerts[i] = template.Alert{
			Status:       status,
			Labels:       labels,
			Annotations:  annotations,
			StartsAt:     time.Now(),
			EndsAt:       time.Now().Add(1 * time.Hour),
			GeneratorURL: "https://prometheus.example.com",
			Fingerprint:  "",
		}
	}

	return data
}

// TestSlackTitleIntegration tests Slack title template with TN-153
func TestSlackTitleIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()

	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{
			name:     "firing_alert",
			status:   "firing",
			expected: "🔥 ALERT: HighCPU",
		},
		{
			name:     "resolved_alert",
			status:   "resolved",
			expected: "✅ RESOLVED: HighCPU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := createTestTemplateData(tt.status, 1)
			result, err := engine.Execute(context.Background(), registry.Slack.Title, data)

			require.NoError(t, err, "Slack title execution should not fail")
			assert.Equal(t, tt.expected, result, "Slack title should match expected output")
		})
	}
}

// TestSlackTextIntegration tests Slack text template with TN-153
func TestSlackTextIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.Slack.Text, data)

	require.NoError(t, err, "Slack text execution should not fail")
	assert.Contains(t, result, "CPU usage is 95%", "Text should contain summary")
}

// TestSlackPretextIntegration tests Slack pretext template with TN-153
func TestSlackPretextIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.Slack.Pretext, data)

	require.NoError(t, err, "Slack pretext execution should not fail")
	assert.Contains(t, result, "production", "Pretext should contain environment")
	assert.Contains(t, result, "us-east-1", "Pretext should contain cluster")
}

// TestSlackFieldsSingleIntegration tests Slack fields (single) with TN-153
func TestSlackFieldsSingleIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.Slack.FieldsSingle, data)

	require.NoError(t, err, "Slack fields execution should not fail")

	// Verify JSON structure
	assert.Contains(t, result, "Severity", "Fields should contain Severity")
	assert.Contains(t, result, "CRITICAL", "Fields should show severity in uppercase")
	assert.Contains(t, result, "Instance", "Fields should contain Instance")
	assert.Contains(t, result, "web-01.example.com", "Fields should show instance name")
	assert.Contains(t, result, "Description", "Fields should contain Description")
	assert.Contains(t, result, "CPU usage has been above 90%", "Fields should show description")
}

// TestSlackFieldsMultiIntegration tests Slack fields (multi) with TN-153
func TestSlackFieldsMultiIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.Slack.FieldsMulti, data)

	require.NoError(t, err, "Slack fields multi execution should not fail")

	// Verify JSON structure
	assert.Contains(t, result, "Severity", "Fields should contain severity")
	assert.Contains(t, result, "production", "Fields should show environment")
}

// TestPagerDutyDescriptionIntegration tests PagerDuty description with TN-153
func TestPagerDutyDescriptionIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()

	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{
			name:     "firing",
			status:   "firing",
			expected: "HighCPU: CPU usage is 95%",
		},
		{
			name:     "resolved",
			status:   "resolved",
			expected: "[RESOLVED] HighCPU: CPU usage is 95%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := createTestTemplateData(tt.status, 1)
			result, err := engine.Execute(context.Background(), registry.PagerDuty.Description, data)

			require.NoError(t, err, "PagerDuty description execution should not fail")
			assert.Equal(t, tt.expected, result, "Description should match expected output")

			// Verify size limit
			assert.Less(t, len(result), 1024, "Description should be < 1024 chars")
		})
	}
}

// TestPagerDutyDetailsSingleIntegration tests PagerDuty details (single) with TN-153
func TestPagerDutyDetailsSingleIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.PagerDuty.DetailsSingle, data)

	require.NoError(t, err, "PagerDuty details execution should not fail")

	// Verify JSON structure
	assert.Contains(t, result, "critical", "Details should contain severity")
	assert.Contains(t, result, "production", "Details should contain environment")
	assert.Contains(t, result, "web-01.example.com", "Details should contain instance")
	assert.Contains(t, result, "runbook_url", "Details should contain runbook URL")
}

// TestPagerDutyDetailsMultiIntegration tests PagerDuty details (multi) with TN-153
func TestPagerDutyDetailsMultiIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.PagerDuty.DetailsMulti, data)

	require.NoError(t, err, "PagerDuty details multi execution should not fail")

	// Verify JSON structure
	assert.Contains(t, result, "severity", "Details should contain severity")
	assert.Contains(t, result, "firing", "Details should show status")
}

// TestEmailSubjectIntegration tests Email subject template with TN-153
func TestEmailSubjectIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()

	tests := []struct {
		name       string
		status     string
		alertCount int
		expected   string
	}{
		{
			name:       "firing_single",
			status:     "firing",
			alertCount: 1,
			expected:   "[ALERT] HighCPU (1 alert)",
		},
		{
			name:       "firing_multiple",
			status:     "firing",
			alertCount: 3,
			expected:   "[ALERT] HighCPU (3 alerts)",
		},
		{
			name:       "resolved_single",
			status:     "resolved",
			alertCount: 1,
			expected:   "[RESOLVED] HighCPU (1 alert)",
		},
		{
			name:       "resolved_multiple",
			status:     "resolved",
			alertCount: 5,
			expected:   "[RESOLVED] HighCPU (5 alerts)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := createTestTemplateData(tt.status, tt.alertCount)
			result, err := engine.Execute(context.Background(), registry.Email.Subject, data)

			require.NoError(t, err, "Email subject execution should not fail")
			assert.Equal(t, tt.expected, result, "Subject should match expected output")
		})
	}
}

// TestEmailHTMLIntegration tests Email HTML template with TN-153
func TestEmailHTMLIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.Email.HTML, data)

	require.NoError(t, err, "Email HTML execution should not fail")

	// Verify HTML structure
	assert.Contains(t, result, "<!DOCTYPE html>", "Should be valid HTML")
	assert.Contains(t, result, "Alert Notification", "Should contain header")
	assert.Contains(t, result, "HighCPU", "Should contain alert name")
	assert.Contains(t, result, "critical", "Should show severity")
	assert.Contains(t, result, "web-01.example.com", "Should show instance")

	// Verify responsive design elements
	assert.Contains(t, result, "viewport", "Should have viewport meta tag")
	assert.Contains(t, result, "@media only screen", "Should have media queries")

	// Verify size limit
	assert.Less(t, len(result), 100*1024, "HTML should be < 100KB")
}

// TestEmailTextIntegration tests Email text template with TN-153
func TestEmailTextIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	result, err := engine.Execute(context.Background(), registry.Email.Text, data)

	require.NoError(t, err, "Email text execution should not fail")

	// Verify plain text structure
	assert.Contains(t, result, "HighCPU", "Should contain alert name")
	assert.Contains(t, result, "CRITICAL", "Should show severity in uppercase")
	assert.Contains(t, result, "web-01.example.com", "Should show instance")
	assert.Contains(t, result, "Generated by Alertmanager++ OSS", "Should have footer")
}

// TestAllTemplatesWithEmptyData tests graceful handling of missing data
func TestAllTemplatesWithEmptyData(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()

	// Create minimal data
	data := template.NewTemplateData("firing", make(map[string]string), make(map[string]string), time.Now())
	data.GroupLabels = map[string]string{"alertname": "TestAlert"}

	// Test all templates execute without error
	templates := map[string]string{
		"slack_title":            registry.Slack.Title,
		"slack_text":             registry.Slack.Text,
		"pagerduty_description":  registry.PagerDuty.Description,
		"email_subject":          registry.Email.Subject,
	}

	for name, tmpl := range templates {
		t.Run(name, func(t *testing.T) {
			result, err := engine.Execute(context.Background(), tmpl, data)
			assert.NoError(t, err, "Template should execute without error")
			assert.NotEmpty(t, result, "Result should not be empty")
		})
	}
}

// TestConcurrentTemplateExecution tests thread-safe concurrent execution
func TestConcurrentTemplateExecution(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	// Execute 100 concurrent template executions
	const concurrency = 100
	errors := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			_, err := engine.Execute(context.Background(), registry.Slack.Title, data)
			errors <- err
		}()
	}

	// Collect results
	for i := 0; i < concurrency; i++ {
		err := <-errors
		assert.NoError(t, err, "Concurrent execution should not fail")
	}
}

// TestExecuteMultipleIntegration tests parallel execution of multiple templates
func TestExecuteMultipleIntegration(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	templates := map[string]string{
		"title":       registry.Slack.Title,
		"text":        registry.Slack.Text,
		"pretext":     registry.Slack.Pretext,
		"description": registry.PagerDuty.Description,
		"subject":     registry.Email.Subject,
	}

	results, err := engine.ExecuteMultiple(context.Background(), templates, data)

	require.NoError(t, err, "ExecuteMultiple should not fail")
	require.Len(t, results, 5, "Should return all 5 results")

	// Verify all results
	assert.Contains(t, results["title"], "🔥 ALERT: HighCPU")
	assert.Contains(t, results["text"], "CPU usage is 95%")
	assert.Contains(t, results["pretext"], "production")
	assert.Contains(t, results["description"], "HighCPU")
	assert.Contains(t, results["subject"], "[ALERT] HighCPU")
}

// TestTemplateCacheHitRate tests cache performance
func TestTemplateCacheHitRate(t *testing.T) {
	engine := createTestTemplateEngine(t)
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)

	// Execute same template 10 times
	for i := 0; i < 10; i++ {
		_, err := engine.Execute(context.Background(), registry.Slack.Title, data)
		require.NoError(t, err)
	}

	// Check cache stats
	stats := engine.GetCacheStats()
	assert.Greater(t, stats.Hits, uint64(0), "Should have cache hits")

	// Cache hit ratio should be high (9/10 = 90%)
	total := stats.Hits + stats.Misses
	if total > 0 {
		hitRatio := float64(stats.Hits) / float64(total)
		assert.Greater(t, hitRatio, 0.8, "Cache hit ratio should be > 80%")
	}
}

// Benchmark tests for integration
func BenchmarkSlackTitleIntegration(b *testing.B) {
	engine, _ := template.NewNotificationTemplateEngine(template.DefaultTemplateEngineOptions())
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.Execute(ctx, registry.Slack.Title, data)
	}
}

func BenchmarkEmailHTMLIntegration(b *testing.B) {
	engine, _ := template.NewNotificationTemplateEngine(template.DefaultTemplateEngineOptions())
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 3)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.Execute(ctx, registry.Email.HTML, data)
	}
}

func BenchmarkExecuteMultipleIntegration(b *testing.B) {
	engine, _ := template.NewNotificationTemplateEngine(template.DefaultTemplateEngineOptions())
	registry := GetDefaultTemplates()
	data := createTestTemplateData("firing", 1)
	ctx := context.Background()

	templates := map[string]string{
		"title": registry.Slack.Title,
		"text":  registry.Slack.Text,
		"desc":  registry.PagerDuty.Description,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.ExecuteMultiple(ctx, templates, data)
	}
}
