# Технический долг (TECH-DEBT)

Список архитектурных проблем, требующих исправления. Отсортировано по приоритету (Critical → High → Medium → Low).

## Critical
- [x] ~~**GOD-OBJECT-MAIN**~~ — закрыто через PHASE-2 Bootstrap Consolidation. `main.go` теперь ~125 строк, логика в `ServiceRegistry`, `Router`, `handlers`.
- [x] ~~**STATE-STORE-LEAK**~~ — закрыто через PHASE-2. State stores вынесены из `cmd/server/`.
- [x] ~~**SPLIT-BRAIN-RISK**~~ — закрыто 2026-08-17 двумя слайсами (research: `tasks/DEBT-STAGE4-SPLITBRAIN/research.md`). Слайс 1 (алерты): ProcessAlert больше не глотает ошибку персиста — БД-fail валит алерт до записи в память; rehydration firing-алертов на старте. Слайс 2 (silences — реальная находка: БД-путь был мёртв, рестарт терял все silences): DB-first POST/DELETE через postgres-репозиторий (standard), память как read-кэш, rehydration active+pending, ID генерит только БД; gc_worker теперь инвалидирует кэш после ExpireSilences. Lite-профиль: silences memory-only с явным Warn. За скобками: wiring SilenceManager/sync_worker (двойной write-path), grouping/inhibition-потоки (модули не в рантайме).

## High
- [x] ~~**DUPLICATED-DB-ADAPTERS**~~ — закрыто 2026-08-17 удалением, не Query Builder'ом: `postgres_adapter.go` (915 строк) и интерфейс `Database` + `NewDatabase` были мёртвым кодом с 0 prod-вызывателей (Standard живёт на `PostgresStorageAdapter`, Lite — на `SQLiteDatabase`). Удалены также 6 мёртвых методов SQLite (~185 строк). Попутно: sentinel-ошибки выровнены (`core.ErrAlertNotFound` в обоих профилях), закрыта SQL-инъекция label-ключа в sqlite `ListAlerts` (json path теперь параметр), добавлены `rows.Err()`-проверки во все scan-циклы. −~1250 строк.
- [x] ~~**DTO-FRAGMENTATION**~~ — закрыто 2026-08-17. Диагноз тикета был неверен: 4 слоя (domain/ingest-wire/internal-state/API-wire) законны; реальный долг — дублированные конверсии. Сделано: пакет `core/alertconv` (единые ParseAlertTime/NormalizeStatus/CloneStringMap/**Fingerprint**/ToGettableAlert); устранены **два разных алгоритма fingerprint на одном endpoint** (ломали дедуп; теперь полный sha256 c alertname, AM-семантика); `/api/v2/alerts/groups` видит сайленсы (раньше state всегда active); `SilencedBy/InhibitedBy/MutedBy` — всегда `[]`, не null; rehydration без строкового round-trip; удалено мёртвое (`pkg/core/`, `APIAlertGroup`, `ExportForPersistence`, dashboard_alerts handler). −3004/+153 строк.
- [x] ~~**MANUAL-SQL-RISK**~~ — закрыто 2026-08-17. Все фильтры параметризованы (`$N` + args); реальные векторы устранены: `ORDER BY` whitelist в `silencing/filter_builder.go` и `template/repository_crud.go`, JSONB-экранирование через `buildJSONBContainmentFilter`/`json.Marshal`, имя таблицы в `migrations/manager.go` параметризовано. Регресс-тесты: `filter_builder_injection_test.go`.

## Medium
- [ ] **HELM-CHART-GAPS** — найдено при аудите `values-production.yaml` (INF-B, 2026-08-20), не исправлено (слишком большой темплейт-скоуп для values-аудита):
  - `valkey.enabled`/`cache.enabled` НЕ гейтят деплой `templates/redis-statefulset.yaml` — единственный гейт — `profile: standard`. Рабочий воркараунд — `valkey.replicas: 0` (заведён в values-production.yaml), но сами флаги мёртвые.
  - `postgresql.existingSecret` читается `templates/secret.yaml` (другой Secret, `<fullname>-secrets`), но НЕ читается `templates/postgresql-secret.yaml`/хелпером `amp.postgresql.secretName` — реальный DB-пароль (`DATABASE_PASSWORD`, `POSTGRES_PASSWORD` в StatefulSet) всегда берётся из `postgresql.password`, existingSecret туда не долетает.
  - BACKLOG когда-то просил "PostgreSQL cluster (3 instances)" — чарт этого не умеет: `postgresql-statefulset.yaml` — single-primary StatefulSet без репликации/failover. Реальная HA (CloudNativePG/Patroni) не реализована.
  - Найдено ревью fix-round (2026-08-20): базовый `values.yaml` до сих пор хардкодит слабый dev-дефолт для `postgresql.password` (dev/test zero-config install path). Не трогали — задача была про `values-production.yaml`, и требование "без дефолта" сломало бы dev-путь без правки `secret.yaml`'s fallback-логики. `values-production.yaml` явно перекрывает его на `""` (см. её комментарий) — инконсистентность зафиксирована здесь, не исправлена.


- [x] ~~**ERROR-REINVENTION**~~ — закрыто 2026-08-17. Все вызыватели deprecated-шимов в `infrastructure/publishing` мигрированы на `pkg/httperror` (NewHTTPError*/PublishingClassifier/Is*), шимы с 0 ссылок удалены (`rootly_errors.go` целиком, Legacy*-алиасы, `NewWebhookErrorWithType`, поле `Workers`). Остался только живой `ErrorType`-классификатор webhook_client (лейблы метрик не 1:1 с httperror).
- [x] ~~**GLOBAL-LOCK-CONTENTION**~~ — закрыто 2026-08-17 измерением: шардирование не нужно. Бенчмарк `alert_store_bench_test.go` (M1 Pro, 8 потоков): параллельный IngestBatch = 1.4 µs/op (~720K алертов/с) — на порядки выше целевой нагрузки. Дорогая операция — `List()` (O(n)-копия всего стора, 1.2 мс на 5000 алертов), и её шардирование мьютекса не ускоряет. Revisit только при профилировании реальной контенции.
- [x] ~~**NOTIFICATION-TIMER-STUBS**~~ — закрыто через PARITY-A1 (2026-04-17).
- [x] ~~**INHIBITION-DEAD-WIRING**~~ — закрыто через PARITY-A2 (2026-04-16). `ShouldInhibit` вызывается в `alert_processor.go:154-155`.
- [x] ~~**DEDUP-STATE-STUB**~~ — закрыто 2026-08-17. Rule 7 реализован в `SimpleFilterEngine`: fingerprint+status в окне (default 1m, `SetDedupWindow`), lazy sweep, потокобезопасно. Смена статуса firing→resolved не блокируется. Тесты: `filter_engine_dedup_test.go` (включая -race).
- [x] ~~**CORS-TODO**~~ — закрыто 2026-08-17. `server.cors.*` (enabled/origins/methods/headers, default off) + `corsMiddleware` в стеке с preflight 204. Тесты: `middleware_cors_test.go`.

## Low
- [x] ~~**DEAD-CODE-MAIN-FULL**~~ — Legacy `main.go.full` удалён. Закрыто 2026-03-08.
- [x] ~~**SIMPLE-PUBLISHER-PANIC**~~ — закрыто 2026-08-17. `core/services/publisher.go` удалён: ссылок на `SimplePublisher` не осталось (runtime на real publishing path с SERVICE-REGISTRY-STUB-PATH).
