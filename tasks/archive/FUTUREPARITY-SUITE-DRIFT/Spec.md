# FUTUREPARITY-SUITE-DRIFT - Spec

**Status**: Implemented for targeted compile/smoke gates  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `dedicated futureparity compatibility harness with compile-first verification and explicit separation from active runtime`
**Implementation Outcome**: single build-tagged compatibility owner landed in `go-app/cmd/server/futureparity_compat.go`, paired with tagged harness smoke tests; full `go test ./cmd/server -tags=futureparity` remains diagnostic-only and is tracked as a separate residual historical/runtime gap.

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/BACKLOG.md`
- `docs/06-planning/DECISIONS.md`
- `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`

---

## 1. Problem Statement

После `ALERTMANAGER-REPLACEMENT-SCOPE` historical wide-surface suites в `go-app/cmd/server` были вынесены под build tag `futureparity`, чтобы перестать определять default active-runtime contract. Это разорвало старые bootstrap seams:

- `runtimeStateFileEnv`
- `registerRoutes`
- `configSHA256`
- и, вероятно, следующими всплывут `runtimeClusterListenAddressEnv`, `runtimeClusterAdvertiseAddressEnv`, `runtimeClusterNameEnv`

Но проблема не ограничивается missing helper names. Current active runtime в `go-app/internal/application/router.go` сознательно монтирует только узкий surface, тогда как historical `futureparity` suite по-прежнему ожидает широкий compatibility layer (`/api/v2/status`, `/-/reload`, `/api/v2/config`, `/history`, `receivers`, `alerts/groups` и др.).

Из-за этого у проекта сейчас нет поддерживаемого opt-in verification path для historical parity:

1. suite не компилируется;
2. compile drift скрывает более глубокий runtime drift;
3. нельзя просто вернуть старые helpers в `main.go`, потому что это размоет active-runtime-first truth, закрепленный в `docs/06-planning/DECISIONS.md`.

Цель этого spec: восстановить **поддерживаемый compatibility harness** для `futureparity`, не превращая задачу в скрытое `RUNTIME-SURFACE-RESTORATION`.

---

## 2. Goals

1. Вернуть `futureparity` suite поддерживаемый owner для helper/env/bootstrap layer без изменений production `main.go`.
2. Зафиксировать, что `futureparity` проверяет **historical/backlog compatibility surface**, а не current active runtime contract.
3. Сделать compile-level verification для `futureparity` воспроизводимым и green.
4. Добавить небольшой targeted smoke path для самого compatibility harness, чтобы задача не свелась к голой компиляции без смысла.
5. Явно отделить residual runtime gaps от helper drift, чтобы они либо оставались в planning artifacts, либо переезжали в отдельный follow-up, а не маскировались под “suite fixed”.

---

## 3. Non-Goals

1. Не возвращать широкий historical surface в active `go-app/internal/application/router.go` или current `go-app/cmd/server/main.go`.
2. Не делать в этом slice весь `go test ./cmd/server -tags=futureparity` green по полному выполнению всех historical tests.
3. Не закрывать `REPO-TEST-MATRIX-RED`.
4. Не переписывать массово `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go` в новый формат, если можно сохранить их через совместимый harness seam.
5. Не протаскивать historical test helpers обратно в production-owned код без build tag.
6. Не менять публичный replacement claim; source of truth остается active-runtime-first.

---

## 4. Key Decisions

### 4.1 `futureparity` Remains Opt-In Historical Compatibility, Not Active Runtime

Current active runtime contract продолжает жить отдельно и по-прежнему закрепляется:

- non-tagged `cmd/server` path;
- `go-app/internal/application/router_contract_test.go`;
- `docs/06-planning/DECISIONS.md` / ADR-002.

`futureparity` не отменяет и не расширяет этот contract. Он существует как отдельный historical/backlog verification layer.

### 4.2 Missing Helpers Move Into A Build-Tagged Compatibility Owner

Symbols, от которых зависит historical suite, должны жить в отдельном build-tagged owner-е внутри `go-app/cmd/server`, а не в production `main.go`.

Минимальный набор ownership:

- env aliases для state/config/cluster knobs, которые нужны historical tests;
- `configSHA256(...)` или его явно зафиксированный эквивалент;
- `registerRoutes(mux)` как historical compatibility entrypoint для tests.

Это может быть один или несколько файлов под `//go:build futureparity`.

### 4.3 `registerRoutes` In `futureparity` Is A Compatibility Harness Entry, Not A Production Contract

Historical tests уже завязаны на `newPhase0TestMux -> registerRoutes(mux)`. Чтобы держать diff узким, этот seam сохраняется, но его owner меняется:

- production path продолжает использовать `application.NewRouter(registry).SetupRoutes(mux)` и `registerLegacyDashboardRoutes(...)`;
- build-tagged `registerRoutes(mux)` становится adapter/harness layer только для `futureparity`.

