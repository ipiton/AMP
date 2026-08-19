# Архитектурные решения (DECISIONS)

## ADR-001: Go как основной язык runtime
- **Дата**: 2026-02 (фиксация факта)
- **Контекст**: Проект начинался на Python, но core runtime переписан на Go для совместимости с Alertmanager API.
- **Решение**: Go — основной язык для серверной части. Python-код удалён.
- **Следствие**: API-совместимость с Alertmanager проще поддерживать на том же языке.

## ADR-002: Alertmanager Replacement Scope Is Active-Runtime-First
- **Дата**: 2026-03-08
- **Контекст**: Historical docs, DONE entries и parity tests начали описывать runtime шире, чем текущий active bootstrap в `go-app/cmd/server/main.go` и `go-app/internal/application/router.go`.
- **Решение**:
  - source of truth для replacement story — active runtime, а не historical docs/tests;
  - текущий допустимый claim — только `controlled replacement`, не `general-purpose drop-in replacement`;
  - текущий active scope ограничен alert ingest, silence CRUD, health/readiness, metrics и real publishing path;
  - deprecated `v1` endpoints не входят в current active scope;
  - wide-surface parity expectations фиксируются как future/backlog parity до отдельного runtime-restoration slice.
- **Следствие**:
  - planning/docs/tests должны синхронизироваться с active runtime first;
  - historical parity suites не должны автоматически определять публичные claims;
  - если нужен stronger Alertmanager replacement claim, это оформляется отдельной runtime/API задачей.

## ADR-003: Solo Kanban (SEMA) как процесс разработки
- **Дата**: 2026-03-08
- **Контекст**: Один разработчик + AI-агент. Нужен легковесный, но структурированный процесс.
- **Решение**: Solo Kanban с WIP max 2, балансом 50/50 maintenance/roadmap, вертикальными срезами и quality gates.
- **Следствие**: Planning files версионируются в `docs/06-planning/`, задачи в `tasks/`.

## ADR-004: Active Storage Bootstrap Is Profile-Aware And State-Aware
- **Дата**: 2026-03-09
- **Контекст**: Active runtime в `go-app/internal/application/service_registry.go` держал `nil` placeholder для `core.AlertStorage`, standard migrations были незавершенными, а health/readiness handlers отвечали статическим success независимо от реального storage/bootstrap state.
- **Решение**:
  - `ProfileLite` использует `internal/infrastructure.SQLiteDatabase` как canonical embedded storage runtime с обязательными `Connect()` и `MigrateUp()` до публикации storage;
  - `ProfileStandard` использует canonical path `PostgresPool.Connect -> goose migrations -> thin Postgres storage adapter`, работающий поверх уже созданного pool и не открывающий второй connection pool;
  - required storage и database bootstrap failures считаются fail-fast и не допускают pseudo-healthy startup;
  - `/health` и `/healthz` закреплены как liveness JSON endpoints, `/ready` и `/readyz` как readiness JSON endpoints, `/-/healthy` и `/-/ready` сохраняют Alertmanager-compatible plain-text contract;
  - optional degradations вроде cache fallback отражаются в runtime report как `degraded`, но не переводят readiness в failure, пока required dependencies healthy.
- **Следствие**:
  - active runtime больше не стартует с отсутствующим required storage и ложным `healthy`;
  - observable health contract теперь различает bootstrap, storage, database и optional degraded state;
  - active alert/silence handlers пока сознательно остаются на memory compatibility stores и требуют отдельного follow-up, если их нужно переводить на persistent backend.

## ADR-005: Active Dashboard Placeholder Pages Stay On Current `/dashboard/*` Surface
- **Дата**: 2026-03-09
- **Контекст**: В active runtime `/dashboard/silences`, `/dashboard/llm` и `/dashboard/routing` были смонтированы, но возвращали placeholder body. При этом в репозитории уже существовал второй UI stack (`internal/ui` + `cmd/server/handlers`), который не был active source of truth для этих routes.
- **Решение**:
  - canonical active owner этих страниц остается в current `go-app/cmd/server` path, а не переносится на dormant `/ui/*` subsystem;
  - страницы реализованы как honest read-only UI через `go-app/cmd/server/legacy_dashboard.go`, `go-app/cmd/server/templates/legacy/*` и `go-app/internal/application/legacy_dashboard.go`;
  - page models строятся из узких runtime summaries `ServiceRegistry` и могут показывать `ready`, `empty`, `limited`, `disabled` или `metrics-only` state вместо placeholder/error semantics;
  - richer operator workflows, full routing editor и полная миграция legacy dashboard остаются отдельным follow-up work.
- **Следствие**:
  - active `/dashboard/*` surface больше не обещает незавершенный UI;
- default non-tagged `cmd/server` tests теперь защищают contract этих страниц;
- если репозиторий в будущем захочет единый UI stack, это должно идти отдельной задачей, а не скрытым follow-up к placeholder removal.

