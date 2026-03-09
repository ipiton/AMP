# Research: PHASE-3-STORAGE-HARDENING

Дата: 2026-03-08

## Scope
Исследование перед `/spec` для hardening active bootstrap path в `go-app/internal/application/service_registry.go`:

- real storage initialization вместо `nil` placeholder;
- policy для migrations/bootstrap order;
- health decomposition для storage/runtime state;
- startup/shutdown semantics по профилям `lite` и `standard`.

## Executive Summary

Главный вывод: **проблема не в отсутствии storage-кода как такового, а в отсутствии одного согласованного bootstrap-контракта**.

Сейчас active runtime живет сразу в нескольких несовместимых слоях:

- `ServiceRegistry` поднимает `*postgres.PostgresPool`, но storage оставляет `nil`;
- `internal/infrastructure/sqlite_adapter.go` и `internal/infrastructure/postgres_adapter.go` уже умеют `Connect/Health/MigrateUp` и реализуют CRUD/Classification/Publishing storage;
- public health endpoints в `internal/application/handlers/status.go` вообще не используют состояние registry и всегда отвечают `200 OK`.

Самый безопасный direction для `/spec`:

1. сохранить `ProfileStandard` на canonical Postgres path через `internal/database/postgres` + goose migrations;
2. reuse `SQLiteDatabase` как canonical embedded storage path для `ProfileLite`;
3. ввести application-level lifecycle-aware storage contract вместо попытки расширять `core.AlertStorage`;
4. перестать маскировать required storage failures под условно healthy runtime;
5. разделить liveness/readiness semantics, иначе `health decomposition` останется внутренней и не станет observable.

## Current Runtime State

### 1. Active runtime не использует `Application`, а идет через `ServiceRegistry` + `Router`
- `go-app/cmd/server/main.go` создает `ServiceRegistry`, затем `application.NewRouter(registry)` и монтирует routes напрямую.
- Значит, текущий observable contract задают:
  - `go-app/internal/application/service_registry.go`
  - `go-app/internal/application/router.go`
  - `go-app/internal/application/handlers/*.go`

### 2. Active HTTP surface опирается на memory stores и статические health handlers
- `AlertsHandler` и `SilencesHandler` работают через:
  - `registry.AlertStore()`
  - `registry.SilenceStore()`
- Эти stores — in-memory compatibility stores из `internal/infrastructure/storage/memory`.
- `/health`, `/ready`, `/healthz`, `/readyz`, `/-/healthy`, `/-/ready` в `internal/application/handlers/status.go` всегда отвечают success и не проверяют ни database, ни storage, ни migrations.

Вывод:
- отсутствие `r.storage` сейчас не ломает базовый alert/silence API;
- но ломает truthfulness bootstrap/dependency model и отключает real persistence-dependent pieces.

### 3. `ServiceRegistry` не honors storage contract, уже зафиксированный в config
- `go-app/internal/config/config.go` явно задает:
  - `ProfileLite` => `storage.backend=filesystem`
  - `ProfileStandard` => `storage.backend=postgres`
- Но в `ServiceRegistry`:
  - `initializeDatabase()` подключает только PostgreSQL pool для non-lite;
  - `initializeStorage()` оставляет `r.storage = nil`;
  - `initializeDeduplication()` из-за этого уходит в graceful degradation;
  - `initializeClassification()` получает `Storage: r.storage`, то есть persistence layer для classification path тоже фактически отсутствует.

## Existing Building Blocks

### 1. Config contract уже достаточно строгий
`go-app/internal/config/config.go` уже фиксирует:

- профили `lite` и `standard`;
- required storage backend по профилю;
- `storage.filesystem_path` для embedded path;
- `UsesEmbeddedStorage()` и `UsesPostgresStorage()` как готовые selectors для bootstrap logic.

Это означает, что `/spec` не должен заново изобретать profile model; он должен заставить runtime реально соблюдать уже существующий contract.

### 2. В репозитории есть реальный SQLite runtime component
`go-app/internal/infrastructure/sqlite_adapter.go` уже реализует:

- `Connect`
- `Disconnect`
- `Health`
- `MigrateUp`
- `AlertStorage`
- `ClassificationStorage`
- `PublishingLogStorage`

Дополнительно на этот path уже есть прямые tests в `go-app/internal/infrastructure/sqlite_adapter_test.go`.

