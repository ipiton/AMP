---
id: ROUTE-TIMINGS-BUNDLE
slug: route-timings
stream: Routing
type: bug
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: наследование таймингов route как в upstream

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-20

Бандл: фаза 1 = `ROUTE-TIMINGS-NOT-INHERITED`, фаза 2 = `ROUTE-EXPLICIT-ZERO-GROUP-WAIT`. Тесты — фаза 3 (после deep-review).

## Touched Files

- `go-app/internal/infrastructure/routing/parser.go` — убрать рекурсивные дефолты route (D1)
- `go-app/internal/infrastructure/grouping/parser.go` — то же (D1)
- `go-app/internal/business/routing/tree_builder.go` — `inheritDuration` по `*grouping.Duration` (D2)
- `go-app/internal/business/routing/tree.go` — `validateDurations` (D3)
- `go-app/internal/infrastructure/grouping/manager.go`, `manager_impl.go` — `GroupTimings` doc, `startGroupWaitTimer`, порядок в `AddAlertToGroup` (D4)
- `go-app/internal/infrastructure/grouping/timer_manager_impl.go`, `timer_models.go` — нулевой `GroupWaitTimer` (D4)
- тесты в `business/routing`, `infrastructure/routing`, `infrastructure/grouping`, `application/handlers`
- `docs/ALERTMANAGER_COMPATIBILITY.md`, `CHANGELOG.md`

## Phase 1: ROUTE-TIMINGS-NOT-INHERITED

- [x] **1.1** Прод-парсер и grouping-парсер не дефолтят тайминги route (D1) <!-- verify: grep -n 'applyRouteDefaults' go-app/internal/infrastructure/{routing,grouping}/parser.go → пусто -->
- [x] **1.2** `inheritDuration` различает `nil`/значение, корень берёт `global.*` (D2) <!-- verify: cd go-app && go build ./... -->

**Phase verification:** `cd go-app && go vet ./internal/business/routing/... ./internal/infrastructure/routing/... ./internal/infrastructure/grouping/...`

## Phase 2: ROUTE-EXPLICIT-ZERO-GROUP-WAIT

- [x] **2.1** `validateDurations`: `group_wait < 0`, interval/repeat `<= 0`, без `group_interval >= group_wait` (D3) <!-- verify: go build ./... -->
- [x] **2.2** `StartTimer`/`GroupTimer.Validate` принимают `0` для `GroupWaitTimer` (D4) <!-- verify: go build ./... -->
- [x] **2.3** `startGroupWaitTimer` берёт `timings.GroupWait` как есть; старт таймера новой группы после `Store` (D4) <!-- verify: go build ./... -->

- [x] **2.4** Прод-парсер отвергает явные `group_interval`/`repeat_interval: 0s` у route и в `global.*` (D3a, deep-review F1) <!-- verify: go test ./internal/infrastructure/routing/... -->
- [x] **2.5** Docs: `ALERTMANAGER_COMPATIBILITY.md` (строка per-route timings, п. 7); `CHANGELOG.md` `[Unreleased]` — на finalize (deep-review F7) <!-- verify: git diff main -- docs/ALERTMANAGER_COMPATIBILITY.md CHANGELOG.md -->

**Phase verification:** `cd go-app && go test ./internal/business/routing/... ./internal/infrastructure/routing/... ./internal/infrastructure/grouping/...` (существующие тесты; падения из-за изменённой семантики разбираются в фазе 3)

## Phase 3: Tests (после deep-review `pass`)

- [x] **3.1** Дерево через прод-парсер: корень → ребёнок без таймингов → внук; промежуточный со своими; `group_wait: 0s` под `10s`; `global.*` для корня и вложенных; `group_interval: 0s`/`repeat_interval: 0s` → ошибка парсера и `Build`; `group_interval < group_wait` грузится <!-- verify: go test ./internal/business/routing/... -->
- [x] **3.2** Парсер: вложенные route без таймингов остаются `nil` после `Parse` <!-- verify: go test ./internal/infrastructure/routing/... ./internal/infrastructure/grouping/... -->
- [x] **3.3** Grouping: `startGroupWaitTimer` с `GroupWait: 0` срабатывает сразу, алерт виден callback'у; `StartTimer(0)` для interval/repeat — ошибка; memory/Redis `SaveTimer` нулевого group_wait <!-- verify: go test ./internal/infrastructure/grouping/... -->
- [x] **3.4** `/api/v2/status` `config.original` без таймингов у route, где их нет <!-- verify: go test ./internal/application/handlers/... -->
- [x] **3.5** Smoke на бинаре → `evidence/smoke.md` <!-- verify: manual, лог + время прихода в webhook -->

**Phase verification:** гейты `WORKFLOW.md` § Гейты AMP.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
