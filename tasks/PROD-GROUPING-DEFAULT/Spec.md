---
id: PROD-GROUPING-DEFAULT
slug: prod-grouping-default
stream: Parity / Grouping
type: bug
status: draft
created_at: 2026-10-10
updated_at: 2026-10-10
based_on:
  - requirements.md
  - research.md
---

# Specification: группировка включена по умолчанию

**Version:** 1.0
**Status:** Draft

## Summary

`grouping.enabled` по умолчанию становится `true` в коде и в чарте, чтобы скопированный `alertmanager.yml` с `route:` группировал алерты как upstream. Без `route:` поведение не меняется, а явное выключение при наличии `route:` сопровождается предупреждением на старте.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `route:` без секции `grouping:` включает группировку | Target Design п.1; тест дефолта в `internal/config`; тест `initializeGrouping` на конфиге из `LoadConfig` |
| Без `route:` — старт как раньше, без ошибки, degraded и ложного WARN | Target Design п.2; Invariants 1; тест на эффективный флаг |
| Явный `false` + `route:` выключает группировку и даёт один WARN на старте | Target Design п.3; тест на лог |
| Поведение по профилям зафиксировано | `research.md` Q2–Q3; Design Premises 3–5; `evidence/smoke-lite-grouping.md` |
| Чарт согласован с кодом, render-тест | Target Design п.4; `helm/amp/tests/render-grouping-default.sh` |
| Доки отражают новое поведение | Target Design п.5; Component Architecture |
| CHANGELOG: Changed + migration note | Impact Analysis; Rollout / Rollback |
| Гейты, `deep-review` | Deep Review; `testing` по `WORKFLOW.md` § Гейты AMP |
| BACKLOG / NEXT | `finalize` |

## Current State

- **Code:**
  - `go-app/internal/config/config.go:875` — `viper.SetDefault("grouping.enabled", false)`; doc-комментарий `GroupingConfig.Enabled` (`:110-115`) описывает состояние task 2.2 («ingest pipeline does not consult the grouping subsystem yet»).
  - `go-app/internal/application/service_registry.go:1617-1627` — `initializeGrouping`: при `!Enabled` INFO и выход; без `route:` INFO и выход.
  - `service_registry.go:2320` — `GroupingEnabled: r.config.Grouping.Enabled` передаётся в `AlertProcessor` без учёта `route:`.
  - `go-app/internal/core/services/alert_processor.go:389` — `warnGroupingFallback`: WARN, когда флаг включён, а группировать нечем.
  - `helm/amp/values.yaml:661` — `grouping.enabled: false`; `templates/configmap.yaml:63` всегда передаёт `GROUPING_ENABLED`.
