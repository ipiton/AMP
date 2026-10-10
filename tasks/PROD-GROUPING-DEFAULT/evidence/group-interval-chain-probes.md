# Evidence: одноразовые прогоны цепочки `group_interval` и notify-chain

Дата: 2026-10-10. Способ: `go test -overlay` (тестовый файл лежит вне дерева, в репозиторий не добавлялся), пакет `internal/infrastructure/grouping`, хелперы `createTestManagerWithPublisher` + `mockPublisher`, callback'и таймеров вызываются напрямую. Это не замена тестам из фазы 4 — их пишет `write-tests`.

## F1 — поздний алерт уходит на следующем `group_interval` (дерево `c344711`)

```
timer after unchanged group_interval fire: group_interval, publishes=1
timer pending for the late alert: group_interval (10ms)
publishes after next group_interval fire: 2
--- PASS: TestF1Probe_LateAlertWaitsGroupIntervalNotRepeat
```

## G1, G2, G3 (дерево `daab077`; `group_wait` = `group_interval` = `repeat_interval` = 1h, чтобы фоновые таймеры не влияли)

```
publishes after 3 more flushes of the shrunk set: 2
--- PASS: TestR2Probe_G1_ShrunkSetIsNotResent
re-armed timer: group_wait
--- PASS: TestR2Probe_G2_GroupWithoutTimerIsRearmed
publishes after 3 flushes with nflog failing: 1
--- PASS: TestR2Probe_G3_NflogErrorDoesNotResend
```

- G1: `[A,B firing]` → 1 публикация; B resolved → 2-я; три следующих flush остатка `[A]` → по-прежнему 2; B снова firing → 3-я.
- G2: таймер группы отменён (`HasTimer` = false); следующий `AddAlertToGroup` ставит `group_wait`; ещё один `AddAlertToGroup` существующий таймер не перезапускает (`StartedAt` тот же).
- G3: после первой отправки `IsDuplicate` всегда возвращает ошибку; три flush → публикаций 1; новый алерт в группе → 2-я публикация.

## Не покрыто этими прогонами

Redis-реализации (`RedisNotifyLog`, `RedisTimerStorage`), две реплики, реальный `PublishingCoordinator` с очередью, живой бинарь.

## R3 fix round (рабочее дерево поверх `6594d28`, 2026-10-10; `go test -overlay -race`)

```
=== RUN   TestR4_H1_NoResolveTarget
    r4_probe_test.go:118: H1: notifications after partial resolve: 1; group size: 1
    r4_probe_test.go:129: H1: after re-fire and a new alert: 2
--- PASS: TestR4_H1_NoResolveTarget (0.00s)
=== RUN   TestR4_ResolveTarget
    r4_probe_test.go:146: resolve target: notifications=2 size=1
--- PASS: TestR4_ResolveTarget (0.00s)
=== RUN   TestR4_H6_CoveredResolvedIsPruned
    r4_probe_test.go:165: H6: notifications=0 size=1
--- PASS: TestR4_H6_CoveredResolvedIsPruned (0.00s)
=== RUN   TestR4_NoConsultNoPrune
--- PASS: TestR4_NoConsultNoPrune (0.00s)
=== RUN   TestR4_ResilientLog
    r4_probe_test.go:197: wrapper: notifications with reads failing: 2
    r4_probe_test.go:214: wrapper: local entries before sweep=101 after=0
--- PASS: TestR4_ResilientLog (0.00s)
=== RUN   TestR4_HA_Race
    r4_probe_test.go:262: T5: B observed holding a local timer 0 times; started A=75 B=0; expired A=74 B=0; notifications=1
--- PASS: TestR4_HA_Race (1.50s)
PASS
ok  	github.com/ipiton/AMP/internal/infrastructure/grouping	3.077s
```

