package publishing

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// EMAIL-RENDER-ESCAPING: alert data reaches the email from /api/v2/alerts, so
// it must not become markup, links or headers.

func renderDefaultEmail(t *testing.T, alert *core.EnrichedAlert) (subject, html, text string) {
	t.Helper()
	target := newTestTarget(map[string]string{})
	_, _, subjectTmpl, htmlTmpl, textTmpl := extractEmailConfig(target)
	subject, html, text, err := renderEmailContent(buildEmailTemplateData(alert, target, ""), subjectTmpl, htmlTmpl, textTmpl)
	require.NoError(t, err)
	return subject, html, text
}

func TestRenderEmailContent_EscapesAlertDataInHTML(t *testing.T) {
	alert := newTestEnrichedAlert(core.StatusFiring)
	alert.Alert.Annotations["description"] = `<script>alert(1)</script>`
	alert.Alert.Annotations["runbook_url"] = `javascript:alert(document.cookie)`

	_, html, text := renderDefaultEmail(t, alert)

	assert.NotContains(t, html, "<script>alert(1)</script>")
	assert.Contains(t, html, "&lt;script&gt;alert(1)&lt;/script&gt;")
	assert.NotContains(t, html, "javascript:")
	assert.Contains(t, html, `href="#ZgotmplZ"`)
	assert.Contains(t, text, "<script>alert(1)</script>", "the plain-text part is not HTML and must not be escaped")
}

func TestRenderEmailContent_SubjectNotHTMLEscaped(t *testing.T) {
	alert := newTestEnrichedAlert(core.StatusFiring)
	alert.Alert.AlertName = `Disk "root" <90%>`
	alert.Alert.Labels["alertname"] = alert.Alert.AlertName

	subject, _, _ := renderDefaultEmail(t, alert)
	assert.Contains(t, subject, `Disk "root" <90%>`)
}

// An alert carrying only alertname must still send: a render error used to
// abort the whole email.
func TestRenderEmailContent_AlertWithOnlyAlertname(t *testing.T) {
	for _, status := range []core.AlertStatus{core.StatusFiring, core.StatusResolved} {
		t.Run(string(status), func(t *testing.T) {
			alert := &core.EnrichedAlert{Alert: &core.Alert{
				Fingerprint: "fp-min",
				AlertName:   "OnlyName",
				Status:      status,
				Labels:      map[string]string{"alertname": "OnlyName"},
				StartsAt:    time.Now(),
			}}

			subject, html, text := renderDefaultEmail(t, alert)
			assert.Contains(t, subject, "OnlyName")
			assert.Contains(t, html, "N/A")
			assert.Contains(t, html, "No description")
			assert.Contains(t, text, "Severity: UNKNOWN")
			assert.NotContains(t, html+text, "<no value>")
		})
	}
}

func TestBuildMIMEMessage_SubjectCRLFStripped(t *testing.T) {
	cases := map[string]string{
		"ascii":     "HighCPU\r\nBcc: attacker@evil.example",
		"non-ascii": "Нагрузка\r\nBcc: attacker@evil.example",
		"bare lf":   "HighCPU\nBcc: attacker@evil.example",
	}
	for name, subject := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := buildMIMEMessage(&EmailMessage{
				To:      []string{"oncall@example.com"},
				From:    "amp@example.com",
				Subject: subject,
				Text:    "body",
			}, "amp@example.com", []string{"oncall@example.com"})
			require.NoError(t, err)

			headers := string(raw)
			if i := strings.Index(headers, "\r\n\r\n"); i >= 0 {
				headers = headers[:i]
			}
			assert.NotContains(t, headers, "\r\nBcc:")
			assert.NotContains(t, headers, "\nBcc:")
			assert.Equal(t, 1, strings.Count(headers, "Subject:"))
		})
	}
}
