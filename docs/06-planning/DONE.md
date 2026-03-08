# DONE

## 2026-03-08
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
- В активном пути работают `alerts`, `silences`, `status`, `webhook`, `receivers`, `alert groups`, `config*`, `classification/*`, `history`.
- Проверка: `make test`, `make test-upstream-parity`, `go test ./cmd/server -run Phase0 -v`.

## 2026-02-25
- **PHASE-0: Baseline and Contract Lock** — route inventory и baseline contract tests добавлены для активного runtime.
- Источник фиксации: `.plans/phase0-baseline-report.md`.
