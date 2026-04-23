package publishing

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/notification/template/defaults"
	notifurl "github.com/ipiton/AMP/internal/notification/url"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// EnhancedEmailPublisher реализует AlertPublisher для SMTP email-доставки.
// Использует SMTPClient (net/smtp) и рендерит HTML+Text multipart письма
// из шаблонов defaults.GetDefaultEmailTemplates().
type EnhancedEmailPublisher struct {
	*BaseEnhancedPublisher
	client      SMTPClient
	externalURL string
}

// NewEnhancedEmailPublisher создаёт email publisher с заданным SMTP клиентом.
// externalURL используется для построения SilenceURL и footer-ссылки в письмах.
func NewEnhancedEmailPublisher(
	client SMTPClient,
	metrics *v2.PublishingMetrics,
	formatter AlertFormatter,
	logger *slog.Logger,
	externalURL string,
) AlertPublisher {
	return &EnhancedEmailPublisher{
		BaseEnhancedPublisher: NewBaseEnhancedPublisher(
			metrics,
			formatter,
			logger.With("component", "email_publisher"),
		),
		client:      client,
		externalURL: externalURL,
	}
}

// Name возвращает имя publisher-а.
func (p *EnhancedEmailPublisher) Name() string {
	return "Email"
}

// Publish рендерит и отправляет email для enrichedAlert через target.
func (p *EnhancedEmailPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	startTime := time.Now()

	p.LogPublishStart(ctx, ProviderEmail, enrichedAlert)

	// Извлечь параметры email из target.Headers
	to, from, subjectTmpl, htmlTmpl, textTmpl := extractEmailConfig(target)
	if len(to) == 0 {
		err := fmt.Errorf("email: no recipients configured for target %q (set 'to' header)", target.Name)
		p.LogPublishError(ctx, ProviderEmail, enrichedAlert.Alert.Fingerprint, err)
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(ProviderEmail, "send", "invalid_recipient")
		}
		return err
	}

	// Построить template data из enrichedAlert
	tmplData := buildEmailTemplateData(enrichedAlert, target, p.externalURL)

	// Рендеринг тела письма
	subject, html, text, err := renderEmailContent(tmplData, subjectTmpl, htmlTmpl, textTmpl)
	if err != nil {
		p.LogPublishError(ctx, ProviderEmail, enrichedAlert.Alert.Fingerprint, err)
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(ProviderEmail, "send", "format_error")
		}
		return fmt.Errorf("email: render templates: %w", err)
	}

	// Собрать EmailMessage
	msg := &EmailMessage{
		To:      to,
		From:    from,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Headers: map[string]string{
			"X-Mailer": "Alertmanager++ OSS",
		},
	}

	// Отправить
	if err := p.client.SendEmail(ctx, msg); err != nil {
		errType := classifyEmailError(err)
		p.LogPublishError(ctx, ProviderEmail, enrichedAlert.Alert.Fingerprint, err)
		if p.GetMetrics() != nil {
			p.GetMetrics().RecordAPIError(ProviderEmail, "send", errType)
			p.GetMetrics().RecordAPIDuration(ProviderEmail, "send", "SMTP", time.Since(startTime))
		}
		return fmt.Errorf("email: send: %w", err)
	}

	// Успех
	duration := time.Since(startTime)
	p.LogPublishSuccess(ctx, ProviderEmail, enrichedAlert.Alert.Fingerprint, duration)
	if p.GetMetrics() != nil {
		p.GetMetrics().RecordMessage(ProviderEmail, "success")
		p.GetMetrics().RecordAPIDuration(ProviderEmail, "send", "SMTP", duration)
	}

	return nil
}

// extractEmailConfig извлекает email-параметры из target.Headers.
//
// Поддерживаемые ключи Headers:
//   - "to"              — comma-separated список получателей (обязательный)
//   - "from"            — адрес отправителя
//   - "subject_template" — кастомный шаблон темы (опционально)
//   - "html_template"   — кастомный HTML шаблон (опционально)
//   - "text_template"   — кастомный text шаблон (опционально)
//
// Возвращает defaults если custom templates не заданы.
func extractEmailConfig(target *core.PublishingTarget) (to []string, from, subjectTmpl, htmlTmpl, textTmpl string) {
	emailDefaults := defaults.GetDefaultEmailTemplates()
	subjectTmpl = emailDefaults.Subject
	htmlTmpl = emailDefaults.HTML
	textTmpl = emailDefaults.Text

	if target.Headers == nil {
		return
	}

	// Получатели
	if v, ok := target.Headers["to"]; ok && v != "" {
		for _, addr := range strings.Split(v, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				to = append(to, addr)
			}
		}
	}

	// Отправитель
	if v, ok := target.Headers["from"]; ok && v != "" {
		from = v
	}

	// Custom templates (переопределяют defaults)
	if v, ok := target.Headers["subject_template"]; ok && v != "" {
		subjectTmpl = v
	}
	if v, ok := target.Headers["html_template"]; ok && v != "" {
		htmlTmpl = v
	}
	if v, ok := target.Headers["text_template"]; ok && v != "" {
		textTmpl = v
	}

	return
}

