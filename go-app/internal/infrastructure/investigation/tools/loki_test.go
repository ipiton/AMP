package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiTool_Definition(t *testing.T) {
	tool := tools.NewLokiTool(&config.LokiToolConfig{Endpoint: "http://localhost:3100"})
	def := tool.Definition()
	assert.Equal(t, "loki_query_range", def.Name)
	assert.Contains(t, def.Parameters.Required, "query")
	assert.Contains(t, def.Parameters.Properties, "start_offset")
	assert.Contains(t, def.Parameters.Properties, "end_offset")
	assert.Contains(t, def.Parameters.Properties, "limit")
	assert.Contains(t, def.Parameters.Properties, "direction")
}

func TestLokiTool_Execute_Success(t *testing.T) {
	// ns timestamp for 2024-01-01T12:00:00Z
	const nsTs = "1704110400000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/loki/api/v1/query_range", r.URL.Path)
		assert.NotEmpty(t, r.URL.Query().Get("query"))
		assert.NotEmpty(t, r.URL.Query().Get("start"))
		assert.NotEmpty(t, r.URL.Query().Get("end"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "streams",
				"result": []map[string]any{
					{
						"stream": map[string]string{"namespace": "default", "pod": "api-abc"},
						"values": [][]string{{nsTs, "ERROR: connection refused"}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	tool := tools.NewLokiTool(&config.LokiToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})

	ctx := investigation.WithAlertTime(context.Background(), time.Now())
	result, err := tool.Execute(ctx, map[string]any{
		"query": `{namespace="default"} |= "error"`,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)
	assert.NotEmpty(t, result.Content)

	var lines []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &lines))
	require.Len(t, lines, 1)
	assert.Equal(t, "ERROR: connection refused", lines[0]["line"])
	// timestamp should be converted to RFC3339, not raw nanoseconds
	ts, _ := lines[0]["ts"].(string)
	assert.Contains(t, ts, "2024")
}

func TestLokiTool_Execute_MissingQuery(t *testing.T) {
	tool := tools.NewLokiTool(&config.LokiToolConfig{Endpoint: "http://localhost:3100"})
	result, err := tool.Execute(context.Background(), map[string]any{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "missing required param")
}

func TestLokiTool_Execute_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	tool := tools.NewLokiTool(&config.LokiToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": `{app="x"}`})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "400")
}

func TestLokiTool_Execute_BasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "loki-user", user)
		assert.Equal(t, "loki-pass", pass)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "streams", "result": []any{}},
		})
	}))
	defer srv.Close()

	tool := tools.NewLokiTool(&config.LokiToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
		Username: "loki-user",
		Password: "loki-pass",
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": `{app="x"}`})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)
}

func TestLokiTool_Execute_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "streams", "result": []any{}},
		})
	}))
	defer srv.Close()

	tool := tools.NewLokiTool(&config.LokiToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": `{app="x"}`})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)
	assert.Equal(t, "null", result.Content)
}
