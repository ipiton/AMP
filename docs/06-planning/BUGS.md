# BUGS

## Open
- [ ] **UI-PLACEHOLDER-PAGES** — `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` возвращают `500` и `not yet implemented` из `go-app/cmd/server/main.go`.
- [ ] **REPO-DOC-LICENSE-DRIFT** — после `DOCS-HONESTY-PASS` core public/docs surface выровнена, но в repo остаются internal/subpackage docs с историческими claims вроде `Apache 2.0` / `Production-Ready` (`CONTRIBUTING.md`, `examples/README.md`, `go-app/pkg/core/README.md`, `go-app/internal/infrastructure/llm/README.md`). Это уже вне минимального top-level public scope, но все еще требует cleanup.
- [ ] **FUTUREPARITY-SUITE-DRIFT** — opt-in historical suite `go test ./cmd/server -tags=futureparity` по-прежнему не компилируется: `main_phase0_contract_test.go` ссылается на отсутствующие `runtimeStateFileEnv`, `registerRoutes`, `configSHA256`. После split это больше не блокирует default active-runtime path, но остаётся backlog на refresh/restoration.
- [ ] **REPO-TEST-MATRIX-RED** — полный `go test ./...` остается red на preexisting проблемах в `internal/business/publishing`, `internal/infrastructure/publishing`, `internal/infrastructure/k8s`, `internal/infrastructure/inhibition`, `internal/infrastructure/migrations`.

## Resolved
- [x] ~~**SERVICE-REGISTRY-STUB-PATH**~~ — active runtime переведен с `SimplePublisher` stub на real publishing path через `ServiceRegistry`. Закрыто 2026-03-08 (`PHASE-4-PRODUCTION-PUBLISHING-PATH`).
- [x] ~~**ACTIVE-RUNTIME-COMPATIBILITY-DRIFT**~~ — active runtime закреплен как canonical source of truth, ADR/planning/default verification path больше не маскируют historical wide surface под current active contract. Закрыто 2026-03-08 (`ALERTMANAGER-REPLACEMENT-SCOPE`).
- [x] ~~**CMD-SERVER-PHASE0-CONTRACT-DRIFT**~~ — historical wide-surface suites вынесены из default path под build tag `futureparity`, поэтому они больше не ломают обычный `go test ./cmd/server` для current active runtime. Закрыто 2026-03-08 (`ALERTMANAGER-REPLACEMENT-SCOPE`).
- [x] ~~**DOCS-OVERCLAIM-COMPATIBILITY**~~ — core public/docs and chart surface больше не держат direct overclaims про `drop-in replacement`, `100% API compatibility`, неподтвержденные benchmark/resource figures, конфликтный install story и top-level license mismatch. Закрыто 2026-03-08 (`DOCS-HONESTY-PASS`).