Вывод:
- для `ProfileLite` storage path ближе всего к reusable ready-made implementation.

### 3. В репозитории есть реальный Postgres storage adapter, но он не совпадает с текущим production bootstrap
`go-app/internal/infrastructure/postgres_adapter.go` тоже реализует полный storage/database surface, но:

- сам владеет подключением и pool lifecycle;
- использует отдельный `internal/infrastructure.Config`;
- содержит `MigrateUp()` с явным комментарием `for dev/test`, а не canonical production goose path.

При этом current `ServiceRegistry` и current migration helper опираются на другой слой:

- `go-app/internal/database/postgres.PostgresPool`
- `go-app/internal/database/migrations.go`

Вывод:
- код для CRUD/storage уже есть;
- но active runtime не имеет canonical способа совместить:
  - current PostgresPool lifecycle
  - production migrations
  - real `core.AlertStorage`

### 4. `internal/storage/storage.go` не является usable implementation seam
`go-app/internal/storage/storage.go` — неиспользуемый placeholder package без runtime logic.

Вывод:
- текущий TODO в `initializeStorage()` — это не “осталось просто подключить package storage”;
- нужен явный architectural choice, какой storage layer becomes canonical for bootstrap.

## Findings

### 1. Проблема — в bootstrap contract drift, а не в полном отсутствии реализации
В репозитории уже есть:

- profile/storage config contract;
- Postgres pool с health/lifecycle;
- goose migration helper;
- SQLiteDatabase и PostgresDatabase с CRUD.

Но эти части не склеены в один runtime contract.

Итог:
- `ServiceRegistry` создаёт впечатление real infra bootstrap;
- фактически critical storage dependency остаётся unset.

### 2. Current graceful degradation story для storage не согласована с product/runtime truth
Комментарии в `ServiceRegistry` обещают fallback/degradation для storage path, но:

- нет реального `core.AlertStorage` fallback implementation;
- `memory.AlertStore` не реализует `core.AlertStorage`, это отдельный compat-state store;
- config contract для `ProfileStandard` прямо говорит, что postgres storage required.

Вывод:
- silent fallback для core storage выглядит ложным контрактом;
- для `/spec` нужно принять более жесткое правило: required storage failure не должен маскироваться как success path.

### 3. Observable health сейчас вообще не связан с реальным bootstrap state
Даже если доработать `ServiceRegistry.Health()`:

- public health handlers останутся статическими;
- `cmd/server/main.go` по-прежнему будет отвечать `200 OK`, даже если migrations/storage/init не произошли.

Это означает:
- правка только `ServiceRegistry.Health()` не закрывает user-visible часть задачи;
- `/spec` должен явно определить, какие endpoints и с какой семантикой считаются source of truth.

### 4. Нужна не одна `Health()` проверка, а split на liveness/readiness
Текущая модель слишком грубая:

- `Application.Readiness()` просто проксирует `Health()`;
- active runtime ее вообще не использует;
- static handlers не различают “процесс жив” и “required storage ready”.

Для storage hardening логичнее разделить:

- `liveness`: процесс жив, bootstrap loop не упал фатально;
- `readiness`: required storage initialized, migrations applied/validated, registry ready to serve active contract.

Иначе storage outage либо будет скрыт, либо превратит все probes в один и тот же status.

### 5. `core.AlertStorage` слишком узкий для lifecycle-aware bootstrap
`internal/core.AlertStorage` описывает только CRUD/stats/cleanup.
В нем нет:

- `Health(ctx)`
- `Disconnect/Close`
- `MigrateUp`

Вывод:
- не стоит расширять `core.AlertStorage` ради одного bootstrap slice;
- лучше ввести application-local composite interface или отдельные runtime fields для lifecycle/health.

### 6. Для `ProfileStandard` нельзя просто взять `PostgresDatabase` и назвать это production path
Почему это риск:

- current runtime уже использует `database/postgres.PostgresPool`;
- `database.RunMigrations()` ожидает именно этот слой;
- `PostgresDatabase.MigrateUp()` явно позиционирован как `dev/test` path;
- прямой switch на `PostgresDatabase` либо дублирует pool, либо уводит runtime в другой migration contract.

Вывод:
- для `standard` safer direction — сохранить current canonical DB/migrations path;
- storage CRUD logic лучше извлекать или адаптировать поверх уже подключенного `pgxpool`, а не подменять весь runtime owner.

