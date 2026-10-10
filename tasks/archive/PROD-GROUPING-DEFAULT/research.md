---
id: PROD-GROUPING-DEFAULT
slug: prod-grouping-default
stream: Parity / Grouping
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-10
updated_at: 2026-10-10
---

# Research Pack - PROD-GROUPING-DEFAULT

## 0) TL;DR

- Solving: verbatim `alertmanager.yml` с `route:` не группирует, потому что `grouping.enabled` по умолчанию `false`, и об этом ничто не сообщает.
- Found: дефолт `true` безопасен без `route:` только с одной правкой — иначе `AlertProcessor` пишет WARN «Grouping enabled but…» на каждой legacy-установке. `lite` группировку поддерживает (in-memory), доки утверждают обратное. От дефолта зависит один тест.
- Chose: вариант A — дефолт `true` в коде и чарте, эффективный флаг для `AlertProcessor` = `enabled && HasRouteTree()`, WARN на старте при явном `false` + `route:`.
- Risks: существующие установки с `route:` без явного ключа начнут задерживать нотификации на `group_wait` (30s по умолчанию); в `lite` — впервые включится группировка, которую доки называли недоступной.
- Next step: -> spec

## 1) Questions

1. Что происходит при `grouping.enabled: true` без `route:`?
2. Работает ли группировка в `lite`?
3. Делает ли новый дефолт `standard`-установку без Redis degraded?
4. Какие тесты и сценарии полагаются на дефолт `false`?
5. Нужно ли отличать «ключ не задан» от явного `false`?
6. Меняется ли флаг на hot reload?

## 2) Findings

- **Codebase:**
  - **Q1.** `initializeGrouping` (`internal/application/service_registry.go:1617`) без `route:` делает чистый пропуск (INFO, без degraded). Но `AlertProcessor` получает `GroupingEnabled: r.config.Grouping.Enabled` напрямую (`service_registry.go:2320`), и `warnGroupingFallback` (`internal/core/services/alert_processor.go:389`) при `groupingEnabled && groupManager == nil` пишет WARN (раз в окно, с `suppressed_since_last_warn`). С дефолтом `true` это ложное предупреждение на каждой установке без `route:`.
  - **Q2.** `newGroupingStorage` (`service_registry.go:1864`) и `newNotifyLog` (`:1966`) для `lite` возвращают in-memory реализации; профиль нигде не выключает подсистему. `service_registry_grouping_test.go:96` фиксирует: «groupManager must be initialized in lite profile with grouping enabled». Snapshot nflog для `lite` есть (`FU-LITE-FILE-SNAPSHOT`). В `lite` игнорируется только `reconciliation_interval` (doc-комментарий `GroupingConfig`). Утверждение «lite игнорирует `grouping.enabled`» в `helm/amp/values.yaml:659`, `helm/amp/README.md:68`, `docs/ALERTMANAGER_COMPATIBILITY.md:859` коду не соответствует. Живым бинарём в `lite` не проверялось.
  - **Q3.** `standard` без Redis: degraded-причину добавляет `initializeCache` (`:1210`) независимо от группировки; `newGroupingStorage`/`newNotifyLog` в этом случае только пишут WARN и уходят на in-memory, новой degraded-причины нет. Отдельная причина появляется только когда Redis-кэш жив, а grouping/nflog-клиент не поднялся. Здоровая установка от смены дефолта degraded не станет.
  - **Q4 (measured).** Дефолт временно изменён на `true`, прогон `go test ./internal/config/... ./internal/application/... ./internal/core/... ./cmd/...`: падает только `TestLoadConfig_GroupingDefaults` (`internal/config/grouping_adapter_test.go:78`), изменение откачено. `deploy/smoke`, `deploy/e2e-ha`, `values-production.yaml` задают `true` явно. Чарт всегда передаёт `GROUPING_ENABLED` из values (`templates/configmap.yaml:63`) — дефолт чарта перекрывает дефолт кода, менять нужно оба.
  - **Q5.** Не нужно: при дефолте `true` условие WARN — `!Grouping.Enabled && HasRouteTree()`; `false` может быть только заданным явно (файл, env или values чарта).
  - **Q6.** В `reload_sections.go`, `reloadable_*.go`, `service_registry_reload.go` группировка не упоминается: флаг читается только на старте.
  - Тайминги без явных значений в `route:` — upstream-дефолты 30s / 5m / 4h (`internal/infrastructure/grouping/config.go:158-164`).
  - Образец стартового WARN о молча не действующей настройке — `config.go:1335` («Notification templating is DISABLED…»).
- **Constraints:** breaking для установок с `route:` без явного ключа; откат — `grouping.enabled: false`. В HA без Redis in-memory группировка не делится между репликами — это уже отражено degraded-причиной кэша. Двойное срабатывание таймера в HA закрыто `GROUPING-TIMER-LOCK-FIX` (2026-10-10).
- **Unknowns:** поведение живого бинаря в `lite` с `route:` и дефолтом `true` (ingest → `group_wait` → доставка) — проверить на `/testing` smoke-прогоном. `go test ./...` по всему модулю с новым дефолтом не гонялся (только четыре дерева выше) — закроет release-gate.

## 3) Options

`Generation:` single-pass — откат стоит одного ключа конфига, параллельные линзы не оправданы.

### Option A - дефолт `true` + эффективный флаг + WARN при явном `false`

- **Pros:** verbatim-конфиг группирует как upstream; Known Gap #13 закрывается, а не документируется; без `route:` поведение прежнее.
- **Cons:** breaking для существующих установок с `route:`; нужна правка проводки флага в `AlertProcessor`, чарта и пяти мест в доках.
- **Cost/Risk:** medium

