# Implementation Checklist: ALERTMANAGER-REPLACEMENT-SCOPE

## Research & Spec
- [x] Завершен `research.md` по active runtime, historical parity drift и replacement options.
- [x] Подготовлен `Spec.md` с canonical source of truth, replacement scope и границами truth-alignment slice.

## Vertical Slices
- [x] **Slice A: Scope + Planning Alignment** — зафиксировать `controlled replacement` как текущий claim, выровнять ADR/planning artifacts и убрать внутренние противоречия.
- [x] **Slice B: Verification Model Split** — разделить current active runtime contract и historical/future parity expectations, подготовить follow-up backlog для runtime surface restoration.

## Implementation
- [x] Шаг 1: Зафиксировать decision artifact: current source of truth = active runtime, current claim = `controlled replacement`.
- [x] Шаг 2: Обновить `docs/06-planning/DECISIONS.md` так, чтобы ADR-002 не конфликтовал с current router и policy по `/api/v1/alerts`.
- [x] Шаг 3: Синхронизировать planning statements в `DONE.md`, `BUGS.md` и при необходимости `NEXT.md`/`ROADMAP.md`, чтобы active runtime не описывался шире кода.
- [x] Шаг 4: Определить судьбу historical wide-surface statements в `README.md`, `docs/ALERTMANAGER_COMPATIBILITY.md`, migration docs: что переводится в follow-up, а что должно быть убрано как active claim.
- [x] Шаг 5: Разделить parity expectations на `active runtime contract` и `future/backlog parity` для `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go`.
- [x] Шаг 6: Завести или уточнить follow-up item для runtime/API restoration (`status`, `receivers`, `alerts/groups`, `/-/reload`, optional wider surface), если stronger replacement claim всё ещё нужен.

## Testing
- [x] Проверить consistency выбранного scope через targeted review измененных planning/docs/test files.
- [x] Если будут изменены tests, прогнать только затронутые suites или compile-level validation для них.
- [x] Targeted validation проходит: `go test ./internal/application -run 'TestActiveRuntimeContract_PresentEndpoints|TestActiveRuntimeContract_HistoricalWideSurfaceIsAbsent' -v`, `go test ./internal/application/handlers -run 'TestAlertsHandler_PostLegacyPayloadUsesProcessorAndStoresAlert|TestAlertsHandler_PostPrometheusPayloadUsesProcessorAndStoresAlert|TestAlertsHandler_SilencedAlertIsSuppressed' -v`.
- [x] Default compile/test path для `cmd/server` больше не блокируется historical parity suite: `go test ./cmd/server -run TestDoesNotExist`.
- [ ] Opt-in historical suite `go test ./cmd/server -tags=futureparity -run TestDoesNotExist` остается red на stale helpers (`runtimeStateFileEnv`, `registerRoutes`, `configSHA256`) и зафиксирован в `docs/06-planning/BUGS.md` как `FUTUREPARITY-SUITE-DRIFT`.
- [x] `git diff --check` проходит.
- [x] Full repo gate (`go vet ./...`, `go test ./...`) не используется как blocker для этого truth-alignment slice и остаётся отдельно задокументированным preexisting concern.

## Documentation & Cleanup
- [x] Синхронизировать `requirements.md`, если в ходе работы изменится фактический scope truth-alignment slice.
- [x] Обновить `Spec.md`, если реальные решения отклонятся от текущих assumptions.
- [x] Сохранить links между truth-alignment решением, `ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md` и follow-up backlog items.
- [x] Перед `/end-task` обновить planning artifacts и финальный статус задачи.

## Final Status
- Truth-alignment slice завершен: `controlled replacement` и `active-runtime-first` закреплены как current replacement story.
- Default verification path для current runtime зеленый; historical wide-surface expectations вынесены в `futureparity`.
- Открытые follow-ups ограничены `DOCS-HONESTY-PASS`, `RUNTIME-SURFACE-RESTORATION` и `FUTUREPARITY-SUITE-DRIFT`.
