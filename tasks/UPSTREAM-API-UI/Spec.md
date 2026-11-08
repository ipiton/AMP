---
id: UPSTREAM-API-UI
slug: upstream-api-ui
stream: Architecture / ADR-019
type: feature
status: active
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
---

# Specification: API среза 6 ADR-019

**Version:** 1.0
**Status:** Approved (автор; решение владельца по UI — 2026-11-08)

## Summary

`amp-upstream` получает два маршрута чтения под `/api/amp/v1/` — запись истории и последний investigation алерта по fingerprint из `api/v2` (или SHA-256 AMP). Для поиска по FNV в `alerts` добавляется колонка `upstream_fingerprint` с индексом. `/api/v2/*` по-прежнему целиком обслуживает upstream-ядро.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| История по fingerprint `api/v2` | API § alert, Data Model, Component `ampapi` |
| Investigation по fingerprint | API § investigation |
| Префикс и auth | Target Design п.1, Security |
| 503 без PostgreSQL | API § Errors, Edge Cases 1 |
| `api/v2` побайтно | Target Design п.1, Invariants, тест композиции, `upstream-core-gate.sh` |
| Обратимая миграция с тестом | Data Model, Rollout |
| `/api/v2/inhibitions`, `group_by` | Decisions (research Q3/Q4), Impact § Breaking |
| `STATUS-VERSIONINFO-CONTRACT`, `COMPAT-VERSION-CONFIG-KEY` | Impact § Planning |
| CHANGELOG | Impact § Breaking |

## Current State

