# Архитектурные решения (DECISIONS)

## ADR-001: Go как основной язык runtime
- **Дата**: 2026-02 (фиксация факта)
- **Контекст**: Проект начинался на Python, но core runtime переписан на Go для совместимости с Alertmanager API.
- **Решение**: Go — основной язык для серверной части. Python-код удалён.
- **Следствие**: API-совместимость с Alertmanager проще поддерживать на том же языке.

## ADR-002: Alertmanager API compatibility (non-deprecated endpoints)
- **Дата**: 2026-02-25
- **Контекст**: Нужно определить scope совместимости с upstream Alertmanager.
- **Решение**: Поддерживаем только non-deprecated core endpoints (alerts, silences, status, receivers, alert groups, config). Deprecated endpoints (v1 API) не реализуем.
- **Следствие**: Contract tests фиксируют method/route matrix. Regression test `TestUpstreamParity_CoreEndpointMethodMatrix` блокирует нарушения.

## ADR-003: Solo Kanban (SEMA) как процесс разработки
- **Дата**: 2026-03-08
- **Контекст**: Один разработчик + AI-агент. Нужен легковесный, но структурированный процесс.
- **Решение**: Solo Kanban с WIP max 2, балансом 50/50 maintenance/roadmap, вертикальными срезами и quality gates.
- **Следствие**: Planning files версионируются в `docs/06-planning/`, задачи в `tasks/`.
