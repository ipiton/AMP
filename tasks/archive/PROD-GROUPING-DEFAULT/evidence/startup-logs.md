# Стартовые логи бинаря: три варианта конфига

- Дата: 2026-10-10, ветка `bugfix/prod-grouping-default`, бинарь собран на `83c3d12`; следующий коммит `4fc7300` меняет только тесты.
- Бинарь: `go build ./cmd/server`, запуск локально, `profile: lite`, `ENVIRONMENT=development`, `AMP_CONFIG_FILE=<конфиг>`; через ~6 с процесс останавливался.
- Конфиги отличаются только секциями `grouping:` и `route:`/`receivers:`.

## (а) `route:` есть, ключа `grouping:` нет (дефолт)

```text
INFO  Initializing grouping subsystem...
INFO  Grouping subsystem using in-memory storage
INFO  Timer manager initialized
INFO  Grouping subsystem initialized
```

Группировка поднялась без ключа в конфиге. Предупреждений про grouping нет.

## (б) `route:` есть, `grouping.enabled: false`

```text
WARN  Grouping is DISABLED (grouping.enabled=false) but a route: tree is configured: group_by/group_wait/group_interval/repeat_interval are ignored and every alert is published immediately
```

Строка встречается в логе ровно один раз; «Initializing grouping subsystem...» нет.

## (в) `route:` нет, ключа `grouping:` нет

```text
INFO  Grouping subsystem not started: no route: tree configured, alerts are published directly
```

Уровень INFO, предупреждения про grouping нет.

## Наблюдение вне задачи

Во всех трёх запусках есть одна строка уровня ERROR, не связанная с группировкой:

```text
ERROR Failed to connect to Redis  error="dial tcp [::1]:6379: connect: connection refused"
WARN  Redis cache unavailable, falling back to in-memory cache
```

`lite` без Redis стучится в `localhost:6379` и пишет ERROR перед штатным откатом на in-memory. Поведение не менялось этой задачей; заносится в follow-ups на `finalize`.
