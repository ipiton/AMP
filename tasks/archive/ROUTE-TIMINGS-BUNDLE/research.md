# Research: ROUTE-TIMINGS-BUNDLE

Level: light (2). Grounded в коде ветки на `28007e8` и upstream `github.com/prometheus/alertmanager@v0.32.0` (модульный кэш).

## TL;DR

Источник правды для таймингов группы — дерево маршрутов (`TreeBuilder.buildNode` → `RouteNode` → `RoutingDecision` → `grouping.WithGroupTimings` → `GroupMetadata.Timings`). Ломают его два слоя: парсер дефолтит каждый route, а дерево и таймеры считают `0` «не задано». Чиним по upstream: парсер не дефолтит route вообще, дерево различает `nil` и `0`, `group_wait: 0` проходит до таймера, который срабатывает сразу.

## Questions

1. Где тайминги route дефолтятся, наследуются, валидируются?
2. Кто опирается на `Route.Defaults()` у вложенных route?
3. Что делает upstream с `nil`/`0` у `group_wait`/`group_interval`/`repeat_interval`?
4. Как пройти `group_wait: 0` через grouping-таймеры, не сломав HA/Redis?

## Findings

### Карта мест

| Слой | Место | Что делает |
|---|---|---|
| Прод-парсер | `infrastructure/routing/parser.go:298` `applyDefaults` → `:856` `applyRouteDefaultsRecursive` | `Route.Defaults()` на каждом route (30s/5m/4h). Прод-вход: `config/config.go:797` `infraroute.NewRouteConfigParser().Parse`. Валидации таймингов route здесь нет. |
| Grouping-парсер | `grouping/parser.go:76,235` `applyRouteDefaults` | То же. `grouping.NewParser` в проде не вызывается (только тесты); `validateGroupWaitRange` допускает `0`. |
| Defaults | `grouping/config.go:156` `Route.Defaults()` | Других вызывающих, кроме двух парсеров, нет. `GetEffective*` (nil → 30s/5m/4h) — `String()` и `manager_impl.go:1143`. |
| Дерево | `business/routing/tree_builder.go:212-214,501-541` `inheritDuration` | route `> 0` → parent `> 0` → `global.*` `> 0` → 30s/5m/4h. `durationOrZero` стирает разницу `nil`/`0`. |
| Валидация дерева | `business/routing/tree.go:493` `validateDurations` | Все три `<= 0` — ошибка; плюс AMP-only `group_interval >= group_wait`. Вызывается на `Build` (`ValidateOnBuild`, старт — `service_registry.go:1391`) и в `RouteTreeManager.Reload` (`tree_manager.go:156`, hot reload). |
| Решение | `evaluator.go:235,450` | `RoutingDecision` копирует тайминги узла. |
| Grouping | `core/services/alert_processor.go:487` | Единственный продюсер `WithGroupTimings` — значения из решения, т.е. всегда разрешённые деревом. |
| Таймер group_wait | `grouping/manager_impl.go:882-895` `startGroupWaitTimer` | root `config.Route.GroupWait` → override только при `timings.GroupWait > 0`. |
| Таймер interval / repeat | `manager_impl.go:970-980`, `:1140-1147` | override при `> 0`; для них `0` невалиден, так что «`0` = не задано» корректно. |
| Порядок в `AddAlertToGroup` | `manager_impl.go:282` vs `:307` | `startGroupWaitTimer` вызывается **до** добавления алерта и `storage.Store`. При нулевом таймере callback может прочитать группу раньше записи (`not found` → цепочка не продолжается). |
| TimerManager | `timer_manager_impl.go:425`, `timer_models.go:265` | `StartTimer` и `GroupTimer.Validate` (её зовут memory и Redis `SaveTimer`) отвергают `duration <= 0`. Redis TTL: `time.Until(expires)+grace`, при `<=0` — `grace`; индекс по `ExpiresAt` — нулевой таймер просто «уже просрочен», reconciliation его подберёт. |
| Grouping-конфиг менеджера | `config/grouping_adapter.go:34` | `GroupingConfig{Route: c.Routing.Route}` — корень прод-парсера. Без дефолтов в парсере корень может иметь `nil` — `startGroupWaitTimer`/`startGroupIntervalTimer`/`effectiveRepeatInterval` уже обрабатывают `nil` (30s/5m/4h). В проде этот фолбэк не используется: `alert_processor` всегда передаёт тайминги. |
| `/api/v2/status` | `application/handlers/status_config.go:128` | `yaml.Marshal(cfg.Routing)` — сериализует распарсенную структуру ⇒ дефолты парсера видны как будто заданные в конфиге. |
| Персистентность | `grouping/manager.go:168` `GroupTimings` (`json:"...,omitempty"`) | `0` не пишется, читается как `0`. |

