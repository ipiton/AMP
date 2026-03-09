# DONE

## 2026-03-09
- **GRAFANA-DASHBOARD-BRANDING-DRIFT** — завершен как narrow visible dashboard title cleanup, а не как full Grafana identity/provisioning rewrite.
- В [alert-history-service.json](/Users/vit/Documents/Projects/AMP/grafana/dashboards/alert-history-service.json) top-level title обновлен с `AMP - Alert History Service` на `AMP - Operations Dashboard`; `uid = amp-alert-history`, filename и весь dashboard content ниже сознательно оставлены без изменений.
- Проверка scope: targeted search по `AMP - Alert History Service|AMP - Operations Dashboard|amp-alert-history`, `jq '{title,uid,version}' grafana/dashboards/alert-history-service.json`, manual review против `docs/06-planning/BUGS.md` / `docs/06-planning/DECISIONS.md` / `README.md`, `git diff --check`.
- Ограничение: identity-shaped residual по `uid` и filename сознательно не маскировался этим task id и вынесен отдельно в `GRAFANA-DASHBOARD-IDENTITY-DRIFT`. Workspace архивирован в `tasks/archive/GRAFANA-DASHBOARD-BRANDING-DRIFT/`.
- **CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT** — завершен как narrow self-contained example-code alignment slice, а не как full rewrite examples policy или strict `pkg/core/interfaces` conformance pass.
- В [custom-publisher/main.go](/Users/vit/Documents/Projects/AMP/examples/custom-publisher/main.go) local `PublishingTarget` переведен с `webhook_url`-centric shape на current canonical field names `url`, `headers`, `filter_config`, `format`, `enabled`; `Publish(...)` теперь строит request через `target.URL` и честно применяет `target.Headers`, а sample target object в `main()` синхронизирован с новой local shape.
- Проверка scope: targeted search по `WebhookURL|webhook_url`, manual review против `docs/CONFIGURATION_GUIDE.md` / archived `PHASE-4` spec / `examples/README.md`, example sanity review, `git diff --check`.
- Ограничение: `examples/` остаются вне `go-app` module root, поэтому отдельного repo-local compile gate для этого slice нет; broader examples policy и strict `pkg/core/interfaces` rewrite сознательно оставлены вне scope. Workspace архивирован в `tasks/archive/CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT/`.
- **SOURCE-EXAMPLES-HISTORICAL-DRIFT** — завершен как source-example narrative/integration cleanup slice, а не как full alignment `examples/custom-*.go` to runtime internals.
- В [custom-classifier/main.go](/Users/vit/Documents/Projects/AMP/examples/custom-classifier/main.go) и [custom-publisher/main.go](/Users/vit/Documents/Projects/AMP/examples/custom-publisher/main.go) убраны historical `Alert History Service` references из top intro и footer/integration blocks; classifier guidance сужен до generic AMP classification flow, а publisher guidance больше не учит obsolete inline `publishing.targets` / `webhook_url` / `filters` story и вместо этого ссылается на current `publishing.*` + Secret discovery docs.
- Проверка scope: targeted marker scans по `examples/custom-*.go`, manual review против `docs/CONFIGURATION_GUIDE.md` / `docs/MIGRATION_QUICK_START.md` / archived `PHASE-4` spec, narrative sanity review, `git diff --check`.
- Ограничение: local executable `custom-publisher` demo shape сознательно не переписывался; residual code-shape mismatch вынесен в `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`. Workspace архивирован в `tasks/archive/SOURCE-EXAMPLES-HISTORICAL-DRIFT/`.
- **EXAMPLES-HISTORICAL-DOC-DRIFT** — завершен как Kubernetes example contract cleanup slice, а не как полный sweep по `examples/**`.
- В [pagerduty-secret-example.yaml](/Users/vit/Documents/Projects/AMP/examples/k8s/pagerduty-secret-example.yaml) и [rootly-secret-example.yaml](/Users/vit/Documents/Projects/AMP/examples/k8s/rootly-secret-example.yaml) examples приведены к canonical publishing Secret contract: `publishing-target=true`, `stringData.config` / `data.config`, generic `monitoring` namespace вместо historical `alert-history`, без legacy `target.json` и discrete secret-field shape.
- Проверка scope: targeted marker scan по `examples/k8s`, manual review против `docs/CONFIGURATION_GUIDE.md` / `docs/MIGRATION_QUICK_START.md` / archived `PHASE-4` spec, YAML sanity review, `git diff --check`.
- Ограничение: `.go` source examples сознательно оставлены вне scope; остаточный prose/integration drift вынесен в `SOURCE-EXAMPLES-HISTORICAL-DRIFT`. Workspace архивирован в `tasks/archive/EXAMPLES-HISTORICAL-DOC-DRIFT/`.
- **HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT** — завершен как narrow residual Helm template cleanup без повторного broad sweep по `helm/amp/templates/**`.
- В [postgresql-poddisruptionbudget.yaml](/Users/vit/Documents/Projects/AMP/helm/amp/templates/postgresql-poddisruptionbudget.yaml) и [postgresql-service-headless.yaml](/Users/vit/Documents/Projects/AMP/helm/amp/templates/postgresql-service-headless.yaml) `tn-98` приведен к `Operational hardening baseline`, а в [postgresql-exporter-configmap.yaml](/Users/vit/Documents/Projects/AMP/helm/amp/templates/postgresql-exporter-configmap.yaml) убраны `150% observability`, `50+ Metrics` и `150% Quality Target` из annotation/banner wording без правок SQL queries, metric names/descriptions или template semantics.
- Проверка scope: targeted marker scan, manual review против `README.md` / `docs/06-planning/DECISIONS.md` / `helm/amp/README.md`, `helm template amp-dev ./helm/amp -f helm/amp/values-dev.yaml --set profile=lite`, `helm template amp ./helm/amp -f helm/amp/values-production.yaml --set profile=standard`, `git diff --check`.
- Ограничение: `postgresql-configmap.yaml` сознательно оставлен вне scope как отдельный operational prose review, но текущий marker scan по `helm/amp/templates/**` больше не показывает confirmed historical markers этого класса. Workspace архивирован в `tasks/archive/HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT/`.
- **SECONDARY-REPO-DOC-HISTORICAL-DRIFT** — завершен как первый docs hygiene slice в broader secondary-doc domain: Helm operator-facing assets в `helm/amp/**` синхронизированы с current `AMP` / `controlled replacement` truth без расширения в `examples`, `grafana` или `go-app/internal/**`.
- В [helm/amp/DEPLOYMENT.md](/Users/vit/Documents/Projects/AMP/helm/amp/DEPLOYMENT.md), [helm/amp/values.yaml](/Users/vit/Documents/Projects/AMP/helm/amp/values.yaml), [helm/amp/values-dev.yaml](/Users/vit/Documents/Projects/AMP/helm/amp/values-dev.yaml), [helm/amp/values-production.yaml](/Users/vit/Documents/Projects/AMP/helm/amp/values-production.yaml) и выбранных PostgreSQL templates убраны stale `Alert History` / `Production-Ready` markers; hardcoded `llm.apiKey` defaults в values-файлах санитизированы.
- Проверка scope: targeted marker scan, secret-pattern scan, manual review против `README.md` / `docs/06-planning/DECISIONS.md` / `helm/amp/README.md`, `helm dependency build helm/amp`, `helm template amp-dev ./helm/amp -f helm/amp/values-dev.yaml --set profile=lite`, `helm template amp ./helm/amp -f helm/amp/values-production.yaml --set profile=standard`, `git diff --check`.
- Ограничение: repo-wide secondary docs cleanup этим не закрыт; остаток явно декомпозирован в `HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT`, `EXAMPLES-HISTORICAL-DOC-DRIFT`, `GRAFANA-DASHBOARD-BRANDING-DRIFT`, `INTERNAL-README-HISTORICAL-DRIFT`. Workspace архивирован в `tasks/archive/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/`.
- **APPLICATION-ROUTER-CONTRACT-DRIFT** — active `internal/application` contract suite приведен в соответствие с restored runtime surface без расширения production router scope.
- В `go-app/internal/application/router_contract_test.go` helper `newActiveContractMux(...)` теперь поднимает minimal honest reload-capable state через temporary config, deterministic `startTime` и real `ReloadCoordinator`, а old absent-surface assertion разделен на restored operational endpoints и still-absent historical surface.
- Проверка scope: `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -run TestActiveRuntimeContract -count=1`, `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application -count=1`, `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/... -count=1`, `git diff --check`.
- Ограничение: полный test scope пришлось подтверждать вне sandbox из-за `httptest.NewServer` bind restriction; это не residual code drift и не потребовало нового planning bug. Workspace архивирован в `tasks/archive/APPLICATION-ROUTER-CONTRACT-DRIFT/`.
- **RUNTIME-SURFACE-RESTORATION** — active runtime surface восстановлен для `GET /api/v2/status`, `GET /api/v2/receivers`, `GET /api/v2/alerts/groups` и `POST /-/reload`, а public/docs truth синхронизирован с этим mounted contract.
- В `go-app/internal/application/router.go` эти endpoints снова смонтированы через `StatusAPIHandler`, `ReceiversHandler`, `AlertGroupsHandler` и `ReloadHandler`; `ServiceRegistry` получил `startTime` и `ReloadCoordinator`, а `Config` — minimal `receivers` snapshot.
- Проверка scope: `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/handlers -count=1`, `git diff --check`.
- Ограничение: active application contract suite все еще держит старое ожидание “wide surface absent” и вынесен в отдельный bug `APPLICATION-ROUTER-CONTRACT-DRIFT`; full `futureparity` historical path по-прежнему остается отдельным residual gap. Workspace уже архивирован в `tasks/archive/RUNTIME-SURFACE-RESTORATION/`.
- **REPO-TEST-MATRIX-RED** — завершен как stabilization slice: panic/fixture/config-level red matrix сужен до двух отдельных logic-level follow-up bugs.
- Устранены duplicate metrics, sqlite/Redis test-config drift, nil-logger panics, часть SQL/test-fixture проблем и retryable-error groundwork; green подтвержден для `internal/application/handlers`, `internal/infrastructure/k8s`, `internal/infrastructure/migrations`, `internal/infrastructure/webhook`, `pkg/telemetry`, `pkg/httperror`, а вне sandbox также для `internal/infrastructure/inhibition` и `internal/infrastructure/publishing`.
- Проверка scope: `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/handlers ./internal/infrastructure/k8s ./internal/infrastructure/migrations ./internal/infrastructure/webhook ./pkg/telemetry ./pkg/httperror -count=1`, вне sandbox `go test ./internal/infrastructure/inhibition ./internal/infrastructure/publishing -count=1`, `git diff --check`.
- Ограничение: оставшиеся логические падения вынесены отдельно в `PUBLISHING-HEALTH-REFRESH-DRIFT` и `REPOSITORY-FLAPPING-TRANSITIONS-DRIFT`; workspace уже архивирован в `tasks/archive/REPO-TEST-MATRIX-RED/`.
- **FUTUREPARITY-SUITE-DRIFT** — завершен узкий code/test slice для opt-in historical `futureparity`: missing helper/env/bootstrap seams возвращены в explicit build-tagged compatibility owner без расширения active runtime.
- В `go-app/cmd/server/futureparity_compat.go` собран historical compatibility harness, а в `go-app/cmd/server/futureparity_compat_test.go` добавлен tagged smoke path для route registration и deterministic `configSHA256`.
- Проверка scope: `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run TestDoesNotExist -count=1`, `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -tags=futureparity -run 'TestFutureParityHarness|TestFutureParityConfigHash' -count=1`, `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./cmd/server -count=1`, `git diff --check`.
- Ограничение: full `go test ./cmd/server -tags=futureparity -count=1` остается red на residual historical/runtime mismatch и одном sandbox-limited subtest; это не скрыто и вынесено в `docs/06-planning/BUGS.md` как `FUTUREPARITY-HISTORICAL-RUNTIME-GAP`; workspace архивирован в `tasks/archive/FUTUREPARITY-SUITE-DRIFT/`.
- **REPO-DOC-LICENSE-DRIFT** — завершен narrow docs-only cleanup для `CONTRIBUTING.md`, `examples/README.md`, `go-app/pkg/core/README.md` и `go-app/internal/infrastructure/llm/README.md`.
- В этих четырех файлах убраны scoped license/status/branding/package-contract drift: contribution clause выровнен под `AGPL-3.0`, examples index очищен от stale repo story, `pkg/core` и `llm` README сужены до factual local contract.
- Проверка scope: targeted drift-marker scan, manual review против `LICENSE` / `README.md` / `DECISIONS.md`, link/path sanity и `git diff --check` проходят; кодовые runtime gates не требовались, потому что slice не меняет код.
- Ограничение: более широкий historical repo-doc drift вне этого four-file scope не скрыт и остается открытым в `docs/06-planning/BUGS.md` как `SECONDARY-REPO-DOC-HISTORICAL-DRIFT`; workspace архивирован в `tasks/archive/REPO-DOC-LICENSE-DRIFT/`.
- **UI-PLACEHOLDER-REMOVAL** — active `/dashboard/silences`, `/dashboard/llm` и `/dashboard/routing` больше не возвращают placeholder body и закреплены как honest read-only страницы на текущем `/dashboard/*` surface.
- Render path вынесен в `go-app/cmd/server/legacy_dashboard.go` с отдельным simple template stack `go-app/cmd/server/templates/legacy/*`; page-facing runtime summaries собираются через `go-app/internal/application/legacy_dashboard.go`.
- Default non-tagged coverage добавлена в `go-app/cmd/server/legacy_dashboard_test.go`; scope подтвержден через `go test ./cmd/server`, `go test ./internal/application/...`, `go build ./cmd/server`, `git diff --check`.
- Ограничение: opt-in historical `futureparity` suite и полный repo gate остаются вне scope и по-прежнему отражаются отдельно в `docs/06-planning/BUGS.md`; workspace архивирован в `tasks/archive/UI-PLACEHOLDER-REMOVAL/`.
- **PHASE-3-STORAGE-HARDENING** — active bootstrap/storage path hardened: `ProfileLite` теперь поднимает `SQLiteDatabase`, `ProfileStandard` идет через `PostgresPool + goose + thin Postgres storage adapter`, а required storage failures больше не маскируются под pseudo-healthy startup.
- Health plane переведен на state-aware contract: `/health|/healthz` отражают liveness, `/ready|/readyz` отражают readiness, `/-/healthy|/-/ready` сохраняют plain-text Alertmanager-compatible probes, optional degradations видны как `degraded`.
- Planning/public docs и ADR синхронизированы с новым runtime truth; workspace архивирован в `tasks/archive/PHASE-3-STORAGE-HARDENING/`.
- Проверка scope: `go test ./internal/application/... ./internal/database`, `go test ./internal/infrastructure -run SQLiteDatabase`, `go build ./cmd/server`, `git diff --check` проходят.
- Ограничение: полный `go test ./...` остается red на preexisting проблемах вне scope текущего slice; актуальный список зафиксирован в `docs/06-planning/BUGS.md`.

