# PHASE-6A-BUILTIN-TOOLS — Research

## Существующий код

### Tool interface (`internal/core/investigation/tool.go`)

Уже определён полный контракт:
```go
type Tool interface {
    Definition() ToolDefinition  // name, description, JSON Schema params
    Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}
```
`ToolResult` содержит `Content string` (JSON или plain text), `IsError bool`.

### ToolRegistry (`internal/core/investigation/registry.go`)

- `Register(t Tool)` — паникует при дубликате имени
- `Execute(ctx, name, callID, params)` — по имени; возвращает `ToolResult{IsError:true}` при unknown tool
- `Definitions()` — все зарегистрированные tools для LLM function spec

### Agent loop (`internal/core/investigation/agent_loop.go`)

Agentic loop уже работает: LLM получает tool definitions, делает tool call, registry выполняет, результат идёт в следующую итерацию. Никаких изменений в loop не требуется.

### K8s client (`internal/infrastructure/k8s/`)

- `client.go` — существующий K8s client (предположительно `client-go` или `controller-runtime`)
- `errors.go` — типизированные ошибки
- Нужно изучить API client при реализации, но wrap в tool простой

### LLM client (`internal/infrastructure/llm/`)

- `client.go` — multi-provider client (Claude/OpenAI/Azure)
- `circuit_breaker.go` — circuit breaker поверх HTTP
- `investigate_with_tools.go` — integration entry point

### Database (`internal/infrastructure/` или `internal/database/postgres/`)

- Существующий PG connection pool — нужно найти и переиспользовать
- Не создавать новое соединение — использовать existing `*sql.DB` или аналог

## Points of Integration

### 1. Prometheus tool

HTTP API: `GET /api/v1/query_range?query=...&start=...&end=...&step=...`

Нет Go SDK — чистый HTTP client. Структура ответа:
```json
{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [{"metric": {...}, "values": [[ts, "val"], ...]}]
  }
}
```

Параметры tool для LLM:
- `query` (required) — PromQL выражение
- `start_offset` (optional, default "-15m") — offset от alert time
- `end_offset` (optional, default "+15m")
- `step` (optional, default "1m")

### 2. Loki tool

HTTP API: `GET /loki/api/v1/query_range?query=...&start=...&end=...&limit=...`

Аутентификация — Basic Auth или Bearer token (опционально).
Структура ответа аналогична Prometheus (streams вместо matrix).

Параметры tool для LLM:
- `query` (required) — LogQL выражение, например `{namespace="prod", pod=~"api-.*"} |= "error"`
- `start_offset`, `end_offset` — как Prometheus
- `limit` (optional, default 100) — max log lines

### 3. K8s tool

Операции (отдельные `action` в params):
- `list_pods` — namespace + label selector
- `get_pod` — namespace + pod name → status, conditions, container states, restart count
- `get_events` — namespace + involved object → последние N events
- `get_logs` — namespace + pod + container + tail lines
- `get_deployments` — namespace → recent deployments с revision history
- `top_pods` — metrics server (resource usage vs limits)

Реализация через существующий `infrastructure/k8s/client.go` — добавить новые методы или обернуть в tool adapter.

### 4. DB tool

Все запросы read-only (SELECT). Использует существующий PG pool.

Запросы:
```sql
-- активные запросы + ожидание
SELECT pid, state, wait_event_type, wait_event, query_start, query
FROM pg_stat_activity WHERE state != 'idle' ORDER BY query_start;

-- медленные запросы (pg_stat_statements)
SELECT query, calls, mean_exec_time, total_exec_time
FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 10;

-- replication lag
SELECT client_addr, state, sent_lsn - replay_lsn AS lag_bytes
FROM pg_stat_replication;

-- connection pool stats
SELECT count(*), state FROM pg_stat_activity GROUP BY state;
```

Параметры tool для LLM:
- `query_type` — `active_queries | slow_queries | replication_lag | connection_stats`

## Структура файлов

```
internal/core/investigation/
  tools/
    prometheus.go        # PrometoolTool
    loki.go              # LokiTool
    kubernetes.go        # KubernetesTool
    database.go          # DatabaseTool
    prometheus_test.go
    loki_test.go
    kubernetes_test.go
    database_test.go
```

Каждый файл: один тип, конструктор `New*Tool(cfg, deps)`, реализация `Tool` интерфейса.

## Конфиг

Новая секция в `config.yaml` (пример):
```yaml
investigation:
  tools:
    prometheus:
      endpoint: "http://prometheus:9090"
      timeout: 30s
    loki:
      endpoint: "http://loki:3100"
      timeout: 30s
    kubernetes:
      enabled: true   # in-cluster auto-detect
    database:
      enabled: true   # reuse main PG connection
```