Если внутри harness нужен richer internal API, он должен прятаться за internal helper functions, а не менять сигнатуру старых tests.

### 4.4 Compile-First Green Is Mandatory, Full Historical Execution Is Not

Для этого mergeable slice обязательный outcome:

- `futureparity` path компилируется;
- есть небольшой targeted smoke path поверх harness.

Но full execution всего historical suite не является обязательным outcome этого slice, потому что это уже упирается в реальные missing runtime surfaces и тянет на `RUNTIME-SURFACE-RESTORATION`.

### 4.5 Unsupported Wide-Surface Expectations Must Stay Explicit

Если после восстановления harness часть historical tests все еще красная из-за реально отсутствующих endpoints/behaviors, это должно быть явно классифицировано как:

- intentional non-active surface;
- runtime-restoration follow-up;
- или отдельный residual bug/planning note.

Недопустимо:

- подделывать ответы “заглушками успеха” только ради зеленых тестов;
- молча расширять active runtime;
- объявлять `futureparity fixed`, если реально закрыт только compile drift.

### 4.6 Test-Facing Hash Logic Must Not Depend On Production `main.go`

Для `configSHA256` есть существующие реализационные эквиваленты в `internal/config`, но `futureparity` не должен тянуть test-facing hash helper через production `main.go`.

Допустимы два пути:

1. локальный build-tagged helper в compatibility harness;
2. узкий shared helper вне `main.go`, если он действительно reusable и не вносит production-only drift.

Предпочтение для этого slice: build-tagged local helper, если shared extraction не дает явной ценности.

---

## 5. Scope Model

### 5.1 In Scope

- build-tagged compatibility owner для historical env/helper symbols;
- compatibility implementation для `registerRoutes(mux)` в `futureparity` path;
- compile-level verification path для `futureparity`;
- 1-2 targeted harness smoke tests;
- task/planning updates, если по итогам реализации residual runtime gaps нужно явно оставить открытыми.

### 5.2 Out Of Scope

- возврат `status`, `receivers`, `alerts/groups`, `/-/reload`, `config*`, `history*`, `classification*` в active runtime;
- green full historical suite без явного восстановления runtime surface;
- migration default tests на новый ownership beyond minimal harness support;
- repository-wide quality gate cleanup.

---

## 6. Target Architecture

```text
go-app/cmd/server/main.go
  -> production owner, unchanged
  -> application.NewRouter(...).SetupRoutes(...)
  -> registerLegacyDashboardRoutes(...)

go-app/cmd/server/*futureparity*.go (build tag: futureparity)
  -> historical env aliases
  -> configSHA256 helper
  -> registerRoutes(mux) compatibility entrypoint
  -> internal compatibility builder/helpers

historical tests
  -> newPhase0TestMux(...)
  -> registerRoutes(mux)
  -> compile against futureparity compatibility owner

active runtime tests
  -> remain separate and non-tagged
```

Ключевая идея:

- production path не меняется owner-ship-wise;
- historical tests получают свой explicit compatibility seam;
- `futureparity` становится отдельным harness, а не неявной тенью production bootstrap.

---

## 7. Component Design

### 7.1 Build-Tagged Compatibility Layer

Нужен отдельный build-tagged слой в `go-app/cmd/server`, который:

- объявляет missing env constants;
- объявляет `configSHA256`;
- предоставляет `registerRoutes(mux)` с прежней сигнатурой.

Этот слой может использовать внутренние helper functions наподобие:

- `buildFutureParityRegistryFromEnv()`
- `setupFutureParityRoutes(mux, registry)`
- `mustRegisterFutureParityRoutes(mux)`

Названия не фиксированы, важен ownership boundary.

### 7.2 Compatibility Route Policy

`registerRoutes(mux)` в harness может собирать mux из:

- current reusable active router pieces;
- legacy dashboard routes, если они реально нужны historical tests;
- compatibility-only route wiring для тех historical expectations, где в репозитории еще есть реальный code path.

Но harness не должен:

- invent fake-success handlers;
- silently claim unsupported endpoints as active;
- протаскивать massive dormant subsystem без явной необходимости.

### 7.3 Targeted Smoke Tests

Чтобы verification не свелся к голому `-run TestDoesNotExist`, slice должен добавить небольшой tagged smoke слой, который проверяет минимум:

1. compatibility harness собирается и может зарегистрировать mux;
2. `configSHA256` детерминирован и совместим по basic behavior;
3. при необходимости один базовый route smoke проходит через harness без паники/boot failure.

Эти smoke tests должны проверять harness layer, а не пытаться закрыть всю historical parity matrix.

### 7.4 Residual Gap Handling

Если после compile green выяснится, что full `futureparity` execution остается red на intentional missing runtime surface, задача должна:

- либо оставить это в `BUGS.md` как открытый residual gap;
- либо явно переформулировать остаток под `RUNTIME-SURFACE-RESTORATION` / equivalent follow-up.

Главное требование: planning truth должен различать “helper drift fixed” и “historical wide surface still absent”.

---

## 8. Verification Model

### 8.1 Mandatory Verification

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run TestDoesNotExist
```

Это базовый gate для закрытия compile drift.

### 8.2 Mandatory Targeted Smoke

После добавления harness smoke tests нужен отдельный targeted run уровня:

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run 'TestFutureParityHarness|TestFutureParityConfigHash'
```

Точные имена тестов могут отличаться, но intent должен остаться:

- один smoke на harness/mux;
- один smoke на hash/helper semantics.

### 8.3 Regression Guard For Active Path

Чтобы не потянуть historical helpers в active runtime contract, нужно сохранить non-tagged compile sanity:

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -run TestDoesNotExist
```

Если touched code заденет `internal/application`, допустим targeted run для active contract tests.

### 8.4 Non-Goal Verification

Full:

```bash
cd go-app
go test ./cmd/server -tags=futureparity
```

не является обязательным acceptance gate этого slice.

Если он остается red, это должно быть описано как residual runtime gap, а не скрыто.

---

## 9. Deliverables

1. `tasks/FUTUREPARITY-SUITE-DRIFT/Spec.md` фиксирует compatibility-harness direction.
2. В `go-app/cmd/server` появляется build-tagged compatibility owner для missing helpers/env symbols.
3. Появляется reproducible compile gate для `futureparity`.
4. Появляется небольшой tagged smoke layer для harness.
5. Planning/task docs отражают, что именно закрыто:
   - helper/harness compile drift;
   - и что остается открытым, если wide-surface runtime expectations еще не поддержаны.

---

## 10. Acceptance Criteria

Слайс считается завершенным, если одновременно выполнены условия:

1. `futureparity` compile gate из `go-app/` проходит.
2. Missing helper/env symbols, блокировавшие компиляцию, больше не берутся из production `main.go`.
3. Есть хотя бы один build-tagged smoke test на compatibility harness и один на helper semantics, и они проходят.
4. Default non-tagged `cmd/server` compile path не сломан.
5. Если full `futureparity` execution все еще red, planning/task artifacts явно объясняют остаток как runtime-surface gap, а не как unresolved helper drift.

---

## 11. Proposed Implementation Direction

### Step 1. Introduce Build-Tagged Compatibility Owner

Добавить build-tagged file(s) в `go-app/cmd/server`, где сосредоточить:

- historical env aliases;
- `configSHA256`;
- thin compatibility entrypoint `registerRoutes(mux)`.

### Step 2. Keep Old Test Seam, Move New Logic Behind It

Не переписывать сразу `newPhase0TestMux(...)` call sites. Вместо этого оставить старый seam и спрятать новые детали в compatibility internals.

### Step 3. Add Narrow Harness Smoke Tests

Добавить отдельные tagged smoke tests, которые:

- не пытаются проходить всю historical matrix;
- но подтверждают, что harness реально поднимается и дает deterministic helper behavior.

### Step 4. Reclassify Residual Gaps Explicitly

После compile/smoke verification зафиксировать:

- что именно теперь green;
- какие runtime expectations still absent;
- какой planning artifact это теперь держит.

---

## 12. Risks

### Risk A: Scope Creep Into Runtime Restoration

Если в ходе реализации начать чинить `status/config/history/reload` как active routes, задача перестанет быть mergeable slice.

### Risk B: Fake Green

Если harness будет строиться на заглушках, которые просто имитируют успех, futureparity потеряет ценность как verification layer.

### Risk C: Incomplete Residual Documentation

Если после compile fix не задокументировать remaining runtime gaps, planning снова станет двусмысленным.

### Risk D: Ownership Leakage Back Into Production

Если helper/env compatibility symbols вернутся в non-tagged production files, repo снова смешает active runtime и historical parity concerns.

---

## 13. Open Questions For Implementation

1. Достаточно ли build-tagged local helper для `configSHA256`, или extraction в shared helper даст более чистый diff?
2. Нужны ли compatibility-only routes сверх current active router уже в этом slice, или compile + harness smoke можно закрыть без них?
3. Останется ли residual red full suite под текущим bug `FUTUREPARITY-SUITE-DRIFT`, или его стоит после implementation разрезать на helper drift и runtime-surface gap?

## 14. Post-Implementation Notes

1. `configSHA256` оставлен локальным build-tagged helper-ом; shared extraction не дала бы более чистый diff.
2. Для mergeable slice хватило current active router pieces + legacy dashboard routes; compatibility-only wide-surface handlers сознательно не возвращались.
3. Residual red full suite после implementation разрезан в planning truth на закрытый helper/harness drift и отдельный `FUTUREPARITY-HISTORICAL-RUNTIME-GAP`.
