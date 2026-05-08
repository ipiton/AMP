# Forge Briefing: phase-6a-builtin-tools


## Requirements
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


## Specification
# PHASE-6A-BUILTIN-TOOLS — Spec

## Архитектурные решения

**Решение 1: tools в `internal/core/investigation/tools/`**
Не в `infrastructure/` — tools — это часть investigation domain logic, а не general инфра.
Infrastructure (k8s client, llm client) остаётся в `infrastructure/`, tools её оборачивают.

**Решение 2: `action` param для K8s tool**
Один tool с `action` вместо отдельного tool на каждую операцию.
LLM видит меньше tools → меньше confusion, проще system prompt.

**Решение 3: Результат всегда JSON string в `ToolResult.Content`**
LLM получает структурированный JSON, может парсить и ссылаться на конкретные поля.
Не markdown-таблицы — LLM лучше работает с JSON в function calling контексте.

**Решение 4: alert_time в ctx, не в params**
Alert timestamp передаётся через `context.Context` (custom key), не как параметр каждого вызова.
LLM не должен знать о времени — time window вычисляется автоматически относительно alert time.

## Config структуры

```go
// internal/config/investigation_tools.go

type InvestigationToolsConfig struct {
    Prometheus *PrometheusToolConfig `yaml:"prometheus"`
    Loki       *LokiToolConfig       `yaml:"loki"`
    Kubernetes *KubernetesToolConfig `yaml:"kubernetes"`
    Database   *DatabaseToolConfig   `yaml:"database"`
}

type PrometheusToolConfig struct {
    Endpoint string        `yaml:"endpoint"`
    Timeout  time.Duration `yaml:"timeout"`
    Username string        `yaml:"username,omitempty"`
    Password string        `yaml:"password,omitempty"`
}

type LokiToolConfig struct {
    Endpoint string        `yaml:"endpoint"`
    Timeout  time.Duration `yaml:"timeout"`
    Username string        `yaml:"username,omitempty"`
    Password string        `yaml:"password,omitempty"`
}

type KubernetesToolConfig struct {
    Enabled    bool   `yaml:"enabled"`
    Kubeconfig string `yaml:"kubeconfig,omitempty"` // пусто = in-cluster
}

type DatabaseToolConfig struct {
    Enabled bool `yaml:"enabled"` // использует main PG pool
}
```

## Tool: prometheus_query_range

**Definition:**
```go
ToolDefinition{
    Name: "prometheus_query_range",
    Description: "Execute a PromQL query over a time range relative to the alert time. Returns time series data as JSON.",
    Parameters: JSONSchemaObject{
        Type: "object",
        Properties: map[string]JSONSchemaField{
            "query":        {Type: "string", Description: "PromQL expression, e.g. rate(http_requests_total[5m])"},
            "start_offset": {Type: "string", Description: "Start time offset from alert time, e.g. -15m (default: -15m)"},
            "end_offset":   {Type: "string", Description: "End time offset from alert time, e.g. +15m (default: +15m)"},
            "step":         {Type: "string", Description: "Query resolution step, e.g. 1m (default: 1m)"},
        },
        Required: []string{"query"},
    },
}
```

**Execute contract:**
- Извлекает `alert_time` из ctx (если нет — `time.Now()`)
- Вызывает `GET {endpoint}/api/v1/query_range`
- При ошибке HTTP: `ToolResult{IsError: true, Error: "prometheus: <status> <body>"}`
- При success: `Content` = JSON array `[{metric: {...}, values: [[ts, val], ...]}]`
- Таймаут: из config (default 30s)

## Tool: loki_query_range

