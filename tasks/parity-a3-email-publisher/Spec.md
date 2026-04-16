# PARITY-A3: Email Publisher — Spec

## Новые файлы

```
go-app/internal/infrastructure/publishing/
├── email_client.go           # SMTPClient интерфейс + SMTPDialer реализация
├── email_models.go           # EmailMessage, SMTPConfig
├── email_errors.go           # классификация SMTP-ошибок
├── email_publisher_enhanced.go  # EnhancedEmailPublisher
└── email_publisher_test.go   # unit-тесты
```

## Изменения в существующих файлах

```
go-app/internal/infrastructure/publishing/
├── models.go        # + TargetTypeEmail, + ParseTargetType case
└── publisher.go     # + emailClientMap, + cases в CreatePublisher/ForTarget, + createEnhancedEmailPublisher
```

---

## Контракты

### email_models.go

```go
package publishing

// SMTPConfig содержит параметры подключения к SMTP-серверу.
// Заполняется из PublishingTarget.Config при создании publisher-а.
type SMTPConfig struct {
    Host       string // SMTP-хост (без порта)
    Port       int    // SMTP-порт (по умолчанию 587)
    Username   string // SMTP AUTH username
    Password   string // SMTP AUTH password
    RequireTLS bool   // Требовать TLS (STARTTLS или direct TLS)
    From       string // Адрес отправителя (MAIL FROM)
}

// EmailMessage представляет готовое к отправке письмо.
type EmailMessage struct {
    To      []string // Список получателей
    From    string   // Отправитель
    Subject string   // Тема письма (rendered)
    HTML    string   // HTML-тело (rendered)
    Text    string   // Plain text тело (rendered)
    Headers map[string]string // Дополнительные заголовки
}
```

### email_client.go

```go
package publishing

import (
    "context"
    "crypto/tls"
    "fmt"
    "net"
    "net/smtp"
    ...
)

// SMTPClient определяет интерфейс SMTP-клиента.
// Интерфейс для тестируемости — в тестах подменяется mock-ом.
type SMTPClient interface {
    SendEmail(ctx context.Context, msg *EmailMessage) error
    Health(ctx context.Context) error   // smtp.Noop() к серверу
    Close() error
}

// SMTPConfig передаётся в NewSMTPDialer.
// (определён в email_models.go)

// SMTPDialer реализует SMTPClient через net/smtp.
type SMTPDialer struct {
    config SMTPConfig
    logger *slog.Logger
}

// NewSMTPDialer создаёт SMTP-клиент.
// Соединение открывается per-send (stateless), не при создании.
func NewSMTPDialer(config SMTPConfig, logger *slog.Logger) SMTPClient

// SendEmail отправляет письмо.
// Каждый вызов открывает соединение, аутентифицируется, отправляет, закрывает.
// Это упрощает retry-логику и избегает stale connection.
func (d *SMTPDialer) SendEmail(ctx context.Context, msg *EmailMessage) error

// buildMIMEMessage собирает MIME multipart сообщение (text/html + text/plain).
// Возвращает raw bytes готовые к smtp.SendMail.
func buildMIMEMessage(msg *EmailMessage) ([]byte, error)

// Health проверяет доступность SMTP-сервера через NOOP.
func (d *SMTPDialer) Health(ctx context.Context) error

func (d *SMTPDialer) Close() error // no-op для stateless клиента
```

**Детали реализации SendEmail:**

```
1. net.DialTimeout(host:port, 10s)
2. smtp.NewClient(conn, host)
3. client.StartTLS(&tls.Config{ServerName: host}) если RequireTLS
4. client.Auth(smtp.PlainAuth("", username, password, host))
5. client.Mail(from)
6. for each recipient: client.Rcpt(to)
7. client.Data() → write MIME message bytes
8. client.Quit()
```

### email_errors.go

```go
package publishing

// classifyEmailError классифицирует SMTP-ошибку для метрик.
func classifyEmailError(err error) string

// Маппинг SMTP кодов → тип ошибки:
// 421, 451, 452 → "rate_limit"      (временный отказ, retry уместен)
// 535           → "auth_error"      (неверные credentials)
// 550, 551, 552 → "invalid_recipient" (постоянная ошибка, retry не нужен)
// 5xx           → "server_error"    (постоянная ошибка сервера)
// TLS error     → "tls_error"
// timeout/conn  → "network_error"
```

