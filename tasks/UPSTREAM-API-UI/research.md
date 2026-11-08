---
id: UPSTREAM-API-UI
slug: upstream-api-ui
stream: Architecture / ADR-019
type: feature
status: complete
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Research: API среза 6 ADR-019

**Level:** light (несколько вариантов, миграция данных; внешних интеграций нет).

## TL;DR

- AMP-маршруты монтируются в `cmd/amp-upstream` **перед** handler'ом ядра под отдельным префиксом `/api/amp/v1/`: всё остальное, включая `/api/v2/*`, уходит в `core.Handler()` нетронутым — побайтность по построению, проверка — контрактные сьюты и amtool-гейт.
- Поиск по FNV — **колонка `alerts.upstream_fingerprint` с индексом**, заполняется при каждой записи истории и фоновым backfill'ом старых строк. Через labels/in-memory store не работает для того, ради чего история нужна: resolved-алерт удаляется из store на GC.
- Investigation не трогает свою таблицу: FNV → строка `alerts` → SHA-256 → `alert_investigations`.
- `/api/v2/inhibitions` — удалить окончательно: данные есть в upstream `api/v2` (`status.inhibitedBy`), а свой маршрут в namespace `/api/v2` ломает «`api/v2` = upstream».
- `?group_by=`/`global.group_by` не возвращаются; `STATUS-VERSIONINFO-CONTRACT` уже закрыт срезом 5; `COMPAT-VERSION-CONFIG-KEY` по-прежнему без use case.

## Questions

1. Как отдать AMP-маршруты, не изменив ни байта `api/v2`?
2. Как найти историю и investigation по FNV-fingerprint из `api/v2`?
3. Нужен ли ещё `/api/v2/inhibitions`?
4. Что с `?group_by=`, `global.group_by`, `STATUS-VERSIONINFO-CONTRACT`, `COMPAT-VERSION-CONFIG-KEY`?

## Findings

### F1. HTTP-композиция

- `upstreamcore.Core.Handler()` = `tracing.Middleware(apih.Register(router, routePrefix))` (`internal/upstreamcore/core.go:450-459`). `api.API.Register` (`alertmanager@v0.34.1/api/api.go`) строит свой `http.ServeMux`: `routePrefix+"/api/v2/"` → v2-handler, `/` → `route.Router` (`/-/*`, `/metrics`, `/debug/*`, deprecation-router `/api/v1/*` → 410).
- `cmd/amp-upstream/main.go:131` оборачивает `core.Handler()` в `wrapWebAuth` — значит маршрут, вставленный между ними, получает ту же auth.
- Контрактные сьюты (`internal/upstreamcore/contract/harness_test.go:93`) гоняются против `core.Handler()`, не против бинаря; amtool-часть `scripts/upstream-core-gate.sh` — против собранного `amp-upstream`, т.е. увидит и композицию.
- Старые маршруты (`git show 501ceaa^:go-app/internal/application/router.go`): `/api/v2/inhibitions` и `/api/v1/alerts/` (investigation) жили в одном mux с `api/v2`.

### F2. Ключи истории и investigation

- `alerts`: одна строка на SHA-256 fingerprint (`migrations/20250911094416_initial_schema.sql`: `idx_alerts_fingerprint_unique`), upsert в `PostgresStorageAdapter.SaveAlert`/`UpdateAlert` (`internal/infrastructure/postgres_storage_adapter.go:51,308`), labels — JSONB.
- `alert_investigations.fingerprint` — тот же SHA-256 (`toCoreAlert`, `internal/upstreamai/convert.go`), выборка `GetLatestByFingerprint` (`internal/infrastructure/repository/investigation_repository.go:207`).
- FNV upstream = `model.LabelSet.Fingerprint()` (`alert.Alert` встраивает `model.Alert`, `alertmanager@v0.34.1/alert/alert.go:30`); `alertconv.UpstreamFingerprint(labels)` (`internal/core/alertconv/alertconv.go:116`) — та же функция с форматом `%016x`.
- Resolved-алерты удаляются из upstream store на GC (`alertmanager@v0.34.1/store/store.go` `gcAlerts`, по умолчанию раз в 30 мин), история их переживает (`History.PostGC` ничего не удаляет).
- Триггер `update_alerts_updated_at` ставит `updated_at = NOW()` на любом UPDATE — backfill сдвинет `updated_at` старых строк.
- Единственный писатель `alerts` — `cmd/amp-upstream/amp.go:newHistory` (`grep NewPostgresStorageAdapter`).

