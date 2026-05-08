# Investigation Package

Core domain interfaces and logic for the LLM-based alert investigation pipeline.

## Packages

- `internal/core/investigation` — interfaces, registry, agent loop, context helpers
- `internal/infrastructure/investigation/tools` — built-in tool implementations

## Tool Interface

Every investigation tool implements:

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}
```

`Execute` must never return a non-nil error for expected failures (bad params, HTTP errors).
Return `ToolResult{IsError: true, Error: "..."}` instead. Errors are reserved for
unrecoverable panics or context cancellation propagation.

## Built-in Tools (PHASE-6A)

| Tool name               | Package file    | Config key                     |
|-------------------------|-----------------|--------------------------------|
| `prometheus_query_range`| prometheus.go   | `investigation.tools.prometheus` |
| `loki_query_range`      | loki.go         | `investigation.tools.loki`       |
| `kubernetes_action`     | kubernetes.go   | `investigation.tools.kubernetes` |
| `database_query`        | database.go     | `investigation.tools.database`   |

### prometheus_query_range

Executes a PromQL range query anchored to the alert time stored in context via
`WithAlertTime`. Time window defaults to ±15 minutes around the alert.

Parameters: `query` (required), `start_offset`, `end_offset`, `step`.

### loki_query_range

Executes a LogQL range query against Loki. Timestamps are converted from
nanosecond Unix strings to RFC3339. Supports optional basic auth.

Parameters: `query` (required), `start_offset`, `end_offset`, `limit`, `direction`.

### kubernetes_action

Dispatches one of five diagnostic actions using the Kubernetes API:
`list_pods`, `get_pod`, `get_events`, `get_logs`, `get_deployments`.

Results are compact JSON summaries (not full k8s objects) to reduce token usage.

Parameters: `action` (required), `namespace`, `name`, `container`, `tail_lines`,
`label_selector`.

### database_query

Executes a fixed PostgreSQL diagnostic query by name:

| `query_type`       | Source view                | Notes                                   |
|--------------------|----------------------------|-----------------------------------------|
| `active_queries`   | `pg_stat_activity`         | Non-idle queries ordered by duration    |
| `slow_queries`     | `pg_stat_statements`       | Graceful fallback if extension missing  |
| `replication_lag`  | `pg_stat_replication`      | Lag in bytes and seconds                |
| `connection_stats` | `pg_stat_activity`         | Grouped by database name                |

## Alert Time Context

The investigation agent sets alert time in context before executing tools:

```go
ctx = investigation.WithAlertTime(ctx, alert.StartsAt)
// tools retrieve it:
alertTime := investigation.AlertTimeFromCtx(ctx) // falls back to time.Now()
```

## Wiring

Tools are conditionally registered in `internal/application/service_registry.go`
when `llm.agent_mode: true`. Each tool is skipped gracefully if its config
entry is absent or disabled.