### 7. `RunMigrations()` нельзя считать drop-in ready без дополнительной фиксации contract
В `go-app/internal/database/migrations.go` есть два скрытых риска:

1. helper открывает `sql.Open("pgx", ...)`, но package graph `internal/database` сам не импортирует `github.com/jackc/pgx/v5/stdlib`;
2. helper использует относительный путь `migrations`, то есть зависит от рабочего каталога процесса/тестов.

Практическое следствие:
- в Docker runtime это может работать, потому что `Dockerfile` копирует `migrations` в `/app/migrations` и запускает binary из `/app`;
- но для package-level tests и произвольного local run этот contract хрупкий.

Вывод:
- `/spec` должен явно решить, как задаётся migration dir и кто гарантирует driver registration.

### 8. Текущий active runtime использует storage только опосредованно
Прямо сейчас `r.storage` нужен не для базового HTTP surface, а для:

- deduplication;
- classification persistence path;
- будущих history/repository integrations.

Вывод:
- задача не про “починить GET/POST /api/v2/alerts”;
- задача про то, чтобы bootstrap/runtime truth перестал расходиться с declared infra contract.

### 9. Прямых bootstrap tests почти нет
В `internal/application` есть tests на:

- router contract;
- classification health endpoint selection;
- publishing adapters.

Но почти нет прямых tests на:

- `ServiceRegistry.Initialize()` matrix;
- profile-specific storage bootstrap;
- storage/readiness failure semantics.

Вывод:
- verification path для этой задачи нужно проектировать отдельно;
- full repo gates уже известны как red вне scope (`docs/06-planning/BUGS.md`).

## Options

### Option A: Application-level storage runtime abstraction; standard stays on PostgresPool + goose

Суть:

- сохранить `database/postgres.PostgresPool` как canonical standard DB connectivity layer;
- сохранить goose через `internal/database/migrations.go` как canonical standard migration path;
- в `internal/application` ввести lifecycle-aware storage contract, например:
  - `core.AlertStorage`
  - `Health(ctx) error`
  - `Shutdown(ctx) error`
- для `lite` reuse `SQLiteDatabase` как runtime storage component;
- для `standard` сделать thin storage adapter поверх уже подключенного `pgxpool`/`PostgresPool`, желательно извлекая CRUD logic из `internal/infrastructure/postgres_adapter.go`, а не открывая второй pool;
- public health/readiness handlers переключить на реальное registry/app state.

Плюсы:

- соответствует уже принятому config contract;
- не ломает current production-oriented Postgres/bootstrap story;
- даёт минимальный и честный vertical slice;
- не требует расширять `core.AlertStorage`.

Минусы:

- нужен новый thin adapter или refactor существующего Postgres CRUD code;
- `/spec` должен чётко зафиксировать readiness semantics.

### Option B: Полностью унифицировать runtime на `internal/infrastructure.Database`

Суть:

- отказаться от `database/postgres.PostgresPool` как отдельного слоя;
- и `lite`, и `standard` вести через `internal/infrastructure/database.go` + adapters.

Плюсы:

- единый storage abstraction;
- меньше branching в bootstrap logic.

Минусы:

- для `standard` это конфликтует с текущим goose/migration direction;
- `PostgresDatabase.MigrateUp()` сейчас выглядит как dev/test schema path, не как production truth;
- выше риск расползания scope в runtime redesign.

### Option C: Оставить placeholder bootstrap, улучшив только health messaging

Суть:

- не подключать real storage;
- оставить `r.storage = nil`;
- поверх этого улучшить docs/health/errors.

Плюсы:

- минимальный diff.

Минусы:

- не закрывает цель задачи;
- сохраняет bootstrap contract drift;
- по сути консервирует технический долг.

## Recommendation

Рекомендую **Option A**.

Это самый прагматичный путь, потому что он:

- уважает уже существующий profile/storage contract;
- не подменяет standard runtime на dev/test migration path;
- позволяет reuse готовый SQLite path;
- локализует изменения в `internal/application` + storage boundary;
- даёт понятный `/spec`, не расползающийся в полную замену database stack.

## Decisions To Freeze In Spec

Следующий `/spec` должен явно зафиксировать:

1. **Standard storage is required**
   - `ProfileStandard` не должен silently стартовать без working Postgres storage.
   - Database connect failure / migration failure / storage init failure должны либо фейлить startup, либо переводить runtime в явно documented non-ready state.
   - Рекомендация: fail startup, а не скрытая деградация.