### F3. `/api/v2/inhibitions`

- Старый handler (`501ceaa^:…/handlers/inhibitions.go`) отдавал список активных ингибиций `{targetFingerprint, sourceFingerprint, ruleName, inhibitedAt, expiresAt}` из своего state manager'а; движок удалён в срезе 5.
- Upstream: `GET /api/v2/alerts` даёт `status.inhibitedBy` (FNV источников) у каждого подавленного алерта, правила — в `GET /api/v2/status` (`config.original`). Записи `ruleName`/`expiresAt` у upstream inhibitor'а нет.
- Потребители: старый UI (удалён), доки `ALERTMANAGER_COMPATIBILITY.md:95,112,129`, `MIGRATION_COMPARISON.md:37` (переписываются в срезе 7). В контрактной сьюте — `TestPhase0RouteInventory/inhibitions_get` уже в `divergences.tsv` (class a, fails).

### F4. Прочее

- `?group_by=` на `/api/v2/alerts/groups` — AMP-расширение (`501ceaa^:…/handlers/alerts.go:305,341`), `global.group_by` — ключ своего ядра. Upstream группирует по `group_by` маршрута. Вернуть можно только своим маршрутом поверх `api/v2` — без потребителя не нужно.
- `STATUS-VERSIONINFO-CONTRACT`: с среза 5 `versionInfo.version` = версия upstream-контракта (`cmd/amp-upstream/version.go` `setContractVersion`; `Dockerfile` `-X prometheus/common/version.Version=${UPSTREAM_VERSION#v}`), версия AMP — в `amp_build_info`. Вопрос записи решён, запись устарела.
- `COMPAT-VERSION-CONFIG-KEY`: `alertmanager_build_info{version}` — тот же `version.Version` (`versioncollector`, `main.go:58`). Use case «солгать инструменту про версию» так и не появился; к срезу 6 не относится.

## Options

**Generation:** один автор. Fan-out по линзам не запускался: миграция аддитивная (nullable колонка + индекс), откат — `goose down` и одна строка в upsert; ошибка выбора дешёвая.

### Q2 — поиск по FNV

| Вариант | Как | Плюсы | Минусы |
|---|---|---|---|
| A. Через labels из upstream store | FNV → `alerts.Get(fp)` → labels → SHA-256 → БД | без миграции | resolved и GC'нутые алерты (основной случай для истории) не находятся; нужен доступ к provider из `cmd` |
| B. Labels от клиента | клиент шлёт labels, сервер считает SHA-256 | без миграции, всегда находит | клиент должен знать все labels; это не «по fingerprint из `api/v2`» |
| C. SQL по JSONB labels | вычислить FNV в SQL | без колонки в коде | FNV-1a 64 над отсортированными байтами в PL/pgSQL (переполнение bigint, collation) — хрупко и медленно |
| **D. Колонка `upstream_fingerprint` + индекс** | пишется на каждом upsert из `alertconv.UpstreamFingerprint`; старые строки — фоновый backfill в Go той же функцией | находит всё, что есть в истории; одна функция FNV в Go; O(log n) | миграция; backfill сдвигает `updated_at` старых строк (триггер) |

### Q1 — композиция

| Вариант | Плюсы | Минусы |
|---|---|---|
| **Префикс-диспетчер в `cmd/amp-upstream` перед `core.Handler()`** | `api/v2` физически не проходит через код AMP | сьюты не видят композицию — нужен свой тест + amtool-гейт |
| Seam в `upstreamcore.Options` (extra routes в upstream mux) | сьюты видят | правка порта upstream, лишний seam при каждом bump |
| Внешний `http.ServeMux` | стандартно | свой ServeMux чистит/редиректит пути до upstream — лишнее поведение перед `api/v2` |

