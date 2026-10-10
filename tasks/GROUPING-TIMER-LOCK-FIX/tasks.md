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
- `internal/infrastructure/grouping/timer_fire_dedup_test.go` (новый) — T1, T1b, T1c, T2/T3, T4, T5, T5b, F5d.
- `internal/infrastructure/grouping/distributed_timer_ownership_test.go` — T6b (шаг 3.5).
- `CHANGELOG.md` (корень) — `[Unreleased]` → Fixed.
- `internal/infrastructure/grouping/README.md` — раздела про HA-таймеры нет (grep `lock`/`HA` 2026-10-09), не трогаем.

## Phase 1: Implementation

> **Wave 1**

- [x] **1.1** Неэкспортируемая чистая функция `fireStillDue(stored *GroupTimer, firedHandle *timerHandle, timerType TimerType, now time.Time) (bool, string)`. Правило из Spec § Target Design п. 2. Причины: `type_changed`, `rescheduled`; `not_found` обрабатывается у вызывающего. <!-- verify: cd go-app && go build ./... && go vet G -->

> **Wave 2**

- [x] **1.2** В `onTimerExpired` сразу после успешного `AcquireLock` и defer `release()`, до блока `GetGroup`:
  - `LoadTimer` с 5s-контекстом от `tm.ctx`;
  - `errors.Is(err, ErrTimerNotFound)` или `!fireStillDue` ⇒ Debug `"Timer fire already handled elsewhere, skipping"` (`group_key`, `timer_type`, `reason`) + `dropLocalHandle` + return;
  - другая ошибка ⇒ Error `"Failed to load timer for expiration check"` + `dropLocalHandle` + return. <!-- depends: 1.1 | verify: cd go-app && go build ./... && go vet G -->
- [x] **1.3** Doc-comment `onTimerExpired`: lock — «не одновременно», перечитанная запись storage — «ещё не обработано»; ссылка на `GROUPING-TIMER-LOCK-FIX`. Комментарий у `AcquireLock` («exactly-once delivery», `:867`) — уточнить. <!-- depends: 1.2 | verify: review diff -->

> **Wave 3** — смоук до deep-review (новые тесты не пишем)

- [x] **1.4** Существующие тесты пакета и application зелёные под `-race`. <!-- depends: 1.2 | verify: cd go-app && go test -race ./internal/infrastructure/grouping/... ./internal/application/... -->
- [x] **1.5** Флейк исчез на рычаге из research. <!-- depends: 1.2 | verify: cd go-app && go test -race -cpu=1 -count=400 -run 'TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires$' G  → 0 FAIL (до фикса 8/400) -->
- [x] **1.6** Spec Open Question / Edge Case 11: минимальные допустимые `group_wait`/`group_interval` в валидации конфига против round-trip `ResetTimer` (`:631`). Если «воскрешение» реально — запись в `docs/06-planning/TECH-DEBT.md`, вывод — в Notes ниже. <!-- verify: grep валидации интервалов в internal/config + запись в Notes -->

**Phase verification:** `cd go-app && go vet G && go test -race ./internal/infrastructure/grouping/... ./internal/application/...`

## Phase 2: Deep Review

- [x] **2.1** `/deep-review` (Spec § Deep Review: recommended, will run). Фокус: ложный skip (группа замолкает), пути `nil`-handle (reconcile/restore), continuation, lite-режим. <!-- depends: 1.2–1.6 | verify: tasks/GROUPING-TIMER-LOCK-FIX/review-verdict.json → "gate": "pass" -->

**Phase verification:** `review-verdict.json` `gate: pass`.

## Phase 3: Tests (write-tests, после verdict pass)

- [x] **3.1** T1: детерминированный тест опоздавшей реплики (A срабатывает → `tmB.onTimerExpired(nil, …)` ⇒ callback 1 раз). Мутационная проверка: без проверки из 1.2 тест падает. <!-- depends: 2.1 | verify: go test -race -run LateReplica G; временно закомментировать проверку → FAIL -->
- [x] **3.2** T2/T3: continuation другого типа; запись того же типа в будущем с чужим `expiresAt` ⇒ skip, handle сброшен, запись в storage не тронута. <!-- depends: 2.1 | verify: go test -race -run 'Continuation|Rescheduled' G -->
- [x] **3.3** T4: табличный тест `fireStillDue` (7 строк из Spec § Test Plan). <!-- depends: 2.1 | verify: go test -run FireStillDue G -->
- [x] **3.4** T5: ошибка `LoadTimer` (miniredis закрыт или обёртка storage с ошибкой) ⇒ callback не вызван, handle сброшен. <!-- depends: 2.1 | verify: go test -race -run LoadTimerError G -->

