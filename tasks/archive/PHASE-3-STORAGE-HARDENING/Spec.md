# PHASE-3-STORAGE-HARDENING - Spec

**Status**: Implemented v1  
**Date**: 2026-03-08, validated 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `profile-aware storage bootstrap hardening with fail-fast required storage and state-aware health/readiness`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/ROADMAP.md`
- `docs/06-planning/BUGS.md`

---

## 1. Problem Statement

В active runtime `go-app/internal/application/service_registry.go` уже отвечает за bootstrap инфраструктуры, но storage path в нем остается незавершенным:

1. `initializeStorage()` оставляет `r.storage = nil`;
2. PostgreSQL migrations в standard profile помечены как TODO;
3. health endpoints в active router статически отвечают `200 OK` и не отражают реальное состояние bootstrap;
4. downstream services уже зависят от `core.AlertStorage`, но runtime contract не гарантирует, что storage вообще поднят.

Это создает двойной drift:

- между config contract (`lite=filesystem`, `standard=postgres`) и фактическим bootstrap path;
- между observable health/readiness и реальным состоянием storage/migrations.

Цель этого spec: закрыть bootstrap/storage drift минимальным вертикальным slice, не превращая задачу в полный persistence rewrite.

---

## 2. Goals

1. Зафиксировать real storage initialization path для `ProfileLite` и `ProfileStandard`.
2. Убрать `nil` placeholder из active storage bootstrap path.
3. Зафиксировать canonical migration policy по профилям.
4. Сделать health/readiness endpoints state-aware вместо unconditional success.
5. Сохранить diff узким: hardenить bootstrap/runtime truth, а не переписывать весь alert/silence data flow.

---

## 3. Non-Goals

1. Не переводить active `GET/POST /api/v2/alerts` и silence CRUD с in-memory compatibility stores на persistent storage в этом slice.
2. Не переделывать `internal/application/Application` и `HandlerRegistry`; source of truth остается current active runtime через `cmd/server/main.go` + `Router`.
3. Не унифицировать весь runtime на `internal/infrastructure.Database`.
4. Не делать full dashboard/UI refresh под новый health report.
5. Не чинить full repo test matrix вне targeted verification path.
6. Не добавлять новый публичный config contract для migration dir, если можно обойтись internal helper-ом.

---

## 4. Key Decisions

### 4.1 Scope Boundary

Этот slice hardenит **bootstrap truth**, а не runtime persistence semantics всех handlers.

Остается в силе:

- active alert/silence handlers продолжают использовать `memory.AlertStore` и `memory.SilenceStore`;
- storage hardening в этой задаче нужен для:
  - deduplication path
  - classification persistence path
  - future repository/history integrations
  - truthful bootstrap and health contract

Причина:
- это позволяет сделать mergeable slice без расползания в полную смену active API storage model.

### 4.2 Canonical Storage Owner By Profile

#### `ProfileLite`

Canonical runtime storage owner: `internal/infrastructure.SQLiteDatabase`.

Он уже реализует:

- connect/disconnect
- health
- `MigrateUp`
- `core.AlertStorage`
- classification/publishing persistence methods

Решение:
- `ServiceRegistry` должен использовать `SQLiteDatabase` как real embedded storage runtime для `lite`.

#### `ProfileStandard`

Canonical DB connectivity owner остается `internal/database/postgres.PostgresPool`.

Canonical migration path остается goose через `internal/database/migrations.go`.

Решение:
- `ServiceRegistry` не переключается wholesale на `PostgresDatabase`;
- для storage CRUD используется thin standard-storage adapter, который работает поверх уже подключенного Postgres pool и **не открывает второй pool**.

Причина:
- это сохраняет current production-oriented Postgres/bootstrap truth;
- не уводит runtime на `PostgresDatabase.MigrateUp()`, который сейчас явно позиционирован как dev/test path.

### 4.3 Required Storage Is Fail-Fast

Required storage failures не должны silently деградировать в pseudo-healthy runtime.

Фиксируем:

- `ProfileLite`: failure открыть embedded DB или выполнить schema/migration init => `ServiceRegistry.Initialize()` возвращает error;
- `ProfileStandard`: DB connect failure, migration failure или standard storage adapter init failure => `ServiceRegistry.Initialize()` возвращает error.

Итог:
- сервер не должен стартовать в режиме “required storage отсутствует, но health = OK”.

### 4.4 Degraded Runtime Remains Only For Optional Components

Graceful degradation сохраняется только для non-critical components:

- cache fallback;
- optional classification path;
- optional publishing/internal aux services, если они уже были non-fatal.

Но для этого slice:

- core storage больше не считается optional dependency;
- degraded state должен быть observable в health report, а не только в логах.

### 4.5 Canonical Migration Policy

#### `ProfileStandard`

Canonical path:

- connect `PostgresPool`
- run goose migrations
- only after successful migrations expose standard storage runtime

#### `ProfileLite`

Canonical path:

- create/connect `SQLiteDatabase`
- run `SQLiteDatabase.MigrateUp()`
- only after successful migration expose storage runtime

#### Explicit non-goal

- rollback/down migrations не входят в bootstrap slice;
- bootstrap отвечает только за up/init path.

### 4.6 Health And Readiness Contract

Health semantics разделяются явно:

- `liveness`: процесс жив, registry инициализирован, fatal bootstrap failure не зафиксирован;
- `readiness`: required runtime dependencies готовы обслуживать active contract.

Source-of-truth mapping:

- `/health` и `/healthz` -> liveness JSON
- `/ready` и `/readyz` -> readiness JSON
- `/-/healthy` -> Alertmanager-compatible liveness
- `/-/ready` -> Alertmanager-compatible readiness

Статические unconditional handlers должны уйти из active path.

### 4.7 Observable Health Must Include Component Checks

Одних status codes недостаточно для диагностики bootstrap drift.

Минимальный contract JSON health/readiness response:

- top-level status
- per-component checks минимум для:
  - `bootstrap`
  - `database` (только когда профиль требует Postgres)
  - `storage`
- optional degraded reasons для non-fatal components

Формат может быть компактным, но должен позволять отличить:

- required storage ready
- database unhealthy
- runtime degraded из-за optional fallback

### 4.8 No `core.AlertStorage` Interface Expansion In This Slice

`core.AlertStorage` остается узким CRUD interface.

Вместо его расширения вводится application-local lifecycle-aware boundary, например:

```go
type storageRuntime interface {
    core.AlertStorage
    Health(ctx context.Context) error
    Disconnect(ctx context.Context) error
}
```

Причина:
- это закрывает bootstrap/health needs без каскадного влияния на весь доменный слой.

---

## 5. Target Architecture

```text
ProfileLite:
  Config(lite/filesystem)
    -> SQLiteDatabase.Connect
    -> SQLiteDatabase.MigrateUp
    -> storageRuntime
    -> ServiceRegistry.storage