**Definition:**
```go
ToolDefinition{
    Name: "loki_query_range",
    Description: "Query Loki logs using LogQL. Returns log lines as JSON array.",
    Parameters: JSONSchemaObject{
        Type: "object",
        Properties: map[string]JSONSchemaField{
            "query":        {Type: "string", Description: "LogQL query, e.g
... (truncated)

## Tasks
# PHASE-6A-BUILTIN-TOOLS — Tasks

## Чеклист реализации

### Slice 1: Config + scaffolding (0.5d)
- [ ] Добавить `InvestigationToolsConfig` в `internal/config/` (4 вложенных struct)
- [ ] Добавить `investigation.tools` секцию в `config.yaml` пример (с placeholder endpoints)
- [ ] Создать директорию `internal/core/investigation/tools/`
- [ ] Добавить helper `AlertTimeFromCtx` / `WithAlertTime` в `internal/core/investigation/context.go` (или расширить existing context support)

### Slice 2: Prometheus tool (1.5d)
- [ ] `tools/prometheus.go` — `PrometheusTool` struct с HTTP client, `Definition()`, `Execute()`
- [ ] Парсинг `start_offset`/`end_offset`/`step` из params с дефолтами
- [ ] HTTP вызов `GET /api/v1/query_range`, десериализация ответа, JSON → `ToolResult.Content`
- [ ] `tools/prometheus_test.go` — mock HTTP server, проверка params, error cases

### Slice 3: Loki tool (1.5d)
- [ ] `tools/loki.go` — `LokiTool` struct, аналогично Prometheus
- [ ] LogQL query_range endpoint, конвертация ns timestamp → ISO
- [ ] Optional Basic Auth header если username/password в config
- [ ] `tools/loki_test.go` — mock server

### Slice 4: Kubernetes tool (1.5d)
- [ ] `tools/kubernetes.go` — `KubernetesTool`, dispatch по `action` param
- [ ] Реализовать: `list_pods`, `get_pod`, `get_events`, `get_logs`, `get_deployments`
- [ ] Обернуть существующий `infrastructure/k8s/client.go` (не дублировать client-go setup)
- [ ] `tools/kubernetes_test.go` — fake k8s client или mock

### Slice 5: Database tool (1d)
- [ ] `tools/database.go` — `DatabaseTool`, принимает `*sql.DB`, dispatch по `query_type`
- [ ] 4 SQL запроса: active_queries, slow_queries (graceful если pg_stat_statements нет), replication_lag, connection_stats
- [ ] Результат сериализуется как `[]map[string]any` → JSON
- [ ] `tools/database_test.go` — sqlmock или реальный тест с test DB

### Slice 6: Wiring + интеграционный тест (1d)
- [ ] Найти точку инициализации агента, добавить conditional wiring всех 4 tools
- [ ] Smoke-тест: агент с реальным `ToolRegistry` + все 4 tools зарегистрированы → проверить `Definitions()` не пустой
- [ ] Обновить `README.md` в `internal/core/investigation/` — добавить секцию Built-in Tools

## Вертикальные слайсы (порядок реализации)

Prometheus → Loki → K8s → DB → Wiring

Каждый слайс независим и может быть протестирован отдельно до следующего.
Приоритет: Prometheus и K8s наиболее ценны для первого demo.

## Definition of Done

- [ ] `go build ./...` проходит
- [ ] `go test ./internal/core/investigation/tools/...` проходит
- [ ] Все 4 tools регистрируются при `enabled: true` в config
- [ ] `ToolRegistry.Definitions()` возвращает корректные JSON Schema для всех tools
- [ ] Документация (research.md + Spec.md) актуальна
- [ ] `NEXT.md` обновлён: PHASE-6A перемещён в WIP


## Instructions
1. Сначала составь план из 5-7 конкретных шагов, потом выполняй последовательно
2. Реализуй задачу согласно требованиям и спецификации выше
3. Запусти тесты перед коммитом: cd go-app && make test
4. Контекст проекта: CLAUDE.md, AGENTS.md, WORKFLOW.md в корне repo (если есть)
5. Закоммить все изменения: git add -u + git commit -m "feat: <краткое описание>" (conventional commits)
6. После завершения создай файл execution-report.json в корне worktree (см. схему ниже, НЕ коммить)

## Quality Checklist (ОБЯЗАТЕЛЬНО проверить перед коммитом)
- **Тесты при смене сигнатур**: если изменил сигнатуру — найди ВСЕ вызовы (grep) и обнови тесты
- **Тесты проходят**: `cd go-app && make test`
- **Сборка проходит**: `cd go-app && go build ./...`
- **go vet**: `cd go-app && go vet ./...`
- **Nil-safety**: каждый указатель из функции/map/type assertion — проверь на nil

## Execution Report
После завершения создай execution-report.json в корне worktree. Формат:
`{"task_id":"SLUG","model":"agent","stage":"implement","summary":"...","files_changed":["file.go"],"acceptance_criteria_status":[{"criterion":"...","status":"done","evidence":"..."}],"checks_run":{"build":"pass","tests":"pass","lint":"pass","typecheck":"not_run"},"risks":[],"open_questions":[],"duration_seconds":0}`
НЕ коммить этот файл.

## Retry Context

> Это повторная попытка. Предыдущая не удалась — учти контекст ниже.

- **Попытка:** 2 из 11
- **Причина:** retry
- **Предыдущий агент:** claude
- **QA последней попытки:**
  - build=pass
  - lint=pass
  - tests=fail
- **Изменения предыдущей попытки:**
6f7d01d feat(phase-6a): add smoke test, README, and config example for built-in tools
c301d87 feat(phase-6a): wire 4 investigation tools into agentic loop
375be1f fix(phase-6a): address review feedback on kubernetes and database tools
b1e0cf9 feat(phase-6a): add built-in investigation tools (prometheus, loki, k8s, database)
- **Хронология:**
  - stage_completed (end-task)
  - stage_failed (review) reason=uncommitted_worktree_at_close
  - classifier_decision
  - session_started agent=claude
  - stage_started (implement)
  - session_stopped reason=auto_detected agent=claude
  - execution_report
  - mechanical_check
  - risk_scored
  - stage_failed (implement) reason=retry agent=claude

Не повторяй подход, который привёл к ошибке. Адаптируй стратегию. Если указаны конкретные упавшие тесты — прочитай их, пойми что они проверяют, и реализуй код так, чтобы они прошли.