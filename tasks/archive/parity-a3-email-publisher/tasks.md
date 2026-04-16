# PARITY-A3: Email Publisher — Tasks

## Статус: TODO

Ветка: `feature/parity-a3-email-publisher`

---

## Вертикальные слайсы

### Слайс 1: Core infrastructure (день 1)

Создать SMTP клиент и модели без интеграции в factory. После этого слайса: можно отправить письмо вручную.

- [ ] **1.1** Создать `email_models.go` — структуры `SMTPConfig` и `EmailMessage`
- [ ] **1.2** Создать `email_client.go` — интерфейс `SMTPClient` и реализация `SMTPDialer`
  - [ ] `NewSMTPDialer(config SMTPConfig, logger *slog.Logger) SMTPClient`
  - [ ] `SendEmail(ctx, msg) error` — per-send dial, STARTTLS, PLAIN auth, MIME multipart
  - [ ] `buildMIMEMessage(msg) ([]byte, error)` — multipart/alternative (text + html)
  - [ ] `Health(ctx) error` — NOOP к серверу
  - [ ] `Close() error` — no-op
- [ ] **1.3** Создать `email_errors.go` — `classifyEmailError(err) string`
  - [ ] Маппинг SMTP 535 → `"auth_error"`
  - [ ] Маппинг 421/451/452 → `"rate_limit"`
  - [ ] Маппинг 550/551/552 → `"invalid_recipient"`
  - [ ] Маппинг 5xx → `"server_error"`
  - [ ] TLS errors → `"tls_error"`
  - [ ] network/timeout → `"network_error"`

---

### Слайс 2: Publisher реализация (день 1-2)

Реализовать `EnhancedEmailPublisher`. После этого слайса: publisher работает изолированно с mock-клиентом.

- [ ] **2.1** Создать `email_publisher_enhanced.go`
  - [ ] Структура `EnhancedEmailPublisher` с `*BaseEnhancedPublisher` и `SMTPClient`
  - [ ] `NewEnhancedEmailPublisher(client, config, metrics, formatter, logger) AlertPublisher`
  - [ ] `Name() string` → `"Email"`
  - [ ] `Publish(ctx, enrichedAlert, target) error`:
    - [ ] `extractEmailConfig(target)` — извлечь `to`, `from`, custom templates из `target.Config`
    - [ ] `renderEmailContent(alert, html, text, subject)` — рендеринг через `text/template`
    - [ ] Собрать `EmailMessage`
    - [ ] `client.SendEmail(ctx, msg)`
    - [ ] Метрики и логирование (паттерн из slack_publisher_enhanced.go)
- [ ] **2.2** Вспомогательные функции:
  - [ ] `extractEmailConfig(target *core.PublishingTarget) (to []string, from string, htmlTmpl, textTmpl, subjectTmpl string)`
  - [ ] `renderEmailContent(alert *core.EnrichedAlert, ...) (subject, html, text string, err error)`
  - [ ] `buildEmailTemplateData(alert *core.EnrichedAlert) emailTemplateData` — маппинг EnrichedAlert → template context

---

### Слайс 3: Factory интеграция (день 2)

Зарегистрировать Email в factory. После этого слайса: End-to-end путь работает.

- [ ] **3.1** Изменить `models.go`:
  - [ ] Добавить `TargetTypeEmail TargetType = "email"`
  - [ ] Добавить `case "email": return TargetTypeEmail` в `ParseTargetType`
- [ ] **3.2** Изменить `publisher.go`:
  - [ ] Добавить `emailClientMap map[string]SMTPClient` в `PublisherFactory`
  - [ ] Инициализировать `emailClientMap: make(map[string]SMTPClient)` в `NewPublisherFactory`
  - [ ] Добавить `case TargetTypeEmail:` в `CreatePublisher` → `NewEmailPublisher(f.formatter, f.logger)`
  - [ ] Добавить `case TargetTypeEmail:` в `CreatePublisherForTarget` → `f.createEnhancedEmailPublisher(target)`
  - [ ] Реализовать `createEnhancedEmailPublisher(target) (AlertPublisher, error)`
  - [ ] Добавить `extractSMTPConfig(target *core.PublishingTarget) SMTPConfig` — helper для factory

---

### Слайс 4: Тесты (день 2-3)

- [ ] **4.1** Создать `email_publisher_test.go`:
  - [ ] `MockSMTPClient` — реализует `SMTPClient` для тестов
  - [ ] `TestEnhancedEmailPublisher_Publish_Firing` — subject содержит "[ALERT]"
  - [ ] `TestEnhancedEmailPublisher_Publish_Resolved` — subject содержит "[RESOLVED]"
  - [ ] `TestEnhancedEmailPublisher_Publish_SMTPError` — ошибка клиента пробрасывается
  - [ ] `TestEnhancedEmailPublisher_Publish_MultipleRecipients` — to разбивается по запятой
  - [ ] `TestExtractEmailConfig` — корректное извлечение из target.Config
  - [ ] `TestRenderEmailContent_DefaultTemplates` — шаблоны рендерятся без ошибок
  - [ ] `TestRenderEmailContent_HTMLSize` — HTML < 100KB
  - [ ] `TestClassifyEmailError` — все ветки классификации
- [ ] **4.2** Создать `email_client_test.go` (опционально):
  - [ ] `TestBuildMIMEMessage` — структура multipart правильная
  - [ ] `TestSMTPDialer_Health_ConnectionRefused` — возвращает ошибку

---

### Слайс 5: Проверка и документация (день 3)

- [ ] **5.1** Запустить `go build ./...` — нет ошибок компиляции
- [ ] **5.2** Запустить `go vet ./...` — нет предупреждений
- [ ] **5.3** Запустить `go test ./internal/infrastructure/publishing/... -run Email` — все тесты проходят
- [ ] **5.4** Проверить что существующие тесты не сломаны: `go test ./internal/infrastructure/publishing/...`
- [ ] **5.5** Обновить `docs/06-planning/NEXT.md` — перенести задачу в Done
- [ ] **5.6** Обновить `docs/06-planning/BACKLOG.md` если нужно

---

## Известные риски

| Риск | Вероятность | Митигация |
|------|------------|-----------|
| STARTTLS несовместимость со старыми SMTP серверами | Средняя | Добавить опцию `smtp_skip_starttls` в config |
| Шаблоны не компилируются с `text/template` (функции `upper`, `lower`, `default`) | Средняя | Зарегистрировать FuncMap перед рендерингом |
| MIME encoding ломает спецсимволы | Низкая | Использовать `mime/quotedprintable` для тела |
| `core.FormatEmail` константа не существует | Низкая | Добавить в `core` package или не использовать FormatAlert |

## Зависимости между задачами

```
1.1 → 1.2 → 2.1 → 3.2
1.3 → 2.1
1.2 → 4.2
2.1 → 4.1
3.1 → 3.2
3.2 → 5.*
```

## Definition of Done

- [ ] `go build ./...` — OK
- [ ] `go test ./internal/infrastructure/publishing/... -run Email` — все проходят
- [ ] Существующие тесты не сломаны
- [ ] `PublisherFactory.CreatePublisherForTarget` возвращает `*EnhancedEmailPublisher` для `type: "email"`
- [ ] `TargetTypeEmail` определён в `models.go`
- [ ] Документация в `tasks/parity-a3-email-publisher/` актуальна
- [ ] Ветка не `main`
