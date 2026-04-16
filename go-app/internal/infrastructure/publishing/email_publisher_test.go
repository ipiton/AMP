package publishing

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/prometheus/client_golang/prometheus"
)

// ============================================================================
// MockSMTPClient — тестовый mock для SMTPClient
// ============================================================================

type MockSMTPClient struct {
	SendEmailCalls  []*EmailMessage
	SendEmailErr    error
	HealthErr       error
	CloseCalled     bool
}

func (m *MockSMTPClient) SendEmail(_ context.Context, msg *EmailMessage) error {
	m.SendEmailCalls = append(m.SendEmailCalls, msg)
	return m.SendEmailErr
}

func (m *MockSMTPClient) Health(_ context.Context) error {
	return m.HealthErr
}

func (m *MockSMTPClient) Close() error {
	m.CloseCalled = true
	return nil
}

// ============================================================================
// Helpers
// ============================================================================

func newTestEnrichedAlert(status core.AlertStatus) *core.EnrichedAlert {
	now := time.Now()
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-test-001",
			AlertName:   "HighCPU",
			Status:      status,
			Labels: map[string]string{
				"alertname": "HighCPU",
				"severity":  "critical",
				"instance":  "node-1",
			},
			Annotations: map[string]string{
				"description": "CPU usage above 90%",
				"summary":     "High CPU load",
			},
			StartsAt: now,
		},
	}
}

func newTestTarget(headers map[string]string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:    "test-email-target",
		Type:    "email",
		URL:     "http://placeholder.local", // URL required by core model
		Enabled: true,
		Headers: headers,
		Format:  core.FormatWebhook,
	}
}

func newTestMetrics(t *testing.T) *v2.PublishingMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return v2.NewPublishingMetrics(reg)
}

// ============================================================================
// Тесты EnhancedEmailPublisher
// ============================================================================

func TestEnhancedEmailPublisher_Name(t *testing.T) {
	mock := &MockSMTPClient{}
	pub := NewEnhancedEmailPublisher(mock, nil, nil, testLogger())
	if pub.Name() != "Email" {
		t.Errorf("Name() = %q, want %q", pub.Name(), "Email")
	}
}

func TestEnhancedEmailPublisher_Publish_Success(t *testing.T) {
	mock := &MockSMTPClient{}
	metrics := newTestMetrics(t)
	pub := NewEnhancedEmailPublisher(mock, metrics, nil, testLogger())

	target := newTestTarget(map[string]string{
		"to":   "ops@example.com, dev@example.com",
		"from": "alerts@example.com",
	})
	alert := newTestEnrichedAlert(core.StatusFiring)

	err := pub.Publish(context.Background(), alert, target)
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	if len(mock.SendEmailCalls) != 1 {
		t.Fatalf("SendEmail called %d times, want 1", len(mock.SendEmailCalls))
	}

	msg := mock.SendEmailCalls[0]
	if len(msg.To) != 2 {
		t.Errorf("msg.To len = %d, want 2", len(msg.To))
	}
	if msg.From != "alerts@example.com" {
		t.Errorf("msg.From = %q, want %q", msg.From, "alerts@example.com")
	}
	if msg.Subject == "" {
		t.Error("msg.Subject is empty")
	}
	if msg.HTML == "" {
		t.Error("msg.HTML is empty")
	}
	if msg.Text == "" {
		t.Error("msg.Text is empty")
	}
}

func TestEnhancedEmailPublisher_Publish_NoRecipients(t *testing.T) {
	mock := &MockSMTPClient{}
	pub := NewEnhancedEmailPublisher(mock, nil, nil, testLogger())

	target := newTestTarget(map[string]string{}) // нет "to"
	alert := newTestEnrichedAlert(core.StatusFiring)

	err := pub.Publish(context.Background(), alert, target)
	if err == nil {
		t.Fatal("expected error for missing recipients, got nil")
	}
	if !strings.Contains(err.Error(), "no recipients") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "no recipients")
	}
	if len(mock.SendEmailCalls) != 0 {
		t.Errorf("SendEmail called %d times, want 0", len(mock.SendEmailCalls))
	}
}

