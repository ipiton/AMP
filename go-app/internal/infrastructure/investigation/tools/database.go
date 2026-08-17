package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ipiton/AMP/internal/core/investigation"
)

// DBQuerier is the minimal interface over *sql.DB required by DatabaseTool.
// Using an interface makes the tool testable without a real Postgres connection.
type DBQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// DatabaseTool provides PostgreSQL diagnostic queries for the investigation agent.
type DatabaseTool struct {
	db DBQuerier
}

// NewDatabaseTool creates a DatabaseTool backed by the given DB querier.
// In production, pass the application's *sql.DB (or pgxpool wrapped as sql.DB).
func NewDatabaseTool(db DBQuerier) *DatabaseTool {
	return &DatabaseTool{db: db}
}

func (t *DatabaseTool) Definition() investigation.ToolDefinition {
	return investigation.ToolDefinition{
		Name:        "database_query",
		Description: "Execute a PostgreSQL diagnostic query. Returns results as JSON array of objects.",
		Parameters: investigation.JSONSchemaObject{
			Type: "object",
			Properties: map[string]investigation.JSONSchemaField{
				"query_type": {
					Type:        "string",
					Description: "Query type: active_queries, slow_queries, replication_lag, connection_stats",
				},
				"limit": {
					Type:        "string",
					Description: "Maximum rows to return (default: 20)",
					Default:     "20",
				},
			},
			Required: []string{"query_type"},
		},
	}
}

func (t *DatabaseTool) Execute(ctx context.Context, params map[string]any) (investigation.ToolResult, error) {
	queryType, _ := params["query_type"].(string)
	if queryType == "" {
		return investigation.ToolResult{IsError: true, Error: "database: missing required param 'query_type'"}, nil
	}

	limit := 20
	if lv, ok := params["limit"].(string); ok && lv != "" {
		if n, err := strconv.Atoi(lv); err == nil && n > 0 {
			limit = n
		}
	}

	var query string
	var args []any
	switch queryType {
	case "active_queries":
		query = sqlActiveQueries
		args = []any{limit}
	case "slow_queries":
		query = sqlSlowQueries
		args = []any{limit}
	case "replication_lag":
		query = sqlReplicationLag
		args = nil
	case "connection_stats":
		query = sqlConnectionStats
		args = nil
	default:
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("database: unknown query_type %q", queryType)}, nil
	}

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		// slow_queries may fail if pg_stat_statements is not installed — graceful fallback
		if queryType == "slow_queries" {
			return investigation.ToolResult{Content: `{"note":"pg_stat_statements not available","rows":[]}`}, nil
		}
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("database: %s: %v", queryType, err)}, nil
	}
	defer func() { _ = rows.Close() }()

	results, err := scanRowsToMaps(rows)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("database: scan %s: %v", queryType, err)}, nil
	}

	out, err := json.Marshal(results)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("database: marshal: %v", err)}, nil
	}
	return investigation.ToolResult{Content: string(out)}, nil
}

// scanRowsToMaps converts sql.Rows into a slice of column-name → value maps.
func scanRowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var results []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = vals[i]
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

const sqlActiveQueries = `
SELECT
    pid,
    usename,
    application_name,
    client_addr,
    state,
    wait_event_type,
    wait_event,
    EXTRACT(EPOCH FROM (now() - query_start))::int AS duration_seconds,
    LEFT(query, 200) AS query
FROM pg_stat_activity
WHERE state != 'idle'
  AND pid != pg_backend_pid()
ORDER BY duration_seconds DESC NULLS LAST
LIMIT $1`

const sqlSlowQueries = `
SELECT
    round(total_exec_time::numeric, 2) AS total_ms,
    calls,
    round(mean_exec_time::numeric, 2) AS mean_ms,
    round(stddev_exec_time::numeric, 2) AS stddev_ms,
    rows,
    LEFT(query, 200) AS query
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
LIMIT $1`

const sqlReplicationLag = `
SELECT
    client_addr,
    state,
    sent_lsn,
    write_lsn,
    flush_lsn,
    replay_lsn,
    pg_wal_lsn_diff(sent_lsn, replay_lsn) AS lag_bytes,
    EXTRACT(EPOCH FROM (now() - reply_time))::int AS reply_lag_seconds
FROM pg_stat_replication
ORDER BY lag_bytes DESC NULLS LAST`

const sqlConnectionStats = `
SELECT
    datname,
    count(*) AS total_connections,
    count(*) FILTER (WHERE state = 'active') AS active,
    count(*) FILTER (WHERE state = 'idle') AS idle,
    count(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
    max(EXTRACT(EPOCH FROM (now() - backend_start))::int) AS max_age_seconds
FROM pg_stat_activity
WHERE pid != pg_backend_pid()
GROUP BY datname
ORDER BY total_connections DESC`
