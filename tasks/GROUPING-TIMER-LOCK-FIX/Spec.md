---
id: GROUPING-TIMER-LOCK-FIX
slug: grouping-timer-lock-fix
stream: Reliability / Grouping
type: bug
status: draft
created_at: 2026-10-09
updated_at: 2026-10-09
based_on:
  - requirements.md
  - research.md
---

# Specification: две реплики не должны срабатывать на один таймер группы

**Version:** 1.0  
**Status:** Draft

## Summary

`onTimerExpired` после захвата distributed lock'а перечитывает запись таймера из `TimerStorage` и вызывает callbacks, только если эта запись — всё ещё то срабатывание, которое пришло обработать. Если другая реплика уже обработала его (запись удалена или заменена continuation'ом), срабатывание тихо пропускается.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Механизм подтверждён | `research.md` Q1, `evidence/flake-rate.txt` |
| Защищённость прода выяснена | `research.md` Q2 (частично: nflog; пробелы a–d) |
| Исправлена корневая причина | Target Design, правило `fireStillDue` |
| Детерминированный тест опоздавшей реплики | Test Plan T1 |
| `-race -count=100` — 0 падений | Test Plan T8 (`-cpu=1 -count=400`, рычаг из research) |
| BUGS/BACKLOG/CHANGELOG | finalize; CHANGELOG `[Unreleased]` → Fixed |

## Current State

- **Code:** `go-app/internal/infrastructure/grouping/timer_manager_impl.go`. Сейчас `onTimerExpired` (`:862`) делает: `AcquireLock` → `GetGroup` → callbacks → `DeleteTimer` (если нет continuation) → `release()`. Состояние storage не проверяется. Вызовы:
  - `handleTimerExpiration` (`:747`, свой handle);
  - `RestoreTimers` для пропущенного таймера (`:1139`, `nil`);
  - `reconcileOrphanedTimers` (`:1297`, `nil`).
- **Data:** Redis `timer:{groupKey}`, JSON `GroupTimer`, ключ только `GroupKey`. TTL = до `ExpiresAt` + `timerTTLGracePeriod` (10m) (`redis_timer_storage.go:242`). Lock `timer:lock:{groupKey}`, SET NX, 30s.
- **Tests:**
  - `distributed_timer_ownership_test.go` — флейкает `TwoReplicasRace…`, рядом тесты orphan adoption;
  - `timer_continuation_regression_test.go`;
  - `timer_wedge_regression_test.go`;
  - `timer_manager_impl_test.go`;
  - e2e-ha шаги 3–5.
- **Docs:** `go-app/internal/infrastructure/grouping/README.md` (раздел про HA-таймеры — проверить на implement), `CHANGELOG.md`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `TimerStorage` реализуют только `RedisTimerStorage` и `InMemoryTimerStorage`; обе на отсутствующую запись возвращают `ErrTimerNotFound` | grep `func (.*) LoadTimer(` по всему `go-app`: 2 реализации, фейков в тестах нет; `redis_timer_storage.go:297-322`, `memory_timer_storage.go:133-149` | call-path-traced | у неизвестной реализации not-found выглядел бы как ошибка ⇒ пропуск + drop, срабатывание теряется до reconciliation |
| Каждое обработанное срабатывание меняет запись в storage до `release()`: либо `DeleteTimer`, либо новая запись continuation (`StartTimer` → `SaveTimer` с другим типом или будущим `ExpiresAt`) | `timer_manager_impl.go:990-1018`, `StartTimer` `:466` | code-read | если callback не трогает storage, а `DeleteTimer` упал, запись остаётся «просроченной» и повторное срабатывание возможно — как сейчас (Edge Case 6) |
| Handle несёт `expiresAt`, равный записанному в storage: `StartTimer` (`timer.ExpiresAt` → handle), `RestoreTimers` (`:1161`, из той же записи); `ResetTimer` пересохраняет запись с тем же `ExpiresAt` (`:624-631`, меняются только метаданные) | code-read всех трёх мест создания `timerHandle` | code-read | при расхождении своя запись не распознаётся как своя ⇒ решает только правило «запись просрочена», а при обратном скачке часов срабатывание откладывается до reconciliation (в lite — теряется) |
| Сравнение `ExpiresAt` через JSON сохраняет точность до наносекунд (RFC3339Nano), монотонная часть отбрасывается, `Equal` сравнивает момент времени | stdlib `time.Time.MarshalJSON`; `InMemoryTimerStorage` хранит `Clone` | code-read | `Equal` с handle никогда не совпадёт ⇒ см. строку выше |
| Reconciliation работает только с Redis-хранилищем; в lite (memory) реплика одна, и запись в storage всегда её собственная | `config.go:122-125`, `service_registry.go:1691` | code-read | — |
| Детерминированный repro (опоздавший `onTimerExpired` после `release()`) даёт 2 срабатывания | временный тест, `evidence/flake-rate.txt` | measured | — |

