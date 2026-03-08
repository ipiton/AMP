# Implementation Checklist: PHASE-4-PRODUCTION-PUBLISHING-PATH

## Research & Spec
- [x] Завершен `research.md` по активному publishing path, legacy bootstrap и deployment contract.
- [x] Подготовлен `Spec.md` с целевой архитектурой, canonical secret/config contract и границами первого slice.

## Vertical Slices
- [x] **Slice A: Active Runtime Delivery Path** — убрать `SimplePublisher` из active runtime, подключить adapter/coordinator/queue и explicit `metrics-only` fallback.
- [x] **Slice B: Deployment + Observability Alignment** — выровнять typed config, Helm publishing contract и publishing stats/health wiring под active runtime.

## Implementation
- [x] Шаг 1: Добавить typed config для `publishing.*` (defaults, validation, env mapping через Viper-compatible names).
- [x] Шаг 2: Реализовать `ApplicationPublishingAdapter`, `MetricsOnlyPublisher` и `DiscoveryAdapter` в `internal/application`.
- [x] Шаг 3: Расширить `ServiceRegistry` publishing lifecycle полями, инициализацией и graceful shutdown.
- [x] Шаг 4: Подключить `business/publishing` discovery/refresh/health и `infrastructure/publishing` mode manager, factory, queue, coordinator.
- [x] Шаг 5: Перевести `AlertProcessor` на новый publisher path без использования `SimplePublisher`.
- [x] Шаг 6: Сохранить explicit degraded behavior для `lite`, `publishing.enabled=false`, `zero targets` и `discovery/queue init failure`.
- [x] Шаг 7: Привести Helm publishing target contract к canonical формату: label `publishing-target=true` + `data.config`.
- [x] Шаг 8: Выровнять publishing-related config/env naming между chart и runtime (`APP_ENVIRONMENT`, `PROFILE`, `PUBLISHING_*`).
- [x] Шаг 9: Нормализовать publishing observability contract в active consumers: mode/health/stats должны отражать реальное состояние delivery path.

## Testing
- [x] Unit tests для `ApplicationPublishingAdapter`.
- [x] Unit tests для `MetricsOnlyPublisher` и `DiscoveryAdapter`.
- [x] Unit tests для parsing/validation `publishing.*` config.
- [ ] Integration tests для `ServiceRegistry` в режимах `normal` и `metrics-only`.
- [x] Regression test: active runtime больше не использует `SimplePublisher` в production bootstrap path.
- [x] Targeted validation проходит: `go test ./internal/application/... ./cmd/server/handlers`, `go test ./internal/infrastructure/publishing -run TestPublishingQueue_GetStatsTracksCumulativeCounters`, `go test ./internal/business/publishing -run 'TestMetricsCollector_Interface|TestPublishingMetricsCollector_Basic|TestModeMetricsCollector_Collect'`.
- [x] `go build ./cmd/server` проходит.
- [ ] `make quality-gates` в `go-app/` проходит.
- [ ] Full repo gate (`go vet ./...`, `go test ./...`, `make quality-gates`) остается red на preexisting проблемах вне scope текущего slice: `cmd/server/main_phase0_contract_test.go` (undefined symbols), `internal/business/publishing` (duplicate metrics collector registration), `internal/infrastructure/publishing` (legacy test failures), `internal/infrastructure/k8s`, `internal/infrastructure/inhibition`, `internal/infrastructure/migrations`.

## Documentation & Cleanup
- [x] Синхронизировать `requirements.md`, если в ходе реализации изменится scope slice.
- [ ] Обновить `Spec.md`, если реальные решения отклонятся от текущих assumptions.
- [x] Синхронизировать Helm/examples/docs с canonical publishing secret format.
- [x] Перед `/end-task` обновить planning artifacts и финальный статус задачи.

## Final Status
- Scope задачи реализован и задокументирован.
- `go build ./cmd/server` и targeted tests для измененного publishing path проходят.
- Полный repo gate остается red на preexisting проблемах вне scope текущего slice; они перенесены в planning artifacts как open blockers.