- H1: target с `send_resolved: false`; `[a,b firing]` → 1 нотификация; `a` resolved → по-прежнему 1, в группе остался 1 алерт (`a` удалён); `a` снова firing → 1 (получатель о resolve не знал; проверяется утверждением в пробе, отдельной строки в выводе нет); новый `c` → 2.
- Target с `send_resolved: true`: resolve уходит один раз (2), удаляется, три следующих flush ничего не шлют.
- H6: запись nflog уже покрывает `a:resolved|b:firing`, а `a` ещё в группе → flush без отправки удаляет `a`.
- Publisher, который никого не опрашивает (metrics-only): resolved не удаляется.
- `resilientNotifyLog`: после своей отправки три flush при падающем чтении → публикаций 1; новый алерт → 2; свежая обёртка без локальной записи — fail-open (утверждение в пробе, строки в выводе нет); 101 локальная запись → 0 после прохода вытеснения.
- H8: две «реплики» на общих in-memory storage, ingest через не-владельца во время 74 срабатываний таймера, под `-race` — отчётов о гонке нет (в R3 на этом же сценарии было 4).

Публикация эмулирует порядок вызовов coordinator'а (фильтр `send_resolved` → `targetAlerts`); реальный `PublishingCoordinator` — в тестах фазы 4.

Файл проб: `evidence/r4-probes.go.txt`. Запуск: скопировать как `go-app/internal/infrastructure/grouping/r4_probe_test.go` (или подложить через `go test -overlay`), затем `cd go-app && go test -race -count=1 -run 'TestR4|TestR5' -v ./internal/infrastructure/grouping/`.

## R4 fix round (код коммита `fix(grouping): keep resolved alerts while the notification log is unreadable`, 2026-10-10; `go test -overlay -race`)

```
=== RUN   TestR4_H1_NoResolveTarget
    r4_probe_test.go:118: H1: notifications after partial resolve: 1; group size: 1
    r4_probe_test.go:129: H1: after re-fire and a new alert: 2
--- PASS: TestR4_H1_NoResolveTarget (0.01s)
=== RUN   TestR4_ResolveTarget
    r4_probe_test.go:146: resolve target: notifications=2 size=1
--- PASS: TestR4_ResolveTarget (0.00s)
=== RUN   TestR4_H6_CoveredResolvedIsPruned
    r4_probe_test.go:165: H6: notifications=0 size=1
--- PASS: TestR4_H6_CoveredResolvedIsPruned (0.00s)
=== RUN   TestR4_NoConsultNoPrune
--- PASS: TestR4_NoConsultNoPrune (0.00s)
=== RUN   TestR4_ResilientLog
    r4_probe_test.go:197: wrapper: notifications with reads failing: 2
    r4_probe_test.go:214: wrapper: local entries before sweep=101 after=0
--- PASS: TestR4_ResilientLog (0.00s)
=== RUN   TestR4_HA_Race
    r4_probe_test.go:262: T5: B observed holding a local timer 0 times; expired A=71 B=0; notifications=1
--- PASS: TestR4_HA_Race (1.50s)
=== RUN   TestR5_K1_LocalAnswerDoesNotPrune
    r4_probe_test.go:289: K1: outage: notifications=1 size=2
    r4_probe_test.go:295: K1: reads back: notifications=2 size=1
--- PASS: TestR5_K1_LocalAnswerDoesNotPrune (0.00s)
=== RUN   TestR5_K5_PruneKeepsRefiredAlert
    r4_probe_test.go:317: K5: re-fired alert kept, resolved one pruned
--- PASS: TestR5_K5_PruneKeepsRefiredAlert (0.00s)
PASS
ok  	github.com/ipiton/AMP/internal/infrastructure/grouping	3.080s
```

- K1: своя отправка `[a,b firing]` записана; чтение журнала падает, `a` resolved → flush ничего не шлёт (нотификаций 1) и `a` остаётся в группе (размер 2); чтение восстановлено → resolve уходит (2), `a` удалён (размер 1). До правки на первом шаге `a` удалялся, и resolve не уходил никогда.
- K5: алерт стал firing между отправкой и prune → остаётся в группе; соседний resolved удалён.
- `TestR4_HA_Race`: 10 прогонов подряд под `-race` без отчётов о гонке (после замены `len(group.Alerts)` на `alertCount` в callback'ах).