`assumed`-премис нет.

## Target Design

1. В `onTimerExpired` сразу после успешного `AcquireLock` (до `GetGroup`): `stored, err := tm.storage.LoadTimer(ctx, groupKey)` с тем же 5s-контекстом от `tm.ctx`.
2. Решение `fireStillDue(stored, firedHandle, timerType, now)` — срабатывание всё ещё актуально, если выполнены все условия:
   - запись есть;
   - `stored.TimerType == timerType`;
   - и одно из двух:
     - **своя запись**: `firedHandle != nil && stored.ExpiresAt.Equal(firedHandle.expiresAt)`;
     - **запись просрочена**: `!stored.ExpiresAt.After(now)`.
3. Не актуально (`ErrTimerNotFound`, другой тип, чужая запись ещё в будущем) ⇒ Debug-лог с причиной, `dropLocalHandle(firedHandle, groupKey)`, return. Storage не трогаем. Lock отпускается штатным defer.
4. Другая ошибка `LoadTimer` ⇒ та же ветка, что у ошибки lock-store: Error-лог, `dropLocalHandle`, return. Повтор — через reconciliation.

Почему так: после обработки срабатывание всегда меняет запись (удаляет или заменяет continuation), поэтому сама запись и есть маркер «уже сработало», новые ключи не нужны. Правило «своя запись» не зависит от часов: автор последней записи гарантированно сработает, даже если его wall clock отстаёт. Правило «запись просрочена» нужно для `nil`-handle путей (reconcile/restore) и для реплики, чью запись перезаписал сосед. Если она не сработает (её часы отстают), сработает автор записи или reconciliation.

## API Contracts

Не применимо: интерфейсы `TimerManager`/`TimerStorage` не меняются, новых экспортируемых символов нет.

## Data Model / Migrations

Не применимо: формат `GroupTimer` и ключи Redis не меняются. Migration: no.

## Component Architecture

- `go-app/internal/infrastructure/grouping/timer_manager_impl.go`:
  - проверка после `AcquireLock` в `onTimerExpired`;
  - неэкспортируемая функция `fireStillDue` (чистая, тестируется таблицей);
  - doc-comment `onTimerExpired`: lock — «не одновременно», запись storage — «ещё не обработано».
- `go-app/internal/infrastructure/grouping/distributed_timer_ownership_test.go` — новый детерминированный тест опоздавшей реплики. Существующий `TwoReplicasRace…` не ослабляется.
- `go-app/internal/infrastructure/grouping/timer_manager_impl_test.go` (или рядом) — таблица `fireStillDue`, тест ветки ошибки `LoadTimer`.
- `CHANGELOG.md` `[Unreleased]` → Fixed.

## Security Design

- [x] Ownership validation — не применимо (внутренний механизм таймеров).
- [x] Input validation — не применимо.
- [x] Sensitive data not logged — в логе только `group_key`, `timer_type`, причина; как в соседних логах.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] Одна реплика (lite, memory storage): каждый Go-таймер, дошедший до `onTimerExpired`, срабатывает, как раньше — его запись всегда «своя».
- [ ] Continuation (`group_wait` → `group_interval` → `repeat_interval`) продолжает строиться: проверка стоит до callbacks и не мешает им писать новую запись.
- [ ] Orphan adoption (reconcile, `nil`-handle) и пропущенный при старте таймер (restore) срабатывают: их записи по построению просрочены.
- [ ] Пропуск никогда не удаляет запись storage — только локальный handle, чтобы reconciliation мог подобрать группу.
- [ ] Ветки «lock занят» и «ошибка lock-store» не меняются.

## Edge Cases

1. Опоздавшая реплика после `release()` победителя, continuation не создан ⇒ `ErrTimerNotFound` ⇒ skip. (Repro из research, T1.)
2. Опоздавшая реплика, победитель создал continuation другого типа ⇒ тип не совпал ⇒ skip. (T2)
3. Continuation того же типа (`group_interval` → `group_interval`), опоздавший `group_interval` ⇒ `ExpiresAt` чужой и в будущем ⇒ skip. (T3)
4. Две реплики почти одновременно сделали `StartTimer`, запись перезаписал B (`ExpiresAt` на δ позже):
   - A срабатывает, видит чужую запись в будущем ⇒ skip;
   - B срабатывает на своей записи ⇒ fire;
   - если A всё-таки видит её просроченной (часы впереди) ⇒ A срабатывает под lock'ом и меняет запись, B потом пропускает.
   Итог — ровно один раз. (T4)
