# PARITY-A5 — исследование кода

## 1. Где объявлены поля

### `TemplateData` (`internal/notification/template/data.go:98–104`)
```go
// ExternalURL is Alert History external URL
ExternalURL string

// SilenceURL is direct link to create silence for this alert
// Example: "https://alerts.company.com/silences/new?filter=alertname%3DHighCPU"
SilenceURL string
```
Builder-методы существуют (`WithExternalURL`, `WithSilenceURL`, строки 254–264).
**Поля объявлены, но нигде не заполняются из конфига.**

### `emailTemplateData` (`internal/infrastructure/publishing/email_models.go:37`)
```go
ExternalURL string
```
Отдельная структура для email-шаблонов.

## 2. Точки с захардкоженным `""`

| Файл | Строка | Контекст |
|---|---|---|
| `internal/infrastructure/publishing/formatter.go` | 175 | `result["externalURL"] = ""` в `formatAlertmanager()` |
| `internal/infrastructure/publishing/email_publisher_enhanced.go` | 240 | `ExternalURL: ""` в `buildEmailTemplateData()` |
| `internal/infrastructure/webhook/prometheus_parser.go` | 238 | `ExternalURL: ""` в структуре-результате парсера |

> `prometheus_parser.go` — парсит входящий вебхук от Prometheus, `ExternalURL` там означает URL источника (от Prometheus), а не наш AMP. Это поле не надо трогать.

## 3. Как данные шаблонов используются

### Webhook publisher (`webhook_publisher_enhanced.go`)
- Вызывает `formatter.FormatAlert(ctx, enrichedAlert, format)`
- `formatAlertmanager` заполняет `result["externalURL"] = ""` — это и есть исходящий Alertmanager-формат

### Email publisher (`email_publisher_enhanced.go`)
- Вызывает `buildEmailTemplateData(enrichedAlert, target)` → получает `*emailTemplateData`
- `ExternalURL: ""` — футер email не содержит ссылку на AMP

### Notification template defaults
- `defaults/webhook.go:52–53`: `"silence": "{{ .SilenceURL }}"`, `"external": "{{ .ExternalURL }}"` — поля есть, но пустые
- `defaults/webhook.go:121`: Teams-кнопка `{{ if .SilenceURL }}` — кнопка не показывается
- `defaults/email.go:266`: `{{ if .ExternalURL }}<a href=...>{{ end }}` — ссылка не рендерится

## 4. Как config доходит до publishers

```
main.go
  └── config.LoadConfig()
  └── application.NewServiceRegistry(cfg, logger)
        └── ServiceRegistry хранит cfg *config.Config
        └── Создаёт publishers (через factory/registry)
              └── NewEnhancedWebhookPublisher(client, validator, formatter, metrics, logger)
              └── NewEmailEnhancedPublisher(...)
```

`NewAlertFormatter()` (`formatter.go:88`) — принимает ноль параметров. Нет места для `externalURL`.

`buildEmailTemplateData` — вспомогательная функция, принимает `enrichedAlert` и `target`, не принимает `externalURL`.

**Вывод**: нужно пробросить `externalURL` через:
1. Конфиг (`ServerConfig.ExternalURL`)
2. `ServiceRegistry` → при создании formatter и publishers
3. `DefaultAlertFormatter` — поле `externalURL string`
4. `buildEmailTemplateData` — параметр `externalURL string`

## 5. Alertmanager-совместимый формат SilenceURL

Оригинальный Alertmanager формирует:
```
{externalURL}/#/silences?filter={matchers}
```

Пример:
```
https://alertmanager.example.com/#/silences?filter=%7Balertname%3D%22HighCPU%22%2C+instance%3D%22prod-1%22%7D
```

Формат `filter`: Alertmanager использует OpenMetrics label-matcher format:
```
{alertname="HighCPU", instance="prod-1"}
```
URL-encoded: `%7Balertname%3D%22HighCPU%22%2Cinstance%3D%22prod-1%22%7D`

Альтернативный (проще, без фигурных скобок) — comma-separated matchers:
```
alertname%3D"HighCPU",instance%3D"prod-1"
```

**Решение**: использовать формат Alertmanager UI (`{...}`), как делает оригинал.

## 6. Где в конфиге правильно разместить поле

`ServerConfig` (`config.go:91–99`) — уже содержит `Port`, `Host`, `ReadTimeout`.
`ExternalURL string` логично добавить туда — это URL, по которому сервер доступен снаружи.

YAML-ключ: `server.external_url`
Env-переменная: `AMP_SERVER_EXTERNAL_URL`

## 7. Места в конфиге setDefaults / валидация

`setDefaults()` (`config.go:404+`) — там задаются дефолты через viper.
Добавить: `viper.SetDefault("server.external_url", "")`.

Валидация: если `server.external_url != ""`, то `url.ParseRequestURI(v)` должен не вернуть ошибку.
Можно добавить в `Config.Validate()`.

## 8. Существующие тесты (точки для обновления)

| Файл | Что тестирует |
|---|---|
| `formatter_test.go` | `formatAlertmanager`, `formatWebhook` |
| `email_publisher_test.go` | рендер email-шаблона |
| `defaults/defaults_integration_test.go:50` | задаёт `data.ExternalURL = "https://alertmanager.example.com"` напрямую |

## 9. Helm chart

Путь: `helm/values.yaml` — нужна проверка наличия секции `config.server`.
