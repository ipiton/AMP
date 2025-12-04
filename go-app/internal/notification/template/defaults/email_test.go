package defaults

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// TN-154: Default Templates - Email Template Tests
// ================================================================================

func TestGetDefaultEmailTemplates(t *testing.T) {
	templates := GetDefaultEmailTemplates()

	require.NotNil(t, templates)
	assert.NotEmpty(t, templates.Subject)
	assert.NotEmpty(t, templates.HTML)
	assert.NotEmpty(t, templates.Text)
}

func TestDefaultEmailSubject(t *testing.T) {
	assert.NotEmpty(t, DefaultEmailSubject)
	assert.Contains(t, DefaultEmailSubject, "Status")
	assert.Contains(t, DefaultEmailSubject, "GroupLabels.alertname")
	assert.Contains(t, DefaultEmailSubject, "len .Alerts")
}

func TestDefaultEmailHTML(t *testing.T) {
	assert.NotEmpty(t, DefaultEmailHTML)

	// Verify HTML structure
	assert.Contains(t, DefaultEmailHTML, "<!DOCTYPE html>")
	assert.Contains(t, DefaultEmailHTML, "<html")
	assert.Contains(t, DefaultEmailHTML, "</html>")
	assert.Contains(t, DefaultEmailHTML, "<head>")
	assert.Contains(t, DefaultEmailHTML, "<body>")

	// Verify meta tags
	assert.Contains(t, DefaultEmailHTML, "charset=\"UTF-8\"")
	assert.Contains(t, DefaultEmailHTML, "viewport")

	// Verify CSS
	assert.Contains(t, DefaultEmailHTML, "<style>")
	assert.Contains(t, DefaultEmailHTML, "</style>")

	// Verify responsive design
	assert.Contains(t, DefaultEmailHTML, "@media")
	assert.Contains(t, DefaultEmailHTML, "max-width: 600px")

	// Verify key sections
	assert.Contains(t, DefaultEmailHTML, "class=\"header\"")
	assert.Contains(t, DefaultEmailHTML, "class=\"content\"")
	assert.Contains(t, DefaultEmailHTML, "class=\"alert-table\"")
	assert.Contains(t, DefaultEmailHTML, "class=\"footer\"")

	// Verify template variables
	assert.Contains(t, DefaultEmailHTML, ".Status")
	assert.Contains(t, DefaultEmailHTML, ".Alerts")
	assert.Contains(t, DefaultEmailHTML, ".CommonLabels")
	assert.Contains(t, DefaultEmailHTML, ".Receiver")
}

func TestDefaultEmailText(t *testing.T) {
	assert.NotEmpty(t, DefaultEmailText)

	// Verify structure
	assert.Contains(t, DefaultEmailText, "ALERTS")
	assert.Contains(t, DefaultEmailText, "COMMON LABELS")

	// Verify template variables
	assert.Contains(t, DefaultEmailText, ".Status")
	assert.Contains(t, DefaultEmailText, ".Alerts")
	assert.Contains(t, DefaultEmailText, ".CommonLabels")
	assert.Contains(t, DefaultEmailText, ".Receiver")

	// Verify no HTML tags
	assert.NotContains(t, DefaultEmailText, "<html>")
	assert.NotContains(t, DefaultEmailText, "<div>")
	assert.NotContains(t, DefaultEmailText, "<table>")
}

func TestValidateEmailHTMLSize(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected bool
	}{
		{
			name:     "small HTML",
			html:     "<html><body>Test</body></html>",
			expected: true,
		},
		{
			name:     "medium HTML",
			html:     strings.Repeat("x", 50*1024), // 50KB
			expected: true,
		},
		{
			name:     "large but valid HTML",
			html:     strings.Repeat("x", 99*1024), // 99KB
			expected: true,
		},
		{
			name:     "exactly 100KB (invalid)",
			html:     strings.Repeat("x", 100*1024), // 100KB
			expected: false,
		},
		{
			name:     "too large HTML",
			html:     strings.Repeat("x", 200*1024), // 200KB
			expected: false,
		},
		{
			name:     "empty HTML",
			html:     "",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateEmailHTMLSize(tt.html)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEmailTemplatesStructure(t *testing.T) {
	templates := &EmailTemplates{
		Subject: "test-subject",
		HTML:    "test-html",
		Text:    "test-text",
	}

	assert.Equal(t, "test-subject", templates.Subject)
	assert.Equal(t, "test-html", templates.HTML)
	assert.Equal(t, "test-text", templates.Text)
}

func TestEmailTemplateConstants(t *testing.T) {
	// Verify all constants are defined and non-empty
	constants := map[string]string{
		"DefaultEmailSubject": DefaultEmailSubject,
		"DefaultEmailHTML":    DefaultEmailHTML,
		"DefaultEmailText":    DefaultEmailText,
	}

	for name, value := range constants {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, value, "%s should not be empty", name)
			assert.Greater(t, len(value), 10, "%s should have reasonable length", name)
		})
	}
}

func TestEmailHTMLSeverityClasses(t *testing.T) {
	// Verify all severity CSS classes are defined
	severities := []string{"critical", "error", "warning", "info"}

	for _, severity := range severities {
		t.Run("severity_"+severity, func(t *testing.T) {
			className := ".severity-" + severity
			assert.Contains(t, DefaultEmailHTML, className,
				"HTML should contain CSS class for %s severity", severity)
		})
	}
}

func TestEmailHTMLResponsiveDesign(t *testing.T) {
	// Verify responsive design elements
	assert.Contains(t, DefaultEmailHTML, "viewport")
	assert.Contains(t, DefaultEmailHTML, "@media")
	assert.Contains(t, DefaultEmailHTML, "max-width")

	// Verify mobile-friendly font sizes
	assert.Contains(t, DefaultEmailHTML, "font-size")
}