## ADR-006: Restored Operational Alertmanager Endpoints Stay Inside The Controlled-Replacement Scope
- **Дата**: 2026-03-09
- **Контекст**: После ADR-002 active runtime был сознательно сужен до controlled-replacement narrative, но practical Grafana / automation flows все еще требовали `GET /api/v2/status`, `GET /api/v2/receivers`, `GET /api/v2/alerts/groups` и `POST /-/reload` в current mounted router.
- **Решение**:
  - active runtime снова монтирует `status`, `receivers`, `alerts/groups` и `reload` в `go-app/internal/application/router.go`;
  - `ServiceRegistry` получает `startTime` и `ReloadCoordinator`, чтобы эти endpoints опирались на живое runtime state, а не на historical stubs;
  - current public claim остается `controlled replacement`, а не `general-purpose drop-in replacement`;
  - config/history/inhibition/classification surfaces, deprecated `v1` alias и broader dashboard/runtime parity остаются отдельным follow-up work.
- **Следствие**:
  - public docs и compatibility matrix больше не должны описывать эти четыре endpoints как backlog-only;
  - planning/tests не должны продолжать фиксировать их отсутствие в active router contract;
  - restoration этого operational surface не отменяет active-runtime-first policy и не возвращает repo к broad historical parity claim.

## ADR-007: Grafana Dashboard Identity (uid, filename) Is Stable On Purpose
- **Дата**: 2026-08-17
- **Контекст**: После `GRAFANA-DASHBOARD-BRANDING-DRIFT` (visible title → `AMP - Operations Dashboard`) остались identity-shaped поля: `uid = amp-alert-history` и путь `grafana/dashboards/alert-history-service.json`. `GRAFANA-DASHBOARD-IDENTITY-DRIFT` требовал явного решения, а не механического rename.
- **Решение**:
  - `uid` и filename — стабильный import/provisioning contract, не branding surface; они НЕ меняются без явного provisioning owner;
  - смена `uid` ломает существующие Grafana provisioning references и dashboard links у всех, кто уже импортировал дашборд, ради чисто косметической консистентности — trade-off отвергнут;
  - при появлении provisioning owner (Helm-managed provisioning или отдельный dashboards repo) допустима миграция с alias/redirect-планом отдельной задачей.
- **Следствие**:
  - `GRAFANA-DASHBOARD-IDENTITY-DRIFT` закрывается как «working as intended, документировано»;
  - historical naming в uid/filename не считается drift и не должен попадать в новые BUGS-записи.

## ADR-008: `http_config.oauth2` — Skip The Target Only When The Integration Asked For It
- **Дата**: 2026-08-19
- **Контекст**: `FU-HTTP-CONFIG` (AMP-PARITY-WAVE7 track C) доставляет per-integration `http_config`, но `oauth2` не поддержан (нужен token endpoint + refresh loop, это отдельная единица работы — `FU-HTTP-OAUTH2`). Первая реализация всегда логировала `WARN` и отправляла запрос БЕЗ OAuth2-креденшелов. Security-review предложил обратное: skip target (fail closed), по аналогии с нечитаемым `password_file`. Controller ruling: ни один из двух вариантов целиком не верен — решает ПРОВЕНАНС блока.
- **Решение**:
  - `oauth2` в СОБСТВЕННОМ `http_config` интеграции → **target пропускается** (`fail closed`) с громким `WARN`, содержащим receiver/kind/index. Endpoint явно объявил требование auth, поэтому доставка без него — осознанная неаутентифицированная отправка. Радиус — ровно одна интеграция, которая об этом попросила.
  - `oauth2`, унаследованный из `global.http_config` → интеграция **продолжает доставлять** (без OAuth2), с `WARN` **и** счётчиком `amp_publishing_unsupported_http_config_total{field="oauth2",target=...}`.
  - Провенанс сохраняется через неэкспортированное поле `routing.HTTPConfig.inheritedFromGlobal` (устанавливается только в `ResolveHTTPConfigFallback`), потому что parse-time resolution стирает структурную разницу между own и inherited. Поле неэкспортированное намеренно: `yaml.v3` и struct-validator игнорируют такие поля, поэтому оно не может протечь в `/api/v2/status`.
- **Обоснование асимметрии**:
  - `global.http_config` распространяется WHOLESALE на каждую `webhook`/`slack`/`pagerduty`/`telegram` интеграцию, которая не задала свой блок. При политике «всегда skip» ОДИН глобальный `oauth2` блок = **полный notification outage по всему процессу**, включая Slack/Telegram/PagerDuty, которые аутентифицируются своим URL/header-креденшелом и OAuth2 никогда не требовали.
  - Неаутентифицированный запрос уходит на **собственный настроенный host оператора**, который его auth-gate-ит и вернёт 401. Это отвергнутый запрос к предполагаемому получателю, а не раскрытие данных третьей стороне — материально слабее, чем C1 (где выбрасывались креденшелы, которые оператор ДАЛ).
- **Accepted risk**:
  - Интеграция, унаследовавшая `global.http_config.oauth2`, отправляет запросы без OAuth2-креденшелов до тех пор, пока `FU-HTTP-OAUTH2` не закрыт. Сигнал: `WARN` на каждый config load/reload + монотонный счётчик + 401 от самого endpoint.
  - Осознанно принято, потому что альтернатива (skip) превращает одну неподдерживаемую опцию в отказ всей доставки.
- **Следствие**:
  - `WARN` один раз на config load — не per-alert; поэтому счётчик обязателен, а не «nice to have» (review I1).
  - `docs/ALERTMANAGER_COMPATIBILITY.md` описывает оба варианта поведения в секции `Per-integration http_config`.
  - При закрытии `FU-HTTP-OAUTH2` этот ADR отменяется целиком: оба пути станут «поддержано».