## 2026-03-08
- **DOCS-HONESTY-PASS** — top-level public/docs honesty slice завершен: README, migration/compatibility docs и chart surface переведены на `controlled replacement` / `active-runtime-first` narrative.
- Убраны direct overclaims про `drop-in replacement`, `100% API compatibility`, неподтвержденные benchmark/resource figures, конфликтный install story и top-level license mismatch в core public/docs scope.
- `helm/amp/README.md` и `helm/amp/Chart.yaml` выровнены с repo-local source of truth (`./helm/amp`, AGPL-3.0, phased parity); residual deeper repo-doc cleanup вынесен в `BUGS.md` как `REPO-DOC-LICENSE-DRIFT`.
- Проверка scope: targeted review, search pass по overclaim markers и `git diff --check` для touched docs/metadata files проходят; workspace архивирован в `tasks/archive/DOCS-HONESTY-PASS/`.

- **ALERTMANAGER-REPLACEMENT-SCOPE** — truth-alignment slice завершен: source of truth для replacement story закреплен за active runtime, current claim сужен до `controlled replacement`.
- Historical wide-surface parity вынесен из default `cmd/server` path под build tag `futureparity`, а active router contract зафиксирован отдельными tests.
- Planning/public docs синхронизированы с active-runtime-first narrative; follow-up work вынесен в `DOCS-HONESTY-PASS`, `RUNTIME-SURFACE-RESTORATION` и `FUTUREPARITY-SUITE-DRIFT`.
- Ограничение: opt-in `futureparity` suite остается red, а полный docs honesty pass по performance/license/install claims еще не завершен; workspace архивирован в `tasks/archive/ALERTMANAGER-REPLACEMENT-SCOPE/`.