// extractSMTPConfig извлекает SMTP-параметры из target.Headers.
//
// Поддерживаемые ключи Headers:
//   - "smtp_host"     — SMTP сервер (обязательный)
//   - "smtp_port"     — SMTP порт (по умолчанию 587)
//   - "smtp_username" — SMTP auth username
//   - "smtp_password" — SMTP auth password
//   - "smtp_tls"      — "true" для STARTTLS (по умолчанию false)
//   - "from"          — адрес отправителя
func extractSMTPConfig(target *core.PublishingTarget) SMTPConfig {
	cfg := SMTPConfig{
		Port: 587,
	}
	if target.Headers == nil {
		return cfg
	}

	if v, ok := target.Headers["smtp_host"]; ok {
		cfg.Host = v
	}
	if v, ok := target.Headers["smtp_port"]; ok {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.Port = port
		}
	}
	if v, ok := target.Headers["smtp_username"]; ok {
		cfg.Username = v
	}
	if v, ok := target.Headers["smtp_password"]; ok {
		cfg.Password = v
	}
	if v, ok := target.Headers["smtp_tls"]; ok {
		cfg.RequireTLS = strings.EqualFold(v, "true")
	}
	if v, ok := target.Headers["from"]; ok {
		cfg.From = v
	}

	return cfg
}

// buildEmailTemplateData строит контекст шаблона из EnrichedAlert и PublishingTarget.
func buildEmailTemplateData(enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget, externalURL string) *emailTemplateData {
	alert := enrichedAlert.Alert

	// Для single-alert publisher GroupLabels = {alertname: alert.AlertName}
	groupLabels := map[string]string{
		"alertname": alert.AlertName,
	}

	// CommonLabels / Labels — все labels алерта
	commonLabels := make(map[string]string, len(alert.Labels))
	for k, v := range alert.Labels {
		commonLabels[k] = v
	}

	// CommonAnnotations / Annotations — все annotations алерта
	commonAnnotations := make(map[string]string, len(alert.Annotations))
	for k, v := range alert.Annotations {
		commonAnnotations[k] = v
	}

	// Один алерт в массиве Alerts
	alertItem := emailAlertItem{
		Labels:      commonLabels,
		Annotations: commonAnnotations,
		StartsAt:    alert.StartsAt,
		EndsAt:      alert.EndsAt,
	}

	return &emailTemplateData{
		Status:            string(alert.Status),
		GroupLabels:       groupLabels,
		CommonLabels:      commonLabels,
		CommonAnnotations: commonAnnotations,
		Labels:            commonLabels,
		Annotations:       commonAnnotations,
		Alerts:            []emailAlertItem{alertItem},
		Receiver:          target.Name,
		ExternalURL:       externalURL,
		SilenceURL:        notifurl.BuildSilenceURL(externalURL, alert.Labels),
	}
}

// emailTemplateFuncs возвращает FuncMap с функциями для email-шаблонов.
var emailTemplateFuncs = template.FuncMap{
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"default": func(def string, val string) string {
		if val == "" {
			return def
		}
		return val
	},
}

// renderEmailContent рендерит subject, html и text из шаблонов и данных.
func renderEmailContent(data *emailTemplateData, subjectTmpl, htmlTmpl, textTmpl string) (subject, html, text string, err error) {
	subject, err = renderTemplate("subject", subjectTmpl, data)
	if err != nil {
		return "", "", "", fmt.Errorf("subject template: %w", err)
	}

	html, err = renderTemplate("html", htmlTmpl, data)
	if err != nil {
		return "", "", "", fmt.Errorf("html template: %w", err)
	}

	text, err = renderTemplate("text", textTmpl, data)
	if err != nil {
		return "", "", "", fmt.Errorf("text template: %w", err)
	}

	return subject, html, text, nil
}

// renderTemplate рендерит один шаблон с заданными данными.
func renderTemplate(name, tmplStr string, data *emailTemplateData) (string, error) {
	t, err := template.New(name).Funcs(emailTemplateFuncs).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", name, err)
	}
	return buf.String(), nil
}
