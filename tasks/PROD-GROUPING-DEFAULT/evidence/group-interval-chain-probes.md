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
