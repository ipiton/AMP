# DONE

## 2026-03-09
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
