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
            "query":        {Type: "string", Description: "LogQL query, e.g. {namespace=\"prod\"} |= \"error\""},
            "start_offset": {Type: "string", Description: "Start offset from alert time (default: -15m)"},
            "end_offset":   {Type: "string", Description: "End offset from alert time (default: +15m)"},
            "limit":        {Type: "integer", Description: "Max log lines to return (default: 100)"},
        },
        Required: []string{"query"},
    },
}
```

**Execute contract:**
- Вызывает `GET {endpoint}/loki/api/v1/query_range`
- `Content` = JSON `[{stream: {...}, values: [["ts_ns", "line"], ...]}]`
- Лимит ns timestamp конвертировать в ISO для читаемости

## Tool: kubernetes

**Definition:**
```go
ToolDefinition{
    Name: "kubernetes",
    Description: "Inspect Kubernetes resources: pods, events, logs, deployments.",
    Parameters: JSONSchemaObject{
        Type: "object",
        Properties: map[string]JSONSchemaField{
            "action":    {Type: "string", Description: "One of: list_pods, get_pod, get_events, get_logs, get_deployments"},
            "namespace": {Type: "string", Description: "Kubernetes namespace"},
            "name":      {Type: "string", Description: "Resource name (pod/deployment)"},
            "selector":  {Type: "string", Description: "Label selector for list operations, e.g. app=api"},
            "container": {Type: "string", Description: "Container name for get_logs"},
            "tail":      {Type: "integer", Description: "Number of log lines to return (default: 100)"},
        },
        Required: []string{"action"},
    },
}
```

**Действия:**
| action | Требуемые params | Результат |
|---|---|---|
| `list_pods` | namespace (optional) | `[{name, namespace, phase, ready, restarts, age}]` |
| `get_pod` | namespace, name | `{name, phase, conditions[], containers[{name, ready, restartCount, state}], events[]}` |
| `get_events` | namespace | `[{type, reason, object, message, count, lastSeen}]` |
| `get_logs` | namespace, name, container (optional), tail | raw log string |
| `get_deployments` | namespace | `[{name, replicas, readyReplicas, updatedReplicas, revision}]` |

## Tool: database_diagnostics

**Definition:**
```go
ToolDefinition{
    Name: "database_diagnostics",
    Description: "Query PostgreSQL diagnostic views: active queries, slow queries, replication, connections.",
    Parameters: JSONSchemaObject{
        Type: "object",
        Properties: map[string]JSONSchemaField{
            "query_type": {Type: "string", Description: "One of: active_queries, slow_queries, replication_lag, connection_stats"},
        },
        Required: []string{"query_type"},
    },
}
```

**SQL per query_type:**

`active_queries`:
```sql
SELECT pid, state, wait_event_type, wait_event,
       EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_sec,
       LEFT(query, 200) AS query
FROM pg_stat_activity
WHERE state != 'idle' AND pid != pg_backend_pid()
ORDER BY query_start;
```

`slow_queries` (требует pg_stat_statements extension):
```sql
SELECT LEFT(query, 200) AS query, calls,
       round(mean_exec_time::numeric, 2) AS mean_ms,
       round(total_exec_time::numeric, 2) AS total_ms
FROM pg_stat_statements
ORDER BY mean_exec_time DESC LIMIT 10;
```

`replication_lag`:
```sql
SELECT client_addr, state, sync_state,
       (sent_lsn - replay_lsn) AS lag_bytes
FROM pg_stat_replication;
```

`connection_stats`:
```sql
SELECT count(*) AS total, state, wait_event_type
FROM pg_stat_activity
GROUP BY state, wait_event_type
ORDER BY total DESC;
```

## Context Key для alert_time

```go
// internal/core/investigation/context.go
type contextKey string
const AlertTimeKey contextKey = "alert_time"

func WithAlertTime(ctx context.Context, t time.Time) context.Context {
    return context.WithValue(ctx, AlertTimeKey, t)
}

func AlertTimeFromCtx(ctx context.Context) time.Time {
    if t, ok := ctx.Value(AlertTimeKey).(time.Time); ok {
        return t
    }
    return time.Now()
}
```

## Wiring (DI)

В точке старта агента (предположительно `application/` или `business/`):

```go
registry := investigation.NewToolRegistry()

if cfg.Investigation.Tools.Prometheus != nil {
    registry.Register(tools.NewPrometheusTool(cfg.Investigation.Tools.Prometheus))
}
if cfg.Investigation.Tools.Loki != nil {
    registry.Register(tools.NewLokiTool(cfg.Investigation.Tools.Loki))
}
if cfg.Investigation.Tools.Kubernetes != nil && cfg.Investigation.Tools.Kubernetes.Enabled {
    registry.Register(tools.NewKubernetesTool(k8sClient))
}
if cfg.Investigation.Tools.Database != nil && cfg.Investigation.Tools.Database.Enabled {
    registry.Register(tools.NewDatabaseTool(db))
}
```