ProfileStandard:
  Config(standard/postgres)
    -> PostgresPool.Connect
    -> RunMigrations(goose)
    -> StandardStorageAdapter(existing pool)
    -> storageRuntime
    -> ServiceRegistry.storage

Health Plane:
  ServiceRegistry
    -> Liveness(ctx)
    -> Readiness(ctx)
    -> HealthReport(ctx)
  Router handlers
    -> JSON /health|/healthz|/ready|/readyz
    -> plain text /-/healthy|/-/ready
```

---

## 6. Component Design

### 6.1 `ServiceRegistry` Changes

`ServiceRegistry` остается owner-ом bootstrap lifecycle и получает явный storage runtime state.

Новые runtime responsibilities:

1. создать storage runtime по профилю;
2. прогнать migration/init path до экспонирования `r.storage`;
3. хранить lifecycle-aware storage owner отдельно от узкого `core.AlertStorage`;
4. накапливать non-fatal degraded reasons;
5. отдавать liveness/readiness/health report для handlers.

Минимальные новые поля:

- `storageRuntime storageRuntime`
- `degradedReasons []string` или typed equivalent
- `initialized bool` использовать как часть liveness contract

Допустимо сохранить `database *postgres.PostgresPool` как standard-only field.

### 6.2 Standard Storage Adapter

Для `standard` нужен adapter, который:

- работает поверх уже созданного `*pgxpool.Pool` или `*postgres.PostgresPool`;
- реализует `core.AlertStorage`;
- умеет `Health(ctx)` и no-op/explicit `Disconnect(ctx)` в рамках application-local storage runtime;
- не создает новый DB pool.

Implementation note:
- если для этого потребуется вынести общие CRUD helpers из `internal/infrastructure/postgres_adapter.go`, это допустимо;
- но spec не требует wholesale migration runtime на `PostgresDatabase`.

### 6.3 Lite Storage Runtime

Для `lite` `SQLiteDatabase` используется как storage runtime напрямую.

Ожидаемое поведение:

1. path берется из `config.Storage.FilesystemPath`;
2. `Connect()` обязателен;
3. `MigrateUp()` обязателен;
4. после успешного init объект назначается и в `storageRuntime`, и в `r.storage`.

### 6.4 Migration Helper Hardening

`internal/database/migrations.go` должен стать runtime-safe для active bootstrap.

Минимальные изменения по contract:

1. гарантированная регистрация `pgx` driver внутри package graph migration helper-а;
2. предсказуемый поиск migration dir для двух реальных execution contexts:
   - запуск внутри `go-app` / Docker image, где каталог называется `migrations`;
   - запуск из repo root/tests, где каталог лежит в `go-app/migrations`.

Предпочтительный путь:
- internal helper c ordered candidate paths;
- без нового public config key в этом slice.

### 6.5 Health Handlers And Router Wiring

Current `handlers.HealthHandler`/`ReadyHandler` должны перестать быть static helpers без registry state.

Новый minimal handler contract:

- handlers получают registry/provider;
- вызывают `Liveness`, `Readiness` или `HealthReport`;
- возвращают корректный status code;
- JSON endpoints отдают body с component checks;
- Alertmanager-compatible endpoints сохраняют короткий text contract.

Совместимость:

- route set не меняется;
- меняется только truthfulness response behavior.

### 6.6 Degraded State Tracking

Все non-fatal bootstrap degradations, которые уже были допустимы, должны записываться в state, а не только логироваться.

Минимум:

- cache fallback;
- classification unavailable;
- другие existing non-fatal bootstrap downgrades, если они остаются в active path.

Эти причины:

- не делают runtime unready, если required storage checks pass;
- но должны отражаться в JSON health response top-level status как `degraded` или equivalent.

---

## 7. Runtime Behavior Matrix

| Profile / Condition | Startup Result | Liveness | Readiness |
|---|---|---|---|
| `lite` + SQLite connect/migrate OK | starts | 200 | 200 |
| `lite` + SQLite init failure | startup error | no server | no server |
| `standard` + Postgres connect/migrate/storage OK | starts | 200 | 200 |
| `standard` + Postgres connect failure | startup error | no server | no server |
| `standard` + goose migration failure | startup error | no server | no server |
| `standard` + storage adapter init failure | startup error | no server | no server |
| any started profile + optional component degraded | starts | 200 with degraded body | 200 with degraded body |
| any started profile + runtime loses required storage health | stays running | 200 | 503 |

Последняя строка нужна для post-start observability: readiness должен отличать “процесс жив” от “required storage больше не ready”.

---

## 8. Deliverables

### 8.1 Bootstrap Hardening

- profile-aware real storage init path в `ServiceRegistry`
- `r.storage` больше не `nil` после успешного bootstrap
- явный storage runtime owner с lifecycle/health methods

### 8.2 Migration Hardening

- standard startup действительно выполняет goose migrations
- lite startup действительно выполняет embedded schema init
- migration helper больше не зависит от случайного cwd в неочевидной форме

### 8.3 Observable Health

- active router health endpoints используют registry state
- JSON health/readiness reports различают required и degraded states
- plain-text compatibility endpoints следуют той же логике по status code

### 8.4 Targeted Verification

- tests на bootstrap matrix
- tests на health/readiness handler behavior
- `go build ./cmd/server`

---

## 9. Acceptance Criteria

Слайс считается завершенным, если одновременно выполнены условия:

1. `ServiceRegistry` больше не завершает успешный bootstrap с `r.storage == nil`.
2. `ProfileLite` использует реальный embedded storage runtime и валится на startup при его init failure.
3. `ProfileStandard` использует current Postgres pool + goose migrations + real storage adapter без второго connection pool.
4. Failure required storage path не маскируется под healthy runtime ни в startup, ни в readiness endpoints.
5. `/health`, `/healthz`, `/ready`, `/readyz`, `/-/healthy`, `/-/ready` больше не являются unconditional success handlers.
6. JSON health/readiness responses содержат минимум `bootstrap`, `storage` и, для standard profile, `database` checks.
7. Non-fatal degraded bootstrap states отражаются в runtime state/report, а не только в логах.
8. Добавлен targeted verification path:
   - bootstrap/profile tests
   - health/readiness handler tests
   - `go build ./cmd/server`

---

## 10. Risks

### Risk A: Standard storage adapter потянет слишком много refactor-а

Если reuse кода из `internal/infrastructure/postgres_adapter.go` окажется неудобным, можно случайно уйти в большой storage rewrite.

Mitigation:
- жестко держать правило `no second pool`;
- извлекать только минимально нужные CRUD helpers;
- не менять ownership стандартного DB lifecycle.

### Risk B: Health body contract станет несовместим с историческими ожиданиями

JSON body health endpoints изменится по содержанию.

Mitigation:
- route paths и основные status codes сохраняются;
- Alertmanager-compatible endpoints остаются короткими и plain text.

### Risk C: Migration path останется хрупким из-за execution context

Если helper останется зависим от cwd, tests и локальные запуски будут флакать.

Mitigation:
- internal deterministic path resolver;
- targeted tests на candidate resolution, если diff остается компактным.

### Risk D: Persistence truth все еще будет расходиться с active alert/silence handlers

Даже после этого slice активные handlers еще не начнут читать/писать через real storage runtime.

Mitigation:
- это explicit non-goal текущего slice;
- spec фиксирует это как допустимую границу, а не скрытый долг.

---

## 11. Explicit Follow-Ups (Out Of Scope For This Slice)

1. Перевести active alert query/ingest path с compatibility memory stores на persistent storage.
2. Выровнять `Application`/`HandlerRegistry` path с active `cmd/server/main.go`.
3. Поднять dashboard/UI на новый runtime health report.
4. Расширить storage-backed history/config/query surfaces, если это потребуется отдельными задачами.

---

## 12. Verification Strategy

Из-за известного `REPO-TEST-MATRIX-RED` verification для этого slice таргетированный.

Минимальный expected gate:

1. targeted unit/integration tests для `ServiceRegistry` bootstrap behavior;
2. targeted router/handler tests для health/readiness semantics;
3. `go build ./cmd/server`;
4. `git diff --check`.

Full `go test ./...` и full repo gates остаются вне обязательного acceptance path, если продолжают падать на preexisting проблемах, уже зафиксированных в `docs/06-planning/BUGS.md`.

---

## 13. Implementation Result

Реализация соответствует выбранному направлению без расширения scope в полный persistence rewrite:

1. `ServiceRegistry` получил application-local `storageRuntime`, fail-fast storage bootstrap и state-aware liveness/readiness reporting.
2. `ProfileLite` использует `SQLiteDatabase` как canonical runtime owner для embedded storage.
3. `ProfileStandard` использует existing `PostgresPool`, запускает goose migrations и подключает thin adapter поверх текущего pool.
4. Health plane теперь различает required failures и optional degraded state:
   - `/health`, `/healthz` -> liveness JSON
   - `/ready`, `/readyz` -> readiness JSON
   - `/-/healthy`, `/-/ready` -> Alertmanager-compatible plain-text endpoints
5. Active alert/silence handlers намеренно остались на compatibility memory stores; это осталось explicit non-goal текущего slice.
