# Технический долг (TECH-DEBT)

Список архитектурных проблем, требующих исправления. Отсортировано по приоритету (Critical → High → Medium → Low).

## Critical
- [x] ~~**GOD-OBJECT-MAIN**~~ — закрыто через PHASE-2 Bootstrap Consolidation. `main.go` теперь ~125 строк, логика в `ServiceRegistry`, `Router`, `handlers`.
- [x] ~~**STATE-STORE-LEAK**~~ — закрыто через PHASE-2. State stores вынесены из `cmd/server/`.
- [ ] **SPLIT-BRAIN-RISK** — Отсутствие транзакционной консистентности между In-Memory Store и БД. ~3d

## High
- [ ] **DUPLICATED-DB-ADAPTERS** — `PostgresDatabase` и `SQLiteDatabase` дублируют 80% логики. Нужен Query Builder. ~3d
- [ ] **DTO-FRAGMENTATION** — Избыток структур `apiAlert`, `storedAlert`, `core.Alert`. Нужна консолидация. ~1d
- [x] ~~**MANUAL-SQL-RISK**~~ — закрыто 2026-08-17. Все фильтры параметризованы (`$N` + args); реальные векторы устранены: `ORDER BY` whitelist в `silencing/filter_builder.go` и `template/repository_crud.go`, JSONB-экранирование через `buildJSONBContainmentFilter`/`json.Marshal`, имя таблицы в `migrations/manager.go` параметризовано. Регресс-тесты: `filter_builder_injection_test.go`.

## Medium
- [ ] **ERROR-REINVENTION** — Свои типы ошибок в каждом модуле вместо `pkg/httperror`. ~0.5d
- [ ] **GLOBAL-LOCK-CONTENTION** — Глобальный мьютекс в Store при высокой нагрузке. Нужно шардирование. ~1d
- [x] ~~**NOTIFICATION-TIMER-STUBS**~~ — закрыто через PARITY-A1 (2026-04-17).
- [x] ~~**INHIBITION-DEAD-WIRING**~~ — закрыто через PARITY-A2 (2026-04-16). `ShouldInhibit` вызывается в `alert_processor.go:154-155`.
- [x] ~~**DEDUP-STATE-STUB**~~ — закрыто 2026-08-17. Rule 7 реализован в `SimpleFilterEngine`: fingerprint+status в окне (default 1m, `SetDedupWindow`), lazy sweep, потокобезопасно. Смена статуса firing→resolved не блокируется. Тесты: `filter_engine_dedup_test.go` (включая -race).
- [ ] **CORS-TODO** — `middleware.go:77` — CORS конфигурация отсутствует, помечена TODO. ~0.5d

## Low
- [x] ~~**DEAD-CODE-MAIN-FULL**~~ — Legacy `main.go.full` удалён. Закрыто 2026-03-08.
- [ ] **SIMPLE-PUBLISHER-PANIC** — `core/services/publisher.go` — `SimplePublisher` паникует в prod (fail-safe). Реальные publishers в `infrastructure/publishing/`. Сам stub безвреден (panics предотвращают silent data loss), но должен быть удалён после полной интеграции. ~0.5d
