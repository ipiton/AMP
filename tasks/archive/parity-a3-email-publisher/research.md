# PARITY-A3: Email Publisher — Research

## Анализ существующего кода

### 1. Интерфейс AlertPublisher

`go-app/internal/infrastructure/publishing/publisher.go:19-25`

```go
type AlertPublisher interface {
    Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error
    Name() string
}
```

Минимальный интерфейс — только `Publish` и `Name`. Нет `Health()`, `Shutdown()` на уровне интерфейса.

### 2. BaseEnhancedPublisher

`go-app/internal/infrastructure/publishing/base_publisher.go`

Все enhanced publishers (Slack, PagerDuty, Rootly, Webhook) встраивают `*BaseEnhancedPublisher`. Предоставляет:
- `GetMetrics() *v2.PublishingMetrics`
- `GetFormatter() AlertFormatter`
- `GetLogger() *slog.Logger`
- `LogPublishStart/Success/Error()`
- `RecordCacheHit/Miss()`

EmailPublisher должен следовать той же структуре.

### 3. AlertFormatter

`go-app/internal/infrastructure/publishing/formatter.go:73-77`

```go
type AlertFormatter interface {
    FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error)
}
```

Formatter возвращает `map[string]any`. Для Email-формата нужно определить `core.FormatEmail` (или использовать существующий `core.FormatEmail` если уже определён).

### 4. PublisherFactory

`go-app/internal/infrastructure/publishing/publisher.go:186-402`

```go
type PublisherFactory struct {
    formatter          AlertFormatter
    logger             *slog.Logger
    rootlyCache        IncidentIDCache
    rootlyClientMap    map[string]RootlyIncidentsClient
    pagerDutyCache     EventKeyCache
    pagerDutyClientMap map[string]PagerDutyEventsClient
    slackCache         MessageIDCache
    slackClientMap     map[string]SlackWebhookClient
    slackCleanupWorker func()
    metrics            *v2.PublishingMetrics
}
```

Паттерн для каждого провайдера: `map[string]<ProviderClient>` — cache клиентов по ключу конфигурации (API key / webhook URL / SMTP host). Email: `map[string]SMTPClient` по `smarthost`.

`CreatePublisher` и `CreatePublisherForTarget` — два switch-а, оба нужно расширить.

### 5. TargetType и models.go

`go-app/internal/infrastructure/publishing/models.go`

```go
const (
    TargetTypeRootly       TargetType = "rootly"
    TargetTypePagerDuty    TargetType = "pagerduty"
    TargetTypeSlack        TargetType = "slack"
    TargetTypeWebhook      TargetType = "webhook"
    TargetTypeAlertmanager TargetType = "alertmanager"
)
```

**`TargetTypeEmail` отсутствует** — нужно добавить `TargetTypeEmail TargetType = "email"` и case в `ParseTargetType`.

### 6. Конфигурация Email

`go-app/internal/alertmanager/config/config.go:91-99` — `EmailConfig`:

```go
type EmailConfig struct {
    To         string            // Адрес получателя
    From       string            // Адрес отправителя (overrides Global)
    Smarthost  string            // SMTP сервер host:port (overrides Global)
    Headers    map[string]string // Дополнительные email-заголовки
    HTML       string            // Кастомный HTML шаблон (overrides Default)
    Text       string            // Кастомный Text шаблон (overrides Default)
    RequireTLS *bool             // Требовать TLS (overrides Global)
}
```

`GlobalConfig` (строки 17-28) содержит глобальные SMTP-настройки:
- `SMTPFrom` — sender по умолчанию
- `SMTPSmarthost` — SMTP сервер по умолчанию
- `SMTPAuthUsername`, `SMTPAuthPassword` — credentials
- `SMTPRequireTLS` — TLS по умолчанию

**Важно**: `PublishingTarget.Config map[string]interface{}` — поле для type-specific конфигурации. Для Email туда маппируется содержимое `EmailConfig`.

### 7. Email Templates

`go-app/internal/notification/template/defaults/email.go`

Готовые шаблоны:
- `DefaultEmailSubject` — Go template, переменные: `.Status`, `.GroupLabels.alertname`, `.Alerts`
- `DefaultEmailHTML` — responsive HTML с inline CSS, переменные: `.Status`, `.GroupLabels`, `.CommonLabels`, `.CommonAnnotations`, `.Labels`, `.Annotations`, `.Receiver`, `.ExternalURL`, `.Alerts`
- `DefaultEmailText` — plain text fallback, те же переменные
- `GetDefaultEmailTemplates()` — возвращает `*EmailTemplates`
- `ValidateEmailHTMLSize(html string) bool` — проверка < 100KB

