---
id: SILENCED-ALERTS-INGEST
slug: silenced-alerts-ingest
stream: API
type: bug
status: active
created_at: 2026-10-23
updated_at: 2026-10-23
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: заглушенный алерт проходит ingest

## Touched Files

- `go-app/internal/core/services/alert_processor.go`
- `go-app/internal/application/service_registry.go`
- `go-app/internal/application/handlers/alerts.go`
- `go-app/internal/application/handlers/alerts_test.go`, `go-app/cmd/server/main_phase0_contract_test.go` (`TestPhase0SilenceAffectsAlertIngest`, `TestPhase0E2ESmoke_IngestSilenceAndHistoryRecent`, тег `futureparity`) — существующие тесты под новое поведение (Phase 1, иначе гейт красный)
- `go-app/internal/core/services/alert_processor_silence_test.go` — Phase 2
- `CHANGELOG.md`

## Phase 1: Implement

- [x] **1.1** `SilenceChecker` в `AlertProcessorConfig`, проверка в `ProcessAlert` <!-- verify: cd go-app && go build ./... -->
- [x] **1.2** Сборка в `service_registry.go` <!-- verify: cd go-app && go build ./... -->
- [x] **1.3** Убрать фильтр из `handleAlertsPost`; переписать `TestPhase0SilenceAffectsAlertIngest` и `TestAlertsHandler_SilencedAlertIsSuppressed` <!-- verify: go test ./cmd/server/ ./internal/application/handlers/ -->
- [x] **1.4** CHANGELOG [Unreleased] Fixed + Breaking changes <!-- verify: git diff --check -->

## Phase 2: Tests (после deep-review `pass`)

- [x] **2.1** Процессор: прямой режим (firing заглушен/нет, resolved), группировка (в группу, без публикации и LLM), кеш inhibition <!-- verify: go test -race -run Silence ./internal/core/services/ — с настоящими DeduplicationService и SimpleFilterEngine (сценарии B/C deep-review) -->

## Phase 3: Gates

- [x] **3.1** Гейты `WORKFLOW.md` § Гейты AMP <!-- verify: evidence/release-gate.txt -->

## Notes
- 2026-10-23, deep-review: раунд 1 `fix_required` @ `336aeb5` (F1 major — алерт без `endsAt` после снятия silence не публиковался: dedup `ignored`), раунд 2 `fix_required` @ `bc9a0be` (F6 major — правка F2 вызывала фильтр с состоянием Rule 7 и снова ломала F1), раунд 3 `pass` @ `d179b42`. F2 → TECH-DEBT `SILENCED-GROUP-UNFILTERED`, F5 → BACKLOG `SILENCE-SKIP-METRIC`.
- 2026-10-23, testing: `go vet`, `go test -race` services/application, `-tags futureparity ./cmd/server/` зелёные; новые тесты краснеют при откате фикса F1; `make -C go-app quality-gates-fast` зелёный; `scripts/release-gate.sh` — 11/12 PASS, `lint` FAIL только в `delivery_confirmation_test.go` и `silencing/redis_event_bus_test.go` (предсуществующий `LINT-ERRCHECK-CHANNEL-RECEIVE`). `git diff --check` чистый. Флейк: один раз упал `internal/infrastructure/inhibition` в полном прогоне `go test ./...`, пакет диффом не затронут, три повтора зелёные.
