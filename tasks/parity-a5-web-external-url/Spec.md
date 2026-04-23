# PARITY-A5-WEB-EXTERNAL-URL — Спецификация

## Обзор

Пробросить `server.external_url` из конфига через `ServiceRegistry` → `DefaultAlertFormatter` и `EnhancedEmailPublisher`, чтобы заполнить три захардкоженных `""` реальным URL и автоматически строить `SilenceURL`.

---

## 1. Конфиг (`internal/config/config.go`)

### 1.1 Структура `ServerConfig`

```go
type ServerConfig struct {
    Port                    int           `mapstructure:"port"`
    Host                    string        `mapstructure:"host"`
    ReadTimeout             time.Duration `mapstructure:"read_timeout"`
    WriteTimeout            time.Duration `mapstructure:"write_timeout"`
    IdleTimeout             time.Duration `mapstructure:"idle_timeout"`
    GracefulShutdownTimeout time.Duration `mapstructure:"graceful_shutdown_timeout"`
    ExternalURL             string        `mapstructure:"external_url"` // новое поле
}
```

### 1.2 `setDefaults()` (после строки 416)

```go
viper.SetDefault("server.external_url", "")
```

### 1.3 `Validate()` (после проверки `Server.Host`, строка ~592)

```go
if c.Server.ExternalURL != "" {
    if _, err := url.ParseRequestURI(c.Server.ExternalURL); err != nil {
        return fmt.Errorf("invalid server.external_url %q: %w", c.Server.ExternalURL, err)
    }
}
```

**Env var**: `SERVER_EXTERNAL_URL` (viper `SetEnvKeyReplacer(".", "_")`, без префикса).

---

## 2. Новая функция `BuildSilenceURL`

**Файл**: `internal/infrastructure/publishing/silence_url.go`

```go
package publishing

import (
    "fmt"
    "net/url"
    "sort"
    "strings"
)

// BuildSilenceURL builds Alertmanager-compatible silence URL.
// Returns "" if externalURL is empty.
// Format: {externalURL}/#/silences?filter={encodedMatchers}
// Matcher format: {alertname="HighCPU",instance="prod-1"}
func BuildSilenceURL(externalURL string, labels map[string]string) string {
    if externalURL == "" {
        return ""
    }

    // Sort label keys for deterministic output
    keys := make([]string, 0, len(labels))
    for k := range labels {
        keys = append(keys, k)
    }
    sort.Strings(keys)

    parts := make([]string, 0, len(labels))
    for _, k := range keys {
        parts = append(parts, fmt.Sprintf(`%s="%s"`, k, labels[k]))
    }
    matcher := "{" + strings.Join(parts, ",") + "}"

    return fmt.Sprintf("%s/#/silences?filter=%s", externalURL, url.QueryEscape(matcher))
}
```

**Возвращает**:
- `""` если `externalURL == ""`
- `https://amp.example.com/#/silences?filter=%7Balertname%3D%22HighCPU%22%2Cinstance%3D%22prod-1%22%7D`

---

## 3. `DefaultAlertFormatter` (`internal/infrastructure/publishing/formatter.go`)

### 3.1 Структура

```go
type DefaultAlertFormatter struct {
    formatters  map[core.PublishingFormat]formatFunc
    externalURL string // новое поле
}
```

### 3.2 Конструктор (сигнатура меняется)

```go
// NewAlertFormatter creates a formatter with the given external URL.
func NewAlertFormatter(externalURL string) AlertFormatter {
    formatter := &DefaultAlertFormatter{
        formatters:  make(map[core.PublishingFormat]formatFunc),
        externalURL: externalURL,
    }
    // ... регистрация форматтеров без изменений ...
    return formatter
}
```

### 3.3 `formatAlertmanager` (строка 175)

```go
// было:
result["externalURL"] = ""

// стало:
result["externalURL"] = f.externalURL
```

---

## 4. `EnhancedEmailPublisher` (`internal/infrastructure/publishing/email_publisher_enhanced.go`)

### 4.1 Структура

```go
type EnhancedEmailPublisher struct {
    *BaseEnhancedPublisher
    client      SMTPClient
    externalURL string // новое поле
}
```

### 4.2 Конструктор (сигнатура меняется)

```go
func NewEnhancedEmailPublisher(
    client SMTPClient,
    metrics *v2.PublishingMetrics,
    formatter AlertFormatter,
    logger *slog.Logger,
    externalURL string, // новый параметр
) AlertPublisher {
    return &EnhancedEmailPublisher{
        BaseEnhancedPublisher: NewBaseEnhancedPublisher(metrics, formatter,
            logger.With("component", "email_publisher")),
        client:      client,
        externalURL: externalURL,
    }
}
```

### 4.3 `buildEmailTemplateData` (сигнатура и тело)

```go
func buildEmailTemplateData(
    enrichedAlert *core.EnrichedAlert,
    target *core.PublishingTarget,
    externalURL string, // новый параметр
) *emailTemplateData {
    // ... существующий код без изменений ...
    return &emailTemplateData{
        // ...
        ExternalURL: externalURL,                                              // было: ""
        SilenceURL:  BuildSilenceURL(externalURL, alert.Labels),              // новое поле
    }
}
```

