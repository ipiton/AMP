---
id: ROUTE-TIMINGS-BUNDLE
slug: route-timings
stream: Routing
type: bug
status: approved
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - research.md
---

# Specification: наследование таймингов route как в upstream

**Version:** 1.0  
**Status:** Approved

## Summary

Тайминги route разрешаются только деревом маршрутов и только по правилу upstream: свой (если задан, включая `0`) → родителя → `global.*` (AMP-only) → 30s/5m/4h. Парсеры перестают дефолтить route; `group_wait: 0` проходит до grouping-таймера, который срабатывает сразу.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Ребёнок/внук наследуют ближайшие заданные тайминги | D1, D2; тест дерева через прод-парсер |
| `group_wait: 0s` → без задержки | D2, D3, D4; тесты дерева, `startGroupWaitTimer`, memory/Redis-хранилище таймеров |
| Явные `group_interval`/`repeat_interval: 0s` отвергаются | D3; тест `Build` |
| `global.*` работает в прод-пути | D1, D2; тест через прод-парсер |
| `config.original` без лишних полей | D1; тест `AlertmanagerConfigYAML` |
| Hot reload / HA не регрессируют | D2 общий для `Build` и `Reload`; D4 через обычный путь таймера; прогон пакетов grouping/routing |
| Smoke на бинаре | `evidence/smoke.md` |

## Current State

- **Code:** см. карту в `research.md` § Findings.
- **Data:** `GroupMetadata.Timings` в memory/Redis (`json`, `omitempty`); таймеры в Redis (`GroupTimer`, `Duration`, `ExpiresAt`).
- **Tests:** `business/routing/global_inheritance_test.go`, `tree_builder_test.go`; `grouping/manager_test.go`, `timer_*_test.go`; `application/handlers/status_*_test.go`.
- **Docs:** `docs/ALERTMANAGER_COMPATIBILITY.md` строка «Per-route group_wait/...», § known differences п. 7 (`global.*`).

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `Route.Defaults()` вызывают только два парсера | `grep -rn 'Defaults()'` по `go-app` (не тесты) | call-path-traced | кто-то читает тайминги вложенного route напрямую и получит `nil` |
| Тайминги прочитанного конфига вне дерева читают только `DefaultGroupManager` (корень, `nil` уже обработан) и `/api/v2/status` | `grep` `Route.GroupWait\|GetEffective*` по `go-app` | call-path-traced | паника на `nil` или иной дефолт в стороннем потребителе |
| Единственный продюсер `GroupTimings` — `alert_processor.go:487` из `RoutingDecision` (значения разрешены деревом) | `grep 'WithGroupTimings('` | call-path-traced | частичный `GroupTimings` с `GroupWait == 0` «не задан» начнёт слать сразу |
| Сохранённые до фикса группы имеют `GroupWait > 0` | `inheritDuration` до фикса всегда возвращал `> 0` | code-read | старая группа с `0` при re-arm сработает сразу — для «уже просроченной» группы это и так ожидаемо |
| Дерево строится и валидируется одинаково при старте и hot reload | `service_registry.go:1391`, `tree_manager.go:145-156` | code-read | расхождение старт/reload |
| `time.NewTimer(0)` и Redis-индекс по `ExpiresAt` корректно обрабатывают нулевую длительность | `timer_manager_impl.go:480`, `redis_timer_storage.go:245-260` | code-read | нулевой таймер не сработает либо не сохранится |

## Target Design

**D1. Парсеры не дефолтят тайминги route.** `infrastructure/routing/parser.go` `applyDefaults` больше не вызывает `applyRouteDefaultsRecursive` (функция удаляется). `grouping/parser.go` `applyRouteDefaults` — удаляется аналогично (единообразие; потребитель уже обрабатывает `nil`). `Route.Defaults()` остаётся как API, не вызывается парсерами. Распарсенный конфиг — ровно то, что написано, как в upstream.

**D2. Дерево различает «не задан» и `0`.** `inheritDuration(parent, *grouping.Duration, field)`: `route != nil` → `route.Duration`; `parent != nil` → разрешённое значение родителя (как есть); `global.<field> != nil` → оно; иначе 30s/5m/4h. `durationOrZero` удаляется. Корень наследует от `global.*`, потомки — через корень.

**D3. Валидация дерева как в upstream.** `validateDurations`: `group_wait < 0` — ошибка; `group_interval <= 0`, `repeat_interval <= 0` — ошибка (аналог upstream `cannot be zero`); AMP-only проверка `group_interval >= group_wait` удаляется (upstream её не делает, а правильное наследование сделало бы её достижимой для валидных upstream-конфигов).

**D4. Нулевой group_wait в grouping.**
- `startGroupWaitTimer`: при `timings != nil` берёт `timings.GroupWait` как есть. `GroupTimings` документируется как полностью разрешённые тайминги route: `GroupWait == 0` — «сразу»; для interval/repeat `0` — «нет override» (нулём они быть не могут).
- `StartTimer` и `GroupTimer.Validate`: длительность `0` допустима только для `GroupWaitTimer`; отрицательная — всегда ошибка; `0` для interval/repeat — ошибка (защита от цикла перевзвода).
- `AddAlertToGroup`: таймер новой группы стартует после добавления алерта и `storage.Store`, чтобы нулевой таймер не прочитал группу раньше записи.

## API Contracts

Внешние API не меняются. `/api/v2/status` `config.original` перестаёт показывать `group_wait`/`group_interval`/`repeat_interval`, которых нет в конфиге.

## Data Model / Migrations

- **Migration:** нет. `GroupTimer.Duration == 0` теперь допустим для `group_wait`; старые записи остаются валидными.
- **Rollback / forward-fix:** откат коммита. Нулевой `group_wait`-таймер в Redis при откате на старый бинарь не пройдёт `Validate` при `RestoreTimers` — таймер «уже просрочен», группа подхватится `ensureGroupTimer` на следующем алерте; окно — секунды.

## Impact Analysis

- Конфиги с вложенными route без таймингов меняют поведение: берут тайминги предка (это и есть фикс).
- Конфиги с явным `group_interval: 0s`/`repeat_interval: 0s` раньше грузились (тихо наследовали), теперь не загружаются — как upstream. Запись в CHANGELOG.
- Конфиги, которые падали только на AMP-only `group_interval >= group_wait`, теперь грузятся.
- `config.original` меньше полей.

## Deep Review

Discretionary-running: дифф >200 строк ожидается, `X R`, путь таймеров HA, запрошено владельцем. Линзы: correctness, premises, `X` (инварианты между деревом и grouping), `R` (поведение таймеров, rollback), maintainability.

## Open Questions

Нет.
