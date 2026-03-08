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

## Low
- [x] ~~**DEAD-CODE-MAIN-FULL**~~ — Legacy `main.go.full` удалён. Закрыто 2026-03-08.
