# PARITY-A3: Email Publisher — Requirements

## Контекст задачи

AMP (Alertmanager++) реализует замену Alertmanager с расширенными возможностями. Для production-ready паритета с Alertmanager необходим Email-канал доставки алертов.

Email — один из базовых каналов оригинального Alertmanager. Без него AMP не может использоваться как полноценная замена в большинстве production-сценариев.

## Проблема

В проекте существует:
- `EmailConfig` в `internal/alertmanager/config/config.go` — описывает конфигурацию email-получателя
- Email-шаблоны в `internal/notification/template/defaults/email.go` — готовые HTML/Text/Subject шаблоны
- `GlobalConfig.SMTP*` поля — глобальные SMTP-настройки

**Отсутствует**:
- SMTP-клиент (Go `net/smtp` wrapper)
- `EmailPublisher` — реализация `AlertPublisher` интерфейса
- Регистрация `"email"` в `PublisherFactory`

Тип `"email"` в `TargetType` не определён — при получении алерта с `target.Type = "email"` factory возвращает fallback `WebhookPublisher`, что означает тихий отказ доставки.

## Критерии приёмки

### Must Have
- [ ] SMTP-клиент отправляет email через `net/smtp` с поддержкой STARTTLS и TLS
- [ ] `EmailPublisher.Publish()` рендерит HTML+Text тело из существующих шаблонов (`DefaultEmailHTML`, `DefaultEmailText`, `DefaultEmailSubject`)
- [ ] `PublisherFactory.CreatePublisher("email")` и `CreatePublisherForTarget` возвращают `EmailPublisher`
- [ ] `TargetTypeEmail` добавлен в `models.go` и `ParseTargetType`
- [ ] Конфигурация SMTP читается из `target.Config` (специфичная) с fallback на глобальный `GlobalConfig.SMTP*`
- [ ] Ошибки SMTP правильно классифицируются для метрик и retry-логики
- [ ] Unit-тесты с mock SMTP-сервером (или `net/smtp/test`)

### Should Have
- [ ] Поддержка множества получателей (`To` как список через запятую)
- [ ] Метрики через `v2.PublishingMetrics` (аналогично Slack/PagerDuty publisher-ам)
- [ ] MIME multipart (HTML + Text fallback в одном письме)
- [ ] Структурированное логирование через `slog`

### Nice to Have
- [ ] Retry при временных ошибках SMTP (5xx, timeout)
- [ ] Health check (`smtp.Noop()` для проверки соединения)

## Scope

**В scope:**
- `email_client.go` — SMTP-клиент (интерфейс + реализация)
- `email_publisher_enhanced.go` — реализация `AlertPublisher`
- `email_models.go` — модели (EmailMessage, EmailConfig для publisher)
- `email_errors.go` — классификация SMTP-ошибок
- Изменения в `publisher.go` — регистрация в factory
- Изменения в `models.go` — `TargetTypeEmail`
- `email_publisher_test.go` — unit-тесты

**Вне scope:**
- UI для настройки email-таргетов
- OAuth / OAuth2 SMTP-аутентификация (только PLAIN и LOGIN)
- Email-очередь с персистентностью (DLQ уже есть в очереди)
- Изменения в схеме БД

## Зависимости

- Зависит от: ничего (независимая задача)
- Блокирует: PARITY-A4, PARITY-A5 (полный паритет с Alertmanager)
- Оценка: 2-3 дня
