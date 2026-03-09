# Implementation Checklist: RUNTIME-SURFACE-RESTORATION

## Research & Spec
- [x] Исходный gap по `status` / `receivers` / `alerts/groups` / `reload` зафиксирован в `research.md`.
- [x] Подготовлен `Spec.md` с опорой на active router и `ServiceRegistry`, без возврата в broad historical parity claim.

## Implementation
- [x] `ServiceRegistry` получил `startTime`, `ReloadCoordinator` и `ReloadConfig(ctx)`.
- [x] В `Config` добавлен доступ к `receivers`.
- [x] В active router смонтированы `GET /api/v2/status`, `GET /api/v2/receivers`, `GET /api/v2/alerts/groups`, `POST /-/reload`.
- [x] Добавлены handler-level tests для `status`, `reload`, `receivers` и grouped alerts.

## Verification
- [x] `cd go-app && GOCACHE=$(pwd)/.cache/go-build go test ./internal/application/handlers -count=1`
- [x] Residual contract red не скрыт и оформлен как отдельный bug.

## Current Residuals
- [x] `internal/application/router_contract_test.go` все еще описывает restored endpoints как отсутствующие и требует contract refresh.
- [x] Test registry для reload path не инициализирует `ReloadCoordinator`, поэтому reload subtest получает `500` вместо honest active-runtime expectation.

## Documentation & Cleanup
- [x] `docs/ALERTMANAGER_COMPATIBILITY.md`, `README.md`, migration docs и Helm README синхронизированы с фактически смонтированным surface.
- [x] Planning artifacts обновлены так, чтобы задача закрывалась, а residual contract refresh жил отдельным bug.
- [x] Slice перенесен в `DONE.md` как runtime/doc restoration task.
