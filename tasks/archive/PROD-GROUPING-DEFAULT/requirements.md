---
id: PROD-GROUPING-DEFAULT
slug: prod-grouping-default
stream: Parity / Grouping
type: bug
priority: critical
status: active
created_at: 2026-10-10
updated_at: 2026-10-10
---

# Requirements: verbatim `alertmanager.yml` с `route:` должен группировать без ручного флага

## Problem Framing

- **Symptom:** оператор переносит `alertmanager.yml` с деревом `route:` как есть. AMP стартует без ошибок и предупреждений, но каждый алерт уходит получателю сразу: `group_by`/`group_wait`/`group_interval`/`repeat_interval` не действуют. В логе — только INFO `Grouping subsystem disabled (grouping.enabled=false)`.
- **Root Cause:** `viper.SetDefault("grouping.enabled", false)` (`go-app/internal/config/config.go:875`). Дефолт остался с task 2.2, когда подсистема ещё не была подключена к ingest-пути (см. doc-комментарий `GroupingConfig.Enabled`). `warnGroupingFallback` (`internal/core/services/alert_processor.go:389`) при `!groupingEnabled` сразу выходит, так что и предупреждения нет. Чарт повторяет дефолт: `grouping.enabled: false` в `helm/amp/values.yaml`, включено только в `values-production.yaml`, `deploy/smoke`, `deploy/e2e-ha`.
- **Why Now:** P0 в BACKLOG § Production Readiness (повышен аудитом 2026-10-06): первая по порядку незаблокированная задача. Расхождение с заявлением «замена Alertmanager» — молчаливое и проявляется на первом же реальном конфиге (шторм нотификаций вместо групп).
- **How We Measure:** бинарь с конфигом, где есть `route:` и нет ключа `grouping:`, — алерт попадает в группу и уходит после `group_wait`, а не сразу (тест на уровне `ServiceRegistry`/`AlertProcessor` + проверка стартового лога); `helm template` с дефолтными values рендерит `GROUPING_ENABLED: "true"`.

## Risk Profile

- **Signals:** `C R X`
  - `C` — меняется дефолт публичного ключа конфига (`grouping.enabled`) и values чарта: для существующих установок без явного ключа поведение доставки меняется, нужна запись в breaking changes / migration notes.
  - `R` — путь доставки нотификаций: алерты начинают ждать `group_wait` и дедуплицироваться через nflog; в `standard` включаются Redis-backed таймеры и reconciliation.
  - `X` — Go-конфиг, Helm-чарт и публичные доки (compat, migration, README).
- **Tier:** Full
- **Notes:** три сигнала ⇒ Full по Step Matrix, `deep-review` обязателен. Если `/research` покажет, что достаточно WARN без смены дефолта (вариант B ниже), `C` снимается и тир пересматривается на Standard (`R X`) — явным решением, не молча.

## User Stories

1. Как оператор, мигрирующий с Alertmanager, я хочу, чтобы скопированный `alertmanager.yml` группировал алерты так же, как upstream, без дополнительных ключей AMP.
2. Как оператор, осознанно выключивший группировку, я хочу видеть в стартовом логе явное предупреждение, что `route:`-тайминги не применяются.
3. Как оператор существующей установки AMP, я хочу узнать из CHANGELOG, что поведение по умолчанию изменилось и как вернуть прежнее.

## Success Criteria

- [x] Конфиг с `route:` и без секции `grouping:` включает подсистему группировки: тест на загрузку конфига (дефолт) и тест, что алерт идёт в группу, а не в прямую публикацию.
- [x] Конфиг без `route:` стартует как раньше: чистый пропуск, без ошибки и без degraded-причины.
- [x] Явный `grouping.enabled: false` при наличии `route:` по-прежнему выключает группировку, и на старте пишется WARN (один раз, не на каждый алерт).
- [x] Проверено и зафиксировано в `research.md` поведение по профилям: `lite` (in-memory storage) и `standard` без рабочего Redis (fallback + degraded-причина) — новый дефолт не делает здоровую установку degraded и не ломает старт.
- [x] Чарт: `grouping.enabled` в `values.yaml` согласован с дефолтом кода, комментарии в `values.yaml` / `values-production.yaml` и `deploy/*/config.yaml` («must be explicit true») актуализированы; render-тест на `GROUPING_ENABLED`.
- [x] Доки: `ALERTMANAGER_COMPATIBILITY.md` Known Gap #13 и шаг миграции, `MIGRATION_QUICK_START.md`, `README.md` (runtime note), `CONFIGURATION_GUIDE.md` — отражают новое поведение; doc-комментарий `GroupingConfig.Enabled` переписан.
- [x] `CHANGELOG.md` `[Unreleased]`: Changed + запись в breaking changes / migration notes (как вернуть прямую доставку).
- [x] Алерт или resolve, пришедший в уже нотифицированную группу, уходит не позже чем через `group_interval`; неизменная группа напоминает не чаще `repeat_interval` (добавлено 2026-10-10).
- [x] Гейты: затронутые пакеты `go vet` + `go test`, `quality-gates-fast`, `scripts/release-gate.sh` PASS; `deep-review` → `"gate": "pass"`.
- [x] BACKLOG P0 `PROD-GROUPING-DEFAULT` перенесён в «Закрыто», `NEXT.md` WIP очищен.

