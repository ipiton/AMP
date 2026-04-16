# Технический долг (TECH-DEBT)

Список архитектурных проблем, требующих исправления. Отсортировано по приоритету (Critical → High → Medium → Low).

## Critical
- [ ] **GOD-OBJECT-MAIN** — `go-app/cmd/server/main.go` (~3900 строк) смешивает логику, инициализацию и роутинг. ~3d
- [ ] **STATE-STORE-LEAK** — `alert_state_store.go` и `silence_state_store.go` находятся в `cmd/server/`. ~1d
- [ ] **SPLIT-BRAIN-RISK** — Отсутствие транзакционной консистентности между In-Memory Store и БД. ~3d

## High
- [ ] **DUPLICATED-DB-ADAPTERS** — `PostgresDatabase` и `SQLiteDatabase` дублируют 80% логики. Нужен Query Builder. ~3d
- [ ] **DTO-FRAGMENTATION** — Избыток структур `apiAlert`, `storedAlert`, `core.Alert`. Нужна консолидация. ~1d
- [ ] **MANUAL-SQL-RISK** — Прямая конкатенация SQL строк при построении фильтров. Риск ошибок и инъекций. ~2d

## Medium
- [ ] **ERROR-REINVENTION** — Свои типы ошибок в каждом модуле вместо `pkg/httperror`. ~0.5d
- [ ] **GLOBAL-LOCK-CONTENTION** — Глобальный мьютекс в Store при высокой нагрузке. Нужно шардирование. ~1d
- [ ] **NOTIFICATION-TIMER-STUBS** — `group_interval` и `repeat_interval` таймеры в `grouping/manager_impl.go` имеют TODO вместо реального triggering нотификаций (lines 804, 825, 870). Первая нотификация (group_wait) работает, повторные — нет. Связано с PARITY-A1. ~included в PARITY-A1
- [ ] **INHIBITION-DEAD-WIRING** — `InhibitionMatcher` полностью реализован (matcher + parser + cache + metrics), но не вызывается в alert processing pipeline. Код написан и протестирован, но не подключён. Связано с PARITY-A2. ~included в PARITY-A2
- [ ] **DEDUP-STATE-STUB** — `filter_engine.go:98-99` — deduplication marked TODO, state tracking не реализован. Dedup-фильтр в FilterEngine заявлен но не работает. ~1d
- [ ] **CORS-TODO** — `middleware.go:77` — CORS конфигурация отсутствует, помечена TODO. ~0.5d

## Low
- [x] ~~**DEAD-CODE-MAIN-FULL**~~ — Legacy `main.go.full` удалён. Закрыто 2026-03-08.
- [ ] **SIMPLE-PUBLISHER-PANIC** — `core/services/publisher.go` — `SimplePublisher` паникует в prod (fail-safe). Реальные publishers в `infrastructure/publishing/`. Сам stub безвреден (panics предотвращают silent data loss), но должен быть удалён после полной интеграции. ~0.5d
