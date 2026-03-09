# Implementation Checklist: REPO-TEST-MATRIX-RED

## Research & Spec
- [x] Выполнена локализация исходного red-matrix по duplicate metrics, config/driver drift, nil logger panic и error-classification mismatch.
- [x] Подготовлен `Spec.md` с разбиением по типам отказов и целевому targeted verification path.

## Implementation
- [x] Duplicate metrics устранены в затронутых пакетах через изолированные тестовые registry / updated metrics wiring.
- [x] `internal/infrastructure/inhibition` и `internal/infrastructure/migrations` очищены от invalid config / sqlite-driver drift.
- [x] `internal/infrastructure/publishing`, `internal/infrastructure/webhook`, `internal/infrastructure/k8s`, `pkg/telemetry` и `pkg/httperror` получили фиксирующие изменения против panics, matcher drift и retryable-error mismatch.
- [x] `internal/infrastructure/repository` очищен от части SQL/test-fixture drift, но package все еще не полностью green.
- [x] `internal/business/publishing` и остаточный repository drift вынесены в отдельные follow-up bugs вместо дальнейшего разрастания текущего slice.

## Verification
- [x] `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/handlers ./internal/infrastructure/k8s ./internal/infrastructure/migrations ./internal/infrastructure/webhook ./pkg/telemetry ./pkg/httperror -count=1`
- [x] Sandbox rerun зафиксировал environment-specific ограничения для listener/Docker-based tests и не скрыл их.
- [x] Non-sandbox rerun подтвердил green для `./internal/infrastructure/inhibition` и `./internal/infrastructure/publishing`.
- [x] Остаток после verification не скрыт и не объявлен green по умолчанию.

## Current Residuals
- [x] `internal/business/publishing` все еще red на `TestHealthMonitor_*`, `TestSanitizeErrorMessage`, `refresh_*` и связанных flaky/error-classification assertions.
- [x] `internal/infrastructure/repository` все еще red на `TestGetFlappingAlerts_MultipleTransitions`.

## Documentation & Cleanup
- [x] `docs/06-planning/BUGS.md` и `docs/06-planning/NEXT.md` синхронизированы с закрытием slice и переносом остатка в отдельные bugs.
- [x] Slice перенесен в `DONE.md` как stabilization pass, а не как полный matrix-closure.