## Non-Goals

- Верхнеуровневый `inhibit_rules:` (`FU-TOPLEVEL-INHIBIT-RULES`) и встроенный фильтр (`PROD-HARDCODED-FILTER`) — отдельные P0.
- Дефолты чарта под одну ноду, HPA, requests (`HELM-SINGLE-NODE-DEFAULTS`) и нестартующие дефолтные values (`HELM-DEFAULTS-VALIDATE`).
- Изменения в nflog, distributed lock и reconciliation; follow-ups `GROUPING-TIMER-LOCK-FIX` (`GROUPING-CALLBACK-TRANSIENT-LOAD-BREAKS-CHAIN`, `TIMER-*`). Цепочка `group_interval` → `repeat_interval` из non-goals исключена 2026-10-10 (см. Scope).
- Пронос LLM-классификации в групповую нотификацию (review F2) — отдельная задача.
- Удаление ключа `grouping.enabled` как такового.

## Constraints

- **Scope (расширен 2026-10-10, решение владельца после deep-review R1, «вариант 2»):** `go-app/internal/infrastructure/grouping` — группа должна flush'иться каждые `group_interval` (review F1: без этого новый дефолт задерживает алерты существующей группы до `repeat_interval`). Оценка задачи: ~0.5–1d → ~2d; срез не выделяется — смена дефолта без этого фикса не выпускается.
- **Scope (исходный):** `go-app/internal/config` (дефолт, doc-комментарий), стартовый лог в `internal/application/service_registry.go`, `helm/amp/values*.yaml` + render-тест, `deploy/*/config.yaml` (только комментарии), доки и CHANGELOG. Существующие тесты, завязанные на дефолт `false`, правятся по месту.
- **Security:** не применимо (auth, права, секреты не затрагиваются).
- **Compatibility:** breaking для установок с `route:` без явного `grouping.enabled`: нотификации начнут группироваться и задерживаться на `group_wait`. Откат — `grouping.enabled: false`. В `standard` группировка использует Redis; при его отсутствии — in-memory fallback с degraded-причиной (проверить в research, приемлемо ли это как дефолт). Миграций данных нет.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-CONFIG-FILE-FALLBACK/` (смена поведения конфига + breaking notes + чарт), `tasks/archive/GROUPING-TIMER-LOCK-FIX/` (HA-таймеры, закрыт 2026-10-10 — снял риск двойного срабатывания при включённой группировке), `tasks/archive/PROD-GRACEFUL-SHUTDOWN/` (ключ чарта → env → конфиг).
- Relevant patterns:
  - `ServiceRegistry.initializeGrouping` (`service_registry.go:1617`) уже делает чистый пропуск без `route:` (`ErrGroupingRequiresRouteTree`) — дефолт `true` «всегда» не требует условной логики в конфиге.
  - `newGroupingStorage` (`service_registry.go:1864`): `standard` + Redis → Redis storage, иначе in-memory. Комментарии в `values.yaml` и compat-доке («lite игнорирует ключ») с этим расходятся — сверить.
  - Env `GROUPING_ENABLED` из `helm/amp/templates/configmap.yaml:63`; тест «все env-имена чарта известны viper» — `internal/config/config_load_test.go`.
  - Тесты, упоминающие флаг: `internal/config/{config_load,grouping_adapter}_test.go`, `internal/application/service_registry_grouping_test.go`, `internal/core/services/{alert_processor,publish_receiver_scoping}_test.go`.
- Варианты решения (выбор — на `/research` → `/spec`):
  - **A (предпочтительный):** дефолт `true` всегда; без `route:` — существующий чистый пропуск; WARN на старте при явном `false` + `route:`.
  - **B:** дефолт остаётся `false`, громкий WARN на старте при `route:` + строки в доках. Меньше риск для существующих установок, но verbatim-конфиг по-прежнему не группирует — заявление «замена Alertmanager» остаётся с оговоркой.
- Open unknowns:
  - Работает ли группировка в `lite` полностью (snapshot nflog есть) или есть ограничения, из-за которых доки пишут «игнорирует».
  - Что происходит в `standard` при дефолте `true`, если Redis не сконфигурирован: degraded на `/health` у ранее здоровой установки?
  - Какие существующие тесты и e2e неявно полагаются на прямую публикацию при конфиге с `route:` без `grouping:`.
  - Можно ли отличить «ключ не задан» от явного `false` для WARN (viper `IsSet` при env из чарта — чарт всегда передаёт `GROUPING_ENABLED`).
