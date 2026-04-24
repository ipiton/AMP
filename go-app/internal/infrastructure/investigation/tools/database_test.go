package tools_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQuerier directly implements DBQuerier and returns pre-canned results.
type fakeQuerier struct {
	result []map[string]any
	err    error
}

// QueryContext for fakeQuerier returns a *sql.Rows from an in-memory query.
// We use a trick: open a real SQLite-like driver (or the already-registered test driver).
func (f *fakeQuerier) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	if f.err != nil {
		return nil, f.err
	}
	return makeInMemoryRows(ctx, f.result)
}

// makeInMemoryRows builds *sql.Rows from a slice of maps using a registered fake driver.
func makeInMemoryRows(_ context.Context, data []map[string]any) (*sql.Rows, error) {
	if len(data) == 0 {
		return openFakeRows(nil, nil)
	}
	cols := make([]string, 0, len(data[0]))
	for k := range data[0] {
		cols = append(cols, k)
	}
	rows := make([][]driver.Value, len(data))
	for i, row := range data {
		vals := make([]driver.Value, len(cols))
		for j, col := range cols {
			vals[j] = row[col]
		}
		rows[i] = vals
	}
	return openFakeRows(cols, rows)
}

// openFakeRows uses the registered fakeDriver to return pre-set rows.
func openFakeRows(cols []string, rows [][]driver.Value) (*sql.Rows, error) {
	setFakeDriverData(cols, rows)
	db, err := sql.Open("fakedriver", "")
	if err != nil {
		return nil, err
	}
	return db.QueryContext(context.Background(), "SELECT 1")
}

// --- Fake SQL driver implementation ---

func init() {
	sql.Register("fakedriver", &fakeDriver{})
}

var (
	globalFakeCols []string
	globalFakeRows [][]driver.Value
)

func setFakeDriverData(cols []string, rows [][]driver.Value) {
	globalFakeCols = cols
	globalFakeRows = rows
}

type fakeDriver struct{}

func (d *fakeDriver) Open(_ string) (driver.Conn, error) {
	return &fakeConn{}, nil
}

type fakeConn struct{}

func (c *fakeConn) Prepare(_ string) (driver.Stmt, error) {
	return &fakeStmt{cols: globalFakeCols, rows: globalFakeRows}, nil
}
func (c *fakeConn) Close() error  { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }

type fakeStmt struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec(_ []driver.Value) (driver.Result, error) {
	return nil, errors.New("not supported")
}
func (s *fakeStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return &fakeRows{cols: s.cols, rows: s.rows}, nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error       { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

// --- Tests ---

func TestDatabaseTool_Definition(t *testing.T) {
	tool := tools.NewDatabaseTool(&fakeQuerier{})
	def := tool.Definition()
	assert.Equal(t, "database_query", def.Name)
	assert.Contains(t, def.Parameters.Required, "query_type")
	assert.Contains(t, def.Parameters.Properties, "limit")
}

func TestDatabaseTool_Execute_MissingQueryType(t *testing.T) {
	tool := tools.NewDatabaseTool(&fakeQuerier{})
	result, err := tool.Execute(context.Background(), map[string]any{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "missing required param")
}

func TestDatabaseTool_Execute_UnknownQueryType(t *testing.T) {
	tool := tools.NewDatabaseTool(&fakeQuerier{})
	result, err := tool.Execute(context.Background(), map[string]any{"query_type": "unknown"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "unknown query_type")
}

func TestDatabaseTool_Execute_ActiveQueries_Success(t *testing.T) {
	data := []map[string]any{
		{"pid": int64(123), "usename": "app", "state": "active", "duration_seconds": int64(5), "query": "SELECT 1"},
	}
	tool := tools.NewDatabaseTool(&fakeQuerier{result: data})
	result, err := tool.Execute(context.Background(), map[string]any{"query_type": "active_queries"})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)

	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "app", rows[0]["usename"])
}

func TestDatabaseTool_Execute_SlowQueries_GracefulFallback(t *testing.T) {
	tool := tools.NewDatabaseTool(&fakeQuerier{err: errors.New(`relation "pg_stat_statements" does not exist`)})
	result, err := tool.Execute(context.Background(), map[string]any{"query_type": "slow_queries"})
	require.NoError(t, err)
	assert.False(t, result.IsError, "slow_queries should gracefully degrade when pg_stat_statements missing")
	assert.Contains(t, result.Content, "pg_stat_statements not available")
}

func TestDatabaseTool_Execute_DBError_NonSlowQuery(t *testing.T) {
	tool := tools.NewDatabaseTool(&fakeQuerier{err: errors.New("connection refused")})
	result, err := tool.Execute(context.Background(), map[string]any{"query_type": "active_queries"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "connection refused")
}

func TestDatabaseTool_Execute_EmptyResult(t *testing.T) {
	tool := tools.NewDatabaseTool(&fakeQuerier{result: nil})
	result, err := tool.Execute(context.Background(), map[string]any{"query_type": "replication_lag"})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)
	// empty result set marshals as null
	assert.Contains(t, result.Content, "null")
}
