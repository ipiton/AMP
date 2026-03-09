# APPLICATION-ROUTER-CONTRACT-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `refresh active internal/application router contract tests by splitting restored operational surface from still-absent historical surface, with a minimal reload-capable test registry`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `tasks/archive/RUNTIME-SURFACE-RESTORATION/requirements.md`
- `tasks/archive/RUNTIME-SURFACE-RESTORATION/Spec.md`

---

## 1. Problem Statement

После `RUNTIME-SURFACE-RESTORATION` active router в `go-app/internal/application/router.go` снова монтирует:

- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET /api/v2/alerts/groups`
- `POST /-/reload`

Но `go-app/internal/application/router_contract_test.go` по-прежнему держит старую модель active runtime:

- `TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent` ожидает `404` для этих четырех endpoints;
- helper `newActiveContractMux(...)` не моделирует reload-capable runtime state и поэтому для `/-/reload` дает `500` с `reload coordinator not initialized`;
- имя теста смешивает две разные идеи:
  - operational endpoints, которые уже восстановлены и должны считаться частью active runtime;
  - действительно неактивный historical/wider surface вроде `/api/v2/config`, `/history`, deprecated `v1` alias и classification API.

В результате сейчас сломан не production router, а его internal contract-test layer.

Задача этого spec: вернуть `internal/application` тестам согласованность с текущим active runtime, не превращая работу в новый parity pass и не расширяя production surface.

---

## 2. Goals

1. Обновить active router contract tests так, чтобы restored operational endpoints были явно зафиксированы как **present**.
2. Сохранить отдельную проверку для действительно отсутствующего wider/historical surface.
3. Сделать reload-related contract path честным для active runtime, без случайного `reload coordinator not initialized` в success expectations.
4. Сохранить четкую границу между:
   - active runtime contract в `internal/application`;
   - historical compatibility layers (`futureparity`, `cmd/server`).

---

## 3. Non-Goals

1. Не возвращать новые endpoints в production router сверх уже смонтированных.
2. Не менять public replacement claim и planning truth beyond already accepted restoration.
3. Не чинить `futureparity` suite и не переносить active-router expectations в `cmd/server`.
4. Не делать full repo cleanup или unrelated test stabilization за пределами `internal/application`.
5. Не перестраивать `ServiceRegistry.Initialize(...)` в test path, если для contract tests достаточно узкого fake/minimal state.

---

## 4. Key Decisions

### 4.1 Active Router Contract Must Match The Mounted Surface

Source of truth для этой задачи:

- `go-app/internal/application/router.go`
- `go-app/internal/application/handlers/status_api.go`
- `go-app/internal/application/handlers/alerts.go`

Если endpoint смонтирован в active router, `router_contract_test.go` не должен продолжать описывать его как absent только потому, что historical wording осталось от предыдущего slice.

### 4.2 Restored Operational Endpoints Are Part Of The Current Active Contract

Для `internal/application` current active contract теперь включает:

- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET /api/v2/alerts/groups`
- `POST /-/reload`

Это не означает broad historical parity. Это означает только, что данные route-ы теперь являются частью active runtime и должны быть отражены в active contract tests.

### 4.3 Still-Absent Historical Surface Should Stay Explicit

Даже после фикса current absent surface должен оставаться отдельно зафиксированным:

- `/api/v1/alerts`
- `/api/v2/config`
- `/history`
- `/api/v2/classification/health`
- и аналогичные явно неактивные endpoints

Иначе тесты потеряют ценность как защита от неявного расширения runtime scope.

### 4.4 Reload In Contract Tests Needs Minimal Honest State, Not Full Bootstrap

`newActiveContractMux(...)` должен моделировать только то, что реально нужно для contract-level assertions:

- deterministic `startTime`
- reload-capable path для ожидаемого `200` и/или `500`

Предпочтительный подход:

- узкий test seam внутри `router_contract_test.go`
- minimal fake reload behavior

Непредпочтительный подход:

- тащить туда полный production bootstrap через `Initialize(...)`
- делать тест зависимым от реальных config files, migrations или external infra

### 4.5 Test Naming Must Reflect Current Responsibilities

Текущее имя `TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent` устарело.

Предпочтительное разбиение:

