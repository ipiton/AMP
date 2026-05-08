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

// LokiTool executes LogQL range queries against a Loki HTTP API.
type LokiTool struct {
	cfg    *config.LokiToolConfig
	client *http.Client
}

// NewLokiTool creates a Loki tool from config.
func NewLokiTool(cfg *config.LokiToolConfig) *LokiTool {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &LokiTool{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

func (t *LokiTool) Definition() investigation.ToolDefinition {
	return investigation.ToolDefinition{
		Name:        "loki_query_range",
		Description: "Query Loki logs using LogQL. Returns log lines as JSON array.",
		Parameters: investigation.JSONSchemaObject{
			Type: "object",
			Properties: map[string]investigation.JSONSchemaField{
				"query": {
					Type:        "string",
					Description: `LogQL query, e.g. {namespace="default",pod=~"api-.*"} |= "error"`,
				},
				"start_offset": {
					Type:        "string",
					Description: "Start time offset from alert time, e.g. -15m (default: -15m)",
					Default:     "-15m",
				},
				"end_offset": {
					Type:        "string",
					Description: "End time offset from alert time, e.g. +5m (default: +5m)",
					Default:     "+5m",
				},
				"limit": {
					Type:        "string",
					Description: "Maximum number of log entries to return (default: 100)",
					Default:     "100",
				},
				"direction": {
					Type:        "string",
					Description: "Log sort direction: forward or backward (default: backward)",
					Default:     "backward",
				},
			},
			Required: []string{"query"},
		},
	}
}

func (t *LokiTool) Execute(ctx context.Context, params map[string]any) (investigation.ToolResult, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return investigation.ToolResult{IsError: true, Error: "loki: missing required param 'query'"}, nil
	}

	alertTime := investigation.AlertTimeFromCtx(ctx)

	startOffset := parseDuration(params["start_offset"], -15*time.Minute)
	endOffset := parseDuration(params["end_offset"], 5*time.Minute)
	limit := stringParam(params["limit"], "100")
	direction := stringParam(params["direction"], "backward")

	startTS := alertTime.Add(startOffset)
	endTS := alertTime.Add(endOffset)

	reqURL, err := url.Parse(t.cfg.Endpoint + "/loki/api/v1/query_range")
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: invalid endpoint: %v", err)}, nil
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(startTS.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(endTS.UnixNano(), 10))
	q.Set("limit", limit)
	q.Set("direction", direction)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: build request: %v", err)}, nil
	}
	if t.cfg.Username != "" {
		req.SetBasicAuth(t.cfg.Username, t.cfg.Password)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: read body: %v", err)}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: %d %s", resp.StatusCode, truncate(string(body), 200))}, nil
	}

	var lokiResp lokiQueryRangeResponse
	if err := json.Unmarshal(body, &lokiResp); err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: parse response: %v", err)}, nil
	}
	if lokiResp.Status != "success" {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: status=%s", lokiResp.Status)}, nil
	}

	lines := convertLokiStreams(lokiResp.Data.Result)
	out, err := json.Marshal(lines)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("loki: marshal result: %v", err)}, nil
	}
	return investigation.ToolResult{Content: string(out)}, nil
}

type lokiQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string       `json:"resultType"`
		Result     []lokiStream `json:"result"`
	} `json:"data"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"` // [[ns_timestamp, log_line], ...]
}

type lokiLogLine struct {
	Timestamp string            `json:"ts"`
	Labels    map[string]string `json:"labels,omitempty"`
	Line      string            `json:"line"`
}

// convertLokiStreams converts Loki streams to a flat list of log lines.
// Timestamps are converted from nanosecond Unix strings to RFC3339.
func convertLokiStreams(streams []lokiStream) []lokiLogLine {
	var lines []lokiLogLine
	for _, s := range streams {
		for _, v := range s.Values {
			if len(v) < 2 {
				continue
			}
			ts := convertNsTimestamp(v[0])
			lines = append(lines, lokiLogLine{
				Timestamp: ts,
				Labels:    s.Stream,
				Line:      v[1],
			})
		}
	}
	return lines
}

// convertNsTimestamp converts a nanosecond Unix timestamp string to RFC3339.
func convertNsTimestamp(ns string) string {
	nanos, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return ns
	}
	t := time.Unix(nanos/1e9, nanos%1e9).UTC()
	return t.Format(time.RFC3339Nano)
}
