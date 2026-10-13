# Smoke: реальный бинарь, реальный webhook (2026-10-13)

Скрипт: `evidence/smoke-filter.sh`. Стенд `deploy/smoke/` (lite, `docker compose up --build` из рабочего дерева ветки на `d2b02b1`), webhook-приёмник `webhook_receiver.py`. Конфиг копируется во временный каталог, файлы репозитория не меняются. Ожидание после каждого POST — 8 с: `group_wait` у корня 3 с.

## A. Стоковый `deploy/smoke/config.yaml` — алерты «тестового вида» доставляются

| POST `/api/v2/alerts` | Код | Попаданий на webhook | `alertname` в payload |
|---|---|---|---|
| `TestRunnerDown`, `namespace=tmp`, `environment=testing` | 200 | 1 | `TestRunnerDown` |
| `testlowercase`, `environment=test` | 200 | 1 | `testlowercase` |
| `SandboxDiskFull`, `namespace=dev-sandbox` | 200 | 1 | `SandboxDiskFull` |

В `/metrics` нет серий `alert_history_filter_blocked_total{reason="test_alert"}` и `{reason="disabled_namespace"}`.

## B. Конфиг с blackhole-route из migration note (`CHANGELOG.md` § Breaking changes)

К стоковому конфигу дописаны три дочерних route на `blackhole` и пустой receiver `- name: blackhole`, дословно как в migration note, включая `alertname=~"(?i)test.*"`.

Конфиг загружается при старте, в логе есть W024:

```
{"level":"WARN","msg":"configvalidator warning","code":"W024","section":"receivers","field":"receivers[1]","message":"receiver 'blackhole' has no integrations configured; alerts routed here are dropped (blackhole receiver)"}
```

| POST | Код | Попаданий на webhook |
|---|---|---|
| `TestRunnerDown2`, `namespace=prod` | 200 | 0 |
| `testLowerCase2`, `namespace=prod` (проверка `(?i)`) | 200 | 0 |
| `EnvOnlyAlert`, `environment=testing` | 200 | 0 |
| `NsOnlyAlert`, `namespace=dev-sandbox` | 200 | 0 |
| `ProdDiskFull`, `namespace=prod`, `environment=production` (контроль) | 200 | 1 |

На отдельном прогоне B по `GET /api/v2/alerts/groups` все четыре группы стоят на `receiver: blackhole`. Дождавшись срабатывания групп, `alert_history_publishing_blackhole_drops_total{receiver="blackhole"}` = 4, на webhook — 0.

Итог скрипта: `TOTAL FAILS: 0`.

## Находка, вне scope задачи: дочерний route не наследует тайминги

На прогоне B blackhole-группы срабатывали через 30 с, а не через 3 с, как задано у корня. Лог: `"Alert routed to group" ... "receiver":"blackhole","group_wait":30000000000`, следующий таймер — `group_interval` 5m. Причина: `grouping/parser.go` `applyRouteDefaults` рекурсивно вызывает `Route.Defaults()` и проставляет 30s/5m/4h каждому вложенному route. В итоге `TreeBuilder.inheritDuration` (`business/routing/tree_builder.go`) никогда не видит «не задано» и не берёт значение родителя. В upstream дочерний route наследует `group_wait`/`group_interval`/`repeat_interval` от родителя. Ошибка существует с первого коммита (`d096644`), эта задача её не вносила. Заводится в BUGS как `ROUTE-TIMINGS-NOT-INHERITED` (tasks.md 5.4).

На migration note это не влияет: дроп происходит, только позже на `group_wait`. Алерт при этом не доставляется.
