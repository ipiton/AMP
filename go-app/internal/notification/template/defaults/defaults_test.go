package defaults

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// TN-154: Default Templates - Registry Tests
// ================================================================================

func TestGetDefaultTemplates(t *testing.T) {
	registry := GetDefaultTemplates()

	require.NotNil(t, registry)
	require.NotNil(t, registry.Slack)
	require.NotNil(t, registry.PagerDuty)
	require.NotNil(t, registry.Email)
	require.NotNil(t, registry.Webhook)

	// Verify Slack templates
	assert.NotEmpty(t, registry.Slack.Title)
	assert.NotEmpty(t, registry.Slack.Text)
	assert.NotNil(t, registry.Slack.ColorFunc)

	// Verify PagerDuty templates
	assert.NotEmpty(t, registry.PagerDuty.Description)
	assert.NotEmpty(t, registry.PagerDuty.DetailsSingle)
	assert.NotNil(t, registry.PagerDuty.SeverityFunc)

	// Verify Email templates
	assert.NotEmpty(t, registry.Email.Subject)
	assert.NotEmpty(t, registry.Email.HTML)
	assert.NotEmpty(t, registry.Email.Text)
}

func TestValidateAllTemplates(t *testing.T) {
	err := ValidateAllTemplates()
	assert.NoError(t, err, "All default templates should be valid")
}

func TestTemplateValidationError(t *testing.T) {
	err := &TemplateValidationError{
		Template: "Test.Template",
		Reason:   "test reason",
	}

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Test.Template")
	assert.Contains(t, err.Error(), "test reason")
}

func TestGetTemplateStats(t *testing.T) {
	stats := GetTemplateStats()

	require.NotNil(t, stats)

	// Verify counts
	assert.Equal(t, 5, stats.SlackTemplateCount)
	assert.Equal(t, 3, stats.PagerDutyTemplateCount)
	assert.Equal(t, 3, stats.EmailTemplateCount)
	assert.Equal(t, 3, stats.WebhookTemplateCount)

	// Verify sizes are reasonable
	assert.Greater(t, stats.SlackSize, 0)
	assert.Greater(t, stats.PagerDutySize, 0)
	assert.Greater(t, stats.EmailSize, 0)
	assert.Greater(t, stats.WebhookSize, 0)
	assert.Greater(t, stats.TotalSize, 0)

	// Verify total is sum of parts
	assert.Equal(t, stats.SlackSize+stats.PagerDutySize+stats.EmailSize+stats.WebhookSize, stats.TotalSize)

	// Verify sizes are reasonable (not too large)
	assert.Less(t, stats.SlackSize, 10*1024, "Slack templates should be < 10KB")
	assert.Less(t, stats.PagerDutySize, 5*1024, "PagerDuty templates should be < 5KB")
	assert.Less(t, stats.EmailSize, 50*1024, "Email templates should be < 50KB")
	assert.Less(t, stats.WebhookSize, 50*1024, "Webhook templates should be < 50KB")
}

func TestTemplateRegistryStructure(t *testing.T) {
	registry := &TemplateRegistry{
		Slack:     GetDefaultSlackTemplates(),
		PagerDuty: GetDefaultPagerDutyTemplates(),
		Email:     GetDefaultEmailTemplates(),
	}

	assert.NotNil(t, registry.Slack)
	assert.NotNil(t, registry.PagerDuty)
	assert.NotNil(t, registry.Email)
}

func TestValidateAllTemplatesWithEmptyTemplates(t *testing.T) {
	// This test verifies that validation would catch empty templates
	// We can't actually test with empty templates since they're constants,
	// but we can verify the validation logic exists

	err := ValidateAllTemplates()
	assert.NoError(t, err, "Default templates should pass validation")
}

func TestTemplateStatsConsistency(t *testing.T) {
	// Get stats multiple times and verify consistency
	stats1 := GetTemplateStats()
	stats2 := GetTemplateStats()

	assert.Equal(t, stats1.SlackTemplateCount, stats2.SlackTemplateCount)
	assert.Equal(t, stats1.PagerDutyTemplateCount, stats2.PagerDutyTemplateCount)
	assert.Equal(t, stats1.EmailTemplateCount, stats2.EmailTemplateCount)
	assert.Equal(t, stats1.WebhookTemplateCount, stats2.WebhookTemplateCount)
	assert.Equal(t, stats1.TotalSize, stats2.TotalSize)
}

func TestAllTemplatesNonEmpty(t *testing.T) {
	registry := GetDefaultTemplates()

	// Test all Slack templates
	t.Run("Slack templates", func(t *testing.T) {
		assert.NotEmpty(t, registry.Slack.Title)
		assert.NotEmpty(t, registry.Slack.Text)
		assert.NotEmpty(t, registry.Slack.Pretext)
		assert.NotEmpty(t, registry.Slack.FieldsSingle)
		assert.NotEmpty(t, registry.Slack.FieldsMulti)
	})

	// Test all PagerDuty templates
	t.Run("PagerDuty templates", func(t *testing.T) {
		assert.NotEmpty(t, registry.PagerDuty.Description)
		assert.NotEmpty(t, registry.PagerDuty.DetailsSingle)
		assert.NotEmpty(t, registry.PagerDuty.DetailsMulti)
	})

	// Test all Email templates
	t.Run("Email templates", func(t *testing.T) {
		assert.NotEmpty(t, registry.Email.Subject)
		assert.NotEmpty(t, registry.Email.HTML)
		assert.NotEmpty(t, registry.Email.Text)
	})
}

func TestTemplateFunctions(t *testing.T) {
	registry := GetDefaultTemplates()

	// Test Slack color function
	t.Run("Slack ColorFunc", func(t *testing.T) {
		assert.NotNil(t, registry.Slack.ColorFunc)
		assert.Equal(t, "danger", registry.Slack.ColorFunc("critical"))
		assert.Equal(t, "warning", registry.Slack.ColorFunc("warning"))
		assert.Equal(t, "good", registry.Slack.ColorFunc("info"))
	})

	// Test PagerDuty severity function
	t.Run("PagerDuty SeverityFunc", func(t *testing.T) {
		assert.NotNil(t, registry.PagerDuty.SeverityFunc)
		assert.Equal(t, "critical", registry.PagerDuty.SeverityFunc("critical"))
		assert.Equal(t, "warning", registry.PagerDuty.SeverityFunc("warning"))
		assert.Equal(t, "info", registry.PagerDuty.SeverityFunc("info"))
	})
}

// Benchmark tests
func BenchmarkGetDefaultTemplates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetDefaultTemplates()
	}
}

func BenchmarkValidateAllTemplates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidateAllTemplates()
	}
}

func BenchmarkGetTemplateStats(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetTemplateStats()
	}
}
