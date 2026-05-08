package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusTool_Definition(t *testing.T) {
	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{Endpoint: "http://localhost:9090"})
	def := tool.Definition()
	assert.Equal(t, "prometheus_query_range", def.Name)
	assert.Contains(t, def.Parameters.Required, "query")
	assert.Contains(t, def.Parameters.Properties, "start_offset")
	assert.Contains(t, def.Parameters.Properties, "end_offset")
	assert.Contains(t, def.Parameters.Properties, "step")
}

func TestPrometheusTool_Execute_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query_range", r.URL.Path)
		assert.Equal(t, "up", r.URL.Query().Get("query"))
		assert.NotEmpty(t, r.URL.Query().Get("start"))
		assert.NotEmpty(t, r.URL.Query().Get("end"))
		assert.Equal(t, "1m", r.URL.Query().Get("step"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{
					{"metric": map[string]string{"job": "prometheus"}, "values": [][]any{{1700000000, "1"}}},
				},
			},
		})
	}))
	defer srv.Close()

	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})

	ctx := investigation.WithAlertTime(context.Background(), time.Now())
	result, err := tool.Execute(ctx, map[string]any{"query": "up"})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)
	assert.NotEmpty(t, result.Content)

	var series []any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &series))
	assert.Len(t, series, 1)
}

func TestPrometheusTool_Execute_MissingQuery(t *testing.T) {
	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{Endpoint: "http://localhost:9090"})
	result, err := tool.Execute(context.Background(), map[string]any{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "missing required param")
}

func TestPrometheusTool_Execute_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": "up"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "500")
}

func TestPrometheusTool_Execute_PrometheusStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "error",
			"errorType": "bad_data",
			"error":     "invalid query",
		})
	}))
	defer srv.Close()

	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": "bad{"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "error")
}

func TestPrometheusTool_Execute_BasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "admin", user)
		assert.Equal(t, "secret", pass)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "matrix", "result": []any{}},
		})
	}))
	defer srv.Close()

	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
		Username: "admin",
		Password: "secret",
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": "up"})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)
}

func TestPrometheusTool_Execute_OffsetParsing(t *testing.T) {
	var captured url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "matrix", "result": []any{}},
		})
	}))
	defer srv.Close()

	alertTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	tool := tools.NewPrometheusTool(&config.PrometheusToolConfig{
		Endpoint: srv.URL,
		Timeout:  5 * time.Second,
	})
	ctx := investigation.WithAlertTime(context.Background(), alertTime)
	_, err := tool.Execute(ctx, map[string]any{
		"query":        "up",
		"start_offset": "-30m",
		"end_offset":   "+30m",
		"step":         "5m",
	})
	require.NoError(t, err)

	startVal := captured.Get("start")
	endVal := captured.Get("end")
	assert.NotEmpty(t, startVal)
	assert.NotEmpty(t, endVal)
	assert.Equal(t, "5m", captured.Get("step"))

	var startF, endF float64
	_, err = fmt.Sscanf(startVal, "%f", &startF)
	require.NoError(t, err)
	_, err = fmt.Sscanf(endVal, "%f", &endF)
	require.NoError(t, err)

	assert.InDelta(t, alertTime.Add(-30*time.Minute).Unix(), startF, 1)
	assert.InDelta(t, alertTime.Add(30*time.Minute).Unix(), endF, 1)
}
