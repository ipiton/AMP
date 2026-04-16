package publishing

import "time"

// SMTPConfig содержит параметры подключения к SMTP-серверу.
// Заполняется из PublishingTarget.Headers при создании publisher-а.
type SMTPConfig struct {
	Host       string // SMTP-хост (без порта)
	Port       int    // SMTP-порт (по умолчанию 587)
	Username   string // SMTP AUTH username
	Password   string // SMTP AUTH password
	RequireTLS bool   // Требовать TLS: порт 465 → direct TLS (SMTPS), остальные → STARTTLS
	From       string // Адрес отправителя (MAIL FROM)
}

// EmailMessage представляет готовое к отправке письмо.
type EmailMessage struct {
	To      []string          // Список получателей
	From    string            // Отправитель
	Subject string            // Тема письма (rendered)
	HTML    string            // HTML-тело (rendered)
	Text    string            // Plain text тело (rendered)
	Headers map[string]string // Дополнительные заголовки
}

// emailTemplateData — контекст для рендеринга email-шаблонов.
// Структура совместима с шаблонами из defaults.DefaultEmailHTML/Text/Subject.
type emailTemplateData struct {
	Status             string
	GroupLabels        map[string]string
	CommonLabels       map[string]string
	CommonAnnotations  map[string]string
	Labels             map[string]string
	Annotations        map[string]string
	Alerts             []emailAlertItem
	Receiver           string
	ExternalURL        string
}

// emailAlertItem — один алерт в контексте шаблона.
type emailAlertItem struct {
	Labels      map[string]string
	Annotations map[string]string
	StartsAt    time.Time
	EndsAt      *time.Time
}
