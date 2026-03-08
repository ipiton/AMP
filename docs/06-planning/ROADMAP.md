# Стратегический план (ROADMAP)

Общие цели и стримы развития проекта.

## Stream: Runtime/API Stabilization
- [x] **PHASE-0: Baseline and Contract Lock** — Тестовая база для фиксации текущего поведения активного runtime.
- [x] **PHASE-1: API Unstabbing** — Активный runtime переведен на реальные обработчики core API (`status`, `alerts`, `silences`, `webhook`) и закреплен тестами.
- [ ] **PHASE-2: Bootstrap Consolidation** — Единый путь инициализации, удаление `main.go.full`.

## Stream: Storage & Reliability
- [ ] **PHASE-3: Storage Hardening** — Стабильный startup/shutdown, migrations, health decomposition.

## Stream: Delivery & Publishing
- [ ] **PHASE-4: Production Publishing Path** — Реальный publisher path, retries/rate limits и метрики.

## Stream: Intelligence (ML/LLM/MCP)
- [ ] **PHASE-5: ML/LLM Integration** — End-to-end classification path, provider switch и fallback-политики.
- [ ] **PHASE-6: MCP Context Triage MVP** — Сбор контекста (K8s, Deploy, Metrics) + гипотезы и чеклисты.
- [ ] **PHASE-7: UI/UX Workflow** — Интеграция триажа в интерфейс, Human-in-the-loop.

## Stream: Release
- [ ] **PHASE-8: Release & Rollout** — Quality gates, documentation, canary rollout.

## Notes
- Статус синхронизирован с кодом и тестами по состоянию на 2026-03-08.
- Источники: `.plans/phase0-baseline-report.md`, `.plans/runtime-status-2026-02-25.md`, `go-app/cmd/server/main_phase0_contract_test.go`, `go-app/cmd/server/main_upstream_parity_regression_test.go`.
