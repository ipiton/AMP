# Deep Review Findings: две реплики не должны срабатывать на один таймер группы

**Trigger classification:** discretionary-running (author doubt / novel pattern — распределённая корректность в HA-пути; Spec § Deep Review)
**Reviewer perspective:** R1 — два независимых агента (general-purpose), не видевших выводов друг друга: (A) корректность/конкурентность + премисы + сопровождаемость; (B) runtime/observability + история + тест-стратегия. R2 — третий независимый агент, в R1 не участвовал
**Reviewed at:** 2026-10-10
**Reviewed tree:** R1 — bugfix/grouping-timer-lock-fix @ 291bd7d; R2 — @ 93b686f (+ `d459a6e`: только комментарий и Spec, nit N1/N2)
**Verdict:** R1 — fix_required; R2 — pass (see `review-verdict.json`)

## Round 1 — Findings

### F1 — skip под lock'ом заставляет автора записи сдаться: не срабатывает никто
- **Severity:** major
- **Location:** `go-app/internal/infrastructure/grouping/timer_manager_impl.go:906-916` (ветка `ErrLockAlreadyAcquired`) + `:944-966` (новая проверка)
- **Issue:** ветка «lock занят» (`7aaba3f`) исходит из «держатель lock'а = обработчик срабатывания» и сбрасывает handle. Фикс это нарушает. Сценарий: A и B почти одновременно сделали `StartTimer`, в storage запись B. A срабатывает, берёт lock и видит чужую ещё не наступившую запись (часы A отстают) ⇒ `rescheduled`, skip. Пока A держит lock (~2 RTT), срабатывает B ⇒ «lock занят» ⇒ drop. Итог — 0 срабатываний, запись ничья. Замер ревьюера A (miniredis, 0,5 мс/операция): сдвиг часов 0 — 0/200, 1 мс — 1/60, 3 мс — 113/200. С reconciliation первая нотификация задерживается до `ExpiresAt` + grace + тик. При `reconciliation_interval: 0` (`config.go:130`) группа молчит до рестарта реплики.
- **Recommendation:** предпроверка без lock'а: до `AcquireLock` выполнить `LoadTimer` + `fireStillDue`; при `!due` — drop и return, lock не трогать. Проверку под lock'ом оставить (double-checked). + детерминированный тест: «lock держит пропускающая реплика ⇒ автор записи всё равно срабатывает» (или эквивалент через предпроверку).
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Status:** fixed (93b686f; подтверждено R2)

### F2 — gauge `active_timers` дрейфует на пути skip
- **Severity:** minor (A: nit, B: minor — принят старший)
- **Location:** `timer_manager_impl.go` `dropLocalHandle` (`:849`); `IncActiveTimers` `:511`, `:1242`; `DecActiveTimers` `:573`, `:1095`
- **Issue:** у handle'а был `Inc` в `StartTimer`/`RestoreTimers`, а ранние выходы через `dropLocalHandle` не делают `Dec`. Раньше это была редкая ветка «lock занят»; теперь через skip проходит каждое отсеянное HA-срабатывание ⇒ gauge на проигравшей реплике растёт на +1 за срабатывание группы (панель в `grafana/dashboards/alert-history-service.json`).
- **Recommendation:** `DecActiveTimers()` в `dropLocalHandle`, когда handle реально удалён.
- **Disposition:** fix-here
- **Follow-up:** n/a. Обратный дрейф (`Dec` без `Inc` на `nil`-handle путях reconcile/restore) — предсуществующий, вне scope.
- **Status:** fixed (93b686f; подтверждено R2)

### F3 — премиса «обработанное срабатывание меняет запись до release» неполна; нет премисы про ветку «lock занят»
- **Severity:** minor
- **Location:** `Spec.md` § Design Premises, строка 2; комментарии фикса
- **Issue:** `lockTTL` = 30s без продления, а callback ждёт подтверждения доставки дольше (`manager_impl.go:36-41`, `TimerLockTTL()`): lock «routinely expires mid-fire». После истечения TTL другая реплика может увидеть ещё не изменённую запись. Для handle'ов одного поколения это практически недостижимо (опоздание >30s ⇒ `rescheduled`), страхует nflog-claim. Премисы про допущение ветки «lock занят» (F1) в Spec не было.
- **Recommendation:** уточнить премису и комментарии; добавить премису про ветку «lock занят».
- **Disposition:** fix-here (docs)
- **Follow-up:** n/a
- **Status:** fixed (93b686f (Spec v1.1); подтверждено R2)

