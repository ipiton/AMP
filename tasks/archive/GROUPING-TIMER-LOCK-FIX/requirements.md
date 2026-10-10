---
id: GROUPING-TIMER-LOCK-FIX
slug: grouping-timer-lock-fix
stream: Reliability / Grouping
type: bug
priority: high
status: complete
created_at: 2026-10-09
updated_at: 2026-10-09
---

# Requirements: две реплики не должны срабатывать на один таймер группы

## Problem Framing

- **Symptom:** `TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires` (`go-app/internal/infrastructure/grouping/distributed_timer_ownership_test.go`) под `-race` примерно в 1 прогоне из 15 видит 2 срабатывания callback'а вместо 1. Шаг `race` required-гейта `gate` краснеет случайно (BUGS `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`).
- **Root Cause:** гипотеза — TOCTOU в `onTimerExpired` (`timer_manager_impl.go`): distributed lock отпускается (`defer release()`) сразу после callback'а, и реплика, дошедшая до `AcquireLock` позже, берёт свободный lock и срабатывает повторно. Lock защищает от «одновременно», а не от «уже сработало». Не подтверждено.
- **Why Now:** P0 в BACKLOG § Production Readiness: краснит required check, а дефолт чарта (HPA `minReplicas: 2`) ставит AMP ровно в конфигурацию, где возможна двойная нотификация.
- **How We Measure:** `go test -race -count=100 -run TwoReplicasRace ./internal/infrastructure/grouping/` — 0 падений (сейчас ~2/30); детерминированный тест на сценарий «опоздавшая реплика после release» проходит на фиксе и падает без него; e2e-ha шаг 4 зелёный.

## Risk Profile

- **Signals:** `R`
  - `R` — меняется поведение срабатывания grouping-таймеров в HA (путь доставки нотификаций).
- **Tier:** Standard
- **Notes:** если фикс потребует нового ключа/формата в Redis timer storage (маркер «уже сработало») — пересмотреть на `C`; это всё ещё Standard. Если выяснится, что прод реально шлёт дубликаты, — `C R` остаётся Standard, `deep-review` по желанию.

## User Stories

1. Как оператор AMP с 2+ репликами, я хочу, чтобы истечение таймера группы приводило ровно к одной обработке, чтобы получатели не получали дубликаты.
2. Как разработчик, я хочу, чтобы required `gate` не краснел случайно на grouping-тесте, чтобы красный CI означал реальную проблему.

## Success Criteria

- [ ] Механизм двойного срабатывания подтверждён (или опровергнут) воспроизведением, зафиксирован в `research.md`.
- [ ] Выяснено, защищает ли nflog-claim (task 6.1) прод от повторной доставки; ответ подкреплён кодом/тестом/e2e-ha.
- [ ] Исправлена корневая причина: продукт (lock не отпускается раньше, чем повторное срабатывание становится невозможным, или маркер срабатывания в storage) либо ожидание теста — с обоснованием.
- [ ] Детерминированный тест на сценарий опоздавшей реплики (без опоры на `time.Sleep`-тайминг).
- [ ] `-race -count=100` флейкающего теста — 0 падений; release-gate зелёный по шагу `race`.
- [ ] BUGS-запись удалена, BACKLOG P0 закрыт, CHANGELOG `[Unreleased]` — если меняется поведение в проде.

## Non-Goals

- Остальные grouping-флейки (`GROUPING-ORPHAN-ADOPTION-TEST-FLAKY`) и `FLAKE-REFRESH-IN-PROGRESS` — отдельные записи, если корень не общий.
- Изменение дефолтов HPA чарта (`HELM-SINGLE-NODE-DEFAULTS`).
- Переработка reconciliation loop и формата timer storage сверх нужного для фикса.

## Constraints

- **Scope:** `go-app/internal/infrastructure/grouping/` (+ точка вызова callback'а, если затронута); тесты пакета.
- **Security:** не применимо.
- **Compatibility:** rolling upgrade со смешанными версиями реплик не должен давать двойных срабатываний сверх текущего поведения; изменения формата Redis-ключей — с обратной совместимостью или явной migration note.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-CI-IMAGES/` (нашла баг), `tasks/archive/PROD-DEPS-VULN/` (замеры частоты); дубликатов в `tasks/`, `tasks/archive/`, `DONE.md`, `archive/DONE-*.md` нет.
- Relevant patterns: `distributed_timer_ownership_test.go` (miniredis, две реплики), `e2e-ha` (`deploy/e2e-ha/run.sh`, шаг 4 — повторная доставка), nflog-claim из task 6.1.
- Open unknowns: точный механизм (TOCTOU vs. что-то в local timer/reconcile); защищён ли прод nflog-claim'ом; не одна ли причина у `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY`.
