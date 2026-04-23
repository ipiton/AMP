# PARITY-A5-WEB-EXTERNAL-URL — Чеклист реализации

## Срез 1: Конфиг

- [ ] `internal/config/config.go` — добавить `ExternalURL string \`mapstructure:"external_url"\`` в `ServerConfig` (после `GracefulShutdownTimeout`)
- [ ] `internal/config/config.go` — добавить `viper.SetDefault("server.external_url", "")` в `setDefaults()` (после строки с `graceful_shutdown_timeout`)
- [ ] `internal/config/config.go` — добавить URL-валидацию в `Validate()`: если `c.Server.ExternalURL != ""` → `url.ParseRequestURI` (импорт `"net/url"` уже должен быть, иначе добавить)
- [ ] Убедиться что `go build ./...` проходит

## Срез 2: `BuildSilenceURL`

- [ ] Создать `internal/infrastructure/publishing/silence_url.go` с функцией `BuildSilenceURL(externalURL string, labels map[string]string) string`
- [ ] Создать `internal/infrastructure/publishing/silence_url_test.go` — таблица тестов (пустой URL, пустые labels, нормальный кейс, порядок ключей)
- [ ] `go test ./internal/infrastructure/publishing/... -run TestBuildSilenceURL` — зелёный

## Срез 3: Formatter

- [ ] `internal/infrastructure/publishing/formatter.go` — добавить поле `externalURL string` в `DefaultAlertFormatter`
- [ ] `formatter.go` — обновить `NewAlertFormatter()` → `NewAlertFormatter(externalURL string)`, сохранить в структуре
- [ ] `formatter.go` строка 175 — заменить `result["externalURL"] = ""` на `result["externalURL"] = f.externalURL`
- [ ] `internal/infrastructure/publishing/formatter_test.go` — обновить все вызовы `NewAlertFormatter()` → `NewAlertFormatter("")`
- [ ] `formatter_test.go` — добавить тест: `NewAlertFormatter("https://test.example.com")` → `formatAlertmanager` возвращает правильный `externalURL`
- [ ] `go test ./internal/infrastructure/publishing/... -run TestFormatter` — зелёный

## Срез 4: Email Publisher

- [ ] `internal/infrastructure/publishing/email_models.go` — добавить поле `SilenceURL string` в `emailTemplateData`
- [ ] `internal/infrastructure/publishing/email_publisher_enhanced.go` — добавить поле `externalURL string` в `EnhancedEmailPublisher`
- [ ] `email_publisher_enhanced.go` — обновить `NewEnhancedEmailPublisher(...)` — добавить параметр `externalURL string`, сохранить в структуре
- [ ] `email_publisher_enhanced.go` — обновить сигнатуру `buildEmailTemplateData` — добавить параметр `externalURL string`
- [ ] `email_publisher_enhanced.go` строка 240 — заменить `ExternalURL: ""` на `ExternalURL: externalURL`
- [ ] `email_publisher_enhanced.go` — добавить `SilenceURL: BuildSilenceURL(externalURL, alert.Labels)` в возвращаемую структуру
- [ ] Найти вызов `buildEmailTemplateData(...)` в теле publisher — добавить `p.externalURL` как аргумент
- [ ] `internal/infrastructure/publishing/email_publisher_test.go` — обновить вызовы `NewEnhancedEmailPublisher(...)` — добавить `""` как последний параметр
- [ ] `email_publisher_test.go` — добавить тест с `externalURL = "https://amp.example.com"` → проверить `ExternalURL` и `SilenceURL` в template data
- [ ] `go test ./internal/infrastructure/publishing/... -run TestEmail` — зелёный

## Срез 5: `PublisherFactory` и wiring

- [ ] `internal/infrastructure/publishing/publisher.go` — добавить поле `externalURL string` в `PublisherFactory`
- [ ] `publisher.go` — обновить `NewPublisherFactory(...)` — добавить параметр `externalURL string`, сохранить в структуре
- [ ] `publisher.go` `createEnhancedEmailPublisher` — добавить `f.externalURL` в вызов `NewEnhancedEmailPublisher(...)`
- [ ] `internal/infrastructure/publishing/publisher_test.go` — обновить вызовы `NewPublisherFactory(...)` — добавить `""` как последний параметр
- [ ] Найти в `ServiceRegistry` (скорее всего `internal/application/publishing_runtime.go`) строки создания `NewAlertFormatter()` и `NewPublisherFactory(...)`:
  - Обновить → `NewAlertFormatter(r.config.Server.ExternalURL)`
  - Обновить → `NewPublisherFactory(formatter, r.logger, publishingMetrics, r.config.Server.ExternalURL)`
- [ ] `go build ./...` — без ошибок
- [ ] `go vet ./...` — без предупреждений

## Срез 6: Helm

- [ ] `helm/amp/values.yaml` — добавить секцию `server:` с полем `externalUrl: ""` и комментарием (env: `SERVER_EXTERNAL_URL`)
- [ ] `helm/amp/templates/configmap.yaml` — добавить условный блок `{{- if .Values.server.externalUrl }} SERVER_EXTERNAL_URL: {{ .Values.server.externalUrl | quote }} {{- end }}`

## Финальная проверка

- [ ] `go test ./...` — зелёный
- [ ] `go vet ./...` — чистый
- [ ] `git diff --check` — нет trailing whitespace
- [ ] Ручная проверка: запустить AMP с `SERVER_EXTERNAL_URL=https://amp.example.com`, отправить тестовый алерт → в email footer есть ссылка, в webhook payload `externalURL` не пустой
- [ ] Обновить `docs/06-planning/NEXT.md` — убрать задачу из WIP
- [ ] Обновить `docs/06-planning/DONE.md` — добавить запись