### F4 — ассерт лога в `TwoReplicasRace…` закрепляет механизм, а не исход
- **Severity:** minor
- **Location:** `distributed_timer_ownership_test.go:171`; `tasks.md` 3.5
- **Issue:** какая ветка отсеет проигравшего (lock, `rescheduled`, срабатывание по просроченной записи), недетерминировано. Строку «Lock already acquired by another instance» пишет и `RedisTimerStorage.AcquireLock` (`redis_timer_storage.go:527`), то есть ассерт сейчас совпадает не с веткой `onTimerExpired`.
- **Recommendation:** заменить ассерт лога проверками исхода: `publishCount == 1`; суммарный `totalExpired == 1`; пустой `tm.timers` у обеих реплик (гард finding 3 / `7aaba3f`); в логе нет `Failed to load timer for expiration check`. Поправить doc-comment теста.
- **Disposition:** fix-here (в write-tests, шаг 3.5)
- **Follow-up:** n/a
- **Status:** planned (write-tests, `tasks.md` 3.3–3.8; покрытие подтверждено R2)

### F5 — пробелы в тест-плане
- **Severity:** minor
- **Location:** `tasks.md` Phase 3; `Spec.md` § Test Plan
- **Issue / Recommendation:**
  - (a) строка T4: `stored.ExpiresAt` после JSON round-trip от `handle.expiresAt`, `now` раньше (отстающие часы) ⇒ due;
  - (b) сквозной тест ветки `rescheduled` без потери: обе реплики делают `StartTimer`, вторая перезаписывает ⇒ callback ровно 1, **не 0**;
  - (c) T2/T3 с реальным handle (restore/StartTimer), не только `nil`;
  - (d) регрессия lite/memory: цепочка continuation не рвётся.
- **Disposition:** fix-here (в write-tests): (a), (b) и тест F1; (c), (d) — по возможности
- **Follow-up:** n/a
- **Status:** planned (write-tests, `tasks.md` 3.3–3.8; покрытие подтверждено R2)