func TestEmailHTMLInlineCSS(t *testing.T) {
	// Verify CSS is inline (not external)
	assert.Contains(t, DefaultEmailHTML, "<style>")
	assert.NotContains(t, DefaultEmailHTML, "<link rel=\"stylesheet\"")
}

func TestEmailHTMLColorCoding(t *testing.T) {
	// Verify status-based colors
	assert.Contains(t, DefaultEmailHTML, "#28a745") // Green for resolved
	assert.Contains(t, DefaultEmailHTML, "#dc3545") // Red for firing

	// Verify severity colors
	assert.Contains(t, DefaultEmailHTML, "#ffc107") // Yellow for warning
	assert.Contains(t, DefaultEmailHTML, "#17a2b8") // Blue for info
}

func TestEmailHTMLTableStructure(t *testing.T) {
	// Verify table elements
	assert.Contains(t, DefaultEmailHTML, "<table")
	assert.Contains(t, DefaultEmailHTML, "<thead>")
	assert.Contains(t, DefaultEmailHTML, "<tbody>")
	assert.Contains(t, DefaultEmailHTML, "<th>")
	assert.Contains(t, DefaultEmailHTML, "<td>")

	// Verify table headers
	assert.Contains(t, DefaultEmailHTML, "Alert")
	assert.Contains(t, DefaultEmailHTML, "Severity")
	assert.Contains(t, DefaultEmailHTML, "Instance")
	assert.Contains(t, DefaultEmailHTML, "Description")
}

func TestEmailTextStructure(t *testing.T) {
	// Verify plain text has clear sections
	assert.Contains(t, DefaultEmailText, "====")

	// Verify no HTML entities
	assert.NotContains(t, DefaultEmailText, "&nbsp;")
	assert.NotContains(t, DefaultEmailText, "&lt;")
	assert.NotContains(t, DefaultEmailText, "&gt;")
}

func TestEmailSubjectPluralization(t *testing.T) {
	// Verify subject handles singular/plural correctly
	assert.Contains(t, DefaultEmailSubject, "alert{{ if gt (len .Alerts) 1 }}s{{ end }}")
}

func TestEmailHTMLSize(t *testing.T) {
	// Verify default HTML template is reasonable size
	size := len(DefaultEmailHTML)
	assert.Less(t, size, 20*1024, "Default HTML template should be < 20KB")
	assert.Greater(t, size, 1000, "Default HTML template should be > 1KB")
}

func TestEmailTextSize(t *testing.T) {
	// Verify default text template is reasonable size
	size := len(DefaultEmailText)
	assert.Less(t, size, 5*1024, "Default text template should be < 5KB")
	assert.Greater(t, size, 100, "Default text template should be > 100 bytes")
}

func TestEmailHTMLNoExternalResources(t *testing.T) {
	// Verify no external resources (images, scripts, stylesheets)
	assert.NotContains(t, DefaultEmailHTML, "<img src=\"http")
	assert.NotContains(t, DefaultEmailHTML, "<script src=")
	assert.NotContains(t, DefaultEmailHTML, "<link href=")

	// Verify no external fonts
	assert.NotContains(t, DefaultEmailHTML, "@import url")
	assert.NotContains(t, DefaultEmailHTML, "fonts.googleapis.com")
}

func TestEmailHTMLAccessibility(t *testing.T) {
	// Verify accessibility features
	assert.Contains(t, DefaultEmailHTML, "lang=\"en\"")
	assert.Contains(t, DefaultEmailHTML, "charset=\"UTF-8\"")

	// Verify semantic HTML
	assert.Contains(t, DefaultEmailHTML, "<h1>")
	assert.Contains(t, DefaultEmailHTML, "<h2>")
	assert.Contains(t, DefaultEmailHTML, "<strong>")
}

func TestEmailTemplateVariables(t *testing.T) {
	// Verify required template variables are present in HTML and Text
	requiredVars := []string{
		".Status",
		".Alerts",
		".GroupLabels",
		".CommonLabels",
		".Receiver",
	}

	for _, tmpl := range []string{DefaultEmailHTML, DefaultEmailText} {
		for _, varName := range requiredVars {
			assert.Contains(t, tmpl, varName,
				"Template should contain variable %s", varName)
		}
	}

	// Subject has fewer variables
	subjectVars := []string{".Status", ".Alerts", ".GroupLabels"}
	for _, varName := range subjectVars {
		assert.Contains(t, DefaultEmailSubject, varName,
			"Subject should contain variable %s", varName)
	}
}

func TestEmailHTMLFooter(t *testing.T) {
	// Verify footer content
	assert.Contains(t, DefaultEmailHTML, "Alertmanager++ OSS")
	assert.Contains(t, DefaultEmailHTML, ".Receiver")
	assert.Contains(t, DefaultEmailHTML, ".ExternalURL")
}

func TestEmailHTMLRunbookButton(t *testing.T) {
	// Verify runbook button
	assert.Contains(t, DefaultEmailHTML, "runbook_url")
	assert.Contains(t, DefaultEmailHTML, "class=\"button\"")
	assert.Contains(t, DefaultEmailHTML, "View Runbook")
}

// Benchmark tests
func BenchmarkGetDefaultEmailTemplates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GetDefaultEmailTemplates()
	}
}

func BenchmarkValidateEmailHTMLSize(b *testing.B) {
	html := strings.Repeat("x", 50*1024) // 50KB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ValidateEmailHTMLSize(html)
	}
}
