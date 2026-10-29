# Testing — INVESTIGATION-PROMPT-ISOLATION

Дата: 2026-10-29, ветка `bugfix/investigation-prompt-isolation` @ `2ad0aef`.

## Затронутые пакеты

- `go vet` по `core/investigation`, `infrastructure/llm`, `infrastructure/investigation/...`, `config`, `application` — чисто.
- `go test -race -count=1` по тем же пакетам — PASS (9 пакетов, включая `runbook(s)` и `application/handlers`).
- `git diff --check` — чисто; новых `_, _ :=` в коде задачи нет.

## make -C go-app quality-gates-fast

PASS (exit 0), `git status` после прогона — без посторонних изменений.

## scripts/release-gate.sh

`RESULT: FAIL`, оба красных шага — известные и не связаны с диффом:

| Шаг | Причина | Запись |
|---|---|---|
| `lint` | 7 × errcheck на приёме из канала в `publishing/delivery_confirmation_test.go` и `silencing/redis_event_bus_test.go` — файлы задачей не тронуты | `BUGS.md` `LINT-ERRCHECK-CHANNEL-RECEIVE` |
| `test` | `TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica`: `continuation timer type = "group_wait", want "group_interval"`. Отдельно `-count=5` — 5/5 PASS | `BUGS.md` `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY` (superseded ADR-019, срез 4) |

Остальные шаги PASS: `build`, `futureparity`, `upstream-core`, `race`, `helm-*`, `amtool-compat` (в этот раз порт 18080 был свободен).
