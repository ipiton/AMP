# PARITY-A5-WEB-EXTERNAL-URL — требования

## Проблема

Alertmanager принимает флаг `--web.external-url` и передаёт его значение в шаблоны нотификаций как `{{ .ExternalURL }}`.
На основе этого URL шаблоны формируют:

- `{{ .ExternalURL }}` — ссылку на сам Alertmanager (footer в email, заголовок карточки)
- `{{ .SilenceURL }}` — прямую ссылку «заглушить этот алерт»
  (`{ExternalURL}/#/silences?filter={matchers}`)

В AMP:
- `ExternalURL` объявлен в `TemplateData` (поле + builder `WithExternalURL`)
- `SilenceURL` объявлен в `TemplateData` (поле + builder `WithSilenceURL`)
- **Но оба поля нигде не заполняются**: захардкожены в `""` в трёх точках

```
formatter.go:175           result["externalURL"] = ""
email_publisher_enhanced.go:240   ExternalURL: ""
prometheus_parser.go:238   ExternalURL: ""
```

Результат: ссылки в нотификациях (email-footer, Teams-кнопка «Silence Alert», webhook-поле `urls.silence`) пустые — функциональность сломана.

## Цели / Success Criteria

1. **Конфиг**: добавить `server.external_url` в `ServerConfig` (тип `string`); env-переменная `AMP_SERVER_EXTERNAL_URL`; по умолчанию пустая строка (функциональность gracefully деградирует).
2. **Propagation**: значение из конфига передаётся через `ServiceRegistry` → publishers/formatter; ни один publisher не хранит его как глобальное состояние.
3. **ExternalURL в шаблонах**: все три захардкоженных `""` заменены на реальный URL из конфига.
4. **SilenceURL строится автоматически**: `BuildSilenceURL(externalURL, labels)` → `{externalURL}/#/silences?filter={encodedMatchers}`. Если `externalURL` пуст — возвращает `""`.
5. **SilenceURL инжектится** в `TemplateData` при построении данных для шаблона (webhook publisher, email publisher).
6. **Тесты**: unit-тест `BuildSilenceURL`, интеграционный тест нотификации проверяет, что `urls.silence` и `externalURL` корректны.
7. **Helm**: `values.yaml` содержит `config.server.externalUrl: ""` с комментарием.

## Scope

### В scope
- `internal/config/config.go` — добавить `ExternalURL` в `ServerConfig`
- `internal/config/config.go` — дефолт + валидация (если задан — должен быть валидным URL)
- Новая функция `BuildSilenceURL` (пакет `internal/notification/url` или рядом с formatter)
- `internal/infrastructure/publishing/formatter.go` — `DefaultAlertFormatter` принимает `externalURL string`, использует его в `formatAlertmanager`
- `internal/infrastructure/publishing/email_publisher_enhanced.go` — `buildEmailTemplateData` принимает `externalURL`, строит SilenceURL
- `internal/infrastructure/webhook/prometheus_parser.go` — заполнять `ExternalURL` из переданного значения (или оставить как есть, если webhook-вход — это внешний Alertmanager, а не наш)
- Wiring: `ServiceRegistry` → publishers получают `externalURL`
- Тесты: `BuildSilenceURL`, обновлённый `formatter_test.go`, обновлённый `email_publisher_test.go`
- Helm `values.yaml`

### Не в scope
- UI/Dashboard (отдельная задача)
- Изменение формата `SilenceURL` (соблюдаем Alertmanager-совместимый `/#/silences?filter=...`)
- HTTP-эндпоинт для чтения конфига (PARITY-B)
- Глобальный hot-reload конфига (RELOADABLE-COMPONENT-INTERFACES)

## Критерий готовности

`/end-task` проходит когда:
- `go test ./...` зелёный
- `go vet ./...` без ошибок
- Email/webhook-тест с `externalURL: "https://amp.example.com"` показывает корректные ссылки
- Helm `values.yaml` обновлён
