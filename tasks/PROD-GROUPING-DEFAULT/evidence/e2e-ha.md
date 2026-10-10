# Evidence: `deploy/e2e-ha/run.sh`

Дата: 2026-10-10. Дерево: код fix-раунда R4 (коммит `fix(grouping): keep resolved alerts while the notification log is unreadable` поверх `5df0a32`; прогон сделан на рабочем дереве с этим кодом до коммита). Docker, две реплики AMP + Redis + Postgres, `group_wait` 8s, `group_interval` 30s, `repeat_interval` 1h, publisher — metrics-only.

```
[e2e-ha] building + starting stack (redis, postgres, amp-a, amp-b)
[e2e-ha] waiting for both replicas' /healthz
[e2e-ha] both replicas healthy
[e2e-ha] waiting for cluster heartbeat convergence (both replicas see 2 peers), using jq
[e2e-ha] PASS: both replicas report cluster.status=ready with exactly 2 peers
[e2e-ha] posting alert 'E2EHaTestAlertOne' to replica A
[e2e-ha] waiting group_wait + margin (14s)
[e2e-ha] PASS: exactly one replica published 'E2EHaTestAlertOne' (metrics-only log line, a=1 b=0)
[e2e-ha] PASS: exactly one replica reached the publish step for 'E2EHaTestAlertOne' (a=1 b=0)
[e2e-ha] PASS: no nflog:entry recorded for 'E2EHaTestAlertOne' -- metrics-only publish did not poison the shared dedup log
[e2e-ha] restarting replica B so RestoreTimers arms a local timer for 'E2EHaTestAlertOne' on BOTH replicas
[e2e-ha] replica B restored timers from shared Redis: amp-b-1  | {"time":"2026-10-10T17:33:09.456306005Z","level":"INFO","msg":"Timer restoration completed","restored":1,"missed":0}
[e2e-ha] polling (up to 45 times) for the group_interval fire both replicas' timers for 'E2EHaTestAlertOne' race for
[e2e-ha] PASS: both replicas held a timer for 'E2EHaTestAlertOne' and exactly one publish got through (a=2 b=0)
[e2e-ha] posting alert 'E2EHaTestAlertAdopted' to replica A, then killing A before group_wait expires
[e2e-ha] replica A killed
[e2e-ha] waiting for replica B's reconciliation loop to adopt the orphaned timer (20s)
[e2e-ha] PASS: replica B adopted the dead replica's in-flight timer and published 'E2EHaTestAlertAdopted'
[e2e-ha] posting alert 'E2EHaTestAlertTwo' to replica B (replica A is dead)
[e2e-ha] PASS: replica B alone still delivers exactly once after replica A's death (failover)
[e2e-ha] ALL PASS
[e2e-ha] tearing down stack
exit=0
```

Что это подтверждает: Redis-таймеры и `TryClaim` (сквозной метод обёртки `resilientNotifyLog` над `RedisNotifyLog`) работают на двух репликах; после рестарта B восстановила таймер группы из Redis, и за следующий `group_interval` публикация прошла одна (шаг 4: перед опросом проверяется, что публикаций ровно одна, иначе сценарий падает как холостой); осиротевший таймер подхватывается (шаг 5); metrics-only публикация не пишет запись nflog.

Чего не подтверждает: `IsDuplicate`, `RecordSent` и ответ обёртки из локальной памяти — metrics-only publisher не вызывает `targetAlerts`, эти методы в прогоне не выполнялись; одновременность срабатывания таймеров на обеих репликах (видно только, что таймер был у обеих и публикация одна); реальную доставку получателю; поведение при недоступном Redis; rolling upgrade со смешанными версиями.