func TestEnhancedEmailPublisher_Publish_SMTPError(t *testing.T) {
	smtpErr := errors.New("535 Authentication failed")
	mock := &MockSMTPClient{SendEmailErr: smtpErr}
	metrics := newTestMetrics(t)
	pub := NewEnhancedEmailPublisher(mock, metrics, nil, testLogger())

	target := newTestTarget(map[string]string{"to": "ops@example.com"})
	alert := newTestEnrichedAlert(core.StatusFiring)

	err := pub.Publish(context.Background(), alert, target)
	if err == nil {
		t.Fatal("expected error from SMTP, got nil")
	}
	if !strings.Contains(err.Error(), "send") {
		t.Errorf("error = %q, expected to contain %q", err.Error(), "send")
	}
}

func TestEnhancedEmailPublisher_Publish_Resolved(t *testing.T) {
	mock := &MockSMTPClient{}
	pub := NewEnhancedEmailPublisher(mock, nil, nil, testLogger())

	target := newTestTarget(map[string]string{"to": "ops@example.com"})
	alert := newTestEnrichedAlert(core.StatusResolved)

	err := pub.Publish(context.Background(), alert, target)
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	msg := mock.SendEmailCalls[0]
	// Subject должен содержать [RESOLVED]
	if !strings.Contains(msg.Subject, "[RESOLVED]") {
		t.Errorf("Subject = %q, want to contain [RESOLVED]", msg.Subject)
	}
}

// ============================================================================
// Тесты extractEmailConfig
// ============================================================================

func TestExtractEmailConfig_Defaults(t *testing.T) {
	target := newTestTarget(map[string]string{"to": "user@example.com"})
	to, from, subjectTmpl, htmlTmpl, textTmpl := extractEmailConfig(target)

	if len(to) != 1 || to[0] != "user@example.com" {
		t.Errorf("to = %v, want [user@example.com]", to)
	}
	if from != "" {
		t.Errorf("from = %q, want empty", from)
	}
	if subjectTmpl == "" {
		t.Error("subjectTmpl is empty (expected default)")
	}
	if htmlTmpl == "" {
		t.Error("htmlTmpl is empty (expected default)")
	}
	if textTmpl == "" {
		t.Error("textTmpl is empty (expected default)")
	}
}

func TestExtractEmailConfig_MultipleRecipients(t *testing.T) {
	target := newTestTarget(map[string]string{
		"to": "a@example.com, b@example.com ,c@example.com",
	})
	to, _, _, _, _ := extractEmailConfig(target)
	if len(to) != 3 {
		t.Errorf("len(to) = %d, want 3", len(to))
	}
}

func TestExtractEmailConfig_CustomTemplates(t *testing.T) {
	target := newTestTarget(map[string]string{
		"to":               "a@example.com",
		"subject_template": "Custom Subject: {{ .Status }}",
	})
	_, _, subjectTmpl, _, _ := extractEmailConfig(target)
	if subjectTmpl != "Custom Subject: {{ .Status }}" {
		t.Errorf("subjectTmpl = %q, want custom template", subjectTmpl)
	}
}

// ============================================================================
// Тесты extractSMTPConfig
// ============================================================================

func TestExtractSMTPConfig_Defaults(t *testing.T) {
	target := newTestTarget(map[string]string{})
	cfg := extractSMTPConfig(target)
	if cfg.Port != 587 {
		t.Errorf("Port = %d, want 587", cfg.Port)
	}
	if cfg.RequireTLS {
		t.Error("RequireTLS should be false by default")
	}
}

