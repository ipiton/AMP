# Технический долг (TECH-DEBT)

Список архитектурных проблем, требующих исправления. Отсортировано по приоритету (Critical → High → Medium → Low).

## Critical
- [x] ~~**GOD-OBJECT-MAIN**~~ — закрыто через PHASE-2 Bootstrap Consolidation. `main.go` теперь ~125 строк, логика в `ServiceRegistry`, `Router`, `handlers`.
- [x] ~~**STATE-STORE-LEAK**~~ — закрыто через PHASE-2. State stores вынесены из `cmd/server/`.
- [ ] **SPLIT-BRAIN-RISK** — Отсутствие транзакционной консистентности между In-Memory Store и БД. ~3d

## High
- [x] ~~**DUPLICATED-DB-ADAPTERS**~~ — закрыто 2026-08-17 удалением, не Query Builder'ом: `postgres_adapter.go` (915 строк) и интерфейс `Database` + `NewDatabase` были мёртвым кодом с 0 prod-вызывателей (Standard живёт на `PostgresStorageAdapter`, Lite — на `SQLiteDatabase`). Удалены также 6 мёртвых методов SQLite (~185 строк). Попутно: sentinel-ошибки выровнены (`core.ErrAlertNotFound` в обоих профилях), закрыта SQL-инъекция label-ключа в sqlite `ListAlerts` (json path теперь параметр), добавлены `rows.Err()`-проверки во все scan-циклы. −~1250 строк.
- [ ] **DTO-FRAGMENTATION** — Избыток структур `apiAlert`, `storedAlert`, `core.Alert`. Нужна консолидация. ~1d
- [x] ~~**MANUAL-SQL-RISK**~~ — закрыто 2026-08-17. Все фильтры параметризованы (`$N` + args); реальные векторы устранены: `ORDER BY` whitelist в `silencing/filter_builder.go` и `template/repository_crud.go`, JSONB-экранирование через `buildJSONBContainmentFilter`/`json.Marshal`, имя таблицы в `migrations/manager.go` параметризовано. Регресс-тесты: `filter_builder_injection_test.go`.

## Medium
- [x] ~~**ERROR-REINVENTION**~~ — закрыто 2026-08-17. Все вызыватели deprecated-шимов в `infrastructure/publishing` мигрированы на `pkg/httperror` (NewHTTPError*/PublishingClassifier/Is*), шимы с 0 ссылок удалены (`rootly_errors.go` целиком, Legacy*-алиасы, `NewWebhookErrorWithType`, поле `Workers`). Остался только живой `ErrorType`-классификатор webhook_client (лейблы метрик не 1:1 с httperror).
- [ ] **GLOBAL-LOCK-CONTENTION** — Глобальный мьютекс в Store при высокой нагрузке. Нужно шардирование. ~1d
- [x] ~~**NOTIFICATION-TIMER-STUBS**~~ — закрыто через PARITY-A1 (2026-04-17).
- [x] ~~**INHIBITION-DEAD-WIRING**~~ — закрыто через PARITY-A2 (2026-04-16). `ShouldInhibit` вызывается в `alert_processor.go:154-155`.
- [x] ~~**DEDUP-STATE-STUB**~~ — закрыто 2026-08-17. Rule 7 реализован в `SimpleFilterEngine`: fingerprint+status в окне (default 1m, `SetDedupWindow`), lazy sweep, потокобезопасно. Смена статуса firing→resolved не блокируется. Тесты: `filter_engine_dedup_test.go` (включая -race).
- [x] ~~**CORS-TODO**~~ — закрыто 2026-08-17. `server.cors.*` (enabled/origins/methods/headers, default off) + `corsMiddleware` в стеке с preflight 204. Тесты: `middleware_cors_test.go`.

## Low
- [x] ~~**DEAD-CODE-MAIN-FULL**~~ — Legacy `main.go.full` удалён. Закрыто 2026-03-08.
- [x] ~~**SIMPLE-PUBLISHER-PANIC**~~ — закрыто 2026-08-17. `core/services/publisher.go` удалён: ссылок на `SimplePublisher` не осталось (runtime на real publishing path с SERVICE-REGISTRY-STUB-PATH).