### F6 — пропажа ключа таймера в Redis теперь глушит живой handle
- **Severity:** minor
- **Location:** `timer_manager_impl.go` (ветка `not_found`)
- **Issue:** раньше локальный handle срабатывал независимо от storage. Теперь при eviction (allkeys-lru), FLUSHDB или failover на отстающую реплику Redis будет `not_found` ⇒ skip, а reconciliation ключа не видит. Group storage имеет failback (`StorageManager`), timer storage — нет (`service_registry.go:1897-1911`). Отличить «удалено обработчиком» от «потеряно» без нового маркера нельзя.
- **Recommendation:** зафиксировать как известный компромисс (Spec Edge Cases + TECH-DEBT).
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` — заводится на finalize

### F7 — транзиентная ошибка в callback рвёт цепочку; двойное срабатывание раньше это маскировало
- **Severity:** minor (предсуществующий, needs-testing)
- **Location:** `manager_impl.go:1866-1873` (`onGroupWaitExpired`: ошибка `storage.Load` ⇒ `return nil`) + cleanup `timer_manager_impl.go:1059-1083`
- **Issue:** при транзиентной ошибке `Load` continuation не заводится, а `onTimerExpired` удаляет запись ⇒ группа больше не нотифицирует. Раньше опоздавшая реплика иногда случайно «повторяла»; теперь она отсеивается по `not_found`.
- **Recommendation:** отдельный баг: в callback различать not-found и транзиентную ошибку; при транзиентной не удалять таймер.
- **Disposition:** defer-bug
- **Follow-up:** `BUGS.md` — заводится на finalize

### F8 — skip виден только в Debug, метрики нет
- **Severity:** minor
- **Location:** `timer_manager_impl.go` (Debug «Timer fire already handled elsewhere»)
- **Issue:** так же, как у ветки «lock занят». Поднимать до Info нельзя: `rescheduled`/`type_changed` — штатный исход каждого HA-срабатывания. Ложный skip в Redis-режиме страхует reconcile (Warn `adopting orphaned group timer`).
- **Recommendation:** счётчик исходов срабатывания `outcome=fired|lock_held|skipped_<reason>|load_error` — отдельной задачей.
- **Disposition:** defer-backlog
- **Follow-up:** `BACKLOG.md` P2 — заводится на finalize

### F9 — устаревшие комментарии про exactly-once; doc-comment `onTimerExpired` оторван от функции
- **Severity:** nit
- **Location:** doc-comment `reconcileOrphanedTimers` (~`:1290-1335`); блок `:758-848` слит с комментарием `dropLocalHandle` (предсуществующее), `fireStillDue` вставлена между ними
- **Recommendation:** одна фраза у reconcile (lock + re-check). `fireStillDue` вынести выше doc-блока `onTimerExpired`, чтобы не усугублять разрыв. Слияние doc-блока с `dropLocalHandle` — предсуществующее, не трогаем.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Status:** fixed (93b686f; подтверждено R2)

### Проверено ревьюерами, проблем нет
- Continuation другого и того же типа.
- Callback, упавший до continuation.
- Ветки `GroupNotFound`, transient `GetGroup`, `gm == nil`.
- `CancelTimer` внутри callback.
- Restore missed и reconcile (`nil`-handle, запись просрочена).
- Restored handle из чужой записи (e2e-ha шаг 4, по коду).
- Lite/одна реплика: ложного пропуска нет.
- Ошибка `LoadTimer` согласована с ошибкой `AcquireLock`.
- JSON round-trip `ExpiresAt` (RFC3339Nano, `Equal`).
- Смешанные версии при rolling upgrade.
- Прошлые fix rounds `7aaba3f`, `e47bcca`, `8791d43`, `7c6c416`, `0171418` не задеты, кроме допущения ветки «lock занят» (F1).
- **Needs-testing:** e2e-ha шаги 3–6 — прогон в `tasks.md` 4.3. **Подтверждено** (testing, 2026-10-10, `deploy/e2e-ha/run.sh` локально, `ALL PASS`): шаг 4 — обе реплики держали таймер, прошла ровно одна публикация; шаг 5 — B подобрала таймер упавшей A; шаг 6 — B одна доставляет ровно один раз.

### Премисы Spec (ревьюер A)
- #1 верна, но класс завышен: это grep + code-read, а не `call-path-traced`. «Фейков нет» неточно: `lockFailingTimerStorage` (`timer_wedge_regression_test.go:168`) встраивает `TimerStorage`, но делегирует `LoadTimer` — безвредно. → исправить класс (в рамках F3).
- #2 частично (F3).
- #3, #4 верны, класс заслужен.
- #5 верна; reconciliation в standard можно выключить (`reconciliation_interval: 0`) — учесть в Risks (в рамках F3).
- #6 measured — формально да.

## Round 2 — Findings

Проверка R1: F1 исправлен (probe ревьюера на копии go-app: две реплики, задержка Redis 0,5 мс, сдвиг часов через обёртку `SaveTimer`, 150 прогонов на сдвиг; при 0, ±1, ±3, ±10 мс — 0 без срабатывания, 0 двойных; мутация «без предпроверки» — 12/60 без срабатывания при +3 мс, 26/60 при +10 мс). F2 исправлен, двойного `Dec` нет (identity guard; после `dropLocalHandle` всегда return). F3 исправлен, F9 — кроме N1. F4/F5 покрыты планом 3.3–3.8; F5d — существующие `TestTimerContinuation_*` на in-memory storage. Edge Case 12 описан верно, нужно было лишь дополнить continuation предыдущего держателя (сделано в `d459a6e`). Гонка предпроверки с той же репликой, reconcile/restore, протухание lock'а посреди callback (как до фикса, страхует nflog), 2 GET на срабатывание — проблем нет. `go test -race ./internal/infrastructure/grouping/...` — ok.

### N1 — doc-comment `fireStillDue` говорит «under the distributed lock»
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/grouping/timer_manager_impl.go:759`
- **Issue:** запись перечитывается и до lock'а.
- **Recommendation:** «before and again under the distributed lock».
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Status:** fixed (d459a6e)

### N2 — Spec: Component Architecture и порядок Edge Cases
- **Severity:** nit
- **Location:** `Spec.md` § Component Architecture, § Edge Cases
- **Issue:** «проверка после `AcquireLock`» при двух проверках; нумерация 10, 12, 13, 11.
- **Recommendation:** синхронизировать.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Status:** fixed (d459a6e)

### N3 — gauge +1, если сработавший handle заменил внешний `StartTimer` во время предпроверки
- **Severity:** nit
- **Location:** `timer_manager_impl.go:441-445` (`StartTimer` заменяет handle без `Dec`), `dropLocalHandle`
- **Issue:** при замене handle'а не из callback (например, `group_wait` пересозданной группы на той же реплике) skip не удаляет чужой handle ⇒ `Inc` старого не компенсирован. Раньше его компенсировал `Dec` полного пути. Корень предсуществующий; на доставку не влияет.
- **Recommendation:** вместе с остальным дрейфом gauge (`gm == nil`/`GroupNotFound` без `Dec`, `nil`-handle `Dec` без `Inc`).
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` — заводится на finalize

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
