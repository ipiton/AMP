---
id: GROUPING-TIMER-LOCK-FIX
slug: grouping-timer-lock-fix
stream: Reliability / Grouping
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-09
updated_at: 2026-10-09
---

# Research Pack - две реплики не должны срабатывать на один таймер группы

Level 2 (`R` + неопределённость механизма), `--grounded` не требовался, но выводы привязаны к коду и прогонам.

## 0) TL;DR

- Solving: опоздавшая реплика повторно вызывает timer-callback для уже обработанного срабатывания.
- Found: TOCTOU подтверждён **детерминированно**. Lock в `onTimerExpired` защищает от одновременного срабатывания, но не от уже состоявшегося. Прод от двойной **доставки** в основном прикрывает nflog (`IsDuplicate`), но не во всех случаях.
- Chose: Option A — после захвата lock'а сверять запись в timer storage: нет записи или она уже не та, что срабатывает, — значит, срабатывание уже обработано, пропускаем.
- Risks: никто не сработает, если сверка ошибётся в сторону «пропустить»; страховка — reconciliation, в spec нужна таблица случаев.
- Next step: -> spec

## 1) Questions

1. Подтверждается ли TOCTOU, или флейк — тайминг теста?
2. Защищён ли прод от повторной доставки (nflog-claim, task 6.1)?
3. Что чинить: продукт или ожидание теста?

## 2) Findings

- **Q1 — подтверждено.** `onTimerExpired` (`timer_manager_impl.go:862`): `AcquireLock` (SET NX, `lockTTL` 30s) → callbacks → `DeleteTimer`/continuation → `defer release()` (`:892-899`). Ни после захвата lock'а, ни раньше состояние storage не проверяется. Repro (временный тест, удалён): A срабатывает, после этого в Redis нет ключей — ни таймера, ни lock'а. Затем `tmB.onTimerExpired(nil, …)` ⇒ `publishCount = 2`. Флейк в тесте — тот же механизм: горутина B доходит до `AcquireLock` после `release()` реплики A.
- **Частота** (`evidence/flake-rate.txt`): `-race -count=60` — 0/60; `-race -cpu=1` — 8/400, все падения `actual: 2`. Исторически 2/30 под нагрузкой гейта. `-cpu=1` — рычаг для проверки, но доказательство фикса — детерминированный тест.
- **Q2 — защищён частично.** Callback = `publishGroupAlerts` → `TryClaim` (`manager_impl.go:1331`, отпускается после publish) → `IsDuplicate` (`redis_notify_log.go:259`: та же signature и `SentAt` в окне repeat ⇒ дубликат). Опоздавшее повторное срабатывание с тем же набором алертов после **успешной** доставки подавляется. Не подавляется:
  - (a) набор алертов успел измениться — группа уходит раньше `group_interval`;
  - (b) metrics-only/неуспешный publish не пишет `nflog:entry` (e2e-ha шаг 3, finding 4);
  - (c) Redis недоступен — fail-open;
  - (d) кроме того, у B callback перезапускает свою continuation и перетирает запись storage.
- **Q3:** ожидание теста («callback ровно один раз») совпадает с контрактом lock'а из комментария «exactly-once delivery» (`:867`). Чинить продукт, тест не ослаблять.
- **Storage как маркер:** `StartTimer` пишет `SaveTimer` до запуска Go-таймера (`:466`, `:475`). Ключ — только `GroupKey` (не `+TimerType`). После срабатывания запись либо удалена, либо заменена continuation с `ExpiresAt` в будущем. Reconcile и RestoreTimers вызывают `onTimerExpired(nil, …)` для записей, уже просроченных в storage.

## 3) Options

`Generation:` single-pass (Standard tier, фикс обратим: один пакет, формат storage не меняется)

### Option A - сверка с timer storage под lock'ом

После `AcquireLock`: `LoadTimer(groupKey)`. Записи нет ⇒ срабатывание уже обработано. Запись есть, но `TimerType` не тот или `ExpiresAt` заметно в будущем (там уже continuation) ⇒ тоже обработано. В обоих случаях — тихий skip + `dropLocalHandle` (как для проигравшего lock'а). Ошибка `LoadTimer` ⇒ тот же путь, что для ошибки lock-store (drop + повтор через reconciliation).

- **Pros:** закрывает и пробел (a–d), и тест; новых ключей и формата нет; один Redis GET на срабатывание; reconcile и restore получают ту же защиту.
- **Cons:** нужен допуск на «чужую» запись того же типа: B перезаписал `ExpiresAt` на δ мс позже, плюс расхождение часов реплик. Допуск должен быть заметно меньше минимального интервала continuation.
- **Cost/Risk:** low–medium

### Option B - держать lock до TTL (не отпускать после callback'а)

- **Pros:** минимальный дифф.
- **Cons:** `lockTTL` 30s дольше коротких `group_interval`: собственная continuation той же реплики упрётся в lock и потеряется. Lock не равен «срабатывание обработано»: после истечения TTL та же дыра. Отклонён.
- **Cost/Risk:** high

### Option C - ослабить тест, положиться на nflog

- **Pros:** ноль изменений в продукте.
- **Cons:** оставляет (a–d), противоречит контракту lock'а. Отклонён.
- **Cost/Risk:** medium (тихий продуктовый дефект)

## 4) Decision

- **Chosen:** Option A.
- **Why:** устраняет корневую причину (нет проверки «уже сработало»), а не тайминг; маркер уже есть — сама запись storage; изменения формата и миграции не нужны; поведение одной реплики (lite, memory storage) не меняется: запись на момент срабатывания всегда своя.

## 5) Spec Inputs

- API/contracts: не меняются (`TimerStorage` уже имеет `LoadTimer`).
- Data model: без изменений. Решить в spec: сравнение по `TimerType` + допуск по `ExpiresAt` или по `StartedAt`/`Metadata.CreatedBy`. Таблица случаев: своя запись, чужая того же типа, continuation, удалена, ошибка, `nil`-handle из reconcile/restore.
- Rollout: смешанные версии при rolling upgrade — старая реплика может сработать повторно, как сейчас; не хуже текущего.
- Observability: Debug-лог skip'а с причиной; при необходимости счётчик — решить в spec.
- Tests: детерминированный тест «опоздавшая реплика после release» (повторить repro через `onTimerExpired(nil, …)`) + случаи из таблицы; флейкающий тест — `-race -count=100`.
- Security: не применимо.

## 6) References

- `go-app/internal/infrastructure/grouping/timer_manager_impl.go` (`StartTimer`, `onTimerExpired`, `reconcileOrphanedTimers`, `RestoreTimers`)
- `go-app/internal/infrastructure/grouping/redis_timer_storage.go` (`AcquireLock`, `lockTTL`)
- `go-app/internal/infrastructure/grouping/redis_notify_log.go`, `manager_impl.go` (`publishGroupAlerts`)
- `go-app/internal/infrastructure/grouping/distributed_timer_ownership_test.go`, `deploy/e2e-ha/run.sh` (шаги 3–4)
