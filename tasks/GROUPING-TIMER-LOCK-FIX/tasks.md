---
id: GROUPING-TIMER-LOCK-FIX
slug: grouping-timer-lock-fix
stream: Reliability / Grouping
type: bug
status: active
created_at: 2026-10-09
updated_at: 2026-10-09
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: две реплики не должны срабатывать на один таймер группы

**Based on:** requirements.md / research.md / Spec.md v1.0  
**Date:** 2026-10-09

Пути ниже — относительно `go-app/`, если не указано иное. `G` = `./internal/infrastructure/grouping/`.

## Touched Files

- `internal/infrastructure/grouping/timer_manager_impl.go` — `fireStillDue`, проверка в `onTimerExpired` после `AcquireLock`, doc-comment.
- `internal/infrastructure/grouping/distributed_timer_ownership_test.go` — T1–T3, T5 (две реплики на miniredis).
- `internal/infrastructure/grouping/timer_manager_impl_test.go` — T4 (таблица `fireStillDue`).
- `CHANGELOG.md` (корень) — `[Unreleased]` → Fixed.
- `internal/infrastructure/grouping/README.md` — раздела про HA-таймеры нет (grep `lock`/`HA` 2026-10-09), не трогаем.

## Phase 1: Implementation

> **Wave 1**

- [ ] **1.1** Неэкспортируемая чистая функция `fireStillDue(stored *GroupTimer, firedHandle *timerHandle, timerType TimerType, now time.Time) (bool, string)`. Правило из Spec § Target Design п. 2. Причины: `type_changed`, `rescheduled`; `not_found` обрабатывается у вызывающего. <!-- verify: cd go-app && go build ./... && go vet G -->

> **Wave 2**

- [ ] **1.2** В `onTimerExpired` сразу после успешного `AcquireLock` и defer `release()`, до блока `GetGroup`:
  - `LoadTimer` с 5s-контекстом от `tm.ctx`;
  - `errors.Is(err, ErrTimerNotFound)` или `!fireStillDue` ⇒ Debug `"Timer fire already handled elsewhere, skipping"` (`group_key`, `timer_type`, `reason`) + `dropLocalHandle` + return;
  - другая ошибка ⇒ Error `"Failed to load timer for expiration check"` + `dropLocalHandle` + return. <!-- depends: 1.1 | verify: cd go-app && go build ./... && go vet G -->
- [ ] **1.3** Doc-comment `onTimerExpired`: lock — «не одновременно», перечитанная запись storage — «ещё не обработано»; ссылка на `GROUPING-TIMER-LOCK-FIX`. Комментарий у `AcquireLock` («exactly-once delivery», `:867`) — уточнить. <!-- depends: 1.2 | verify: review diff -->

> **Wave 3** — смоук до deep-review (новые тесты не пишем)

- [ ] **1.4** Существующие тесты пакета и application зелёные под `-race`. <!-- depends: 1.2 | verify: cd go-app && go test -race ./internal/infrastructure/grouping/... ./internal/application/... -->
- [ ] **1.5** Флейк исчез на рычаге из research. <!-- depends: 1.2 | verify: cd go-app && go test -race -cpu=1 -count=400 -run 'TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires$' G  → 0 FAIL (до фикса 8/400) -->
- [ ] **1.6** Spec Open Question / Edge Case 11: минимальные допустимые `group_wait`/`group_interval` в валидации конфига против round-trip `ResetTimer` (`:631`). Если «воскрешение» реально — запись в `docs/06-planning/TECH-DEBT.md`, вывод — в Notes ниже. <!-- verify: grep валидации интервалов в internal/config + запись в Notes -->

**Phase verification:** `cd go-app && go vet G && go test -race ./internal/infrastructure/grouping/... ./internal/application/...`

## Phase 2: Deep Review

- [ ] **2.1** `/deep-review` (Spec § Deep Review: recommended, will run). Фокус: ложный skip (группа замолкает), пути `nil`-handle (reconcile/restore), continuation, lite-режим. <!-- depends: 1.2–1.6 | verify: tasks/GROUPING-TIMER-LOCK-FIX/review-verdict.json → "gate": "pass" -->

**Phase verification:** `review-verdict.json` `gate: pass`.

## Phase 3: Tests (write-tests, после verdict pass)

- [ ] **3.1** T1: детерминированный тест опоздавшей реплики (A срабатывает → `tmB.onTimerExpired(nil, …)` ⇒ callback 1 раз). Мутационная проверка: без проверки из 1.2 тест падает. <!-- depends: 2.1 | verify: go test -race -run LateReplica G; временно закомментировать проверку → FAIL -->
- [ ] **3.2** T2/T3: continuation другого типа; запись того же типа в будущем с чужим `expiresAt` ⇒ skip, handle сброшен, запись в storage не тронута. <!-- depends: 2.1 | verify: go test -race -run 'Continuation|Rescheduled' G -->
- [ ] **3.3** T4: табличный тест `fireStillDue` (7 строк из Spec § Test Plan). <!-- depends: 2.1 | verify: go test -run FireStillDue G -->
- [ ] **3.4** T5: ошибка `LoadTimer` (miniredis закрыт или обёртка storage с ошибкой) ⇒ callback не вызван, handle сброшен. <!-- depends: 2.1 | verify: go test -race -run LoadTimerError G -->

**Phase verification:** `cd go-app && go test -race ./internal/infrastructure/grouping/...`

## Phase 4: Testing & Docs

- [ ] **4.1** Гейты AMP: `make -C go-app quality-gates-fast` → `git status` (go fmt); `scripts/release-gate.sh`; `git diff --check`; в диффе нет `_, _ :=`. <!-- depends: 3.* | verify: вывод гейтов в Testing notes -->
- [ ] **4.2** T8 повторно на финальном коде: `-race -cpu=1 -count=400` ⇒ 0/400, результат дописать в `evidence/flake-rate.txt`. <!-- depends: 3.* | verify: файл evidence -->
- [ ] **4.3** e2e-ha: локально `deploy/e2e-ha/run.sh`, если Docker доступен; иначе — CI job после push, явно отметить. <!-- depends: 3.* | verify: PASS шагов 3–6 -->
- [ ] **4.4** `CHANGELOG.md` `[Unreleased]` → `### Fixed`: в HA повторное срабатывание таймера группы опоздавшей репликой больше не вызывает повторную обработку (пробелы, где nflog не спасал). <!-- verify: git diff CHANGELOG.md -->

## Phase 5: Finalize

- [ ] **5.1** `/finalize`:
  - `BUGS.md` — удалить `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`;
  - `BACKLOG.md` — P0 `GROUPING-TIMER-LOCK-FIX` в «Закрыто»;
  - строку P0 в `NEXT.md` и WIP — обновить;
  - `DONE.md` — запись;
  - архив workspace.
  <!-- verify: grep -c GROUPING-TIMER-LOCK-RELEASED docs/06-planning/BUGS.md → 0 -->

## Notes

<!-- Сюда — выводы 1.6, отклонения от Spec, Testing notes. -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