2. **Lite embedded storage is required**
   - `ProfileLite` должен стартовать только при рабочем embedded storage path.
   - `filesystem_path` failure — это startup error, не pseudo-healthy mode.

3. **Health must be decomposed**
   - liveness и readiness не должны быть одним и тем же.
   - Readiness должна зависеть от required storage init/migrations.
   - Liveness не должна быть unconditional static `OK`, если bootstrap не завершён.

4. **Canonical migration path for standard**
   - production truth для `standard` — goose migrations, а не in-code dev/test schema.
   - Значит migration helper нужно сделать runtime-safe:
     - driver registration;
     - predictable migrations path contract.

5. **Canonical storage owner per profile**
   - `lite`: `SQLiteDatabase` reuse как embedded storage runtime.
   - `standard`: PostgresPool + thin storage adapter over current pool path, а не wholesale switch на `PostgresDatabase`.

## Questions To Resolve In Spec

1. Какие именно endpoints становятся source of truth для health semantics:
   - `/health`, `/ready`
   - `/healthz`, `/readyz`
   - `/-/healthy`, `/-/ready`
   - все перечисленные

2. Что считаем readiness failure для `standard`:
   - database connect failure
   - migration failure
   - storage adapter init failure
   - все три

3. Хотим ли мы fail-fast startup при required storage failure, или допускаем процессный startup с `not ready` состоянием?
   - Рекомендация: fail-fast для required storage.

4. Какой минимальный storage lifecycle contract нужен в `internal/application`:
   - единый composite interface
   - отдельные поля `storage`, `storageHealth`, `storageShutdown`
   - отдельный `StorageRuntime` owner struct

5. Входит ли в scope этого slice обновление router health handlers, или это отдельный sub-step?
   - Рекомендация: входит, иначе задача не даёт observable outcome.

## Proposed Spec Direction

### A. Runtime contract
- `ServiceRegistry` становится владельцем real storage runtime, как уже стал владельцем publishing lifecycle.
- Storage path должен быть profile-aware и lifecycle-aware.

### B. Profile behavior
- `lite`:
  - connect embedded SQLite
  - apply embedded migrations/schema
  - expose storage as runtime dependency
- `standard`:
  - connect PostgresPool
  - run goose migrations
  - create real storage adapter over current pool
  - expose storage as runtime dependency

### C. Health semantics
- readiness проверяет required storage readiness;
- liveness проверяет, что runtime initialized и не находится в fatal bootstrap failure state;
- static handlers заменяются на state-aware handlers.

### D. Verification path
Реалистичный targeted verification для этого slice:

- новые tests на `ServiceRegistry` bootstrap matrix:
  - `lite` happy path;
  - `standard` storage failure behavior;
  - readiness/liveness behavior;
- existing router contract tests с обновлением health expectations;
- targeted build/tests вместо full repo matrix, потому что `REPO-TEST-MATRIX-RED` уже зафиксирован как preexisting blocker.

## Research Outcome

Задача требует `/spec` перед кодом.

Это не “подключить готовый storage package”, а согласовать четыре слоя:

1. profile/storage contract из `internal/config`;
2. standard DB + migrations truth (`database/postgres` + goose);
3. real `core.AlertStorage` owner/lifecycle in `ServiceRegistry`;
4. observable health/readiness contract active runtime.

Без этих решений любая реализация либо оставит `nil`/static-OK drift, либо незаметно расползётся в отдельный database/runtime rewrite.

## Verification Notes

В рамках `/research` новые тесты не запускались.

Выводы основаны на чтении и сопоставлении:

- `go-app/cmd/server/main.go`
- `go-app/internal/application/router.go`
- `go-app/internal/application/handlers/alerts.go`
- `go-app/internal/application/handlers/silences.go`
- `go-app/internal/application/handlers/status.go`
- `go-app/internal/application/service_registry.go`
- `go-app/internal/config/config.go`
- `go-app/internal/database/migrations.go`
- `go-app/internal/database/postgres/pool.go`
- `go-app/internal/infrastructure/sqlite_adapter.go`
- `go-app/internal/infrastructure/postgres_adapter.go`
- `go-app/internal/infrastructure/sqlite_adapter_test.go`
- `go-app/internal/infrastructure/storage/memory/alert_store.go`
- `Dockerfile`
- `docs/06-planning/BUGS.md`