- [x] **3.6** (R1 F1) T1b: пропускающая реплика не держит lock — автор записи срабатывает. <!-- depends: 2.1 | verify: go test -race -run SkipperDoesNotHoldLock G; мутация: перенести предпроверку под lock → FAIL -->
- [x] **3.7** (R1 F5b) T1c: обе реплики `StartTimer`, вторая перезаписывает ⇒ ровно 1, не 0. <!-- depends: 2.1 | verify: go test -race -cpu=1 -count=200 -run OverwrittenEntry G -->
- [x] **3.8** (R1 F2) T5b: gauge — `DecActiveTimers` в `dropLocalHandle` только при удалении. <!-- depends: 2.1 | verify: go test -run DropLocalHandle G -->
- [x] **3.5** `TwoReplicasRace…` (`distributed_timer_ownership_test.go:171`): ассерт лога требует ветку `ErrLockAlreadyAcquired`, а опоздавший проигравший теперь уходит через `fireStillDue`. По R1 F4: ассерт лога заменить исходом — `publishCount == 1`, суммарный `totalExpired == 1`, пустой `tm.timers` у обеих реплик, в логе нет `Failed to load timer for expiration check`; поправить doc-comment теста. T4 (3.3) дополнить строкой R1 F5a (JSON round-trip). <!-- depends: 2.1 | verify: go test -race -cpu=1 -count=400 -run 'TwoReplicasRace' G → 0/400 -->

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

- **1.4** (2026-10-09): `go build ./...`, `go vet G`, `go test -race ./internal/infrastructure/grouping/... ./internal/application/...` — 1054 passed, 3 пакета.
- **1.6 / Spec Edge Case 11:** `ResetTimer` в прод-коде не вызывается: grep `\.ResetTimer(` по `internal`, `cmd` без `_test.go` пуст. `alert_processor.go:450-462` объясняет, почему его намеренно не зовут (upstream не продлевает `group_interval`). «Воскрешение» записи сейчас недостижимо, TECH-DEBT не заводим. Если `ResetTimer` начнут вызывать — пересмотреть.
- **1.5** (2026-10-09): двойных срабатываний 0/600 (`-cpu=1`, 400 + 200). Падения 10/600 — только ассерт лога `:171`, закреплявший механизм (ветку lock'а), а не результат → шаг 3.5. Сырые данные — `evidence/flake-rate.txt`. Флейк в CI до 3.5 остаётся: тот же тест, другой ассерт.
- **Deep-review round 1** (2026-10-10, `291bd7d`): fix_required, 1 major. Исправлено в коде:
  - F1 — предпроверка `skipHandledFire` до `AcquireLock` + double-check под lock'ом;
  - F2 — `DecActiveTimers` в `dropLocalHandle`;
  - F9 — `fireStillDue` перенесена выше doc-блока `onTimerExpired`, комментарий reconcile.
  - F3 — Spec v1.1 (премисы, Edge Cases 12–13).
  - F4/F5 — шаги 3.5–3.8.
  - Отложено на finalize: F6 → TECH-DEBT, F7 → BUGS, F8 → BACKLOG.
- **Deep-review round 2** (2026-10-10, `93b686f`): pass, 3 nit. N1/N2 исправлены в `d459a6e`, N3 (дрейф gauge при внешнем `StartTimer`) → TECH-DEBT на finalize, вместе с остальным дрейфом gauge. Verdict — `d459a6e`.
- **write-tests** (2026-10-10, на `d459a6e` + тесты):
  - 3.1–3.8 — `timer_fire_dedup_test.go`: 9 тестов; добавлен R1 F5d — цепочка continuation в lite/memory не рвётся; `TwoReplicasRace…` — проверка лога заменена проверкой результата.
  - Мутации (исходник восстановлен, `git diff --stat` тот же):
    - без сверки — падают T1, T1b, T2/T3, T5;
    - сверка только под lock'ом — падает T1b («must not hold the fire lock»);
    - без `Dec` в `dropLocalHandle` — падает T5b.
  - `-race -cpu=1 -count=400 TwoReplicasRace` — 0/400, два прогона.
  - `-race -cpu=1 -count=200` T1/T1b/T1c — 600/600.
  - `go test -race ./internal/infrastructure/grouping/... ./internal/application/...` — 1075 passed.
  - `gofmt -l` пуст, `git diff --check` чистый.
- **Отклонения от плана (write-tests):** новые тесты собраны в отдельный файл `timer_fire_dedup_test.go`, а не разнесены по `distributed_timer_ownership_test.go` и `timer_manager_impl_test.go`: у них общий хелпер реплики `newSharedRedisReplica`, им же теперь пользуется `TwoReplicasRace…`.
- **Отклонения от Spec:** нет. `not_found` возвращается самой `fireStillDue` (`stored == nil`), а не обрабатывается у вызывающего, как сказано в плане 1.1, — так ветка одна.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