### email_publisher_enhanced.go

```go
package publishing

// EnhancedEmailPublisher реализует AlertPublisher для SMTP email.
// Следует паттерну EnhancedSlackPublisher: BaseEnhancedPublisher + специфичный клиент.
type EnhancedEmailPublisher struct {
    *BaseEnhancedPublisher
    client SMTPClient
    config SMTPConfig   // для извлечения From и других defaults
}

// NewEnhancedEmailPublisher создаёт publisher.
func NewEnhancedEmailPublisher(
    client SMTPClient,
    config SMTPConfig,
    metrics *v2.PublishingMetrics,
    formatter AlertFormatter,
    logger *slog.Logger,
) AlertPublisher

// Name возвращает "Email".
func (p *EnhancedEmailPublisher) Name() string

// Publish отправляет алерт на email.
// Шаги:
//   1. LogPublishStart()
//   2. Извлечь to, from, customHTML, customText из target.Config
//   3. FormatAlert(ctx, alert, core.FormatEmail) → map["subject"], map["html"], map["text"]
//      или рендерить шаблоны напрямую из defaults/email.go
//   4. Собрать EmailMessage
//   5. p.client.SendEmail(ctx, msg)
//   6. Metrics + Logging
func (p *EnhancedEmailPublisher) Publish(
    ctx context.Context,
    enrichedAlert *core.EnrichedAlert,
    target *core.PublishingTarget,
) error

// extractEmailConfig извлекает параметры из target.Config.
// Возвращает адреса получателей и кастомные шаблоны.
func extractEmailConfig(target *core.PublishingTarget) (to []string, from string, htmlTmpl, textTmpl string)

// renderEmailContent рендерит шаблоны (subject, html, text) через text/template.
// Использует GetDefaultEmailTemplates() если кастомные не заданы.
func renderEmailContent(enrichedAlert *core.EnrichedAlert, htmlTmpl, textTmpl, subjectTmpl string) (subject, html, text string, err error)
```

### Изменения в models.go

```go
const (
    TargetTypeRootly       TargetType = "rootly"
    TargetTypePagerDuty    TargetType = "pagerduty"
    TargetTypeSlack        TargetType = "slack"
    TargetTypeWebhook      TargetType = "webhook"
    TargetTypeAlertmanager TargetType = "alertmanager"
    TargetTypeEmail        TargetType = "email"        // NEW
)

// ParseTargetType — добавить:
case "email":
    return TargetTypeEmail
```

### Изменения в publisher.go

```go
type PublisherFactory struct {
    // ... существующие поля ...
    emailClientMap map[string]SMTPClient // NEW: cache клиентов по "host:port"
}

// CreatePublisher — добавить case:
case TargetTypeEmail:
    return NewEmailPublisher(f.formatter, f.logger), nil  // базовый fallback

// CreatePublisherForTarget — добавить case:
case TargetTypeEmail:
    return f.createEnhancedEmailPublisher(target)

// Новый метод:
func (f *PublisherFactory) createEnhancedEmailPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
    config := extractSMTPConfig(target)
    if config.Host == "" {
        f.logger.Warn("Email target missing SMTP host, falling back to no-op", "target", target.Name)
        return NewEmailPublisher(f.formatter, f.logger), nil
    }

    smarthostKey := fmt.Sprintf("%s:%d", config.Host, config.Port)
    client, ok := f.emailClientMap[smarthostKey]
    if !ok {
        client = NewSMTPDialer(config, f.logger)
        f.emailClientMap[smarthostKey] = client
    }

    return NewEnhancedEmailPublisher(client, config, f.metrics, f.formatter, f.logger), nil
}
```

## Конфигурация таргета (PublishingTarget.Config)

```yaml
# Пример K8s Secret для email-таргета:
apiVersion: v1
kind: Secret
metadata:
  name: amp-target-email-ops
  labels:
    amp.io/target-type: email
stringData:
  target.yaml: |
    name: ops-email
    type: email
    config:
      smtp_host: smtp.example.com
      smtp_port: 587          # 587 STARTTLS, 465 TLS, 25 plain
      smtp_username: alerts@example.com
      smtp_password: secret
      smtp_require_tls: true
      from: "AMP Alerts <alerts@example.com>"
      to: "ops-team@example.com,sre@example.com"
      # Опционально: кастомные шаблоны (переопределяют defaults)
      # html_template: "..."
      # text_template: "..."
      # subject_template: "..."
```