func TestExtractSMTPConfig_Full(t *testing.T) {
	target := newTestTarget(map[string]string{
		"smtp_host":     "smtp.example.com",
		"smtp_port":     "465",
		"smtp_username": "user",
		"smtp_password": "secret",
		"smtp_tls":      "true",
		"from":          "noreply@example.com",
	})
	cfg := extractSMTPConfig(target)

	if cfg.Host != "smtp.example.com" {
		t.Errorf("Host = %q, want smtp.example.com", cfg.Host)
	}
	if cfg.Port != 465 {
		t.Errorf("Port = %d, want 465", cfg.Port)
	}
	if cfg.Username != "user" {
		t.Errorf("Username = %q, want user", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Errorf("Password = %q, want secret", cfg.Password)
	}
	if !cfg.RequireTLS {
		t.Error("RequireTLS should be true")
	}
	if cfg.From != "noreply@example.com" {
		t.Errorf("From = %q, want noreply@example.com", cfg.From)
	}
}

// ============================================================================
// Тесты buildEmailTemplateData
// ============================================================================

func TestBuildEmailTemplateData(t *testing.T) {
	alert := newTestEnrichedAlert(core.StatusFiring)
	target := newTestTarget(map[string]string{})

	data := buildEmailTemplateData(alert, target)

	if data.Status != "firing" {
		t.Errorf("Status = %q, want firing", data.Status)
	}
	if data.GroupLabels["alertname"] != "HighCPU" {
		t.Errorf("GroupLabels.alertname = %q, want HighCPU", data.GroupLabels["alertname"])
	}
	if data.Labels["severity"] != "critical" {
		t.Errorf("Labels.severity = %q, want critical", data.Labels["severity"])
	}
	if len(data.Alerts) != 1 {
		t.Errorf("len(Alerts) = %d, want 1", len(data.Alerts))
	}
	if data.Receiver != "test-email-target" {
		t.Errorf("Receiver = %q, want test-email-target", data.Receiver)
	}
}

// ============================================================================
// Тесты renderEmailContent
// ============================================================================

func TestRenderEmailContent_DefaultTemplates(t *testing.T) {
	alert := newTestEnrichedAlert(core.StatusFiring)
	target := newTestTarget(map[string]string{})
	data := buildEmailTemplateData(alert, target)

	_, _, subjectTmpl, htmlTmpl, textTmpl := extractEmailConfig(target)
	subject, html, text, err := renderEmailContent(data, subjectTmpl, htmlTmpl, textTmpl)

	if err != nil {
		t.Fatalf("renderEmailContent() error: %v", err)
	}
	if !strings.Contains(subject, "[ALERT]") {
		t.Errorf("subject = %q, want to contain [ALERT]", subject)
	}
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("html does not contain <!DOCTYPE html>")
	}
	if !strings.Contains(text, "[ALERT]") {
		t.Errorf("text = %q, want to contain [ALERT]", text)
	}
}

func TestRenderEmailContent_BadTemplate(t *testing.T) {
	data := &emailTemplateData{Status: "firing"}
	_, _, _, err := renderEmailContent(data, "{{ .Unknown.Field }}", "", "")
	// text/template may not error on missing fields (zero value), but bad syntax should
	// Test bad syntax instead
	_, _, _, err = renderEmailContent(data, "{{ unclosed", "", "")
	if err == nil {
		t.Error("expected error for bad template syntax, got nil")
	}
}

// ============================================================================
// Тесты classifyEmailError
// ============================================================================

