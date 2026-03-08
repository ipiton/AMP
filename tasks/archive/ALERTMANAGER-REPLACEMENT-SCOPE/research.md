# Research: ALERTMANAGER-REPLACEMENT-SCOPE

## Context

Нужно понять, что именно означает тезис "AMP может заменить Alertmanager" для текущего `main`, и какой следующий шаг честнее:

1. восстанавливать более широкий runtime/API surface;
2. или сужать публичные claims и тестовые ожидания до реально поддерживаемого active runtime.

## Executive Summary

Главный вывод: **drift шире, чем казалось по первому проходу**. Проблема не ограничивается `status/receivers/alerts/groups/-/reload`. На сегодня active runtime в `go-app/cmd/server/main.go` и `go-app/internal/application/router.go` представляет собой **узкий compatibility-lite bootstrap**, а docs, `DONE.md`, ADR и phase0/parity tests продолжают описывать **существенно более широкий runtime**.

При этом путь `restore runtime surface` не выглядит дешёвым rewiring. Для большинства заявленных endpoints нет просто "готовых роутов, которые забыли смонтировать"; им нужны runtime services, которых текущий `ServiceRegistry` не инициализирует и не экспонирует.

### Recommendation

Для следующего `/spec` safest recommendation:

- **Phase A**: сначала принять решение `source of truth = active runtime`, сузить replacement scope и зафиксировать, какие claims остаются допустимыми уже сейчас;
- **Phase B**: если продуктово всё ещё нужен тезис "Alertmanager replacement", заводить отдельный runtime-restoration slice c явным scope и acceptance criteria.

Иначе есть риск снова чинить docs поверх несогласованного runtime.

## Findings

### 1. Active runtime уже намного уже, чем заявлено в planning/docs/tests

Текущий `Router` монтирует только:

- `GET/POST /api/v2/alerts`
- `GET/POST /api/v2/silences`
- `GET/DELETE /api/v2/silence/{id}`
- `/health`, `/ready`, `/healthz`, `/readyz`, `/-/healthy`, `/-/ready`
- `/metrics`

Источник: `go-app/internal/application/router.go`.

При этом:

- `docs/06-planning/DONE.md` утверждает, что в активном пути работают `status`, `webhook`, `receivers`, `alert groups`, `config*`, `classification/*`, `history`;
- `docs/ALERTMANAGER_COMPATIBILITY.md` помечает широкий набор этих endpoints как `ACTIVE`;
- `go-app/cmd/server/main_phase0_contract_test.go` и `go-app/cmd/server/main_upstream_parity_regression_test.go` ожидают ту же широкую поверхность.

Итог: сейчас у проекта нет единого `source of truth` по active runtime.

### 2. Drift затрагивает не только core Alertmanager parity

В claimed surface также попадают endpoints, которые сейчас не монтируются вообще:

- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET /api/v2/alerts/groups`
- `POST /-/reload`
- alias `POST /api/v1/alerts`
- `GET/POST /api/v2/config*`
- `GET /api/v2/classification/*`
- `GET /history*`
- `GET/POST /api/v2/inhibition/*`
- advanced silences (`/api/v2/silences/check`, `/api/v2/silences/bulk/delete`)

Это уже не "одна-две забытые ручки", а целый historical contract layer, который расходится с текущим bootstrap.

### 3. Restore path не сводится к простому route wiring

В репо есть старые/альтернативные handlers в `go-app/cmd/server/handlers`, но они завязаны на зависимости, которых текущий `ServiceRegistry` не поднимает как usable runtime contract:

- `PrometheusQueryHandler` требует `AlertHistoryRepository`;
- advanced `SilenceHandler` требует `silencing.SilenceManager`;
- config/reload/history stories опираются на `internal/config` services и history storage;
- routing/receivers/groups path требует route tree / config state;
- current `ServiceRegistry` хранит только memory alert/silence stores, publisher path, cache, metrics, alert processor.

Дополнительно:

- `initializeStorage()` в `go-app/internal/application/service_registry.go` сейчас оставляет `r.storage = nil` как placeholder;
- deduplication поэтому тоже не становится полноценной runtime dependency;
- handlers из старого стека не выглядят как готовые drop-in компоненты для текущего simplified application bootstrap.

Вывод: путь `restore runtime surface` — это уже отдельная runtime/builder integration задача, а не docs cleanup.

### 4. Внутри claims уже есть логические противоречия

Есть несколько явных конфликтов:

- ADR-002 говорит: deprecated `v1 API` не реализуем; при этом phase0 tests и compatibility docs ожидают alias `POST /api/v1/alerts`.
- `BUGS.md` уже честно фиксирует `ACTIVE-RUNTIME-COMPATIBILITY-DRIFT` и `DOCS-OVERCLAIM-COMPATIBILITY`, но `DONE.md` всё ещё описывает старую более широкую картину.
- `README.md` обещает `Production-Ready`, хотя quality gates остаются red.
- `README.md` говорит `Plugin system`, а по коду видно скорее extension points, а не полноценный runtime plugin loader.
- `README.md` конфликтует по лицензии: badge/LICENSE указывают AGPL, нижняя секция README пишет `Apache 2.0`.

### 5. У проекта уже есть честное ядро replacement story

Не всё надо "откатывать". После `PHASE-4-PRODUCTION-PUBLISHING-PATH` реальными и полезными стали:

- active ingest path через `AlertProcessor`;
- real publishing runtime через adapter/coordinator/queue;
- silence CRUD;
- health/readiness;
- metrics;
- explicit `metrics-only` fallback.

То есть у AMP уже есть внятный **controlled replacement slice**, просто он уже заявляется как будто это полный replacement story.

## Options

### Option A: Restore Runtime Surface

Суть:

- вернуть в active runtime как минимум `status`, `receivers`, `alerts/groups`, `/-/reload`;
- отдельно решить судьбу `/api/v1/alerts`;
- определить, нужны ли действительно `config*`, `history`, `classification/*`, `inhibition/*` в replacement scope первой очереди;
- восстановить или переписать test harness под текущий bootstrap.

Плюсы:

- docs/product narrative снова может идти в сторону stronger replacement story;
- часть уже написанных tests/docs можно будет переиспользовать.

Минусы:

- это уже runtime integration slice, а не честный docs/spec pass;
- требует нового service exposure в `ServiceRegistry` и, вероятно, возврата части старого route stack;
- высокий риск расползания scope.

### Option B: Narrow Public Claims to Verified Runtime

Суть:

- принять `active runtime` как source of truth;
- сузить ADR/docs/DONE/tests до реально поддерживаемой поверхности;
- отделить текущий `controlled replacement` от будущего `drop-in replacement`.

Плюсы:

- минимальный и честный путь;
- устраняет главную product/docs проблему быстро;
- создаёт чистую отправную точку для следующего runtime slice.

Минусы:

- некоторые historical claims придётся явно снять;
- часть phase0/parity tests придётся архивировать, переписать или разделить на `active` vs `backlog`.

## Recommendation

Рекомендую **Option B как ближайший шаг**, а `Option A` оформить отдельной follow-up задачей только после `/spec`.

Причины:

1. current runtime already compiles and has a real ingest/publishing path;
2. missing surface depends on services not wired in current bootstrap;
3. restoration without explicit spec almost гарантированно размоет scope;
4. сейчас важнее восстановить честный `source of truth`, чем продолжать поддерживать устаревший replacement narrative.

## Questions To Freeze In Spec

Следующий `/spec` должен явно ответить на четыре вопроса:

1. Какой replacement scope считаем допустимым сегодня:
   - `controlled replacement`
   - `core API replacement`
   - `general-purpose drop-in replacement`
2. Что делаем с `/api/v1/alerts`:
   - возвращаем alias
   - или убираем его из claims/tests как deprecated scope
3. Что становится canonical source of truth:
   - active router + active smoke tests
   - или historical phase0 contract suite
4. Что делаем с historical parity/tests/docs:
   - переписываем под active runtime
   - архивируем часть как backlog
   - разделяем на `active` и `future parity`

## Proposed Spec Direction

Для следующего `/spec` наиболее логичный framing:

- **Decision**: source of truth = active runtime (`main.go` + `internal/application/router.go`)
- **Goal**: убрать противоречия между runtime, ADR, DONE, tests и docs
- **Non-goal**: не восстанавливать весь прежний wide API surface в этом же slice
- **Deliverables**:
  - обновлённый ADR/decision note по replacement scope;
  - split тестов на `active runtime contract` и `future parity backlog`;
  - follow-up task list на restore-path, если он всё ещё нужен.

## Verification Notes

В этом research-этапе новые тесты не запускались. Выводы основаны на сравнении:

- `go-app/internal/application/router.go`
- `go-app/cmd/server/main.go`
- `go-app/internal/application/service_registry.go`
- `go-app/cmd/server/main_phase0_contract_test.go`
- `go-app/cmd/server/main_upstream_parity_regression_test.go`
- `docs/06-planning/DONE.md`
- `docs/06-planning/DECISIONS.md`
- `README.md`
- `docs/ALERTMANAGER_COMPATIBILITY.md`
