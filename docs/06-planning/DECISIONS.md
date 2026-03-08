# Архитектурные решения (DECISIONS)

## ADR-001: Go как основной язык runtime
- **Дата**: 2026-02 (фиксация факта)
- **Контекст**: Проект начинался на Python, но core runtime переписан на Go для совместимости с Alertmanager API.
- **Решение**: Go — основной язык для серверной части. Python-код удалён.
- **Следствие**: API-совместимость с Alertmanager проще поддерживать на том же языке.

## ADR-002: Alertmanager Replacement Scope Is Active-Runtime-First
- **Дата**: 2026-03-08
- **Контекст**: Historical docs, DONE entries и parity tests начали описывать runtime шире, чем текущий active bootstrap в `go-app/cmd/server/main.go` и `go-app/internal/application/router.go`.
- **Решение**:
  - source of truth для replacement story — active runtime, а не historical docs/tests;
  - текущий допустимый claim — только `controlled replacement`, не `general-purpose drop-in replacement`;
  - текущий active scope ограничен alert ingest, silence CRUD, health/readiness, metrics и real publishing path;
  - deprecated `v1` endpoints не входят в current active scope;
  - wide-surface parity expectations фиксируются как future/backlog parity до отдельного runtime-restoration slice.
- **Следствие**:
  - planning/docs/tests должны синхронизироваться с active runtime first;
  - historical parity suites не должны автоматически определять публичные claims;
  - если нужен stronger Alertmanager replacement claim, это оформляется отдельной runtime/API задачей.

## ADR-003: Solo Kanban (SEMA) как процесс разработки
- **Дата**: 2026-03-08
- **Контекст**: Один разработчик + AI-агент. Нужен легковесный, но структурированный процесс.
- **Решение**: Solo Kanban с WIP max 2, балансом 50/50 maintenance/roadmap, вертикальными срезами и quality gates.
- **Следствие**: Planning files версионируются в `docs/06-planning/`, задачи в `tasks/`.