func TestClassifyEmailError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected string
	}{
		{"auth_error", "535 Authentication credentials invalid", "auth_error"},
		{"rate_limit_421", "421 Too many connections", "rate_limit"},
		{"rate_limit_451", "451 Requested action aborted", "rate_limit"},
		{"rate_limit_452", "452 Insufficient system storage", "rate_limit"},
		{"invalid_recipient_550", "550 User does not exist", "invalid_recipient"},
		{"invalid_recipient_551", "551 User not local", "invalid_recipient"},
		{"server_error_500", "500 Command unrecognized", "server_error"},
		{"server_error_503", "503 Service unavailable", "server_error"},
		{"tls_error", "tls: failed to verify certificate", "tls_error"},
		{"network_connection_refused", "connection refused", "network_error"},
		{"network_no_host", "no such host", "network_error"},
		{"nil_error", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = errors.New(tt.errMsg)
			}
			got := classifyEmailError(err)
			if got != tt.expected {
				t.Errorf("classifyEmailError(%q) = %q, want %q", tt.errMsg, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// Тесты buildMIMEMessage
// ============================================================================

func TestBuildMIMEMessage_ContainsHeaders(t *testing.T) {
	msg := &EmailMessage{
		To:      []string{"to@example.com"},
		From:    "from@example.com",
		Subject: "Test Subject",
		HTML:    "<b>Hello</b>",
		Text:    "Hello",
	}

	raw, err := buildMIMEMessage(msg)
	if err != nil {
		t.Fatalf("buildMIMEMessage() error: %v", err)
	}

	body := string(raw)
	if !strings.Contains(body, "From: from@example.com") {
		t.Error("MIME message missing From header")
	}
	if !strings.Contains(body, "To: to@example.com") {
		t.Error("MIME message missing To header")
	}
	if !strings.Contains(body, "Subject: Test Subject") {
		t.Error("MIME message missing Subject header")
	}
	if !strings.Contains(body, "multipart/alternative") {
		t.Error("MIME message missing multipart/alternative content type")
	}
	if !strings.Contains(body, "text/html") {
		t.Error("MIME message missing text/html part")
	}
	if !strings.Contains(body, "text/plain") {
		t.Error("MIME message missing text/plain part")
	}
}

func TestBuildMIMEMessage_NoRecipients(t *testing.T) {
	// buildMIMEMessage сам по себе не проверяет получателей — это задача SendEmail
	msg := &EmailMessage{
		To:      nil,
		From:    "from@example.com",
		Subject: "Test",
		Text:    "Hello",
	}
	// Не должен паниковать
	_, err := buildMIMEMessage(msg)
	if err != nil {
		t.Logf("buildMIMEMessage with nil To returned (expected): %v", err)
	}
}

// ============================================================================
// Тесты ParseTargetType
// ============================================================================

func TestParseTargetType_Email(t *testing.T) {
	got := ParseTargetType("email")
	if got != TargetTypeEmail {
		t.Errorf("ParseTargetType(\"email\") = %q, want %q", got, TargetTypeEmail)
	}
}

// ============================================================================
// Тест интеграции: PublisherFactory создаёт EnhancedEmailPublisher
// ============================================================================

func TestPublisherFactory_CreatePublisher_Email(t *testing.T) {
	metrics := newTestMetrics(t)
	factory := NewPublisherFactory(nil, testLogger(), metrics)
	defer factory.Shutdown()

	pub, err := factory.CreatePublisher("email")
	if err != nil {
		t.Fatalf("CreatePublisher(email) error: %v", err)
	}
	if pub.Name() != "Email" {
		t.Errorf("pub.Name() = %q, want Email", pub.Name())
	}
}

func TestPublisherFactory_CreatePublisherForTarget_Email(t *testing.T) {
	metrics := newTestMetrics(t)
	factory := NewPublisherFactory(nil, testLogger(), metrics)
	defer factory.Shutdown()

	target := newTestTarget(map[string]string{
		"to":        "ops@example.com",
		"smtp_host": "smtp.example.com",
	})

	pub, err := factory.CreatePublisherForTarget(target)
	if err != nil {
		t.Fatalf("CreatePublisherForTarget error: %v", err)
	}
	if pub.Name() != "Email" {
		t.Errorf("pub.Name() = %q, want Email", pub.Name())
	}
}

// ============================================================================
// Helpers
// ============================================================================

// testLogger возвращает no-op slog logger для тестов.
func testLogger() *slog.Logger {
	return slog.Default()
}