- **Code:** `cmd/amp-upstream/main.go:126-136` — `wrapWebAuth(core.Handler(), …)`; `cmd/amp-upstream/amp.go` — `newAMPLayer` возвращает только `stop`; `PostgresStorageAdapter` пишет `alerts`; `PostgresInvestigationRepository` создаётся только при включённой очереди investigation.
- **Data:** `alerts` (одна строка на SHA-256 fingerprint, upsert), `alert_investigations` (ключ — тот же SHA-256).
- **Tests:** `cmd/amp-upstream/*_test.go` (auth, version, agent), `internal/upstreamcore/contract` (сьюты против `core.Handler()`), testcontainers в `internal/database`, `internal/infrastructure/repository`.
- **Docs:** CHANGELOG `[Unreleased]` (5: «AMP's own routes … gone until slice 6»), `ALERTMANAGER_COMPATIBILITY.md` (устарел, срез 7).

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| FNV в `api/v2` = `model.LabelSet.Fingerprint()` от labels = `alertconv.UpstreamFingerprint(labels)` | `alertmanager@v0.34.1/alert/alert.go:30` (встраивает `model.Alert`), `api/v2` `fingerprint: a.Fingerprint().String()`; `alertconv.go:116` | code-read | поиск по FNV не находит строки; ловится тестом с `model.LabelSet.Fingerprint()` |
| Единственный писатель `alerts` в коде этой версии — `newHistory` → `DeduplicationService` → `SaveAlert`/`UpdateAlert` | `grep -rn NewPostgresStorageAdapter` по модулю: только `cmd/amp-upstream/amp.go` | code-read (grep, не трассировка) | В деплое ложно на время rolling update или отката на образ среза 5: старые поды вставляют строки без колонки (их `ON CONFLICT` колонку не трогает). Такие строки ищутся по FNV после первого `UpdateAlert` или следующего старта нового пода (Edge Case 8) |
| Labels строки не меняются при `UpdateAlert` (SHA-256 — функция labels) | `deduplication.go:370-394` меняет status/endsAt/annotations/timestamp, не labels | code-read | колонка расходится с labels — исключено, т.к. SHA-ключ тот же |
| Resolved-алерт удаляется из upstream store на GC, история остаётся | `store/store.go` `gcAlerts`; `History.PostGC` пустой | code-read | — (обосновывает отказ от поиска через store) |
| Всё под `/api/v2/` обслуживает `api.API.Register` ядра; AMP-диспетчер перехватывает только `<prefix>/api/amp/` | `alertmanager@v0.34.1/api/api.go` `Register`; Target Design п.1 | code-read | при ошибке диспетчера — сдвиг `api/v2`; ловится тестом композиции и amtool-гейтом |
| Индекс на `alerts` строится за `migrationTimeout` (1 мин) | строка на label set; retention нет (`cleanup_old_data`/`CleanupOldAlerts` никто не вызывает), таблица только растёт; размер в проде не измерен | assumed | Индекс строится `CONCURRENTLY` отдельной `NO TRANSACTION` миграцией (deep-review F1), запись не блокируется. При таймауте история выключается до рестарта, а прерванная сборка оставляет INVALID-индекс; на старте `Warn` с командой починки. Миграции идут до listener, поэтому старт доставки ждёт их, но не дольше `migrationTimeout`. Перед выкаткой на большой таблице — `count(*)` или ручной `CREATE INDEX CONCURRENTLY` (CHANGELOG) |

## Target Design

1. В `cmd/amp-upstream` handler собирается как `wrapWebAuth(withAMPRoutes(core.Handler(), amp, prefix))`: запрос с путём `<prefix>/api/amp/` идёт в AMP-handler, любой другой — в `core.Handler()` без изменений (не через `ServeMux`: никакой чистки путей и редиректов перед upstream).
2. Новый пакет `internal/ampapi` — handler с `GET <prefix>/api/amp/v1/alerts/{fingerprint}` и `…/{fingerprint}/investigation` поверх двух интерфейсов-ридеров; nil-ридер → 503. Путь разбирается вручную, не шаблонами `ServeMux`, так что `ampapi` не зависит от синтаксиса префикса (deep-review F3). Префикс с `{` в пути всё равно роняет сам upstream (`api.Register`). На чтения из БД — таймаут 10 с на запрос: маршруты идут мимо `--web.timeout`/`--web.get-concurrency` upstream, а пул общий с записью истории.
3. `{fingerprint}`: 16 hex — FNV, ищется по `alerts.upstream_fingerprint`; 64 hex — SHA-256 AMP, по `alerts.fingerprint`. Регистр не важен (приводится к нижнему). Иначе 400.
4. Investigation: fingerprint → строка `alerts` → её SHA-256 → `GetLatestByFingerprint`. Для 64 hex без строки истории investigation ищется по SHA напрямую.
5. Миграция добавляет `alerts.upstream_fingerprint` + индекс; `SaveAlert`/`UpdateAlert` пишут её из labels; старые строки заполняет фоновый backfill при старте (Go, та же функция FNV, батчами по `id`, идемпотентно).
6. `newAMPLayer` возвращает и ридеры: оба есть, когда есть PostgreSQL (investigation читается и при `llm.enabled: false` — результаты прошлых запусков).

## API Contracts

Оба маршрута: только `GET` (и `HEAD`), иначе 405 с `Allow: GET, HEAD`. Любой другой путь под `<prefix>/api/amp/` — 404. Ответ всегда `application/json`, ошибка — `{"error": "<текст>"}`.

### GET `<prefix>/api/amp/v1/alerts/{fingerprint}`

- **Auth:** та же basic auth, что у `api/v2` (`--web.config.file`); без неё — открыт, как и весь API.
- **Ownership:** не применимо (single-tenant, как `api/v2`).
- **Response 200:**

```json
{
  "fingerprint": "<16 hex, как в api/v2>",
  "amp_fingerprint": "<64 hex SHA-256>",
  "alert_name": "HighLatency",
  "status": "firing | resolved",
  "labels": {"alertname": "HighLatency", "...": "..."},
  "annotations": {},
  "starts_at": "RFC3339 UTC, если есть (колонка nullable)",
  "ends_at": "RFC3339, если есть",
  "generator_url": "если есть",
  "first_seen_at": "RFC3339 (created_at строки)",
  "last_event_at": "RFC3339 (timestamp строки), если есть"
}
```

`fingerprint` считается из labels строки (не из колонки), так что он есть и у не заполненной ещё строки.

- **Errors:** 400 — fingerprint не 16/64 hex; 404 — нет строки; 503 — истории нет (нет PostgreSQL); 500 — ошибка БД (текст ошибки только в лог, `Warn`).

### GET `<prefix>/api/amp/v1/alerts/{fingerprint}/investigation`

- **Auth / Ownership:** как выше.
- **Response 200:** форма старого `GET /api/v1/alerts/{fingerprint}/investigation` (`501ceaa^:…/handlers/investigation.go`) с двумя изменениями: `fingerprint` — FNV (если строка истории найдена; иначе поле опущено), новое `amp_fingerprint` — SHA-256 (раньше это был `fingerprint`).

```json
{
  "id": "uuid", "fingerprint": "<16 hex>", "amp_fingerprint": "<64 hex>",
  "status": "queued|processing|completed|failed|dlq", "retry_count": 0,
  "queued_at": "...", "created_at": "...", "updated_at": "...",
  "started_at": "...", "completed_at": "...",
  "error_message": "...", "error_type": "transient|permanent|unknown",
  "result": {"summary": "...", "findings": {}, "recommendations": [], "confidence": 0.9, "llm_model": "..."},
  "steps": [], "iterations_count": 0, "tool_calls_count": 0
}
```

Необязательные поля опускаются, как раньше.

- **Errors:** 400 — формат; 404 — нет строки истории для FNV или нет investigation; 503 — нет PostgreSQL; 500 — ошибка БД.

## Data Model / Migrations

### Table: `alerts`

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `upstream_fingerprint` | `VARCHAR(16)` | NULL | FNV из labels; NULL — строка до backfill |

Индекс `idx_alerts_upstream_fingerprint` (не unique: FNV-64 может совпасть у разных labels; при совпадении отдаётся строка с последним `timestamp`, и клиент не видит, что строк несколько. Принято: подобрать совпадение может только тот, кто и так шлёт алерты).

- **Migration:** две goose-миграции. `20261108000000_alerts_upstream_fingerprint.sql` (в транзакции): `ADD COLUMN IF NOT EXISTS` (nullable без default, ACCESS EXCLUSIVE только на изменение каталога; ожидание этой блокировки за долгим запросом ставит в очередь запись истории старых подов, но не дольше `migrationTimeout`); down — `DROP COLUMN IF EXISTS`. `20261108000001_alerts_upstream_fingerprint_index.sql` (`NO TRANSACTION`): `CREATE INDEX CONCURRENTLY IF NOT EXISTS`; down — `DROP INDEX CONCURRENTLY IF EXISTS`. Проверка `UpstreamFingerprintIndexValid` на старте: если индекса нет или он INVALID — `Warn` с командой починки. Рестарт индекс не пересоздаёт (версия уже записана), чинится только вручную: `DROP INDEX CONCURRENTLY`, затем `CREATE INDEX CONCURRENTLY`, двумя отдельными командами.
- **Backfill:** не в миграции (FNV в SQL хрупок, research Q2-C). При старте, после миграций, горутина `BackfillUpstreamFingerprints`: `SELECT id, labels … WHERE upstream_fingerprint IS NULL AND id > $last ORDER BY id LIMIT 500`, затем один `UPDATE … FROM unnest($ids, $fps) … AND upstream_fingerprint IS NULL`. Идемпотентно, безопасно для N реплик. Отменяется при остановке до закрытия пула. Строки с нечитаемыми labels пропускаются (`Warn` на каждом старте). Когда всё заполнено, каждый старт делает один проход `IS NULL`. Строки, которые backfill заполнил, получают от триггера новый `updated_at` (для `HISTORY-API`: сортировка по `updated_at` смещается, BACKLOG). Если две реплики обновляют пересекающиеся батчи, возможен deadlock: одна получает `Warn`, её строки заполнит следующий старт, корректность не страдает.
- **Rollback / forward-fix:** откат образа на срез 5, схема остаётся: колонку и индекс старый код не читает и не пишет. Схему не откатывать (`docs/ROLLBACK_RUNBOOK.md` запрещает `goose down` при откате). Down-миграции проверит тест 3.3.

## Component Architecture

- `migrations/20261108000000_alerts_upstream_fingerprint.sql`, `migrations/20261108000001_alerts_upstream_fingerprint_index.sql` — новые.
- `internal/core/history.go` — тип `AlertHistoryRecord{Alert, FirstSeenAt}`.
- `internal/infrastructure/postgres_storage_adapter.go` — `SaveAlert`/`UpdateAlert` пишут колонку; `AlertRecordByFingerprint`, `AlertRecordByUpstreamFingerprint`, `BackfillUpstreamFingerprints`, `UpstreamFingerprintIndexValid` (в `postgres_alert_records.go`).
- `internal/ampapi/` (новый) — `Handler`, интерфейсы `AlertReader`, `InvestigationReader`, `PathPrefix = "/api/amp/"`, ответы, метрика.
- `cmd/amp-upstream/amp.go` — `newAMPLayer` → `*ampLayer{stop, alerts, investigations}`; repo investigation создаётся один раз при наличии пула; запуск backfill.
- `cmd/amp-upstream/main.go` — `withAMPRoutes`, сборка `ampapi.New`; doc и help флага `--amp.config.file`.

## Security Design

- [x] Ownership — не применимо (single-tenant; как `api/v2`).
- [x] Input validation — fingerprint `^[0-9a-f]{16}$|^[0-9a-f]{64}$` после `ToLower` до запроса в БД; SQL параметризованный, колонка выбирается константой.
- [x] Sensitive data not logged — ошибки БД логируются без labels/annotations; ответ 500 без текста ошибки.
- [x] Rate limiting — лимита конкурентности нет (принятый риск, deep-review F4). `limitHandler` upstream сюда не применяется, но каждый запрос ограничен таймаутом 10 с, а соединения — `MaxConns` пула; чтение по индексу O(log n). Без `--web.config.file` маршруты открыты, как весь API.
- [x] Auth path — `wrapWebAuth` снаружи `withAMPRoutes`: маршруты не попадают в `unauthenticated_paths` (точное совпадение путей, `/-/healthy`, `/-/ready`).

## Invariants

- [ ] Ответ на любой путь вне `<prefix>/api/amp/` побайтно равен ответу `core.Handler()`.
- [ ] Доставка не зависит от PostgreSQL и от backfill (ошибка backfill — `Warn`, история пишется).
- [ ] Запись истории по-прежнему одна на событие; колонка не меняет ключ upsert.
- [ ] Миграция аддитивна: схема после down равна схеме до up.

## Edge Cases

1. Нет `--amp.config.file` / `profile: lite` / PG недоступен на старте → оба маршрута 503 с причиной; `api/v2` работает.
2. FNV-строка ещё не заполнена (до конца backfill) → 404 по FNV, 200 по SHA-256.
3. Resolved-алерт, удалённый из upstream store → находится по FNV (колонка).
4. Совпадение FNV у разных labels → строка с последним `timestamp` (`ORDER BY timestamp DESC, id DESC LIMIT 1`; `timestamp` NOT NULL).
5. Investigation для 64 hex без строки истории → ищется по SHA; `fingerprint` в ответе опущен.
6. `--web.external-url=http://h/am` → маршруты под `/am/api/amp/v1/…`; `/api/amp/…` без префикса → `core.Handler()` (upstream 404).
7. `POST /api/amp/v1/alerts/<fp>` → 405 JSON; `/api/amp/v1/unknown` → 404 JSON; `--web.external-url` с `{` в пути роняет upstream `api.Register` (не AMP).
8. Rolling update или откат на срез 5 и обратно: строки, которые старые поды вставили после backfill, остаются NULL. По FNV — 404, пока их не заполнит `UpdateAlert` или следующий старт.
9. Backfill на N репликах одновременно → каждая строка обновляется не больше одного раза (`AND upstream_fingerprint IS NULL`); триггер сдвигает `updated_at` заполненных строк — колонка `updated_at` в API не отдаётся.

## Impact Analysis

- **Affected modules:** `cmd/amp-upstream`, `internal/infrastructure`, `internal/core`, `migrations`, новый `internal/ampapi`.
- **Breaking changes (API, в CHANGELOG `[Unreleased]`):**
  - `GET /api/v1/alerts/{fingerprint}/investigation` → `GET /api/amp/v1/alerts/{fingerprint}/investigation`; принимает FNV из `api/v2`; поле `fingerprint` теперь FNV, SHA-256 — в `amp_fingerprint`. `/api/v1/*` отвечает 410 (upstream).
  - `GET /api/v2/inhibitions` удалён окончательно (upstream 404); замена — `status.inhibitedBy` в `GET /api/v2/alerts`, правила — `GET /api/v2/status`.
  - `?group_by=` у `GET /api/v2/alerts/groups` не поддерживается. `global.group_by`, `global.group_wait`, `global.group_interval`, `global.repeat_interval` в `alertmanager.yml`: upstream парсит конфиг строго (`yaml.UnmarshalStrict`), поэтому конфиг с ними не загружается — падают старт и `/-/reload` (это поведение среза 5, здесь оно названо явно). Замена — те же ключи на корневом `route:`.
  - `?status=resolved` у `GET /api/v2/alerts` не поддерживается: upstream не отдаёт resolved-алерты. Замена — `GET /api/amp/v1/alerts/{fingerprint}`. Алиасы `/health`, `/healthz`, `/ready`, `/readyz` не возвращаются, есть `/-/healthy` и `/-/ready`. Записи class a в `divergences.tsv` переведены в class d.
  - Новое: `GET /api/amp/v1/alerts/{fingerprint}`.
- **Planning:** `STATUS-VERSIONINFO-CONTRACT` → закрыт срезом 5 (`[x]`, ссылка на `setContractVersion`); `COMPAT-VERSION-CONFIG-KEY` — без изменений по сути, текст уточнён (`alertmanager_build_info` из `version.Version`); `HISTORY-API` — путь `/api/amp/v1/…`, остаток: список/top/flapping; `UPSTREAM-DOCS-MIGRATION` — пункты про inhibitions, `group_by`, новые маршруты; уточнение ADR-019 в `DECISIONS.md` (inhibitions не AMP-маршрут).
- **New dependencies:** нет.
- **Risks:** см. premise `assumed` про размер `alerts`.

## Rollout / Rollback

- **Rollout:** миграции при старте (`RunMigrations` под advisory lock), затем backfill в фоне. Индекс строится `CONCURRENTLY`. Риск (premise `assumed`): если на большой `alerts` построение не укладывается в `migrationTimeout` (1 мин), история выключается до рестарта, а индекс остаётся INVALID (на старте `Warn`). Обход: построить индекс вручную до выкатки (CHANGELOG).
- **Rollback:** откат образа на предыдущий, схема с колонкой совместима. `goose down` не выполнять (runbook). `RunMigrationsDown` откатывает до версии, а не на N шагов (BUGS).
- **Feature flag:** не применимо (маршруты активны вместе с PostgreSQL).

## Observability

- **Logs:** `Info` с числом строк, заполненных backfill (если > 0); `Warn`, если индекса нет или он INVALID; `Warn` на ошибку backfill и на 500 маршрутов (с `handler`, без данных алерта).
- **Metrics:** `amp_api_request_duration_seconds{handler="alert|investigation|other",method,code}` (histogram); 405 в метрику не попадает.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** `S`, `M`, 5 сигналов.
- **Discretionary triggers present:** новый HTTP-слой поверх порта upstream.
- **Decision:** required.

## Open Questions

- [ ] Размер `alerts` в проде владельца (premise `assumed`) — вынесено в Rollout risk; при выкатке посмотреть `SELECT count(*) FROM alerts`.