- **Data:** не применимо.
- **Tests:** `internal/config/grouping_adapter_test.go` (`TestLoadConfig_GroupingDefaults` ждёт `false`), `internal/application/service_registry_grouping_test.go` (`TestInitializeGrouping_DisabledByDefault` и соседи строят `Config` руками), `internal/core/services/alert_processor_test.go` (флаг задаётся явно). Render-тесты чарта — `helm/amp/tests/render-*.sh`, запускаются шагом `helm-tests` release-gate.
- **Docs:** `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #13 (`:855`) и шаг миграции (`:973`), `docs/MIGRATION_QUICK_START.md:24`, `README.md:17`, `helm/amp/README.md` (`:23`, `:53`, `:68`), комментарии в `helm/amp/values.yaml:656-659`, `values-production.yaml:251`, `deploy/smoke/config.yaml:44`, `deploy/e2e-ha/config.yaml:80`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Флаг читают только два места: `initializeGrouping` и проводка `AlertProcessor` | grep `Grouping\.Enabled\|grouping\.enabled\|GROUPING_ENABLED` по всему репозиторию (go, sh, yaml, tpl): вне тестов и комментариев — `service_registry.go:1618`, `:2320`, `config.go:875`, `configmap.yaml:63` | call-path-traced | третий потребитель увидит `true` без `route:` и поведёт себя иначе |
| Без `route:` подсистема не поднимается и ничего не деградирует | `service_registry.go:1623-1627` (`ErrGroupingRequiresRouteTree` → INFO, `return nil`) | code-read | установки без `route:` получат degraded или ошибку старта |
| В `lite` группировка работает целиком: алерт уходит после `group_wait` | `./deploy/smoke/run.sh` 2026-10-10: `profile: lite`, ALL PASS — `evidence/smoke-lite-grouping.md` | measured | `lite`-установки с `route:` перестанут доставлять |
| В `standard` без Redis включённая группировка не добавляет degraded-причину сверх той, что уже ставит кэш | `service_registry.go:1210` (кэш), `:1929` и `:1982` (только WARN) | code-read | здоровая по `/health` установка станет degraded после апгрейда |
| `validateGrouping` не зависит от `Enabled` | `config.go:1191-1202` — проверяет только interval/grace, выполняется всегда | code-read | конфиг, проходивший валидацию, начнёт падать с exit 1 |
| От дефолта `false` зависит один тест в `config`, `application`, `core`, `cmd` | прогон с перевёрнутым дефолтом — `evidence/default-flip-test-run.md` | measured | неожиданные падения на `/testing`; остальной модуль закрывает release-gate |
| Чарт перекрывает дефолт кода: `GROUPING_ENABLED` передаётся всегда | `templates/configmap.yaml:63`; `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/chart-default-env.txt:44` | code-read | правка только кода не дойдёт до Helm-установок |
| Флаг читается только на старте | grep `Grouping` по `reload_sections.go`, `reloadable_*.go`, `service_registry_reload.go` — пусто | call-path-traced | WARN на старте не покроет включение/выключение через reload |
| Без таймингов в `route:` действуют upstream-дефолты 30s / 5m / 4h | `internal/infrastructure/grouping/config.go:158-164` | code-read | migration note назовёт неверную задержку |
| Операторы существующих установок с `route:` без явного ключа прочитают migration note до апгрейда | не проверяемо | assumed | нотификации неожиданно задержатся на `group_wait` — см. Rollout / Rollback |

## Target Design

1. **Дефолт кода.** `viper.SetDefault("grouping.enabled", true)`. Doc-комментарий `GroupingConfig.Enabled` переписывается: включено по умолчанию, действует только при наличии `route:`, читается на старте.
2. **Эффективный флаг.** В `ServiceRegistry` — локальное значение `groupingActive := r.config.Grouping.Enabled && r.config.HasRouteTree()` в месте сборки `AlertProcessorConfig` (`:2320`): `GroupingEnabled: groupingActive`. Отдельный метод или поле не вводятся. Смысл `warnGroupingFallback` сохраняется для случая «`route:` есть, включено, подсистема не поднялась или у алерта нет routing decision».
3. **WARN при явном выключении.** В `initializeGrouping`, ветка `!Enabled`: если `HasRouteTree()` — `Warn` «Grouping is DISABLED (grouping.enabled=false) but a route: tree is configured: group_by/group_wait/group_interval/repeat_interval are ignored and every alert is published immediately»; иначе прежний INFO. Один раз на старт.
4. **Чарт.** `helm/amp/values.yaml`: `grouping.enabled: true`, комментарий переписан (включено по умолчанию; `lite` — in-memory, `standard` — Redis). `values-production.yaml`, `values-dev.yaml` не меняются по значениям; комментарий «turns on» в production выравнивается. Новый `helm/amp/tests/render-grouping-default.sh`: дефолт → `GROUPING_ENABLED: "true"`; `--set grouping.enabled=false` → `"false"`; `values-production.yaml` (с placeholders) → `"true"`.
5. **Доки и комментарии.** Known Gap #13 переписывается в «закрыт, как отключить»; из шагов миграции убирается ручное включение; `README.md`, `MIGRATION_QUICK_START.md`, `helm/amp/README.md` — убрать «off by default» и «lite игнорирует»; `deploy/*/config.yaml` — комментарий «must be explicit true» заменить на «default; kept explicit»; стейл-комментарии в `service_registry.go` (`:199`, `:1612`) и `alert_processor.go:29`.

Итог: дефолт меняется в двух местах, которые его задают — viper и values чарта. Подсистема и раньше чисто пропускала конфиг без `route:`, поэтому условная логика в загрузке конфига не нужна. Единственный побочный эффект — ложное предупреждение `AlertProcessor` на установках без `route:` — снимается тем, что процессору передаётся «включено и есть дерево». Оператор, сознательно выключивший группировку при наличии `route:`, видит одно предупреждение на старте. Откат для любой установки — один ключ.

## API Contracts

Не применимо: HTTP API не меняется. Контракт конфигурации: ключ `grouping.enabled` (YAML), env `GROUPING_ENABLED`, values `grouping.enabled` — имя и тип прежние, дефолт `false` → `true`.

## Data Model / Migrations

Не применимо. Новых ключей Redis и форматов нет; при первом старте с включённой группировкой таймеры и группы создаются штатно.

## Component Architecture

- `go-app/internal/config/config.go` — дефолт, doc-комментарий `GroupingConfig.Enabled`.
- `go-app/internal/application/service_registry.go` — WARN в `initializeGrouping`, эффективный флаг в сборке `AlertProcessorConfig`, комментарии.
- `go-app/internal/core/services/alert_processor.go` — только комментарии (`(the default)`).
- `go-app/internal/config/grouping_adapter_test.go` — `TestLoadConfig_GroupingDefaults` ждёт `true`; кейс явного `false` из YAML и из env.
- `go-app/internal/application/service_registry_grouping_test.go` — переименовать `…DisabledByDefault` в `…DisabledExplicitly` + проверка WARN; тест «дефолт из `LoadConfig` + `route:` → groupManager поднят»; тест «включено, нет `route:` → `AlertProcessor` без `groupingEnabled`, WARN не пишется».
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml` (комментарий), `helm/amp/tests/render-grouping-default.sh` (новый).
- `deploy/smoke/config.yaml`, `deploy/e2e-ha/config.yaml` — комментарии.
- `docs/ALERTMANAGER_COMPATIBILITY.md`, `docs/MIGRATION_QUICK_START.md`, `docs/CONFIGURATION_GUIDE.md`, `README.md`, `helm/amp/README.md`, `CHANGELOG.md`, `helm/amp/CHANGELOG.md`.

## Security Design

- [x] Ownership validation — не применимо
- [x] Input validation — не меняется (bool, существующая валидация)
- [x] Sensitive data not logged — новый WARN не содержит значений конфига
- [x] Rate limiting — не применимо
- [x] Auth/RBAC — не применимо

## Invariants

- [ ] Конфиг без `route:`: прямая публикация, нет WARN про группировку, нет новой degraded-причины.
- [ ] Явный `grouping.enabled: false` (файл, env, values) всегда выключает группировку.
- [ ] `route:` есть, включено, подсистема не поднялась — `warnGroupingFallback` по-прежнему пишет WARN (громкий fallback не теряется).
- [ ] `values-production.yaml`, `deploy/smoke`, `deploy/e2e-ha` ведут себя как до изменения.
- [ ] Механика таймеров, nflog и reconciliation не тронута.

## Edge Cases

1. `route:` есть, ключа `grouping:` нет, `lite` → группировка на in-memory storage, доставка после `group_wait`.
2. `route:` есть, ключа нет, `standard`, Redis недоступен → in-memory группировка, WARN от storage/nflog, degraded-причина только от кэша.
3. `route:` нет, ключа нет → INFO «no route tree configured», прямая публикация, `AlertProcessor.groupingEnabled == false`.
4. `route:` есть, `grouping.enabled: false` → WARN на старте, прямая публикация в receiver из routing decision.
5. `route:` есть, включено, дерево не собралось (`initializeRouting` non-fatal) → эффективный флаг `true` (берётся из конфига, не из результата сборки), `groupManager` может быть поднят, у алерта нет decision → `warnGroupingFallback`, публикация в `DefaultReceiver` — как сейчас при явном `true`.
6. Чарт с `--set grouping.enabled=false` → `GROUPING_ENABLED: "false"` перекрывает дефолт кода.
7. Апгрейд `standard` HA с `route:` без явного ключа → реплики начинают делить таймеры через Redis; двойное срабатывание закрыто `GROUPING-TIMER-LOCK-FIX`.
8. Включение/выключение через `/-/reload` → не действует до рестарта (как и раньше); отмечается в доке.

## Impact Analysis

- **Affected modules:** `internal/config`, `internal/application`, `internal/core/services` (комментарии), Helm-чарт, `deploy/` (комментарии), публичные доки.
- **Breaking changes:** установки с `route:` без явного `grouping.enabled` (в чарте — без `grouping.enabled` в своих values) начинают группировать: первая нотификация группы задерживается на `group_wait` (30s, если не задано), повторы подчиняются `group_interval` / `repeat_interval`, несколько алертов группы приходят одной нотификацией. Запись в `CHANGELOG.md` `[Unreleased]`: `### Changed` + breaking/migration note; то же в `helm/amp/CHANGELOG.md`.
- **New dependencies:** нет.
- **Risks:**
  - Неожиданная задержка нотификаций после апгрейда → migration note с откатом одним ключом; стартовый лог «Initializing grouping subsystem...» уже есть.
  - Получатели, рассчитанные на «один алерт — один вызов», получат сгруппированный payload → в migration note.
  - Diff может превысить ~200 строк за счёт доков — тир уже Full, эскалации нет.

## Rollout / Rollback

- **Rollout:** обычный релиз; специальных шагов нет. Изменение вступает в силу при рестарте с новой версией.
- **Rollback:** `grouping.enabled: false` в конфиге, `GROUPING_ENABLED=false` или `--set grouping.enabled=false` + рестарт. Группы и таймеры, оставшиеся в Redis, после выключения не читаются и истекают по TTL.
- **Feature flag:** сам `grouping.enabled`.
- **Риск (assumed):** оператор не прочитал migration note — смягчается тем, что новое поведение совпадает с upstream и с тем, что `route:` декларирует; WARN для обратного случая (явный `false`) есть.

## Observability

- **Logs:** новый WARN на старте при `grouping.enabled=false` + `route:`; INFO без `route:` — без изменений; ложный fallback-WARN на установках без `route:` не появляется.
- **Metrics:** не меняются. Метрика исхода срабатывания таймера — отдельная задача `TIMER-FIRE-OUTCOME-METRIC`.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** 3+ signals (`C R X`).
- **Discretionary triggers present:** `C+X`.
- **Decision:** required.

## Open Questions

- [ ] Нет блокирующих. На `/testing`: smoke с конфигом без ключа `grouping:` (дефолт) в `lite` — подтвердить Edge Case 1 на собранном бинаре.