- **PHASE-4-PRODUCTION-PUBLISHING-PATH** — active runtime переведен с `SimplePublisher` stub на real publishing path через adapter/coordinator/queue и explicit `metrics-only` fallback.
- Добавлены typed `publishing.*` config, lifecycle wiring в `ServiceRegistry`, queue/mode/discovery metrics, canonical Kubernetes Secret contract (`publishing-target=true` + `data.config`) и Helm/runtime env alignment.
- Документация и production examples синхронизированы с runtime contract; workspace архивирован в `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/`.
- Проверка scope: `go build ./cmd/server` и targeted tests для измененного publishing path проходят.
- Ограничение: full repo gate (`go vet ./...`, `go test ./...`, `make quality-gates`) остается red на preexisting проблемах вне scope задачи; они зафиксированы в `docs/06-planning/BUGS.md`.

- **PHASE-2: Bootstrap Consolidation** — `go-app/cmd/server/main.go` разделен на компоненты.
- Внедрены `ServiceRegistry`, `Router` и пакет `handlers`.
- Хранилища вынесены в `internal/infrastructure/storage/memory`.
- Чистый `main.go` (~200 строк) обеспечивает запуск и управление жизненным циклом.
- Проверка: `go build ./cmd/server` (успешно).

- **SOLO-KANBAN-INIT** — Процесс Solo Kanban и planning-структура синхронизированы с текущим состоянием репозитория.
- Созданы и приведены в актуальное состояние `WORKFLOW.md`, `docs/06-planning/`, `tasks/solo-kanban-init/` и шаблоны задач.
- Выполненные и открытые элементы из `.plans` перенесены в `DONE.md`, `NEXT.md`, `BACKLOG.md`, `BUGS.md`, `ROADMAP.md`.

## 2026-02-27
- **PHASE-1: API Unstabbing** — активный runtime в `go-app/cmd/server/main.go` переведен на реальные handlers для core API.
- В активном пути были сняты ключевые stubs для ingest/silence/runtime bootstrap, но historical parity narrative позже разошёлся с текущим active router и больше не считается source of truth без отдельной проверки по коду.
- Проверка: `make test`, `make test-upstream-parity`, `go test ./cmd/server -run Phase0 -v`.

## 2026-02-25
- **PHASE-0: Baseline and Contract Lock** — route inventory и baseline contract tests добавлены для активного runtime.
- Источник фиксации: `.plans/phase0-baseline-report.md`.