Шаблоны используют Go `text/template` синтаксис с функциями `upper`, `lower`, `default`.

### 8. Паттерн существующих publisher-ов

На примере `EnhancedSlackPublisher` (`slack_publisher_enhanced.go`):

```
Структура:
  EnhancedSlackPublisher
    ├── *BaseEnhancedPublisher (metrics, formatter, logger)
    ├── client SlackWebhookClient  // провайдер-специфичный клиент
    └── cache  MessageIDCache      // (опционально) для lifecycle management

Фабричный метод в PublisherFactory:
  createEnhancedSlackPublisher(target) → извлечь webhookURL из target.URL → get/create client → NewEnhancedSlackPublisher(...)

Publish():
  1. LogPublishStart()
  2. FormatAlert(ctx, alert, format)
  3. Вызов клиента
  4. RecordMetrics
  5. LogPublishSuccess/Error
```

EmailPublisher должен следовать этому же паттерну.

### 9. Метрики v2

`go-app/pkg/metrics/v2/` — `PublishingMetrics`:

```go
RecordMessage(provider, status string)
RecordAPIError(provider, operation, errorType string)
RecordAPIDuration(provider, operation, method string, duration time.Duration)
RecordCacheHit(provider string)
RecordCacheMiss(provider string)
```

Provider-строка для Email: `"email"` (добавить константу в `v2` package).

### 10. Классификация ошибок

`go-app/internal/infrastructure/publishing/errors.go` — `GetPublishingErrorType(err error) string`

Аналогично `slack_errors.go`, `pagerduty_errors.go`, `webhook_errors.go` — нужен `email_errors.go` с классификацией SMTP-ошибок:
- `"auth_error"` — 535 Authentication failed
- `"rate_limit"` — 452 Too many recipients / 421 Service unavailable
- `"server_error"` — 5xx ошибки сервера
- `"tls_error"` — TLS handshake failed
- `"network_error"` — connection refused, timeout

## Точки интеграции

| Файл | Изменение |
|------|-----------|
| `models.go` | Добавить `TargetTypeEmail`, case в `ParseTargetType` |
| `publisher.go` | Добавить `emailClientMap`, case в `CreatePublisher` и `CreatePublisherForTarget`, метод `createEnhancedEmailPublisher` |
| `publisher.go` (Shutdown) | Закрывать email-клиенты если нужно |

## Зависимости Go

Только stdlib:
- `net/smtp` — SMTP-клиент
- `net/mail` — парсинг email-адресов, `mail.Address`
- `mime/multipart` — MIME multipart для HTML+Text
- `mime/quotedprintable` — кодирование тела письма
- `crypto/tls` — TLS-конфигурация
- `text/template` — рендеринг шаблонов (уже используется в проекте)
- `bytes`, `strings`, `fmt` — стандартная обработка

Внешние зависимости: **не нужны**.

## Решения по дизайну

### SMTP Client Interface

Для тестируемости — интерфейс `SMTPClient`:

```go
type SMTPClient interface {
    SendEmail(ctx context.Context, msg *EmailMessage) error
    Health(ctx context.Context) error
    Close() error
}
```

Реализация: `SMTPDialer` использует `net/smtp.Dial` / `smtp.DialTLS`.

### Конфигурация через target.Config

```go
// Извлечение из PublishingTarget.Config:
smtpHost     := target.Config["smtp_host"].(string)
smtpPort     := target.Config["smtp_port"].(int)    // default 587
smtpUsername := target.Config["smtp_username"].(string)
smtpPassword := target.Config["smtp_password"].(string)
smtpTLS      := target.Config["smtp_tls"].(bool)    // default true
toAddresses  := target.Config["to"].(string)
fromAddress  := target.Config["from"].(string)
```

### Рендеринг шаблонов

EmailPublisher рендерит шаблоны самостоятельно (не через `AlertFormatter`), потому что:
1. Formatter возвращает `map[string]any` для JSON/структурированных форматов
2. Email требует rendered string для subject, HTML body, text body
3. Шаблоны уже готовы в `defaults/email.go`

Альтернативно — форматтер для `core.FormatEmail` возвращает map с ключами `"subject"`, `"html"`, `"text"`.

**Решение**: использовать форматтер с `core.FormatEmail` и извлекать строки из результата — это консистентно с остальными publisher-ами.