### Q3 — inhibitions

| Вариант | Плюсы | Минусы |
|---|---|---|
| Оставить под `/api/v2/inhibitions` поверх inhibitor'а | старый путь | правка порта (доступ к inhibitor'у), чужой маршрут в namespace upstream, `ruleName`/`expiresAt` нет |
| Перенести в `/api/amp/v1/inhibitions` | namespace AMP | тот же доступ к inhibitor'у; данные дублируют `status.inhibitedBy` |
| **Удалить** | `api/v2` = upstream; ноль кода | breaking для клиентов старого пути (миграция: `status.inhibitedBy`) |

## Decision

- Q1: префикс-диспетчер в `cmd/amp-upstream`, префикс `/api/amp/v1/` под `--web.external-url`, внутри `wrapWebAuth`.
- Q2: вариант D. Path-параметр принимает 16 hex (FNV, `api/v2`) и 64 hex (SHA-256 AMP); investigation резолвится через строку `alerts`.
- Q3: удалить `/api/v2/inhibitions`, отметить в CHANGELOG и для среза 7; уточнение к ADR-019 (`DECISIONS.md:348` перечисляет его среди AMP-маршрутов) — в `DECISIONS.md` на finalize.
- Q4: `group_by` не возвращать (CHANGELOG + срез 7); `STATUS-VERSIONINFO-CONTRACT` закрыть как решённый срезом 5; `COMPAT-VERSION-CONFIG-KEY` оставить в BACKLOG с уточнённым текстом.
- Q5 (добавлено после deep-review, M1 второго ревьюера): остальные class-a записи `divergences.tsv` тоже не возвращаются, все переведены в class d. Алиасы `/health`, `/healthz`, `/ready`, `/readyz` не нужны: chart с PROD-AUTH ходит в `/-/healthy`, `/-/ready`. UI отложен до `UPSTREAM-UI` (решение владельца). `/api/v1` отдаёт 410, как upstream. `?status=resolved` заменяет `GET /api/amp/v1/alerts/{fingerprint}`, список resolved — `HISTORY-API`. Возвращать что-то из этого внутри `/api/v2` или рядом с ним значит ломать инвариант «`api/v2` = upstream». Решение принял автор задачи по этому инварианту, на ревью владельца.

## Spec Inputs

- Миграция `20261108000000_alerts_upstream_fingerprint.sql`: `ADD COLUMN upstream_fingerprint VARCHAR(16)`, `CREATE INDEX idx_alerts_upstream_fingerprint`; down — обратно. (После deep-review индекс вынесен в `20261108000001_…_index.sql`, `NO TRANSACTION`, `CONCURRENTLY`; см. Spec.)
- `SaveAlert`/`UpdateAlert` пишут колонку; reader'ы по SHA и по FNV; backfill батчами по `id`.
- Новый пакет HTTP-маршрутов с интерфейсами reader'ов, 400/404/503, JSON-ошибки, метрика длительности.
- Тесты: handler на фейках; композиция (байты `/api/v2/*` = `core.Handler()`); testcontainers — миграция up/down/up, запись колонки, backfill, поиск.

## References

- `internal/upstreamcore/core.go:450-459`, `alertmanager@v0.34.1/api/api.go` `Register`.
- `internal/infrastructure/postgres_storage_adapter.go`, `migrations/20250911094416_initial_schema.sql`, `internal/infrastructure/repository/investigation_repository.go:207`.
- `internal/core/alertconv/alertconv.go:76,116`, `internal/upstreamai/convert.go`.
- `git show 501ceaa^:go-app/internal/application/handlers/{inhibitions,investigation,alerts}.go`.
- `docs/06-planning/DECISIONS.md` § ADR-019, `docs/06-planning/BACKLOG.md:142,175,184,185`.