## MIME структура письма

```
MIME-Version: 1.0
From: AMP Alerts <alerts@example.com>
To: ops@example.com
Subject: [ALERT] HighCPU (2 alerts)
Content-Type: multipart/alternative; boundary="boundary42"
Date: ...
X-Mailer: AMP-Alertmanager

--boundary42
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

[ALERT] HighCPU ...  (DefaultEmailText rendered)

--boundary42
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: quoted-printable

<!DOCTYPE html>... (DefaultEmailHTML rendered)

--boundary42--
```

## Шаблонный контекст

Структура данных для рендеринга шаблонов строится из `*core.EnrichedAlert`:

```go
type emailTemplateData struct {
    Status             string
    GroupLabels        map[string]string
    CommonLabels       map[string]string
    CommonAnnotations  map[string]string
    Labels             map[string]string   // первого алерта
    Annotations        map[string]string   // первого алерта
    Alerts             []*core.Alert
    Receiver           string
    ExternalURL        string
}
```

## Метрики

Provider-строка: `"email"` (константа `v2.ProviderEmail`).

```go
// При успехе:
metrics.RecordMessage("email", "success")
metrics.RecordAPIDuration("email", "send_email", "SMTP", duration)

// При ошибке:
metrics.RecordAPIError("email", "send_email", classifyEmailError(err))
metrics.RecordAPIDuration("email", "send_email", "SMTP", duration)
```

## Тестовый план

### Unit-тесты (`email_publisher_test.go`)

```go
// 1. MockSMTPClient
type MockSMTPClient struct {
    SentMessages []*EmailMessage
    Err          error
}
func (m *MockSMTPClient) SendEmail(ctx, msg) error { ... }
func (m *MockSMTPClient) Health(ctx) error { return nil }
func (m *MockSMTPClient) Close() error { return nil }

// 2. TestEnhancedEmailPublisher_Publish_Firing
//    - alert.Status = "firing"
//    - проверить: MockClient.SentMessages[0].Subject содержит "[ALERT]"
//    - проверить: HTML не пустой
//    - проверить: Text не пустой

// 3. TestEnhancedEmailPublisher_Publish_Resolved
//    - alert.Status = "resolved"
//    - проверить: Subject содержит "[RESOLVED]"

// 4. TestEnhancedEmailPublisher_Publish_SMTPError
//    - MockClient.Err = errors.New("connection refused")
//    - проверить: Publish возвращает ошибку

// 5. TestExtractEmailConfig
//    - target.Config с smtp_host, smtp_port, to, from
//    - проверить корректное извлечение

// 6. TestRenderEmailContent
//    - проверить рендеринг с реальными шаблонами DefaultEmail*
//    - проверить HTMLSize < 100KB

// 7. TestClassifyEmailError
//    - 535 → "auth_error"
//    - timeout → "network_error"
//    - 5xx → "server_error"
```

### Integration-тест (опционально)

Использовать `net/smtp` test server или `github.com/emersion/go-smtp` mock server для end-to-end проверки.

## Архитектурные решения

### 1. Stateless SMTP соединение (per-send dial)

**Решение**: Открывать новое SMTP-соединение на каждый `SendEmail` вызов.

**Обоснование**: Алерты приходят нечасто (не high-throughput). Persistent connection требует heartbeat, reconnect logic, и усложняет тестирование. Per-send — проще, надёжнее, достаточно для типичной нагрузки (<100 алертов/мин).

### 2. Шаблоны через text/template, не через AlertFormatter

**Решение**: EmailPublisher рендерит шаблоны самостоятельно из `defaults/email.go`, используя `text/template.Execute`.

**Обоснование**: `AlertFormatter.FormatAlert` возвращает `map[string]any` для structured форматов (JSON blocks для Slack, events для PagerDuty). Для email нужны rendered strings. Прямой рендеринг в publisher проще и не требует дополнительного слоя конвертации.

### 3. emailClientMap keyed by "host:port"

**Решение**: Кешировать SMTP клиентов по `"smtp.example.com:587"`.

**Обоснование**: Разные таргеты могут использовать разные SMTP-серверы. При per-send dial cache клиентов = cache конфигураций, не соединений. Это позволяет переиспользовать одного клиента для разных таргетов с одним SMTP сервером.