Вызов в методе publish/send email publisher:
```go
data := buildEmailTemplateData(enrichedAlert, target, p.externalURL)
```

---

## 5. `emailTemplateData` (`internal/infrastructure/publishing/email_models.go`)

Добавить поле `SilenceURL`:

```go
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
    SilenceURL         string // новое поле
}
```

---

## 6. `PublisherFactory` (`internal/infrastructure/publishing/publisher.go`)

### 6.1 Структура

```go
type PublisherFactory struct {
    formatter          AlertFormatter
    logger             *slog.Logger
    externalURL        string // новое поле
    // ... остальные поля без изменений ...
}
```

### 6.2 Конструктор (сигнатура меняется)

```go
func NewPublisherFactory(
    formatter AlertFormatter,
    logger *slog.Logger,
    metrics *v2.PublishingMetrics,
    externalURL string, // новый параметр
) *PublisherFactory {
    // ...
    return &PublisherFactory{
        formatter:   formatter,
        logger:      logger,
        externalURL: externalURL,
        // ... остальные поля ...
    }
}
```

### 6.3 `createEnhancedEmailPublisher` (строки 411–444)

```go
return NewEnhancedEmailPublisher(
    client,
    f.metrics,
    f.formatter,
    f.logger,
    f.externalURL, // добавить
), nil
```

---

## 7. Wiring в `ServiceRegistry` (`internal/application/publishing_runtime.go`)

Найти строку создания `NewAlertFormatter()` и `NewPublisherFactory(...)`:

```go
// было:
formatter := infrapublishing.NewAlertFormatter()
r.publisherFactory = infrapublishing.NewPublisherFactory(formatter, r.logger, publishingMetrics)

// стало:
externalURL := r.config.Server.ExternalURL
formatter := infrapublishing.NewAlertFormatter(externalURL)
r.publisherFactory = infrapublishing.NewPublisherFactory(formatter, r.logger, publishingMetrics, externalURL)
```

---

## 8. Helm

### 8.1 `helm/amp/values.yaml` — новая секция (добавить рядом с другими server-параметрами)

```yaml
# ===============================
# Server Configuration
# ===============================
server:
  # External URL for callback links in notifications (email footer, Teams silence button).
  # Must be the public URL of this AMP instance. Leave empty to disable links.
  # Env: SERVER_EXTERNAL_URL
  externalUrl: ""
```

### 8.2 `helm/amp/templates/configmap.yaml` — добавить строку

```yaml
{{- if .Values.server.externalUrl }}
SERVER_EXTERNAL_URL: {{ .Values.server.externalUrl | quote }}
{{- end }}
```

---

## 9. Тесты

### 9.1 `silence_url_test.go` (новый файл)

Таблица тестов для `BuildSilenceURL`:

| externalURL | labels | ожидаемый результат |
|---|---|---|
| `""` | `{alertname: "Test"}` | `""` |
| `"https://amp.example.com"` | `{}` | `"https://amp.example.com/#/silences?filter=%7B%7D"` |
| `"https://amp.example.com"` | `{alertname: "HighCPU", instance: "prod-1"}` | правильно URL-encoded |
| `"https://amp.example.com"` | `{b: "2", a: "1"}` | ключи отсортированы (a раньше b) |

### 9.2 Обновить `formatter_test.go`

- Все вызовы `NewAlertFormatter()` → `NewAlertFormatter("")`
- Добавить тест: `NewAlertFormatter("https://test.example.com")` → `formatAlertmanager` → `result["externalURL"] == "https://test.example.com"`

### 9.3 Обновить `email_publisher_test.go`

- Все вызовы `NewEnhancedEmailPublisher(...)` → добавить `""` как последний параметр
- Добавить тест: с `externalURL = "https://amp.example.com"` → `emailTemplateData.ExternalURL` корректен, `emailTemplateData.SilenceURL` не пустой

### 9.4 Обновить `publisher_test.go`

- Все вызовы `NewPublisherFactory(...)` → добавить `""` как последний параметр

---

## 10. Архитектурные решения

| Решение | Альтернатива | Обоснование |
|---|---|---|
| `externalURL` хранится в `DefaultAlertFormatter` | Глобальная переменная | Нет глобального состояния, тестируемо |
| `externalURL` хранится в `PublisherFactory` → `EnhancedEmailPublisher` | Передавать через `publish()` | Factory уже владеет lifecycle publishers |
| `SilenceURL` строится в `buildEmailTemplateData` | В шаблоне через `BuildSilenceURL` | Логика вне шаблона, тестируема |
| Env var `SERVER_EXTERNAL_URL` (без AMP_ префикса) | `AMP_SERVER_EXTERNAL_URL` | Viper `SetEnvKeyReplacer` не задаёт префикс — соответствует существующим `PUBLISHING_*`, `LLM_*` |
| `prometheus_parser.go` строка 238 — НЕ трогать | Заполнять из конфига | Это `ExternalURL` источника (Prometheus), не AMP |
