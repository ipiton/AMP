# SPLIT-BRAIN-RISK — research (2026-08-17)

## Ключевые факты

- «Двойная запись без транзакции» реально только у алертов: `handlers/alerts.go:126` → dedup `SaveAlert` (БД), затем `:134` `store.IngestBatch` (память). Ошибка БД глотается (`alert_processor.go:106-107`) → память полна, БД пустая. Ошибка публикации → алерт в БД, но не в памяти.
- **Silences хуже split-brain: БД-путь мёртв.** Живой API (`handlers/silences.go:93,48`) пишет только в `memory.SilenceStore`. `business/silencing` (manager, sync_worker, gc_worker, postgres-репозиторий) инстанцируется только в тестах. Рестарт = потеря всех silences.
- Rehydration отсутствует: `AlertStore.RestoreFromPersistence` / `SilenceStore.RestoreFromPersistence` не вызываются нигде. После рестарта API отдаёт `[]`, dedup гасит повторные алерты как дубли (БД их помнит).
- Чтение API всегда из памяти; БД читает только dedup (`deduplication.go:226`).
- gc_worker дыра: `ExpireSilences` без инвалидации кэша (до 1 мин истёкшие silences подавляют алерты).
- Grouping/inhibition тоже имеют расхождения (Redis fallback без реплея; best-effort L1/L2), но модули не подключены к рантайму — вне scope.

## Переиспользуемое

- Reconciliation-шаблон: `business/silencing/sync_worker.go:152-197` (fail-safe Rebuild).
- Retry+circuit breaker БД: `internal/database/postgres/retry.go`.
- Полные транзакции: `config/update_storage.go:78-110`, `template/repository_crud.go:52`.
- Listing для rehydration: `postgres_storage_adapter.go:169 ListAlerts`.

## Решение: DB-first + память как read-кэш + rehydration (вариант 2)

### Слайс 1 — алерты (~1д)
1. `alert_processor.go:104-126` — типизировать критическую ошибку персиста, возвращать наверх (не nil).
2. `handlers/alerts.go:126-140` — память только после успешного коммита БД; без 400 после частичной записи.
3. `service_registry.go:213-226` — rehydration `alertStore.RestoreFromPersistence(storage.ListAlerts)` после initializeStorage.
4. Опц.: SaveAlert/UpdateAlert через RetryExecutor.

### Слайс 2 — silences (~1-1.5д), главный риск потери данных
1. `handlers/silences.go` — запись через `silencing.SilenceRepository` (postgres_silence_repository.go:128,408), память как кэш.
2. `service_registry.go` — инстанцировать `NewDefaultSilenceManager`, стартовать sync_worker + gc_worker; решить судьбу дублирующего `cmd/server/handlers/silence.go`.
3. gc_worker: инвалидация кэша после ExpireSilences.
4. Профиль lite (SQLite): нужен SQLite-репозиторий silences или явный отказ (postgres-репо требует pgx).

Outbox отвергнут (нет внешнего консьюмера, DLQ уже покрывает публикацию); один reconciliation-loop не лечит потерю silences (в БД их нет).
