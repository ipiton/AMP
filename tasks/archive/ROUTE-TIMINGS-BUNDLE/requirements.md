---
id: ROUTE-TIMINGS-BUNDLE
slug: route-timings
stream: Routing
type: bug
priority: high
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Requirements: наследование таймингов route как в upstream

Бандл (`docs/solo-kanban/artifact-contract.md` § Bundles) из двух записей `BUGS.md` одного механизма:

1. `ROUTE-TIMINGS-NOT-INHERITED` (high) — вложенный route без своих таймингов получает 30s/5m/4h вместо таймингов родителя.
2. `ROUTE-EXPLICIT-ZERO-GROUP-WAIT` (medium) — явный `group_wait: 0s` трактуется как «не задан» и наследуется.

Полные Problem/Impact/Refs — в записях `BUGS.md`; здесь не дублируются.

## Problem Framing

- **Symptom:** verbatim `alertmanager.yml` с таймингами у корня и дочерними route без своих таймингов нотифицирует через 30s и повторяет по 4h; route `signal="panic"` с `group_wait: 0s` под корнем `10s` нотифицирует через 10s (sema-prod, проверка 2026-10-20).
- **Root Cause:** (1) прод-парсер `infrastructure/routing/parser.go` `applyRouteDefaultsRecursive` (и его копия `grouping/parser.go` `applyRouteDefaults`) вызывает `Route.Defaults()` на каждом route ⇒ у вложенных «не задано» не бывает; (2) `TreeBuilder.inheritDuration` считает «задано» только `> 0`, `validateDurations` запрещает `group_wait <= 0`; (3) grouping-путь: `startGroupWaitTimer` берёт override только при `> 0`, `StartTimer`/`GroupTimer.Validate` отвергают `duration <= 0`.
- **Why Now:** предпосылка запуска рядом с Alertmanager в sema-prod в режиме параллельных нотификаций — расхождение по времени отправки видно сразу.
- **How We Measure:** тесты дерева/таймера (ниже) и smoke на бинаре: корень `group_wait: 3s`/`group_interval: 10s`, ребёнок без таймингов, ребёнок с `group_wait: 0s` — лог `"Alert routed to group"` и время прихода в webhook-приёмник.

## Risk Profile

- **Signals:** `X R`
  - `X` — `business/routing`, `infrastructure/routing`, `infrastructure/grouping` (дерево, парсер, таймеры).
  - `R` — меняется время отправки нотификаций у существующих конфигов; путь таймеров в HA/Redis.
- **Tier:** Full
- **Notes:** по сигналам — Standard, но меняется поведение маршрутизации у всех конфигов с вложенными route, плюс таймерный путь HA; deep-review запрошен владельцем, дифф с тестами ожидается >200 строк.

## User Stories

1. Как оператор, переносящий `alertmanager.yml` в AMP, я хочу, чтобы дочерний route без таймингов брал их у ближайшего предка, как в upstream, чтобы время и частота нотификаций совпадали.
2. Как оператор, я хочу `group_wait: 0s` на критичном route, чтобы алерт уходил сразу.

## Success Criteria

- [ ] Дерево корень (тайминги) → ребёнок без таймингов → внук без таймингов: оба наследуют тайминги корня; промежуточный route со своими таймингами — его потомки наследуют их.
- [ ] Route с `group_wait: 0s` под корнем `10s`: `RoutingDecision.GroupWait == 0`, конфиг загружается, grouping-таймер срабатывает без задержки (и в memory, и в Redis-хранилище таймеров).
- [ ] Явные `group_interval: 0s` / `repeat_interval: 0s` отвергаются при загрузке, как в upstream (`cannot be zero`).
- [ ] `global.group_wait`/`group_interval`/`repeat_interval` (AMP-only) реально применяются в прод-пути: к корню без таймингов и через него — к вложенным.
- [ ] `/api/v2/status` `config.original` не показывает тайминги, которых нет в конфиге (ни у корня, ни у вложенных).
- [ ] Hot reload строит дерево тем же путём; HA/Redis-путь таймеров не регрессирует (`go test ./internal/infrastructure/grouping/...` зелёный, флейк `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY` не учащается).
- [ ] Smoke на бинаре подтверждает 3s / 3s (наследование) / ~0s.

## Non-Goals

- Не менять семантику `group_by` (уже наследуется верно).
- Не чинить `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY`, `FUZZ-RANDOM-ALERTS-RACE-TIMEOUT`.
- Не переделывать `grouping.DefaultParser` (в проде не используется) сверх единообразия с прод-парсером.

## Constraints

- **Scope:** `business/routing` (tree_builder, tree), `infrastructure/routing/parser.go` (одна функция — вероятный конфликт мержа с `SLACK-COLOR-TEMPLATE-REJECTED`, правка минимальная), `infrastructure/grouping` (parser, manager_impl, timer_manager_impl, timer_models).
- **Security:** не применимо.
- **Compatibility:** конфиги с явным `group_interval: 0s`/`repeat_interval: 0s` раньше молча наследовали родителя, теперь не загружаются (как upstream) — запись в CHANGELOG. Сохранённые в Redis группы (`GroupMetadata.Timings`) читаются без миграции.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-HARDCODED-FILTER/evidence/smoke.md` (находка), FU-GLOB-DEFAULT-VALUES (`business/routing/global_inheritance_test.go`).
- Relevant patterns: upstream `dispatch/route.go` `newRoute` (opts = parent или `DefaultRouteOpts`, затем `if cr.X != nil`).
- Open unknowns: нет (см. `research.md`).
