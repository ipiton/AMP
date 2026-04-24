package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/investigation"
)

// PrometheusTool executes PromQL range queries against a Prometheus HTTP API.
type PrometheusTool struct {
	cfg    *config.PrometheusToolConfig
	client *http.Client
}

// NewPrometheusTool creates a Prometheus tool from config.
func NewPrometheusTool(cfg *config.PrometheusToolConfig) *PrometheusTool {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &PrometheusTool{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

func (t *PrometheusTool) Definition() investigation.ToolDefinition {
	return investigation.ToolDefinition{
		Name:        "prometheus_query_range",
		Description: "Execute a PromQL query over a time range relative to the alert time. Returns time series data as JSON.",
		Parameters: investigation.JSONSchemaObject{
			Type: "object",
			Properties: map[string]investigation.JSONSchemaField{
				"query": {
					Type:        "string",
					Description: "PromQL expression, e.g. rate(http_requests_total[5m])",
				},
				"start_offset": {
					Type:        "string",
					Description: "Start time offset from alert time, e.g. -15m (default: -15m)",
					Default:     "-15m",
				},
				"end_offset": {
					Type:        "string",
					Description: "End time offset from alert time, e.g. +15m (default: +15m)",
					Default:     "+15m",
				},
				"step": {
					Type:        "string",
					Description: "Query resolution step, e.g. 1m (default: 1m)",
					Default:     "1m",
				},
			},
			Required: []string{"query"},
		},
	}
}

func (t *PrometheusTool) Execute(ctx context.Context, params map[string]any) (investigation.ToolResult, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return investigation.ToolResult{IsError: true, Error: "prometheus: missing required param 'query'"}, nil
	}

	alertTime := investigation.AlertTimeFromCtx(ctx)

	startOffset := parseDuration(params["start_offset"], -15*time.Minute)
	endOffset := parseDuration(params["end_offset"], 15*time.Minute)
	step := stringParam(params["step"], "1m")

	startTS := alertTime.Add(startOffset)
	endTS := alertTime.Add(endOffset)

	reqURL, err := url.Parse(t.cfg.Endpoint + "/api/v1/query_range")
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: invalid endpoint: %v", err)}, nil
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", strconv.FormatFloat(float64(startTS.UnixNano())/1e9, 'f', 3, 64))
	q.Set("end", strconv.FormatFloat(float64(endTS.UnixNano())/1e9, 'f', 3, 64))
	q.Set("step", step)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: build request: %v", err)}, nil
	}
	if t.cfg.Username != "" {
		req.SetBasicAuth(t.cfg.Username, t.cfg.Password)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: read body: %v", err)}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: %d %s", resp.StatusCode, truncate(string(body), 200))}, nil
	}

	var promResp prometheusRangeResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: parse response: %v", err)}, nil
	}
	if promResp.Status != "success" {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: status=%s error=%s", promResp.Status, promResp.Error)}, nil
	}

	out, err := json.Marshal(promResp.Data.Result)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("prometheus: marshal result: %v", err)}, nil
	}
	return investigation.ToolResult{Content: string(out)}, nil
}

type prometheusRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	} `json:"data"`
}

// parseDuration parses an offset string like "-15m" or "+15m" from params.
// Falls back to defaultVal if missing or invalid.
func parseDuration(v any, defaultVal time.Duration) time.Duration {
	s, ok := v.(string)
	if !ok || s == "" {
		return defaultVal
	}
	// Strip leading "+" — time.ParseDuration doesn't accept it.
	if len(s) > 0 && s[0] == '+' {
		s = s[1:]
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

func stringParam(v any, defaultVal string) string {
	s, ok := v.(string)
	if !ok || s == "" {
		return defaultVal
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
