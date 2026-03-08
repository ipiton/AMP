# ALERTMANAGER-REPLACEMENT-SCOPE - Spec

**Status**: Implemented v1  
**Date**: 2026-03-08  
**Inputs**: `requirements.md`, `research.md`
**Chosen Direction**: `narrow public claims / active-runtime-first`

**Related Planning**:
- `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`
- `docs/06-planning/DECISIONS.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/BACKLOG.md`

---

## 1. Problem Statement

В репозитории нет единого ответа на вопрос, что именно active runtime AMP поддерживает относительно Alertmanager.

Сейчас одновременно существуют четыре несовместимые картины:

1. `go-app/internal/application/router.go` и `go-app/cmd/server/main.go` поднимают узкий active runtime;
2. `docs/06-planning/DONE.md` описывает значительно более широкий активный путь;
3. `docs/ALERTMANAGER_COMPATIBILITY.md`, `README.md` и migration docs заявляют replacement/compatibility scope шире реального runtime;
4. `go-app/cmd/server/main_phase0_contract_test.go` и `go-app/cmd/server/main_upstream_parity_regression_test.go` фиксируют ожидания для этого более широкого historical surface.

Из-за этого тезис "AMP может заменить Alertmanager" не является ни проверяемым, ни честно ограниченным. Этот spec определяет **truth-alignment slice**: сначала синхронизировать scope, источники истины и verification model, а уже потом решать, расширяем ли runtime обратно.

---

## 2. Goals

1. Зафиксировать один canonical source of truth для replacement story.
2. Определить допустимый replacement scope для текущего active runtime.
3. Убрать противоречия между active runtime, ADR, DONE/planning, parity tests и public docs.
4. Разделить будущую работу на два отдельных трека:
   - `truth/docs/test alignment`
   - `runtime surface restoration`, если он по-прежнему нужен.

---

## 3. Non-Goals

1. Не восстанавливать в этом slice весь historical wide API surface.
2. Не возвращать автоматически `status`, `receivers`, `alerts/groups`, `/-/reload`, `config*`, `history*`, `classification/*`, `inhibition/*` в active runtime.
3. Не пытаться в этой задаче закрыть все repo quality gates.
4. Не переписывать весь marketing/docs слой без сначала принятого scope decision.

---

## 4. Key Decisions

### 4.1 Canonical Source of Truth

Для replacement story source of truth становится **active runtime**, то есть:

- `go-app/cmd/server/main.go`
- `go-app/internal/application/router.go`
- runtime-visible handlers и services, реально используемые этим bootstrap path

Historical docs, old tests и старые claims не считаются источником истины, если они противоречат active runtime.

### 4.2 Replacement Scope For Current Slice

Для текущего состояния репозитория фиксируется только такой scope:

- **controlled replacement**

Под controlled replacement здесь понимается:

- ingest через active alert path;
- silence CRUD;
- health/readiness;
- metrics;
- real publishing path с explicit `metrics-only` fallback;
- использование только теми клиентами и операторами, которые ориентируются на фактический runtime surface AMP, а не на full Alertmanager parity expectations.

Следующие формулировки считаются **недопустимыми до отдельного follow-up подтверждения**:

- `general-purpose drop-in replacement`
- `production-ready replacement`
- `100% compatibility`

### 4.3 Replacement Surface Classification

Surface нужно разделить на три класса:

#### A. Active and Verified

То, что реально смонтировано и поддерживается текущим runtime.

#### B. Historical But Not Active

То, что фигурирует в parity tests/docs/DONE, но не смонтировано в текущем active runtime.

#### C. Future Parity Backlog

То, что имеет смысл как потенциальный follow-up runtime restoration scope, но не должно больше маскироваться под уже активную возможность.

### 4.4 `/api/v1/alerts` Policy

`/api/v1/alerts` не считается частью текущего replacement scope.

Причина:

- ADR-002 уже фиксирует, что deprecated v1 endpoints не являются обязательным compatibility target;
- текущий active runtime этот alias не монтирует;
- наличие historical tests/docs, которые его ожидают, трактуется как drift, а не как runtime contract.

Если alias нужен продуктово, он должен возвращаться только в отдельном runtime-restoration slice.

### 4.5 Test Strategy Split

Тесты должны быть разделены на две категории:

#### Active Runtime Contract

Покрывают только реально смонтированную поверхность текущего bootstrap.

#### Future/Backlog Parity

Описывают desired или historical surface, но не блокируют claims о текущем active runtime до тех пор, пока такой surface реально не восстановлен.

Это решение нужно, потому что сейчас historical parity suite одновременно:

- заявляет слишком широкий scope;
- частично не компилируется;
- и тем самым не может быть trustworthy gate для replacement claim.

---

## 5. Assumptions