5. e2e-ha шаг 4: B восстановил handle из записи A (`ExpiresAt` равны) ⇒ обе «свои». Lock упорядочивает, второй видит continuation ⇒ skip.
6. `DeleteTimer` победителя упал, continuation нет ⇒ запись остаётся просроченной ⇒ опоздавшая реплика сработает повторно. Как сейчас; остаётся только nflog. Отдельно не чиним: ошибка уже логируется Warn.
7. `CancelTimer` удалил запись, а Go-таймер уже выстрелил ⇒ `ErrTimerNotFound` ⇒ skip (раньше срабатывал на отменённом таймере — поведение улучшается).
8. `ResetTimer` при уже выстрелившем старом handle ⇒ запись с новым `ExpiresAt` в будущем, не своя для старого handle ⇒ skip; сработает новый handle.
9. Ошибка `LoadTimer` (Redis недоступен) ⇒ Error, drop, повтор через reconciliation спустя grace. Раньше в этой ситуации падал и `AcquireLock`, так что поведение то же.
10. Обратный скачок wall clock на реплике-авторе ⇒ правило «своя запись» всё равно срабатывает.
11. Предсуществующее, вне scope: `ResetTimer` пересохраняет запись после запуска Go-таймера (`:631`). При длительности короче round-trip в Redis таймер может сработать и удалить запись до пересохранения ⇒ запись «воскреснет» и будет подобрана reconciliation. На implement проверить, реально ли это при минимальных интервалах конфига; если да — запись в TECH-DEBT.

## Impact Analysis

- **Affected modules:** `internal/infrastructure/grouping` (только `onTimerExpired`).
- **Breaking changes:** none.
- **New dependencies:** none.
- **Risks:**
  - Ложный skip ⇒ группа не нотифицирует. Митигация: правило «своя запись» не зависит от часов; в Redis-режиме страхует reconciliation (grace = claim TTL + margin); таблица T5–T7 и прогон существующих regression-тестов (continuation, wedge, orphan).
  - Лишний Redis GET на каждое срабатывание. Пренебрежимо: срабатываний — единицы в минуту на группу.

## Rollout / Rollback

- **Rollout:** обычный релиз. Пока при rolling upgrade работают смешанные версии, старая реплика может сработать повторно, как сейчас; хуже не становится.
- **Rollback:** revert коммита; формат данных не меняется.
- **Feature flag:** не применимо — правка корректности, флаг не нужен.

## Observability

- **Logs:** Debug `"Timer fire already handled elsewhere, skipping"` с `group_key`, `timer_type`, `reason` (`not_found` / `type_changed` / `rescheduled`). Error `"Failed to load timer for expiration check"` на ошибку `LoadTimer`.
- **Metrics:** не добавляем. Проигравший lock тоже не считается метрикой; при необходимости — отдельной задачей вместе с ним.
- **Alerts:** не применимо.

## Test Plan

- T1 — детерминированный: A срабатывает, после этого `tmB.onTimerExpired(nil, …)` ⇒ callback ровно один раз (без фикса — 2).
- T2 — continuation другого типа в storage ⇒ skip.
- T3 — запись того же типа с `ExpiresAt` в будущем, handle с другим `expiresAt` ⇒ skip.
- T4 — таблица `fireStillDue`:
  - своя запись в будущем ⇒ fire;
  - чужая просроченная ⇒ fire;
  - чужая в будущем ⇒ skip;
  - `nil`-handle просроченная ⇒ fire;
  - `nil`-handle в будущем ⇒ skip;
  - другой тип ⇒ skip;
  - нет записи ⇒ skip.
- T5 — ошибка `LoadTimer` ⇒ callback не вызван, handle сброшен, запись не тронута.
- T6 — существующие regression-тесты пакета зелёные (continuation, wedge, orphan adoption, restore).
- T7 — `go test -race ./internal/infrastructure/grouping/... ./internal/application/...`.
- T8 — `-race -cpu=1 -count=400 -run TwoReplicasRace…` ⇒ 0/400 (до фикса 8/400).
- e2e-ha — в CI job (Docker). Локально — если Docker доступен.

## Deep Review

- **Mandatory triggers present:** none (сигнал только `R`, не pre-release).
- **Discretionary triggers present:** author doubt / novel pattern — распределённая корректность в HA-пути. Цена ошибки — тихо не нотифицирующая группа, а у пакета история многократных fix round'ов именно в `onTimerExpired`.
- **Decision:** recommended, will run.

## Open Questions

- [ ] Edge Case 11: реален ли «воскресший» таймер `ResetTimer` при минимальных интервалах конфига — проверить на implement, при подтверждении завести TECH-DEBT.