### Upstream (`alertmanager@v0.32.0`)

- `dispatch/route.go:68-99` `newRoute`: `opts := DefaultRouteOpts; if parent != nil { opts = parent.RouteOpts }`, затем `if cr.GroupWait != nil { opts.GroupWait = ... }` (то же для interval/repeat). Дефолты — только у корня (через `DefaultRouteOpts`), дети наследуют разрешённые значения родителя, включая `0`.
- `config/config.go:914-919` `Route.UnmarshalYAML`: `group_interval cannot be zero`, `repeat_interval cannot be zero`. `group_wait: 0` допустим. Конфиг-структура дефолтами не заполняется — `config.original` показывает только заданное.
- Ограничения `group_interval >= group_wait` в upstream нет.

### Следствия

- `global.*`-фолбэк FU-GLOB-DEFAULT-VALUES в прод-пути не работает **даже для корня**: парсер успевает поставить корню 30s/5m/4h. Тесты `global_inheritance_test.go` строят `TreeBuilder` напрямую, мимо парсера.
- Правильное наследование делает достижимой AMP-only проверку `group_interval >= group_wait`: корень `group_wait: 1m`, ребёнок `group_interval: 30s` — раньше ребёнок получал дефолтный `group_wait` 30s и проходил, теперь наследует 1m и упадёт при загрузке. Upstream такой конфиг принимает ⇒ проверку надо убрать.

## Options

Generation: single-agent (решение не дорогое в откате; fan-out не нужен).

**Дерево / парсер**

- A. Парсер дефолтит только корень; дерево различает `nil`/`0`. Минус: `global.*` у корня по-прежнему не срабатывает, `config.original` показывает дефолты корня.
- B. **Парсер не дефолтит route вообще; дерево разрешает own → parent → global → built-in, различая `nil`/`0`.** Совпадает с upstream (дефолты в `DefaultRouteOpts`, а не в конфиге), чинит `global.*` и `config.original`. `grouping.DefaultParser` (не прод) — так же, для единообразия; его потребитель `DefaultGroupManager` уже обрабатывает `nil`.

**Нулевой group_wait в grouping**

- C. Обходить таймер: при `0` сразу вызывать flush из `AddAlertToGroup`. Минус: мимо пути таймера — распределённой блокировки, nflog-последовательности, HA-владения; второй код-путь.
- D. **Разрешить таймер длительностью `0` только для `GroupWaitTimer`** (`StartTimer`, `GroupTimer.Validate`), `startGroupWaitTimer` берёт `timings.GroupWait` как есть при `timings != nil`, а старт таймера новой группы перенести после `storage.Store`. Срабатывание идёт обычным путём (`time.NewTimer(0)` → `handleTimerExpiration` → callback с локом), как в upstream (`time.NewTimer(0)` в `aggrGroup.run`). `group_interval`/`repeat_interval` нулевыми не бывают — `0` для них по-прежнему ошибка, бесконечного цикла перевзвода не возникает.

## Decision

B + D. Плюс убрать AMP-only `group_interval >= group_wait` из `validateDurations`; `group_wait < 0` — ошибка, `group_interval`/`repeat_interval <= 0` — ошибка (аналог upstream `cannot be zero`, через тот же `Build`/`Reload`).

## Spec Inputs

- `inheritDuration(parent, *grouping.Duration, field)`: `!= nil` → значение; parent → значение родителя (уже разрешённое, `0` допустим); `global.*` `!= nil` → значение; иначе built-in.
- `GroupTimings` — всегда полностью разрешённые тайминги route; `GroupWait == 0` = «сразу». Для interval/repeat `0` остаётся «нет override».
- Миграции нет: сохранённые группы имели `GroupWait > 0`.

## References

- `go-app/internal/business/routing/tree_builder.go`, `tree.go`, `tree_manager.go`
- `go-app/internal/infrastructure/routing/parser.go`, `grouping/parser.go`, `grouping/config.go`
- `go-app/internal/infrastructure/grouping/manager_impl.go`, `timer_manager_impl.go`, `timer_models.go`, `redis_timer_storage.go`
- `go-app/internal/application/handlers/status_config.go`
- upstream `dispatch/route.go`, `config/config.go` (v0.32.0)
