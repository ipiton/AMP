# AMP-PG-OPS: проверка мониторинга и отказа встроенного PostgreSQL

Дата: 2026-11-18. Стенд: отдельный k3s `rancher/k3s:v1.35.5-k3s1` в Docker (бинаря `k3d` на машине нет; это тот же k3s, что внутри k3d), один узел, 8 CPU, встроенный контроллер NetworkPolicy. Prometheus Operator: kube-prometheus-stack 55.5.2, как `~55.0.0` в Sema, namespace `sema-monitoring`. AMP собран из этой ветки, `postgres:16.15`, `postgres-exporter:v0.15.0`, лимит postgres 500m как на пилоте, `postgresql.networkPolicy.enabled=true`.

`sema-prod` не читался: запрос к кластеру отклонён правами сессии. Селекторы Prometheus взяты из `infrastructure/helmfile/releases/prometheus-stack-values*.yaml` репозитория Sema.

## 1. ServiceMonitor и NetworkPolicy

| Проверка | Результат |
|---|---|
| `up{namespace="amp"}` | `amp` = 1, `amp-postgres-exporter` = 1 |
| `pg_up` | 1, `job="amp-postgres-exporter"`, `pod="amp-postgres-0"` |
| `count({__name__=~"pg_.*",namespace="amp"})` | 1265 серий |
| Scrape из пода Prometheus, ns `sema-monitoring` (есть в `metricsScrapers`) | HTTP 200 |
| Подключение к 9187 из ns `default` | отклонено: `Connection refused` от NetworkPolicy |

## 2. Правила

Все 10 правил группы `postgresql.rules` загружены, `health=ok`, `state=inactive` на здоровой БД. `promtool check rules` и `promtool test rules` проходят (`helm/amp/tests/render-postgresql-rules.sh`): каждое правило срабатывает на плохом состоянии, здоровая БД и чужие серии `pg_*` молчат, тихая таблица со старым autovacuum не даёт `PostgreSQLVacuumOld`.

| Было | Стало |
|---|---|
| `pg_up == 0` | плюс `absent(pg_up{…})`: exporter — sidecar и исчезает вместе с подом. На стенде при `replicas=0` алерт перешёл в `firing` |
| селекторы без namespace | `namespace`, `job` релиза: в общем Prometheus есть чужие `pg_*` |
| соединения без сопоставления меток | `sum by (namespace, job)` против `max by (namespace, job)` |
| `pg_replication_lag` | правило удалено: репликации нет (ADR-016) |
| `pg_database_age_seconds` | `pg_txid_wraparound_remaining_xids` |
| `pg_stat_statements_mean_exec_time_seconds` | `pg_long_running_queries_duration_seconds > 60` |
| bgwriter без `_total` | имена с `_total`; доля запрошенных checkpoint за 1 час, а не 5 минут |
| `PostgreSQLVacuumOld` по возрасту | только если мёртвых строк больше 1000, таблица за порогом autovacuum и его не было сутки |

Запрос `pg_long_running_queries` группировал и по `query_start`, поэтому две долгие операции давали строки с одинаковыми метками. После правки две параллельные `pg_sleep` дают одну серию `count=2`, scrape отвечает 200.

## 3. Недоступность PostgreSQL

Получатель — webhook в кластере, `group_wait: 1s`. PostgreSQL останавливался через `replicas=0`.

| Сценарий | Приём алертов | Доставка | История |
|---|---|---|---|
| БД пропала при работающем AMP | HTTP 200 | все алерты дошли, `notifications_failed` не растёт | запись падает, `amp_upstream_history_writes_total{outcome="error"}` растёт, строк нет |
| БД вернулась, AMP не перезапускался | HTTP 200 | работает | пишется снова без рестарта |
| Повторная отправка алерта, потерянного в простое | HTTP 200 | по правилам группировки | строка появилась |
| AMP перезапущен при лежащей БД | **не работает 178 с**: под в `wait-for-postgres` | **нет на это время** | — |
| AMP стартовал без БД, потом БД вернулась | HTTP 200 | работает | **выключена до рестарта AMP** |
| После рестарта AMP с живой БД | HTTP 200 | работает | пишется |

`/-/ready` и `/-/healthy` отвечают 200 при лежащей БД. Утверждение README верно только для уже работающего процесса, текст уточнён. Заведён BUGS `AMP-START-BLOCKED-BY-PG`; поведение без переподключения описано в `UPSTREAM-HISTORY-PG-RECONNECT`.

## 4. CPU-throttling

Дельта `cpu.stat` контейнера postgres за 90 с, ServiceMonitor и AMP работают.

| Вариант | CPU | Throttled-периоды |
|---|---:|---:|
| Пробы чарта, лимит 500m | 42,3m; повтор 43,8m | 32,5%; повтор 28,8% |
| Readiness только `pg_isready`, 500m | 37,1m | 17,0% |
| Без readiness и liveness, 500m | 8,1m | 4,8% |
| `tcpSocket` readiness и liveness, 500m | 8,2m | 0% |
| Пробы чарта, 1000m | 39,0m | 6,3% |
| Пробы чарта, 2000m | 53,8m | 0% |

Причина на стенде — exec-пробы: сам запуск процессов в контейнере даёт около 80% CPU и короткие всплески, упирающиеся в квоту 50 мс на период. Убрать только `psql` недостаточно. Число `3,7 периода в секунду` не следует из 37%: `nr_periods` считает лишь периоды с активностью, на стенде их 1,2–2,2 в секунду. Цифры пилота (37,7m, 37%) совпадают по порядку, но на `sema-prod` это не проверено. Запись: BUGS `PG-EXEC-PROBES-CPU-THROTTLING`.