- `TestActiveRuntimeContract_RestoredOperationalEndpointsPresent`
- `TestActiveRuntimeContract_StillAbsentHistoricalSurface`

Это делает intent явным и уменьшает шанс повторного смешения active truth с historical expectations.

---

## 5. Scope Model

### 5.1 In Scope

- `go-app/internal/application/router_contract_test.go`
- local test helper changes, нужные для start time / reload behavior
- уточнение method/status expectations для restored routes
- targeted verification path для `internal/application`

### 5.2 Out Of Scope

- `go-app/cmd/server/*`
- `futureparity`
- public docs rewrite
- broader stabilization work в `internal/business/publishing`, `internal/infrastructure/repository` и других пакетах

---

## 6. Proposed Implementation

### 6.1 Refresh The Contract Test Split

Перестроить current test layout так, чтобы он явно делил active contract на две группы:

1. **present endpoints**
   - existing core alert/silence/health routes
   - restored `status` / `receivers` / `alerts/groups` / `reload`
2. **still absent surface**
   - config/history/classification/v1 alias и другие неактивные endpoints

### 6.2 Extend The Test Registry With Minimal Operational State

`newActiveContractMux(...)` должен получать достаточно состояния, чтобы restored handlers вели себя как честный active runtime:

- `startTime` не должен быть zero-value по умолчанию;
- reload path не должен проваливаться только из-за отсутствия test seam.

Допустимые варианты:

1. добавить в helper test-only fake reload function / state;
2. или вручную задать minimal reload-capable field(s) на registry.

Предпочтение:

- самый маленький diff, который дает детерминированный contract.

### 6.3 Keep Handler-Level Truth As A Lower Layer, Not A Replacement

Handler tests в `go-app/internal/application/handlers/status_api_test.go` и `groups_test.go` уже покрывают локальное поведение.

`router_contract_test.go` не должен дублировать всю их глубину. Его задача:

- подтвердить mounted routes;
- подтвердить method/status contract;
- подтвердить, что active runtime и still-absent surface разделены корректно.

---

## 7. Verification Model

### 7.1 Mandatory Verification

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -count=1
```

### 7.2 Optional Narrow Reproduction While Iterating

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -run TestActiveRuntimeContract -count=1
```

### 7.3 Guardrail

Если для green-пути потребуется менять production router или расширять mounted surface beyond current code, задача должна остановиться и вернуться к planning, потому что это уже выход из scope данного bugfix.

---

## 8. Risks

### 8.1 Overfitting The Contract Test To Current Handler Internals

Если тест начнет проверять слишком много details response body, он станет хрупким дублем handler tests.

Mitigation:

- на router-contract уровне фиксировать только route presence, methods и high-level status expectations.

### 8.2 Under-Modeling Reload Again

Если reload path будет “починен” через слишком грубую заглушку, можно получить формально зеленый test без полезного контракта.

Mitigation:

- fake reload seam должен уметь как минимум различать success/failure path детерминированно.

### 8.3 Silent Scope Expansion

Есть риск незаметно начать чинить соседние tests или runtime behavior.

Mitigation:

- держать diff в `internal/application`;
- не трогать `futureparity`, `cmd/server`, public docs и unrelated packages.

---

## 9. Deliverables

1. `tasks/APPLICATION-ROUTER-CONTRACT-DRIFT/Spec.md` фиксирует active-contract direction.
2. `go-app/internal/application/router_contract_test.go` перестает описывать restored operational surface как absent.
3. `newActiveContractMux(...)` получает минимальный honest reload-capable state.
4. `go test ./internal/application -count=1` становится основным green verification path для этого slice.

---

## 10. Acceptance Criteria

1. `go-app/internal/application/router_contract_test.go` явно отделяет restored operational surface от still-absent historical surface.
2. `/api/v2/status`, `/api/v2/receivers`, `/api/v2/alerts/groups` и `/-/reload` больше не ожидаются как `404` в active router contract tests.
3. Reload-related contract expectations больше не зависят от случайного `reload coordinator not initialized`, если test intent предполагает active mounted behavior.
4. `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -count=1` проходит.
5. Ни production router surface, ни public docs не расширены сверх уже принятого scope `RUNTIME-SURFACE-RESTORATION`.
