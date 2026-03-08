# BUGS

## Open
- [ ] **UI-PLACEHOLDER-PAGES** — `/dashboard/silences`, `/dashboard/llm`, `/dashboard/routing` возвращают `500` и `not yet implemented` из `go-app/cmd/server/main.go`.
- [ ] **DOCS-OVERCLAIM-COMPATIBILITY** — публичные docs все еще заявляют production-ready / drop-in replacement / `100% compatibility`, хотя в репозитории остаются незавершенные UI и delivery-path задачи.
- [ ] **CMD-SERVER-PHASE0-CONTRACT-DRIFT** — `go-app/cmd/server/main_phase0_contract_test.go` ссылается на отсутствующие `runtimeStateFileEnv`, `registerRoutes`, `configSHA256`; это блокирует `go vet ./...` и `make quality-gates`.
- [ ] **REPO-TEST-MATRIX-RED** — полный `go test ./...` остается red на preexisting проблемах в `internal/business/publishing`, `internal/infrastructure/publishing`, `internal/infrastructure/k8s`, `internal/infrastructure/inhibition`, `internal/infrastructure/migrations`.

## Resolved
- [x] ~~**SERVICE-REGISTRY-STUB-PATH**~~ — active runtime переведен с `SimplePublisher` stub на real publishing path через `ServiceRegistry`. Закрыто 2026-03-08 (`PHASE-4-PRODUCTION-PUBLISHING-PATH`).