### Option B - дефолт `false` + WARN на старте при `route:`

- **Pros:** ничего не меняется в доставке; `C` снимается, тир Standard.
- **Cons:** verbatim-конфиг по-прежнему шлёт каждый алерт сразу; заявление «замена Alertmanager» остаётся с оговоркой, P0 по сути не закрыт.
- **Cost/Risk:** low

### Option C - дефолт `true` только при наличии `route:` (условный дефолт в `LoadConfig`)

- **Pros:** не нужна правка проводки флага.
- **Cons:** дефолт, зависящий от другой секции, нельзя выразить в `viper.SetDefault` и в values чарта; результат тот же, что у A, но логика сложнее.
- **Cost/Risk:** medium

## 4) Decision

- **Chosen:** Option A.
- **Why:**
  - Проблема — молчаливое расхождение с upstream; B оставляет расхождение, добавляя только сообщение.
  - Чистый пропуск без `route:` уже есть в `initializeGrouping`; недостающее — одна строка проводки (`Enabled && HasRouteTree()`), что проще условного дефолта из C.
  - Риск ограничен и обратим одним ключом; описывается в CHANGELOG migration notes.
  - Тир остаётся Full (`C R X`), как в `requirements.md`.

## 5) Spec Inputs

- API/contracts: дефолт `grouping.enabled` → `true` (`config.go:875`) и `grouping.enabled: true` в `helm/amp/values.yaml`; ключ и тип не меняются. CHANGELOG `[Unreleased]`: Changed + breaking/migration note с откатом.
- Data model/migrations: не применимо.
- Rollout/rollback: `grouping.enabled: false` (файл, `GROUPING_ENABLED=false`, values) возвращает прямую публикацию; требуется рестарт — флаг не hot-reloadable (зафиксировать в доке).
- Observability:
  - `AlertProcessor.GroupingEnabled` = `Grouping.Enabled && HasRouteTree()` — без ложного WARN на установках без `route:`; WARN остаётся для «`route:` есть, подсистема не поднялась».
  - Стартовый WARN при `!Grouping.Enabled && HasRouteTree()`: тайминги `route:` не применяются. Место — рядом с существующим INFO в `initializeGrouping` либо по образцу `config.go:1335`; выбрать в spec.
  - INFO без `route:` оставить INFO.
- Security/ownership/RBAC: не применимо.
- Tests: переписать `TestLoadConfig_GroupingDefaults`; тест на эффективный флаг (дефолт + нет `route:` → нет fallback-WARN, прямая публикация); тест на стартовый WARN; render-тест чарта на `GROUPING_ENABLED: "true"` по умолчанию и `"false"` при явном выключении; smoke в `lite`.
- Docs: `ALERTMANAGER_COMPATIBILITY.md` (#13, шаг миграции `:973`), `MIGRATION_QUICK_START.md:24`, `README.md:17`, `helm/amp/README.md` (`:23`, `:53`, `:68`), `CONFIGURATION_GUIDE.md`, комментарии `values.yaml` / `values-production.yaml` / `deploy/{smoke,e2e-ha}/config.yaml`, doc-комментарий `GroupingConfig.Enabled`. Формулировку про `lite` править только после smoke-проверки.

## 6) References

- Files: `go-app/internal/config/config.go`, `go-app/internal/config/grouping_adapter.go`, `go-app/internal/application/service_registry.go`, `go-app/internal/core/services/alert_processor.go`, `go-app/internal/infrastructure/grouping/config.go`, `helm/amp/values.yaml`, `helm/amp/templates/configmap.yaml`
- Docs: `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #13, `docs/06-planning/BACKLOG.md` § Production Readiness
- Evidence: `evidence/default-flip-test-run.md`

## 7) Addendum 2026-10-10 — цепочка `group_interval` (после deep-review R1)

- **Вопрос:** как сделать, чтобы новый алерт существующей группы уходил через `group_interval`, а не `repeat_interval` (review F1).
- **Findings (codebase):**
  - `publishGroupAlerts` (Step 4b) уже содержит upstream-логику DedupStage: сигнатура набора алертов + `IsDuplicate` с `ttl = now − repeat_interval`, по каждому target. Неизменный набор в пределах `repeat_interval` не отправляется, изменённый — отправляется сразу.
  - Таймеры ставятся только в трёх местах `manager_impl.go`; `onGroupIntervalExpired` всегда переходил на `repeat_interval`.
  - Логи таймер-менеджера на каждый start/expire — Info; при тике раз в `group_interval` это 3 строки на группу каждые 5m.
- **Options:** (a) тикать `group_interval` постоянно, решение об отправке оставить Dedup — как upstream `aggrGroup`; (b) сбрасывать таймер на `group_interval` при добавлении алерта в существующую группу.
- **Chosen:** (a). Одно изменение покрывает новые алерты, resolved и ретрай недоставленного; не трогает `AddAlertToGroup`, lock и `fireStillDue`; (b) требует ветвления по типу таймера и отдельной обработки HA.
- **Measured:** overlay-прогон (`go test -overlay`, без файла в дереве): после неизменного flush таймер — `group_interval`, публикаций 1; после добавления алерта следующий flush даёт вторую публикацию. `go test -race` по `internal/infrastructure/{grouping,publishing}`, `internal/{application,core,config}`, `cmd` — зелёные; из существующих тестов поменялось одно утверждение (`resolved_prune_test.go`: следующий таймер — `group_interval`).
- **Не измерялось:** нагрузка от тика на большом числе групп; поведение таймера `repeat_interval`, сохранённого прежней версией в Redis (только чтение кода).
