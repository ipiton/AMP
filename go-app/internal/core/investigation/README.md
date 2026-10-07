# Investigation Package

Core domain interfaces and logic for the LLM-based alert investigation pipeline.

## Packages

- `internal/core/investigation` — interfaces, registry, agent loop, context helpers
- `internal/core/investigation/runbook` — runbook parsing, label matching and prompt rendering (no I/O)
- `internal/infrastructure/investigation/tools` — built-in tool implementations
- `internal/infrastructure/investigation/runbooks` — loads runbooks from a directory

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

## Built-in Tools

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

Tools anchor their time windows on the alert time stored in context:

```go
ctx = investigation.WithAlertTime(ctx, alert.StartsAt)
// tools retrieve it:
alertTime := investigation.AlertTimeFromCtx(ctx) // falls back to time.Now()
```

> **Known gap:** no production code path calls `WithAlertTime` yet, so tools
> currently fall back to `time.Now()`. A delayed or retried investigation
> therefore queries a window around the investigation time, not the alert
> time. Tracked in `docs/06-planning/BUGS.md`.

## Runbooks (PHASE-6B)

Operator-written markdown runbooks are matched against the alert's labels and
appended to the agent's system prompt, so the LLM investigates with team
knowledge (known causes, checks, remediation) in addition to tool data.

### File format

One runbook per `*.md` file: YAML frontmatter between two `---` lines, then a
free-form markdown body.

```markdown
---
name: High Memory Usage        # required
match:                         # required, at least one label
  alertname: HighMemoryUsage
  severity: critical
tags: [memory, oom]            # optional, informational
---
## Symptoms
Pod memory usage exceeds 90% of its limit.

## Investigation Steps
1. Check `container_memory_working_set_bytes` for the pod.
2. Look for `OOMKilled` events.
```

Unknown frontmatter fields are ignored. A file is skipped (logged as a warning,
startup continues) when it has no frontmatter, invalid YAML, an empty `name`,
an empty `match` or an empty label/value in `match`, or is larger than 64 KiB.

A complete example lives in [`examples/runbooks/`](../../../../examples/runbooks/).

### Matching

- A runbook matches when **every** `match` label equals the alert's label value
  (exact string equality; no regex). Extra labels on the alert are ignored.
- `alertname` is read from the alert's labels, falling back to the alert's
  `AlertName` field when the label set does not carry it.
- Matches are ordered most specific first (more `match` labels), then by
  `name`, then by file path. At most `max_runbooks` are injected, each body cut
  to `max_chars` characters and marked `…[truncated]`.
- Matching happens once per investigation; the same section is sent on every
  agent iteration. Names of the injected runbooks are logged by the
  investigation queue (`runbooks` field).
- With runbooks disabled, or no match, the prompt is exactly the same as
  without this feature.

### Loading

`investigation.runbooks.path` is walked recursively at startup. Files and
directories whose name starts with `.` are skipped, which covers the
`..data`/`..<timestamp>` entries of a mounted Kubernetes ConfigMap; symlinked
files (ConfigMap keys) are read, symlinked directories are not followed. If the
directory is missing or unreadable, runbooks are disabled with a warning and
the service still starts. Edits are picked up only on restart (no hot reload).

The Helm chart does not yet expose extra volumes, so mounting a runbooks
ConfigMap currently requires patching the Deployment.

### Configuration

```yaml
llm:
  agent_mode: true          # runbooks are injected only by the agentic loop
investigation:
  runbooks:
    enabled: true
    path: /etc/amp/runbooks # default
    max_runbooks: 3         # default; <= 0 falls back to 3
    max_chars: 4000         # default; per runbook body, <= 0 falls back to 4000
```

### Trust

Runbook text goes into the LLM prompt verbatim. Treat the runbooks directory
like configuration: only operators should be able to write to it. The section
tells the model to verify runbook guidance against tool data. `runbook_url`
annotations are already part of the alert JSON in the prompt; AMP does not
fetch them.

## Wiring

Tools are conditionally registered in `internal/application/service_registry.go`
when `llm.agent_mode: true`. Each tool is skipped gracefully if its config
entry is absent or disabled. Runbooks are loaded in the same block by
`ServiceRegistry.configureRunbooks` and passed to the loop with
`AgentLoop.SetRunbooks`; the LLM client receives them through
`PromptContext` on `AgentLLMClient.InvestigateWithTools`.
