# Research: FUTUREPARITY-SUITE-DRIFT

**Date**: 2026-03-09  
**Status**: Completed  
**Inputs**: `requirements.md`, `docs/06-planning/BUGS.md`, `docs/06-planning/DECISIONS.md`, `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`, `tasks/archive/ALERTMANAGER-REPLACEMENT-SCOPE/*`, `go-app/cmd/server/*`, `go-app/internal/application/*`

## Research Question

Как вернуть `futureparity` suite в поддерживаемое состояние, не ломая active-runtime-first truth и не превращая задачу в скрытое восстановление широкого runtime surface?

## Findings

### 1. Текущий compile break воспроизводим, но только из Go module root

Репозиторий root (`/Users/vit/Documents/Projects/AMP`) не является Go module, поэтому любые verification commands для этой задачи должны запускаться из `go-app/`.

Подтвержденный compile command:

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run TestDoesNotExist
```

Фактический первый failure:

- `undefined: runtimeStateFileEnv`
- `undefined: registerRoutes`
- `undefined: configSHA256`

Это совпадает с записью в `docs/06-planning/BUGS.md`.

### 2. Missing helpers отражают рефакторинг bootstrap ownership, а не случайную регрессию

Current `go-app/cmd/server/main.go` после bootstrap split:

- держит только `runtimeConfigFileEnv`;
- создает `application.NewRouter(registry)` и вызывает `router.SetupRoutes(mux)`;
- отдельно монтирует dashboard через `registerLegacyDashboardRoutes(mux, registry)`;
- больше не содержит `registerRoutes`, `runtimeStateFileEnv` и `configSHA256`.

Значит futureparity drift появился не из-за локальной поломки тестов, а из-за того, что historical suite продолжает зависеть от pre-split bootstrap seams.

### 3. Compile drift не исчерпывает проблему: historical suite ожидает wide surface, который active runtime сейчас сознательно не монтирует

Current active router в `go-app/internal/application/router.go` монтирует только:

- `/api/v2/alerts`
- `/api/v2/silences`
- `/api/v2/silence/{id}`
- `/health`, `/ready`, `/healthz`, `/readyz`, `/-/healthy`, `/-/ready`
- `/metrics`

`go-app/internal/application/router_contract_test.go` явно фиксирует, что следующие endpoints **не активны**:

- `/api/v2/status`
- `/api/v2/receivers`
- `/api/v2/alerts/groups`
- `/-/reload`
- `/api/v1/alerts`
- `/api/v2/config`
- `/api/v2/classification/health`
- `/history`

При этом `futureparity` suite ожидает их как mounted и рабочие. Значит simple helper restore не сможет сам по себе вернуть meaningful green path.

### 4. `futureparity` suite большая и сильно связана с одним mux seam

Размеры:

- `go-app/cmd/server/main_phase0_contract_test.go`: `5256` строк
- `go-app/cmd/server/main_upstream_parity_regression_test.go`: `2179` строк

Использование общего seam:

- `newPhase0TestMux(...)` вызывается `68` раз в `main_phase0_contract_test.go`
- `newPhase0TestMux(...)` вызывается `34` раза в `main_upstream_parity_regression_test.go`

Практический вывод: правильная точка разделения не в массовом ручном переписывании отдельных тестов, а в явном ownership для compatibility mux/harness.

### 5. После первых трех undefined symbols likely всплывут дополнительные compile blockers

`main_upstream_parity_regression_test.go` использует еще и:

- `runtimeClusterListenAddressEnv`
- `runtimeClusterAdvertiseAddressEnv`
- `runtimeClusterNameEnv`

В current active code их определений нет. Это означает, что текущий compile break шире, чем первые три ошибки из `BUGS.md`; просто компилятор остановился раньше.

### 6. Эквиваленты для config hash существуют, но не как стабильный shared test helper

SHA256 hash для конфигурации сегодня считается локально в нескольких местах:

- `go-app/internal/config/service.go`
- `go-app/internal/config/reload_coordinator.go`

Но в `cmd/server` нет общего exported helper, который futureparity tests могли бы безопасно использовать как canonical source. Значит `/spec` должен решить, где именно живет test-facing hash helper:

- локально в `futureparity` harness;
- или через новый shared helper;
- но без протаскивания historical test needs обратно в active `main.go`.

### 7. В репозитории есть dormant handler stacks, но не готовый wide-surface route builder

В `go-app/cmd/server/handlers/` есть крупный legacy/dormant surface, однако быстрый обзор не показывает готовый единый builder, который можно просто снова смонтировать и получить `status/config/history/reload` parity.

Это важно: green `futureparity` почти наверняка потребует не просто “подключить старые handlers”, а явно собрать отдельный compatibility harness и решить, какие части historical surface:

- реально еще backed by code,
- требуют адаптации,
- или должны остаться отдельным future restoration backlog.

## Options

### Option A: Minimal compatibility shim only

Сделать build-tagged файл в `cmd/server`, который просто возвращает:

- `runtimeStateFileEnv`
- `registerRoutes(...)` как thin wrapper над current router wiring
- `configSHA256(...)`
- cluster env constants

Плюсы:

- минимальный diff;
- быстро убирает первый compile blocker.

Минусы:

- почти наверняка сразу откроет большой слой runtime assertion failures;
- создает ложное ощущение “починили suite”, хотя wide-surface drift останется;
- thin wrapper над active router конфликтует с active-runtime-first truth, потому что tests ждут больше, чем active router дает.

### Option B: Dedicated `futureparity` compatibility harness

Сделать отдельный build-tagged harness для `cmd/server`, который:

- владеет missing env aliases и helper functions;
- поднимает отдельный `newPhase0TestMux`/compatibility mux для historical suites;
- явно документирует, что `futureparity` проверяет historical/backlog compatibility surface, а не current active runtime.

Плюсы:

- сохраняет separation между active runtime и historical parity;
- дает один осмысленный seam для `7435` строк suite;
- позволяет поэтапно чинить/сужать wide-surface expectations без загрязнения default bootstrap path.

Минусы:

- больше design work в `/spec`;
- нужен явный policy для unsupported endpoints;
- есть риск сделать harness слишком stub-heavy и потерять полезность suite.

### Option C: Aggressively narrow the suite

Оставить под `futureparity` только compile/smoke subset, а широкие expectations вынести в backlog docs или отдельные skipped tests.

Плюсы:

- самый маленький scope;
- минимальный риск незаметно восстанавливать runtime surface.

Минусы:

- существенно снижает ценность самого `futureparity`;
- не дает meaningful opt-in verification для future replacement story;
- может превратить bugfix в чисто косметическое “suite now compiles”.

## Recommendation

Рекомендуемый путь: **Option B**.

Ключевая идея: `futureparity` должен быть оформлен как **compatibility harness**, а не как thin alias к active runtime и не как неявное runtime-restoration усилие.

Практически это означает:

1. Создать build-tagged owner для historical helper/env layer в `go-app/cmd/server`.
2. Явно определить, что `newPhase0TestMux` в `futureparity` поднимает compatibility surface, отдельный от active router contract.
3. Разделить ожидания suite по intent:
   - compile/helper compatibility;
   - endpoints, still backed by reusable code;
   - endpoints, которые уже требуют отдельного restoration follow-up.

`Option A` можно использовать как **первый технический шаг внутри Option B**, но не как полную стратегию.

## Risks

### 1. Scope creep into runtime restoration

Если `/spec` не зафиксирует, что задача не возвращает wide surface в active runtime, реализация быстро уедет в `RUNTIME-SURFACE-RESTORATION`.

### 2. False-green harness

Если compatibility harness станет просто набором заглушек ради passing tests, он перестанет быть trustworthy verification artifact.

### 3. Hidden additional drift

После устранения первых undefined symbols почти наверняка проявятся новые compile/runtime gaps, поэтому success criteria нужно формулировать поэтапно, а не как “сразу весь suite green”.

### 4. Shared-helper leakage

Если для convenience вернуть historical helpers прямо в active `main.go`, это размоет границу ownership, уже выровненную в `ALERTMANAGER-REPLACEMENT-SCOPE`.

## Implication For `/spec`

`/spec` должен зафиксировать:

1. Что именно считается успехом этого slice:
   - compile-only green;
   - compile + selected targeted tests;
   - или green всего `futureparity`.
2. Где живет compatibility harness и какие helper/env symbols он owns.
3. Как маркируются unsupported historical expectations:
   - backlog,
   - temporary skip,
   - отдельный sub-suite,
   - или follow-up bug.
4. Какой verification path обязателен:

```bash
cd go-app
mkdir -p .cache/go-build
GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run TestDoesNotExist
```

и, при необходимости, один-два targeted subtests поверх compatibility harness.

## Did Research Change Likely Scope?

Да.

До исследования задача выглядела как repair трех missing symbols. После просмотра кода и tests более точная формулировка такая:

> `FUTUREPARITY-SUITE-DRIFT` — это refresh отдельного historical compatibility harness и test split-by-intent, а не просто helper restore.

Это **не** меняет верхнеуровневую цель задачи, но меняет ожидаемый подход на `/spec` и снижает риск скрытого возврата wide runtime surface в active path.
