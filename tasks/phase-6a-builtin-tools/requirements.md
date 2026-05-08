# PHASE-6A-BUILTIN-TOOLS — Requirements

## Контекст

LLM-агент расследования (PHASE-5B) уже работает: есть agentic loop, `ToolRegistry`, `Tool` интерфейс.
Но без реальных tools агент видит только alert labels и ничего больше — расследование бесполезно.
Задача: реализовать 4 built-in инструмента, которые дают агенту доступ к реальным данным инфраструктуры.

## Проблема

Агент запрашивает tool calls, но `ToolRegistry` пустой — все вызовы возвращают `unknown tool`.
Без метрик, логов, pod-состояния и DB-статистики LLM не может выдать осмысленный root cause.

## Цель

Наполнить `ToolRegistry` четырьмя production-ready инструментами:
- **prometheus** — PromQL-запросы к Prometheus HTTP API
- **loki** — LogQL-запросы к Loki HTTP API
- **kubernetes** — pod status, events, logs, deployments через existing K8s client
- **database** — PostgreSQL диагностика через existing DB connection

## Success Criteria

- [ ] Каждый tool реализует `investigation.Tool` интерфейс (`Definition()` + `Execute()`)
- [ ] Все tools регистрируются в `ToolRegistry` при старте агента
- [ ] Prometheus tool: query_range с time window ±15min вокруг alert time
- [ ] Loki tool: LogQL query с фильтрацией по namespace/pod и уровню (error/warn)
- [ ] K8s tool: pod list, events, container logs, resource usage
- [ ] DB tool: `pg_stat_activity`, `pg_stat_statements`, replication lag
- [ ] Каждый tool: таймаут (configurable), graceful error handling (не крашит loop)
- [ ] Config-driven endpoints — не хардкод URL
- [ ] Unit-тесты с mock HTTP servers (Prometheus/Loki) и mock K8s client

## Scope

**В рамках задачи:**
- Реализация 4 tool-файлов в `internal/core/investigation/tools/`
- Config структуры для каждого tool (endpoint, timeout, auth)
- Wiring в DI/service initialization
- Unit-тесты с mock-серверами

**Вне рамок задачи:**
- PHASE-6D (per-environment routing) — отдельная задача
- PHASE-6C (MCP tools) — отдельная задача
- PHASE-6B (runbook engine) — отдельная задача
- UI для отображения tool results (PHASE-7A)