1. Ближайшая ценность задачи не в добавлении новых endpoints, а в восстановлении честного product/runtime contract.
2. Реальный publishing path, подключённый в `PHASE-4-PRODUCTION-PUBLISHING-PATH`, уже даёт минимальное ядро controlled replacement story.
3. Runtime restoration, если нужен, будет отдельной задачей с собственным spec и acceptance criteria.
4. `DONE.md`, `DECISIONS.md`, `README.md`, migration docs и compatibility docs допустимо корректировать в сторону более узкого, но честного scope.

---

## 6. Scope Model

### 6.1 In Scope For This Task

- зафиксировать current replacement scope;
- определить canonical source of truth;
- синхронизировать planning/ADR/docs/tests terminology;
- разрезать historical parity expectations на `active` и `backlog`;
- подготовить follow-up list для runtime restoration и docs honesty pass.

### 6.2 Out Of Scope For This Task

- реализация новых API handlers;
- возврат старого route stack из historical phase0 state;
- integration of config/history/classification/inhibition APIs into current bootstrap;
- end-to-end runtime expansion beyond current active surface.

---

## 7. Deliverables

### 7.1 Decision Artifact

Нужно зафиксировать один явный decision record:

- AMP today = `controlled replacement`, not `general-purpose drop-in replacement`

Это может быть:

- обновление `docs/06-planning/DECISIONS.md`
- или отдельная короткая запись в planning docs, если так проще удержать минимальный diff

### 7.2 Planning Alignment

Нужно синхронизировать planning statements:

- `DONE.md`
- `BUGS.md`
- при необходимости `ROADMAP.md` / `NEXT.md`

Так, чтобы planning больше не описывал active runtime шире, чем он есть по коду.

### 7.3 Verification Model Alignment

Нужно определить, что делать с current parity suites:

- какие тесты относятся к current active runtime contract;
- какие тесты должны быть переведены в backlog/disabled/future suite;
- какой минимальный smoke path нужен для future replacement claim.

### 7.4 Follow-Up Tasks

Из этой задачи должны выйти как минимум два follow-up направления:

1. `DOCS-HONESTY-PASS`
2. `RUNTIME-SURFACE-RESTORATION` или эквивалентный runtime/API backlog item, если продуктово всё ещё нужен stronger replacement claim

---

## 8. Acceptance Criteria

Слайс считается завершённым, если одновременно выполнены условия:

1. В проекте явно зафиксировано, что current source of truth для replacement story — active runtime.
2. Replacement claim для текущего состояния описан как `controlled replacement`, а не как полный drop-in replacement.
3. Planning/ADR/docs/tests больше не противоречат друг другу на уровне выбранного scope.
4. Historical wide-surface expectations либо вынесены в backlog/future parity, либо явно помечены как non-active.
5. Есть короткий verification path для будущего более сильного replacement claim.

---

## 9. Proposed Implementation Direction

### Step 1. Freeze Scope Wording

Сначала зафиксировать в planning/decision layer:

- что именно active runtime поддерживает сегодня;
- какие формулировки разрешены в публичных claims;
- какие claim words запрещены до future slice.

### Step 2. Reconcile ADR vs Runtime

Обновить ADR-002 так, чтобы она не конфликтовала с:

- active router
- `/api/v1/alerts` policy
- current verification reality

### Step 3. Split Tests By Intent

Определить для `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go`:

- что остаётся active contract;
- что является future parity backlog;
- что просто stale and must stop driving product claims.

### Step 4. Prepare Runtime Restoration Follow-Up

Если после truth-alignment всё ещё нужен stronger Alertmanager replacement claim, выделить это в отдельную задачу с явным runtime integration scope:

- `status`
- `receivers`
- `alerts/groups`
- `/-/reload`
- и отдельно решение по `config/history/classification/inhibition` surface

---

## 10. Risks

### Risk A: Partial Cleanup Without Real Source Of Truth

Если просто править docs, но не зафиксировать source of truth и test split, drift быстро вернётся.

### Risk B: Scope Creep Into Runtime Restoration

Если в этой задаче начать "заодно" восстанавливать endpoints, задача расползётся в отдельный API/runtime project.

### Risk C: Planning Drift Persists

Если обновить README, но не обновить `DONE.md` и ADR, внутри репозитория останется два конфликтующих narrative.

---

## 11. Verification Expectations

В этой задаче verification ориентирован не на full repo gate, а на consistency checks:

- planning/docs/test references aligned to chosen scope;
- no direct contradictions between active runtime and documented replacement story;
- minimal targeted test/document checks for changed files.

Full `go vet ./...` / `go test ./...` остаются отдельным repo-quality concern и не являются blocker-ом для самого scope decision slice, пока существующие preexisting issues честно зафиксированы.
