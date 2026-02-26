package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validAlertPayload = `[
	{
		"labels": {"alertname":"TestAlert","service":"amp"},
		"annotations": {"summary":"test"},
		"startsAt": "2026-02-25T00:00:00Z",
		"status": "firing"
	}
]`

const validSilencePayload = `{
	"matchers": [{"name":"alertname","value":"TestAlert","isRegex":false}],
	"startsAt": "2099-01-01T00:00:00Z",
	"endsAt": "2099-01-01T01:00:00Z",
	"createdBy": "phase0-test",
	"comment": "maintenance window"
}`

const validConfigPayload = `
route:
  receiver: "default"
receivers:
  - name: "default"
`

const unknownSilenceUUID = "00000000-0000-0000-0000-000000000001"

func activeSilencePayload(now time.Time) string {
	return activeSilencePayloadForAlert(now, "TestAlert")
}

func activeSilencePayloadForAlert(now time.Time, alertName string) string {
	startsAt := now.Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	endsAt := now.Add(59 * time.Minute).UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{
		"matchers": [{"name":"alertname","value":%q,"isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "phase0-test",
		"comment": "active maintenance window"
	}`, alertName, startsAt, endsAt)
}

func writeTestConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}
	return path
}

func newPhase0TestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	return newPhase0TestMuxWithStateFile(t, filepath.Join(t.TempDir(), "runtime-state.json"))
}

func newPhase0TestMuxWithStateFile(t *testing.T, stateFile string) *http.ServeMux {
	t.Helper()
	t.Setenv(runtimeStateFileEnv, stateFile)
	if strings.TrimSpace(os.Getenv(runtimeConfigFileEnv)) == "" {
		t.Setenv(runtimeConfigFileEnv, writeTestConfigFile(t, validConfigPayload))
	}

	initTemplates()

	mux := http.NewServeMux()
	registerRoutes(mux)
	return mux
}

func TestPhase0RouteInventory(t *testing.T) {
	mux := newPhase0TestMux(t)

	type routeProbe struct {
		name          string
		method        string
		path          string
		body          string
		allowedStatus []int
	}

	probes := []routeProbe{
		{name: "root", method: http.MethodGet, path: "/", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard", method: http.MethodGet, path: "/dashboard", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard alerts", method: http.MethodGet, path: "/dashboard/alerts", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard silences", method: http.MethodGet, path: "/dashboard/silences", allowedStatus: []int{http.StatusInternalServerError}},
		{name: "dashboard llm", method: http.MethodGet, path: "/dashboard/llm", allowedStatus: []int{http.StatusInternalServerError}},
		{name: "dashboard routing", method: http.MethodGet, path: "/dashboard/routing", allowedStatus: []int{http.StatusInternalServerError}},
		{name: "script js compatibility", method: http.MethodGet, path: "/script.js", allowedStatus: []int{http.StatusOK}},
		{name: "favicon compatibility", method: http.MethodGet, path: "/favicon.ico", allowedStatus: []int{http.StatusNotFound}},
		{name: "lib compatibility", method: http.MethodGet, path: "/lib/nonexistent.js", allowedStatus: []int{http.StatusNotFound}},
		{name: "health", method: http.MethodGet, path: "/health", allowedStatus: []int{http.StatusOK}},
		{name: "ready", method: http.MethodGet, path: "/ready", allowedStatus: []int{http.StatusOK}},
		{name: "healthz alias", method: http.MethodGet, path: "/healthz", allowedStatus: []int{http.StatusOK}},
		{name: "readyz alias", method: http.MethodGet, path: "/readyz", allowedStatus: []int{http.StatusOK}},
		{name: "alertmanager healthy get", method: http.MethodGet, path: "/-/healthy", allowedStatus: []int{http.StatusOK}},
		{name: "alertmanager healthy head", method: http.MethodHead, path: "/-/healthy", allowedStatus: []int{http.StatusOK}},
		{name: "alertmanager ready get", method: http.MethodGet, path: "/-/ready", allowedStatus: []int{http.StatusOK}},
		{name: "alertmanager ready head", method: http.MethodHead, path: "/-/ready", allowedStatus: []int{http.StatusOK}},
		{name: "alertmanager reload post", method: http.MethodPost, path: "/-/reload", body: `{}`, allowedStatus: []int{http.StatusOK}},
		{name: "debug get", method: http.MethodGet, path: "/debug/pprof/", allowedStatus: []int{http.StatusOK}},
		{name: "debug post", method: http.MethodPost, path: "/debug/pprof/", body: `{}`, allowedStatus: []int{http.StatusMethodNotAllowed}},
		{name: "metrics", method: http.MethodGet, path: "/metrics", allowedStatus: []int{http.StatusOK}},
		{name: "alerts v1 post", method: http.MethodPost, path: "/api/v1/alerts", body: validAlertPayload, allowedStatus: []int{http.StatusOK}},
		{name: "alerts get", method: http.MethodGet, path: "/api/v2/alerts", allowedStatus: []int{http.StatusOK}},
		{name: "alerts post", method: http.MethodPost, path: "/api/v2/alerts", body: validAlertPayload, allowedStatus: []int{http.StatusOK}},
		{name: "alert groups get", method: http.MethodGet, path: "/api/v2/alerts/groups", allowedStatus: []int{http.StatusOK}},
		{name: "receivers get", method: http.MethodGet, path: "/api/v2/receivers", allowedStatus: []int{http.StatusOK}},
		{name: "silences get", method: http.MethodGet, path: "/api/v2/silences", allowedStatus: []int{http.StatusOK}},
		{name: "silences post", method: http.MethodPost, path: "/api/v2/silences", body: validSilencePayload, allowedStatus: []int{http.StatusOK}},
		{name: "silence by id get", method: http.MethodGet, path: "/api/v2/silence/" + unknownSilenceUUID, allowedStatus: []int{http.StatusNotFound}},
		{name: "silence by id delete", method: http.MethodDelete, path: "/api/v2/silence/" + unknownSilenceUUID, allowedStatus: []int{http.StatusNotFound}},
		{name: "status get", method: http.MethodGet, path: "/api/v2/status", allowedStatus: []int{http.StatusOK}},
		{name: "config get", method: http.MethodGet, path: "/api/v2/config", allowedStatus: []int{http.StatusOK}},
		{name: "config post", method: http.MethodPost, path: "/api/v2/config", body: validConfigPayload, allowedStatus: []int{http.StatusOK}},
		{name: "config status get", method: http.MethodGet, path: "/api/v2/config/status", allowedStatus: []int{http.StatusOK}},
		{name: "config history get", method: http.MethodGet, path: "/api/v2/config/history", allowedStatus: []int{http.StatusOK}},
		{name: "config revisions get", method: http.MethodGet, path: "/api/v2/config/revisions", allowedStatus: []int{http.StatusOK}},
		{name: "config revisions prune delete", method: http.MethodDelete, path: "/api/v2/config/revisions/prune", allowedStatus: []int{http.StatusOK}},
		{name: "config rollback post", method: http.MethodPost, path: "/api/v2/config/rollback", body: `{}`, allowedStatus: []int{http.StatusOK, http.StatusConflict}},
		{name: "history get", method: http.MethodGet, path: "/history", allowedStatus: []int{http.StatusOK}},
		{name: "history recent get", method: http.MethodGet, path: "/history/recent", allowedStatus: []int{http.StatusOK}},
		{name: "dashboard overview api", method: http.MethodGet, path: "/api/dashboard/overview", allowedStatus: []int{http.StatusOK}},
		{name: "dashboard recent alerts api", method: http.MethodGet, path: "/api/dashboard/alerts/recent", allowedStatus: []int{http.StatusOK}},
		{name: "webhook post", method: http.MethodPost, path: "/webhook", body: validAlertPayload, allowedStatus: []int{http.StatusOK}},
		{name: "static asset", method: http.MethodGet, path: "/static/css/dashboard.css", allowedStatus: []int{http.StatusOK}},
	}

	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			req := httptest.NewRequest(probe.method, probe.path, bytes.NewBufferString(probe.body))
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			got := rec.Code
			for _, allowed := range probe.allowedStatus {
				if got == allowed {
					return
				}
			}

			t.Fatalf("unexpected status for %s %s: got=%d allowed=%v", probe.method, probe.path, got, probe.allowedStatus)
		})
	}

	t.Run("unknown route falls through catch-all dashboard handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/phase0/not-found", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// Active runtime registers "/" as catch-all dashboard handler but must not
		// mask unknown paths.
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for unknown route, got %d", rec.Code)
		}
	})
}

func TestPhase0Contracts_HealthAndReady(t *testing.T) {
	mux := newPhase0TestMux(t)

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	mux.ServeHTTP(healthRec, healthReq)

	if healthRec.Code != http.StatusOK {
		t.Fatalf("GET /health expected 200, got %d", healthRec.Code)
	}

	var health map[string]any
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("GET /health invalid json: %v", err)
	}
	if health["status"] != "healthy" {
		t.Fatalf("GET /health expected status=healthy, got %v", health["status"])
	}
	if _, ok := health["version"]; !ok {
		t.Fatalf("GET /health expected version field")
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/ready", nil)
	readyRec := httptest.NewRecorder()
	mux.ServeHTTP(readyRec, readyReq)

	if readyRec.Code != http.StatusOK {
		t.Fatalf("GET /ready expected 200, got %d", readyRec.Code)
	}

	var ready map[string]any
	if err := json.Unmarshal(readyRec.Body.Bytes(), &ready); err != nil {
		t.Fatalf("GET /ready invalid json: %v", err)
	}
	readyValue, ok := ready["ready"].(bool)
	if !ok {
		t.Fatalf("GET /ready expected boolean ready field, got %T", ready["ready"])
	}
	if !readyValue {
		t.Fatalf("GET /ready expected ready=true")
	}
}

func TestPhase0Contracts_CoreAPI(t *testing.T) {
	mux := newPhase0TestMux(t)

	t.Run("status contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/status expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("status response is not valid json: %v", err)
		}
		if _, ok := payload["cluster"]; !ok {
			t.Fatalf("status response missing cluster field")
		}
		if _, ok := payload["versionInfo"]; !ok {
			t.Fatalf("status response missing versionInfo field")
		}
		if _, ok := payload["uptime"]; !ok {
			t.Fatalf("status response missing uptime field")
		}
		if _, ok := payload["stats"]; !ok {
			t.Fatalf("status response missing stats field")
		}
		if _, ok := payload["runtime"]; !ok {
			t.Fatalf("status response missing runtime field")
		}

		cluster, ok := payload["cluster"].(map[string]any)
		if !ok {
			t.Fatalf("status cluster expected object, got %T", payload["cluster"])
		}
		clusterStatus, ok := cluster["status"].(string)
		if !ok {
			t.Fatalf("status cluster.status expected string, got %T", cluster["status"])
		}
		switch clusterStatus {
		case "ready", "settling", "disabled":
		default:
			t.Fatalf("status cluster.status unexpected value %q", clusterStatus)
		}
		clusterPeers, ok := cluster["peers"].([]any)
		if !ok {
			t.Fatalf("status cluster.peers expected array, got %T", cluster["peers"])
		}
		if clusterPeers == nil {
			t.Fatalf("status cluster.peers must not be nil")
		}
		if clusterName, exists := cluster["name"]; exists {
			if _, ok := clusterName.(string); !ok {
				t.Fatalf("status cluster.name expected string when present, got %T", clusterName)
			}
		}

		versionInfo, ok := payload["versionInfo"].(map[string]any)
		if !ok {
			t.Fatalf("status versionInfo expected object, got %T", payload["versionInfo"])
		}
		requiredVersionFields := []string{"version", "revision", "branch", "buildUser", "buildDate", "goVersion"}
		for _, field := range requiredVersionFields {
			value, ok := versionInfo[field].(string)
			if !ok || strings.TrimSpace(value) == "" {
				t.Fatalf("status versionInfo.%s expected non-empty string, got %v", field, versionInfo[field])
			}
		}

		configValue, ok := payload["config"].(map[string]any)
		if !ok {
			t.Fatalf("status config expected object, got %T", payload["config"])
		}
		if _, ok := configValue["original"].(string); !ok {
			t.Fatalf("status config.original expected string, got %T", configValue["original"])
		}

		uptimeRaw, ok := payload["uptime"].(string)
		if !ok {
			t.Fatalf("status uptime expected string, got %T", payload["uptime"])
		}
		if _, err := time.Parse(time.RFC3339, uptimeRaw); err != nil {
			t.Fatalf("status uptime expected RFC3339 timestamp, got %q: %v", uptimeRaw, err)
		}
	})

	t.Run("unknown api path returns 404 json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/unknown-path", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v2/unknown-path expected 404, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unknown api response is not valid json: %v", err)
		}
		if payload["error"] != "not found" {
			t.Fatalf("unknown api response expected error=not found, got %v", payload["error"])
		}
	})

	t.Run("config contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config expected 200, got %d", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET /api/v2/config expected json content type, got %q", rec.Header().Get("Content-Type"))
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config response is not valid json: %v", err)
		}
		if _, ok := payload["original"].(string); !ok {
			t.Fatalf("config response missing original string")
		}
	})

	t.Run("config yaml format contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config?format=yaml", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config?format=yaml expected 200, got %d", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/yaml") {
			t.Fatalf("GET /api/v2/config?format=yaml expected yaml content type, got %q", rec.Header().Get("Content-Type"))
		}
		if strings.TrimSpace(rec.Body.String()) == "" {
			t.Fatalf("config yaml response expected non-empty body")
		}
	})

	t.Run("config invalid format contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config?format=xml", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/config with invalid format expected 400, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("invalid format response is not valid json: %v", err)
		}
		if payload["error"] != "invalid format query value" {
			t.Fatalf("invalid format response expected error message, got %v", payload["error"])
		}
	})

	t.Run("config post contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(validConfigPayload))
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("POST /api/v2/config expected json content type, got %q", rec.Header().Get("Content-Type"))
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config post response is not valid json: %v", err)
		}
		if payload["status"] != "applied" {
			t.Fatalf("config post response expected status=applied, got %v", payload["status"])
		}
	})

	t.Run("config post invalid payload contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString("route: [\n"))
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/config with invalid payload expected 400, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("invalid config post response is not valid json: %v", err)
		}
		errMsg, _ := payload["error"].(string)
		if !strings.Contains(errMsg, "invalid config payload") {
			t.Fatalf("invalid config post response expected payload error, got %v", payload["error"])
		}
	})

	t.Run("config status contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/status", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/status expected 200, got %d", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET /api/v2/config/status expected json content type, got %q", rec.Header().Get("Content-Type"))
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config status response is not valid json: %v", err)
		}
		if _, ok := payload["status"].(string); !ok {
			t.Fatalf("config status response expected status string, got %T", payload["status"])
		}
		if _, ok := payload["source"].(string); !ok {
			t.Fatalf("config status response expected source string, got %T", payload["source"])
		}
		if _, ok := payload["appliedAt"].(string); !ok {
			t.Fatalf("config status response expected appliedAt string, got %T", payload["appliedAt"])
		}
		if _, ok := payload["error"].(string); !ok {
			t.Fatalf("config status response expected error string, got %T", payload["error"])
		}
		if _, ok := payload["configPath"].(string); !ok {
			t.Fatalf("config status response expected configPath string, got %T", payload["configPath"])
		}
		if _, ok := payload["inhibitionRuleCount"].(float64); !ok {
			t.Fatalf("config status response expected inhibitionRuleCount number, got %T", payload["inhibitionRuleCount"])
		}
		if _, ok := payload["receiverCount"].(float64); !ok {
			t.Fatalf("config status response expected receiverCount number, got %T", payload["receiverCount"])
		}
	})

	t.Run("config history contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/history?limit=5", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/history expected 200, got %d", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET /api/v2/config/history expected json content type, got %q", rec.Header().Get("Content-Type"))
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config history response is not valid json: %v", err)
		}
		if _, ok := payload["total"].(float64); !ok {
			t.Fatalf("config history response expected total number, got %T", payload["total"])
		}
		if _, ok := payload["limit"].(float64); !ok {
			t.Fatalf("config history response expected limit number, got %T", payload["limit"])
		}
		if _, ok := payload["status"].(string); !ok {
			t.Fatalf("config history response expected status string, got %T", payload["status"])
		}
		if _, ok := payload["source"].(string); !ok {
			t.Fatalf("config history response expected source string, got %T", payload["source"])
		}
		if _, ok := payload["configPath"].(string); !ok {
			t.Fatalf("config history response expected configPath string, got %T", payload["configPath"])
		}
		entries, ok := payload["entries"].([]any)
		if !ok {
			t.Fatalf("config history response expected entries array, got %T", payload["entries"])
		}
		if len(entries) == 0 {
			t.Fatalf("config history response expected at least one entry")
		}
	})

	t.Run("config history invalid limit contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/history?limit=nan", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/config/history with invalid limit expected 400, got %d", rec.Code)
		}
	})

	t.Run("config history status filter contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/history?status=ok", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/history with status filter expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config history status-filter response is not valid json: %v", err)
		}
		if payload["status"] != "ok" {
			t.Fatalf("config history status-filter expected status=ok, got %v", payload["status"])
		}

		entries, ok := payload["entries"].([]any)
		if !ok {
			t.Fatalf("config history status-filter expected entries array, got %T", payload["entries"])
		}
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("config history status-filter entry expected object, got %T", raw)
			}
			if entry["status"] != "ok" {
				t.Fatalf("config history status-filter expected entry status=ok, got %v", entry["status"])
			}
		}
	})

	t.Run("config history source filter contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/history?source=startup", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/history with source filter expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config history source-filter response is not valid json: %v", err)
		}
		if payload["source"] != "startup" {
			t.Fatalf("config history source-filter expected source=startup, got %v", payload["source"])
		}

		entries, ok := payload["entries"].([]any)
		if !ok {
			t.Fatalf("config history source-filter expected entries array, got %T", payload["entries"])
		}
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("config history source-filter entry expected object, got %T", raw)
			}
			if entry["source"] != "startup" {
				t.Fatalf("config history source-filter expected entry source=startup, got %v", entry["source"])
			}
		}
	})

	t.Run("config history invalid status filter contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/history?status=broken", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/config/history with invalid status expected 400, got %d", rec.Code)
		}
	})

	t.Run("config revisions contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/revisions?limit=5", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/revisions expected 200, got %d", rec.Code)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("GET /api/v2/config/revisions expected json content type, got %q", rec.Header().Get("Content-Type"))
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config revisions response is not valid json: %v", err)
		}
		if _, ok := payload["total"].(float64); !ok {
			t.Fatalf("config revisions response expected total number, got %T", payload["total"])
		}
		if _, ok := payload["limit"].(float64); !ok {
			t.Fatalf("config revisions response expected limit number, got %T", payload["limit"])
		}
		if _, ok := payload["currentConfigHash"].(string); !ok {
			t.Fatalf("config revisions response expected currentConfigHash string, got %T", payload["currentConfigHash"])
		}
		if _, ok := payload["configPath"].(string); !ok {
			t.Fatalf("config revisions response expected configPath string, got %T", payload["configPath"])
		}
		revisions, ok := payload["revisions"].([]any)
		if !ok {
			t.Fatalf("config revisions response expected revisions array, got %T", payload["revisions"])
		}
		if len(revisions) == 0 {
			t.Fatalf("config revisions response expected at least one revision")
		}
		entry, ok := revisions[0].(map[string]any)
		if !ok {
			t.Fatalf("config revisions first entry expected object, got %T", revisions[0])
		}
		if _, ok := entry["configHash"].(string); !ok {
			t.Fatalf("config revisions entry expected configHash string, got %T", entry["configHash"])
		}
		if _, ok := entry["source"].(string); !ok {
			t.Fatalf("config revisions entry expected source string, got %T", entry["source"])
		}
		if _, ok := entry["appliedAt"].(string); !ok {
			t.Fatalf("config revisions entry expected appliedAt string, got %T", entry["appliedAt"])
		}
		if _, ok := entry["isCurrent"].(bool); !ok {
			t.Fatalf("config revisions entry expected isCurrent bool, got %T", entry["isCurrent"])
		}
	})

	t.Run("config revisions invalid limit contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/revisions?limit=nan", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/config/revisions with invalid limit expected 400, got %d", rec.Code)
		}
	})

	t.Run("config revisions prune contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		configA := `
route:
  receiver: "team-prune-a"
receivers:
  - name: "team-prune-a"
`
		configB := `
route:
  receiver: "team-prune-b"
receivers:
  - name: "team-prune-b"
`
		applyAReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configA))
		applyARec := httptest.NewRecorder()
		localMux.ServeHTTP(applyARec, applyAReq)
		if applyARec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for prune configA expected 200, got %d", applyARec.Code)
		}
		applyBReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configB))
		applyBRec := httptest.NewRecorder()
		localMux.ServeHTTP(applyBRec, applyBReq)
		if applyBRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for prune configB expected 200, got %d", applyBRec.Code)
		}

		req := httptest.NewRequest(http.MethodDelete, "/api/v2/config/revisions/prune?keep=1", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE /api/v2/config/revisions/prune expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config revisions prune response is not valid json: %v", err)
		}
		if payload["status"] != "pruned" {
			t.Fatalf("config revisions prune expected status=pruned, got %v", payload["status"])
		}
		if _, ok := payload["keep"].(float64); !ok {
			t.Fatalf("config revisions prune expected keep number, got %T", payload["keep"])
		}
		if _, ok := payload["before"].(float64); !ok {
			t.Fatalf("config revisions prune expected before number, got %T", payload["before"])
		}
		if _, ok := payload["after"].(float64); !ok {
			t.Fatalf("config revisions prune expected after number, got %T", payload["after"])
		}
		if _, ok := payload["removed"].(float64); !ok {
			t.Fatalf("config revisions prune expected removed number, got %T", payload["removed"])
		}
		if _, ok := payload["currentConfigHash"].(string); !ok {
			t.Fatalf("config revisions prune expected currentConfigHash string, got %T", payload["currentConfigHash"])
		}
		if _, ok := payload["configPath"].(string); !ok {
			t.Fatalf("config revisions prune expected configPath string, got %T", payload["configPath"])
		}
	})

	t.Run("config revisions prune invalid keep contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodDelete, "/api/v2/config/revisions/prune?keep=nan", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("DELETE /api/v2/config/revisions/prune with invalid keep expected 400, got %d", rec.Code)
		}
	})

	t.Run("config revisions prune dry-run contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		configA := `
route:
  receiver: "team-prune-dry-a"
receivers:
  - name: "team-prune-dry-a"
`
		configB := `
route:
  receiver: "team-prune-dry-b"
receivers:
  - name: "team-prune-dry-b"
`
		applyAReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configA))
		applyARec := httptest.NewRecorder()
		localMux.ServeHTTP(applyARec, applyAReq)
		if applyARec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for prune dry-run configA expected 200, got %d", applyARec.Code)
		}
		applyBReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configB))
		applyBRec := httptest.NewRecorder()
		localMux.ServeHTTP(applyBRec, applyBReq)
		if applyBRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for prune dry-run configB expected 200, got %d", applyBRec.Code)
		}

		req := httptest.NewRequest(http.MethodDelete, "/api/v2/config/revisions/prune?keep=1&dryRun=true", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE /api/v2/config/revisions/prune dry-run expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config revisions prune dry-run response is not valid json: %v", err)
		}
		if payload["status"] != "dry_run" {
			t.Fatalf("config revisions prune dry-run expected status=dry_run, got %v", payload["status"])
		}
		if payload["action"] != "prune_revisions" {
			t.Fatalf("config revisions prune dry-run expected action=prune_revisions, got %v", payload["action"])
		}
		if dryRun, ok := payload["dryRun"].(bool); !ok || !dryRun {
			t.Fatalf("config revisions prune dry-run expected dryRun=true, got %v", payload["dryRun"])
		}
	})

	t.Run("config revisions prune invalid dry-run contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodDelete, "/api/v2/config/revisions/prune?dryRun=broken", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("DELETE /api/v2/config/revisions/prune with invalid dryRun expected 400, got %d", rec.Code)
		}
	})

	t.Run("config rollback requires previous revision contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/config/rollback", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("POST /api/v2/config/rollback without previous revision expected 409, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config rollback conflict response is not valid json: %v", err)
		}
		errMsg, _ := payload["error"].(string)
		if !strings.Contains(errMsg, "no previous config revision") {
			t.Fatalf("config rollback conflict expected previous revision error, got %v", payload["error"])
		}
	})

	t.Run("config rollback contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		updatedConfig := `
route:
  receiver: "team-rollback"
receivers:
  - name: "team-rollback"
`
		applyReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(updatedConfig))
		applyRec := httptest.NewRecorder()
		localMux.ServeHTTP(applyRec, applyReq)
		if applyRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config before rollback expected 200, got %d", applyRec.Code)
		}

		rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v2/config/rollback", bytes.NewBufferString(`{}`))
		rollbackRec := httptest.NewRecorder()
		localMux.ServeHTTP(rollbackRec, rollbackReq)

		if rollbackRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config/rollback expected 200, got %d", rollbackRec.Code)
		}
		if !strings.HasPrefix(rollbackRec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("POST /api/v2/config/rollback expected json content type, got %q", rollbackRec.Header().Get("Content-Type"))
		}

		var payload map[string]any
		if err := json.Unmarshal(rollbackRec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config rollback response is not valid json: %v", err)
		}
		if payload["status"] != "rolled_back" {
			t.Fatalf("config rollback response expected status=rolled_back, got %v", payload["status"])
		}
		if _, ok := payload["fromConfigHash"].(string); !ok {
			t.Fatalf("config rollback response expected fromConfigHash string, got %T", payload["fromConfigHash"])
		}
		if _, ok := payload["toConfigHash"].(string); !ok {
			t.Fatalf("config rollback response expected toConfigHash string, got %T", payload["toConfigHash"])
		}
		if _, ok := payload["configPath"].(string); !ok {
			t.Fatalf("config rollback response expected configPath string, got %T", payload["configPath"])
		}
	})

	t.Run("config rollback invalid configHash contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/config/rollback?configHash=bad", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/config/rollback with invalid configHash expected 400, got %d", rec.Code)
		}
	})

	t.Run("config rollback unknown configHash contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		updatedConfig := `
route:
  receiver: "team-rollback-notfound"
receivers:
  - name: "team-rollback-notfound"
`
		applyReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(updatedConfig))
		applyRec := httptest.NewRecorder()
		localMux.ServeHTTP(applyRec, applyReq)
		if applyRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config before unknown-hash rollback expected 200, got %d", applyRec.Code)
		}

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v2/config/rollback?configHash=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			bytes.NewBufferString(`{}`),
		)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST /api/v2/config/rollback with unknown configHash expected 404, got %d", rec.Code)
		}
	})

	t.Run("config rollback by configHash contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		configA := `
route:
  receiver: "team-rollback-a"
receivers:
  - name: "team-rollback-a"
`
		configB := `
route:
  receiver: "team-rollback-b"
receivers:
  - name: "team-rollback-b"
`

		applyAReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configA))
		applyARec := httptest.NewRecorder()
		localMux.ServeHTTP(applyARec, applyAReq)
		if applyARec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for configA expected 200, got %d", applyARec.Code)
		}

		applyBReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configB))
		applyBRec := httptest.NewRecorder()
		localMux.ServeHTTP(applyBRec, applyBReq)
		if applyBRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for configB expected 200, got %d", applyBRec.Code)
		}

		targetHash := configSHA256(configA)
		rollbackReq := httptest.NewRequest(
			http.MethodPost,
			fmt.Sprintf("/api/v2/config/rollback?configHash=%s", targetHash),
			bytes.NewBufferString(`{}`),
		)
		rollbackRec := httptest.NewRecorder()
		localMux.ServeHTTP(rollbackRec, rollbackReq)

		if rollbackRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config/rollback by configHash expected 200, got %d", rollbackRec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rollbackRec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config rollback by hash response is not valid json: %v", err)
		}
		if payload["status"] != "rolled_back" {
			t.Fatalf("config rollback by hash response expected status=rolled_back, got %v", payload["status"])
		}
		if payload["toConfigHash"] != targetHash {
			t.Fatalf("config rollback by hash expected toConfigHash=%s, got %v", targetHash, payload["toConfigHash"])
		}
	})

	t.Run("config rollback dry-run contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		configA := `
route:
  receiver: "team-rollback-dry-a"
receivers:
  - name: "team-rollback-dry-a"
`
		configB := `
route:
  receiver: "team-rollback-dry-b"
receivers:
  - name: "team-rollback-dry-b"
`

		applyAReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configA))
		applyARec := httptest.NewRecorder()
		localMux.ServeHTTP(applyARec, applyAReq)
		if applyARec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for rollback dry-run configA expected 200, got %d", applyARec.Code)
		}

		applyBReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configB))
		applyBRec := httptest.NewRecorder()
		localMux.ServeHTTP(applyBRec, applyBReq)
		if applyBRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config for rollback dry-run configB expected 200, got %d", applyBRec.Code)
		}

		targetHash := configSHA256(configA)
		rollbackReq := httptest.NewRequest(
			http.MethodPost,
			fmt.Sprintf("/api/v2/config/rollback?configHash=%s&dryRun=true", targetHash),
			bytes.NewBufferString(`{}`),
		)
		rollbackRec := httptest.NewRecorder()
		localMux.ServeHTTP(rollbackRec, rollbackReq)

		if rollbackRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config/rollback dry-run expected 200, got %d", rollbackRec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rollbackRec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("config rollback dry-run response is not valid json: %v", err)
		}
		if payload["status"] != "dry_run" {
			t.Fatalf("config rollback dry-run expected status=dry_run, got %v", payload["status"])
		}
		if payload["action"] != "rollback" {
			t.Fatalf("config rollback dry-run expected action=rollback, got %v", payload["action"])
		}
		if dryRun, ok := payload["dryRun"].(bool); !ok || !dryRun {
			t.Fatalf("config rollback dry-run expected dryRun=true, got %v", payload["dryRun"])
		}
		if payload["toConfigHash"] != targetHash {
			t.Fatalf("config rollback dry-run expected toConfigHash=%s, got %v", targetHash, payload["toConfigHash"])
		}
	})

	t.Run("config rollback invalid dry-run contract", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/config/rollback?dryRun=broken", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/config/rollback with invalid dryRun expected 400, got %d", rec.Code)
		}
	})

	t.Run("history contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/history", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /history expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("history response is not valid json: %v", err)
		}
		if _, ok := payload["total"]; !ok {
			t.Fatalf("history response missing total field")
		}
		if _, ok := payload["alerts"]; !ok {
			t.Fatalf("history response missing alerts field")
		}
	})

	t.Run("history recent contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/history/recent?limit=5", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /history/recent expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("history recent response is not valid json: %v", err)
		}
		if _, ok := payload["total"]; !ok {
			t.Fatalf("history recent response missing total field")
		}
		if _, ok := payload["limit"]; !ok {
			t.Fatalf("history recent response missing limit field")
		}
		if _, ok := payload["alerts"]; !ok {
			t.Fatalf("history recent response missing alerts field")
		}
	})

	t.Run("history invalid query filters contract", func(t *testing.T) {
		reqResolved := httptest.NewRequest(http.MethodGet, "/history?resolved=not-bool", nil)
		recResolved := httptest.NewRecorder()
		mux.ServeHTTP(recResolved, reqResolved)
		if recResolved.Code != http.StatusBadRequest {
			t.Fatalf("GET /history with invalid resolved expected 400, got %d", recResolved.Code)
		}

		reqRecentResolved := httptest.NewRequest(http.MethodGet, "/history/recent?resolved=not-bool", nil)
		recRecentResolved := httptest.NewRecorder()
		mux.ServeHTTP(recRecentResolved, reqRecentResolved)
		if recRecentResolved.Code != http.StatusBadRequest {
			t.Fatalf("GET /history/recent with invalid resolved expected 400, got %d", recRecentResolved.Code)
		}

		reqRecentLimit := httptest.NewRequest(http.MethodGet, "/history/recent?limit=nan", nil)
		recRecentLimit := httptest.NewRecorder()
		mux.ServeHTTP(recRecentLimit, reqRecentLimit)
		if recRecentLimit.Code != http.StatusBadRequest {
			t.Fatalf("GET /history/recent with invalid limit expected 400, got %d", recRecentLimit.Code)
		}
	})

	t.Run("dashboard overview reflects runtime state", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		alertReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(validAlertPayload))
		alertRec := httptest.NewRecorder()
		localMux.ServeHTTP(alertRec, alertReq)
		if alertRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/alerts expected 200, got %d", alertRec.Code)
		}

		silenceReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(activeSilencePayload(time.Now().UTC())))
		silenceRec := httptest.NewRecorder()
		localMux.ServeHTTP(silenceRec, silenceReq)
		if silenceRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/silences expected 200, got %d", silenceRec.Code)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/dashboard/overview expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("overview response is not valid json: %v", err)
		}

		data, ok := payload["data"].(map[string]any)
		if !ok {
			t.Fatalf("overview response missing data object")
		}
		activeAlerts, ok := data["active_alerts"].(float64)
		if !ok || activeAlerts < 1 {
			t.Fatalf("overview expected active_alerts >= 1, got %v", data["active_alerts"])
		}
		activeSilences, ok := data["active_silences"].(float64)
		if !ok || activeSilences < 1 {
			t.Fatalf("overview expected active_silences >= 1, got %v", data["active_silences"])
		}
	})

	t.Run("dashboard recent endpoint supports limit", func(t *testing.T) {
		localMux := newPhase0TestMux(t)

		payload := `[
			{
				"labels": {"alertname":"TestAlertA","service":"amp"},
				"annotations": {"summary":"test-a"},
				"startsAt": "2026-02-25T00:00:00Z",
				"status": "firing"
			},
			{
				"labels": {"alertname":"TestAlertB","service":"amp"},
				"annotations": {"summary":"test-b"},
				"startsAt": "2026-02-25T00:01:00Z",
				"status": "firing"
			}
		]`

		postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
		postRec := httptest.NewRecorder()
		localMux.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts/recent?limit=1", nil)
		rec := httptest.NewRecorder()
		localMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/dashboard/alerts/recent expected 200, got %d", rec.Code)
		}

		var payloadResp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payloadResp); err != nil {
			t.Fatalf("dashboard recent response is not valid json: %v", err)
		}
		data, ok := payloadResp["data"].(map[string]any)
		if !ok {
			t.Fatalf("dashboard recent response missing data object")
		}
		returned, ok := data["returned"].(float64)
		if !ok || returned != 1 {
			t.Fatalf("dashboard recent expected returned=1, got %v", data["returned"])
		}
		total, ok := data["total"].(float64)
		if !ok || total < 2 {
			t.Fatalf("dashboard recent expected total >= 2, got %v", data["total"])
		}
	})

	t.Run("dashboard recent invalid query filters contract", func(t *testing.T) {
		reqResolved := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts/recent?resolved=not-bool", nil)
		recResolved := httptest.NewRecorder()
		mux.ServeHTTP(recResolved, reqResolved)
		if recResolved.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/dashboard/alerts/recent with invalid resolved expected 400, got %d", recResolved.Code)
		}

		reqLimit := httptest.NewRequest(http.MethodGet, "/api/dashboard/alerts/recent?limit=nan", nil)
		recLimit := httptest.NewRecorder()
		mux.ServeHTTP(recLimit, reqLimit)
		if recLimit.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/dashboard/alerts/recent with invalid limit expected 400, got %d", recLimit.Code)
		}
	})

	t.Run("alerts get contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts expected 200, got %d", rec.Code)
		}

		var payload []any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alerts get response is not valid json: %v", err)
		}
		if payload == nil {
			t.Fatalf("alerts get expected array payload")
		}
	})

	t.Run("alerts get invalid resolved filter contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?resolved=not-bool", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts with invalid resolved expected 400, got %d", rec.Code)
		}
	})

	t.Run("alerts get invalid receiver regex contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?receiver=[", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts with invalid receiver regex expected 400, got %d", rec.Code)
		}
	})

	t.Run("alerts get invalid state flag contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?active=not-bool", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts with invalid active expected 400, got %d", rec.Code)
		}
	})

	t.Run("alerts get invalid filter matcher contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?filter=broken-matcher", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts with invalid filter expected 400, got %d", rec.Code)
		}
	})

	t.Run("alerts post contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(validAlertPayload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/alerts expected 200, got %d", rec.Code)
		}
	})

	t.Run("alerts post invalid payload contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/alerts with invalid payload expected 400, got %d", rec.Code)
		}
	})

	t.Run("silences get contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/silences expected 200, got %d", rec.Code)
		}

		var payload []any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences response is not valid json: %v", err)
		}
		if payload == nil {
			t.Fatalf("silences get expected array payload")
		}
	})

	t.Run("silences get invalid filter contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/silences?filter=broken-matcher", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/silences with invalid filter expected 400, got %d", rec.Code)
		}
		var payload string
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences invalid filter expected json string error, got %q (%v)", rec.Body.String(), err)
		}
		if strings.TrimSpace(payload) == "" {
			t.Fatalf("silences invalid filter expected non-empty error message")
		}
	})

	t.Run("silences post contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(validSilencePayload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/silences expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences post response is not valid json: %v", err)
		}
		if _, ok := payload["silenceID"]; !ok {
			t.Fatalf("silences post expected silenceID field")
		}
	})

	t.Run("silences post invalid payload contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences with invalid payload expected 400, got %d", rec.Code)
		}
		var payload string
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences invalid payload expected json string error, got %q (%v)", rec.Body.String(), err)
		}
		if strings.TrimSpace(payload) == "" {
			t.Fatalf("silences invalid payload expected non-empty error message")
		}
	})

	t.Run("silences post invalid regex matcher contract", func(t *testing.T) {
		payload := `{
			"matchers": [{"name":"alertname","value":"[","isRegex":true}],
			"startsAt": "2099-01-01T00:00:00Z",
			"endsAt": "2099-01-01T01:00:00Z",
			"createdBy": "phase0-test",
			"comment": "invalid regex matcher"
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences with invalid regex matcher expected 400, got %d", rec.Code)
		}
	})

	t.Run("silences post invalid matcher name contract", func(t *testing.T) {
		payload := `{
			"matchers": [{"name":"123bad","value":"value","isRegex":false}],
			"startsAt": "2099-01-01T00:00:00Z",
			"endsAt": "2099-01-01T01:00:00Z",
			"createdBy": "phase0-test",
			"comment": "invalid matcher name"
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences with invalid matcher name expected 400, got %d", rec.Code)
		}
	})

	t.Run("silences post endsAt in past contract", func(t *testing.T) {
		now := time.Now().UTC()
		payload := fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"PastEndTime","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "phase0-test",
			"comment": "past end time"
		}`, now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-1*time.Hour).Format(time.RFC3339))

		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences with endsAt in past expected 400, got %d", rec.Code)
		}
	})

	t.Run("silences post update unknown id contract", func(t *testing.T) {
		payload := `{
			"id": "ffffffff-ffff-ffff-ffff-ffffffffffff",
			"matchers": [{"name":"alertname","value":"ContractUnknownID","isRegex":false}],
			"startsAt": "2099-01-01T00:00:00Z",
			"endsAt": "2099-01-01T01:00:00Z",
			"createdBy": "phase0-test",
			"comment": "unknown id update"
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST /api/v2/silences update with unknown id expected 404, got %d", rec.Code)
		}
		var errorPayload string
		if err := json.Unmarshal(rec.Body.Bytes(), &errorPayload); err != nil {
			t.Fatalf("silences unknown id expected json string error, got %q (%v)", rec.Body.String(), err)
		}
		if strings.TrimSpace(errorPayload) == "" {
			t.Fatalf("silences unknown id expected non-empty error message")
		}
	})

	t.Run("silence by id contract", func(t *testing.T) {
		getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/"+unknownSilenceUUID, nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v2/silence/{id} expected 404, got %d", getRec.Code)
		}
		if getRec.Body.Len() != 0 {
			t.Fatalf("GET /api/v2/silence/{id} expected empty body for unknown id, got %q", getRec.Body.String())
		}

		delReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+unknownSilenceUUID, nil)
		delRec := httptest.NewRecorder()
		mux.ServeHTTP(delRec, delReq)
		if delRec.Code != http.StatusNotFound {
			t.Fatalf("DELETE /api/v2/silence/{id} expected 404, got %d", delRec.Code)
		}
		if delRec.Body.Len() != 0 {
			t.Fatalf("DELETE /api/v2/silence/{id} expected empty body for unknown id, got %q", delRec.Body.String())
		}
	})

	t.Run("silence by id invalid id contract", func(t *testing.T) {
		getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/not-a-uuid", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("GET /api/v2/silence/{id} with invalid id expected 422, got %d", getRec.Code)
		}
		var getPayload map[string]any
		if err := json.Unmarshal(getRec.Body.Bytes(), &getPayload); err != nil {
			t.Fatalf("GET /api/v2/silence/{id} with invalid id expected json payload, got %q (%v)", getRec.Body.String(), err)
		}
		if getPayload["code"] != float64(601) {
			t.Fatalf("GET /api/v2/silence/{id} invalid id expected code=601, got %v", getPayload["code"])
		}
		if message, _ := getPayload["message"].(string); !strings.Contains(message, "silenceID in path must be of type uuid") {
			t.Fatalf("GET /api/v2/silence/{id} invalid id expected upstream-like message, got %v", getPayload["message"])
		}

		delReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/not-a-uuid", nil)
		delRec := httptest.NewRecorder()
		mux.ServeHTTP(delRec, delReq)
		if delRec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("DELETE /api/v2/silence/{id} with invalid id expected 422, got %d", delRec.Code)
		}
		var delPayload map[string]any
		if err := json.Unmarshal(delRec.Body.Bytes(), &delPayload); err != nil {
			t.Fatalf("DELETE /api/v2/silence/{id} with invalid id expected json payload, got %q (%v)", delRec.Body.String(), err)
		}
		if delPayload["code"] != float64(601) {
			t.Fatalf("DELETE /api/v2/silence/{id} invalid id expected code=601, got %v", delPayload["code"])
		}
		if message, _ := delPayload["message"].(string); !strings.Contains(message, "silenceID in path must be of type uuid") {
			t.Fatalf("DELETE /api/v2/silence/{id} invalid id expected upstream-like message, got %v", delPayload["message"])
		}
	})

	t.Run("receivers get contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/receivers expected 200, got %d", rec.Code)
		}

		var payload []any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("receivers response is not valid json: %v", err)
		}
		if payload == nil {
			t.Fatalf("receivers get expected array payload")
		}
	})

	t.Run("alert groups get contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", rec.Code)
		}

		var payload []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alert groups response is not valid json: %v", err)
		}
		if payload == nil {
			t.Fatalf("alert groups get expected array payload")
		}
		if len(payload) > 0 {
			receiver, ok := payload[0]["receiver"].(map[string]any)
			if !ok {
				t.Fatalf("alert groups response expected receiver object, got %T", payload[0]["receiver"])
			}
			name, ok := receiver["name"].(string)
			if !ok || name == "" {
				t.Fatalf("alert groups response expected receiver.name string, got %v", receiver["name"])
			}
		}
	})

	t.Run("alert groups invalid query filters contract", func(t *testing.T) {
		reqResolved := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?resolved=not-bool", nil)
		recResolved := httptest.NewRecorder()
		mux.ServeHTTP(recResolved, reqResolved)
		if recResolved.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid resolved expected 400, got %d", recResolved.Code)
		}

		reqReceiver := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?receiver=[", nil)
		recReceiver := httptest.NewRecorder()
		mux.ServeHTTP(recReceiver, reqReceiver)
		if recReceiver.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid receiver regex expected 400, got %d", recReceiver.Code)
		}

		reqActive := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?active=not-bool", nil)
		recActive := httptest.NewRecorder()
		mux.ServeHTTP(recActive, reqActive)
		if recActive.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid active expected 400, got %d", recActive.Code)
		}

		reqMuted := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?muted=not-bool", nil)
		recMuted := httptest.NewRecorder()
		mux.ServeHTTP(recMuted, reqMuted)
		if recMuted.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid muted expected 400, got %d", recMuted.Code)
		}

		reqFilter := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?filter=broken-matcher", nil)
		recFilter := httptest.NewRecorder()
		mux.ServeHTTP(recFilter, reqFilter)
		if recFilter.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid filter expected 400, got %d", recFilter.Code)
		}
	})

	t.Run("method contracts", func(t *testing.T) {
		tests := []struct {
			name   string
			method string
			path   string
		}{
			{name: "status post not allowed", method: http.MethodPost, path: "/api/v2/status"},
			{name: "config put not allowed", method: http.MethodPut, path: "/api/v2/config"},
			{name: "config status post not allowed", method: http.MethodPost, path: "/api/v2/config/status"},
			{name: "config history post not allowed", method: http.MethodPost, path: "/api/v2/config/history"},
			{name: "config revisions post not allowed", method: http.MethodPost, path: "/api/v2/config/revisions"},
			{name: "config revisions prune get not allowed", method: http.MethodGet, path: "/api/v2/config/revisions/prune"},
			{name: "config rollback get not allowed", method: http.MethodGet, path: "/api/v2/config/rollback"},
			{name: "silences put not allowed", method: http.MethodPut, path: "/api/v2/silences"},
			{name: "receivers post not allowed", method: http.MethodPost, path: "/api/v2/receivers"},
			{name: "alert groups post not allowed", method: http.MethodPost, path: "/api/v2/alerts/groups"},
			{name: "silence by id post not allowed", method: http.MethodPost, path: "/api/v2/silence/any-id"},
			{name: "history post not allowed", method: http.MethodPost, path: "/history"},
			{name: "history recent post not allowed", method: http.MethodPost, path: "/history/recent"},
			{name: "dashboard overview post not allowed", method: http.MethodPost, path: "/api/dashboard/overview"},
			{name: "dashboard recent post not allowed", method: http.MethodPost, path: "/api/dashboard/alerts/recent"},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, tt.path, nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s expected 405, got %d", tt.method, tt.path, rec.Code)
				}
			})
		}
	})

	t.Run("webhook method contract", func(t *testing.T) {
		postReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(validAlertPayload))
		postRec := httptest.NewRecorder()
		mux.ServeHTTP(postRec, postReq)

		if postRec.Code != http.StatusOK {
			t.Fatalf("POST /webhook expected 200, got %d", postRec.Code)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/webhook", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)

		if getRec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET /webhook expected 405, got %d", getRec.Code)
		}
	})

	t.Run("webhook invalid payload contract", func(t *testing.T) {
		postReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
		postRec := httptest.NewRecorder()
		mux.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusBadRequest {
			t.Fatalf("POST /webhook with invalid payload expected 400, got %d", postRec.Code)
		}
	})

	t.Run("alertmanager compatibility probes contract", func(t *testing.T) {
		tests := []struct {
			name   string
			method string
			path   string
			body   string
			status int
			textOK bool
			empty  bool
		}{
			{name: "healthy get", method: http.MethodGet, path: "/-/healthy", status: http.StatusOK, textOK: true},
			{name: "healthy head", method: http.MethodHead, path: "/-/healthy", status: http.StatusOK},
			{name: "ready get", method: http.MethodGet, path: "/-/ready", status: http.StatusOK, textOK: true},
			{name: "ready head", method: http.MethodHead, path: "/-/ready", status: http.StatusOK},
			{name: "healthy post not allowed", method: http.MethodPost, path: "/-/healthy", status: http.StatusMethodNotAllowed},
			{name: "ready post not allowed", method: http.MethodPost, path: "/-/ready", status: http.StatusMethodNotAllowed},
			{name: "reload post", method: http.MethodPost, path: "/-/reload", body: `{}`, status: http.StatusOK, empty: true},
			{name: "reload get not allowed", method: http.MethodGet, path: "/-/reload", status: http.StatusMethodNotAllowed},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)

				if rec.Code != tt.status {
					t.Fatalf("%s %s expected %d, got %d", tt.method, tt.path, tt.status, rec.Code)
				}

				if tt.textOK && rec.Body.String() != "OK" {
					t.Fatalf("%s %s expected body OK, got %q", tt.method, tt.path, rec.Body.String())
				}
				if tt.empty && rec.Body.Len() != 0 {
					t.Fatalf("%s %s expected empty body, got %q", tt.method, tt.path, rec.Body.String())
				}
			})
		}
	})

	t.Run("debug compatibility contract", func(t *testing.T) {
		tests := []struct {
			name     string
			method   string
			path     string
			body     string
			status   int
			contains string
		}{
			{name: "debug get", method: http.MethodGet, path: "/debug/pprof/", status: http.StatusOK, contains: "Types of profiles available"},
			{name: "debug post", method: http.MethodPost, path: "/debug/pprof/", body: `{}`, status: http.StatusMethodNotAllowed},
			{name: "debug put not allowed", method: http.MethodPut, path: "/debug/pprof/", status: http.StatusMethodNotAllowed},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != tt.status {
					t.Fatalf("%s %s expected %d, got %d", tt.method, tt.path, tt.status, rec.Code)
				}
				if tt.contains != "" && !strings.Contains(rec.Body.String(), tt.contains) {
					t.Fatalf("%s %s expected body to contain %q, got %q", tt.method, tt.path, tt.contains, rec.Body.String())
				}
			})
		}
	})

	t.Run("upstream static compatibility contract", func(t *testing.T) {
		tests := []struct {
			name   string
			method string
			path   string
			status int
		}{
			{name: "script js get", method: http.MethodGet, path: "/script.js", status: http.StatusOK},
			{name: "script js post not allowed", method: http.MethodPost, path: "/script.js", status: http.StatusMethodNotAllowed},
			{name: "favicon get missing", method: http.MethodGet, path: "/favicon.ico", status: http.StatusNotFound},
			{name: "favicon post not allowed", method: http.MethodPost, path: "/favicon.ico", status: http.StatusMethodNotAllowed},
			{name: "lib get missing", method: http.MethodGet, path: "/lib/nonexistent.js", status: http.StatusNotFound},
			{name: "lib post not allowed", method: http.MethodPost, path: "/lib/nonexistent.js", status: http.StatusMethodNotAllowed},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, tt.path, nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != tt.status {
					t.Fatalf("%s %s expected %d, got %d", tt.method, tt.path, tt.status, rec.Code)
				}
			})
		}
	})

	t.Run("alerts v1 ingest compatibility contract", func(t *testing.T) {
		postReq := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewBufferString(`[]`))
		postRec := httptest.NewRecorder()
		mux.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v1/alerts expected 200, got %d", postRec.Code)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET /api/v1/alerts expected 405, got %d", getRec.Code)
		}
	})
}

func TestPhase0AlertsStateSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	post := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	t.Run("dedup keeps one firing alert", func(t *testing.T) {
		first := post(validAlertPayload)
		if first.Code != http.StatusOK {
			t.Fatalf("first POST expected 200, got %d", first.Code)
		}
		second := post(validAlertPayload)
		if second.Code != http.StatusOK {
			t.Fatalf("second POST expected 200, got %d", second.Code)
		}

		rec := get("/api/v2/alerts")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts expected 200, got %d", rec.Code)
		}

		var payload []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode alerts list: %v", err)
		}
		if len(payload) != 1 {
			t.Fatalf("expected exactly 1 deduplicated alert, got %d", len(payload))
		}
		status, ok := payload[0]["status"].(map[string]any)
		if !ok {
			t.Fatalf("expected alert status object, got %T", payload[0]["status"])
		}
		if status["state"] != "active" {
			t.Fatalf("expected active status.state, got %v", status["state"])
		}
	})

	t.Run("resolved closes firing and appears via resolved filter", func(t *testing.T) {
		resolvedPayload := `[
			{
				"labels": {"alertname":"TestAlert","service":"amp"},
				"startsAt": "2026-02-25T00:00:00Z",
				"endsAt": "2026-02-25T00:05:00Z",
				"status": "resolved"
			}
		]`

		resolvedResp := post(resolvedPayload)
		if resolvedResp.Code != http.StatusOK {
			t.Fatalf("resolved POST expected 200, got %d", resolvedResp.Code)
		}

		activeRec := get("/api/v2/alerts")
		if activeRec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts expected 200, got %d", activeRec.Code)
		}
		var active []map[string]any
		if err := json.Unmarshal(activeRec.Body.Bytes(), &active); err != nil {
			t.Fatalf("failed to decode active alerts: %v", err)
		}
		if len(active) != 0 {
			t.Fatalf("expected no firing alerts after resolve, got %d", len(active))
		}

		resolvedRec := get("/api/v2/alerts?status=resolved")
		if resolvedRec.Code != http.StatusOK {
			t.Fatalf("GET resolved alerts expected 200, got %d", resolvedRec.Code)
		}
		var resolved []map[string]any
		if err := json.Unmarshal(resolvedRec.Body.Bytes(), &resolved); err != nil {
			t.Fatalf("failed to decode resolved alerts: %v", err)
		}
		if len(resolved) != 1 {
			t.Fatalf("expected 1 resolved alert, got %d", len(resolved))
		}
		status, ok := resolved[0]["status"].(map[string]any)
		if !ok {
			t.Fatalf("expected alert status object, got %T", resolved[0]["status"])
		}
		if status["state"] != "unprocessed" {
			t.Fatalf("expected unprocessed status.state for resolved alert, got %v", status["state"])
		}

		historyReq := httptest.NewRequest(http.MethodGet, "/history", nil)
		historyRec := httptest.NewRecorder()
		mux.ServeHTTP(historyRec, historyReq)
		if historyRec.Code != http.StatusOK {
			t.Fatalf("GET /history expected 200, got %d", historyRec.Code)
		}

		var historyPayload map[string]any
		if err := json.Unmarshal(historyRec.Body.Bytes(), &historyPayload); err != nil {
			t.Fatalf("failed to decode history payload: %v", err)
		}
		total, ok := historyPayload["total"].(float64)
		if !ok {
			t.Fatalf("history total has unexpected type: %T", historyPayload["total"])
		}
		if total < 1 {
			t.Fatalf("history total expected >= 1, got %.0f", total)
		}
	})

	t.Run("invalid status filter returns bad request", func(t *testing.T) {
		rec := get("/api/v2/alerts?status=broken")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid status filter expected 400, got %d", rec.Code)
		}
	})
}

func TestPhase0AlertsV1AliasUsesSameIngestPath(t *testing.T) {
	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewBufferString(validAlertPayload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/alerts expected 200, got %d", rec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", getRec.Code)
	}

	var payload []map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode alerts list: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected v1 alias to ingest one alert, got %d", len(payload))
	}
}

func TestPhase0AlertsReceiverFilterSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"CPUOps","service":"api","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"CPUApp","service":"api","receiver":"team-app"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"CPUNoReceiver","service":"api"},
			"startsAt": "2026-02-25T00:02:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	opsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?receiver=^team-ops$", nil)
	opsRec := httptest.NewRecorder()
	mux.ServeHTTP(opsRec, opsReq)
	if opsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with receiver filter expected 200, got %d", opsRec.Code)
	}

	var opsAlerts []map[string]any
	if err := json.Unmarshal(opsRec.Body.Bytes(), &opsAlerts); err != nil {
		t.Fatalf("failed to decode filtered alerts response: %v", err)
	}
	if len(opsAlerts) != 1 {
		t.Fatalf("expected 1 alert for receiver team-ops, got %d", len(opsAlerts))
	}
	labels, ok := opsAlerts[0]["labels"].(map[string]any)
	if !ok || labels["receiver"] != "team-ops" {
		t.Fatalf("expected filtered alert labels.receiver=team-ops, got %v", opsAlerts[0]["labels"])
	}

	defaultReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?receiver=^default$", nil)
	defaultRec := httptest.NewRecorder()
	mux.ServeHTTP(defaultRec, defaultReq)
	if defaultRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with default receiver filter expected 200, got %d", defaultRec.Code)
	}

	var defaultAlerts []map[string]any
	if err := json.Unmarshal(defaultRec.Body.Bytes(), &defaultAlerts); err != nil {
		t.Fatalf("failed to decode default receiver alerts response: %v", err)
	}
	if len(defaultAlerts) != 1 {
		t.Fatalf("expected 1 alert for default receiver, got %d", len(defaultAlerts))
	}
	defaultLabels, ok := defaultAlerts[0]["labels"].(map[string]any)
	if !ok || defaultLabels["alertname"] != "CPUNoReceiver" {
		t.Fatalf("expected default receiver alert CPUNoReceiver, got %v", defaultAlerts[0]["labels"])
	}
}

func TestPhase0AlertsResponseShapeIncludesReceiversAndUpdatedAt(t *testing.T) {
	mux := newPhase0TestMux(t)

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(validAlertPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", getRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert in response, got %d", len(alerts))
	}

	updatedAt, ok := alerts[0]["updatedAt"].(string)
	if !ok || strings.TrimSpace(updatedAt) == "" {
		t.Fatalf("alert updatedAt expected non-empty string, got %v", alerts[0]["updatedAt"])
	}
	if _, err := time.Parse(time.RFC3339, updatedAt); err != nil {
		t.Fatalf("alert updatedAt expected RFC3339 timestamp, got %q: %v", updatedAt, err)
	}
	endsAt, ok := alerts[0]["endsAt"].(string)
	if !ok || strings.TrimSpace(endsAt) == "" {
		t.Fatalf("alert endsAt expected non-empty string, got %v", alerts[0]["endsAt"])
	}
	if _, err := time.Parse(time.RFC3339, endsAt); err != nil {
		t.Fatalf("alert endsAt expected RFC3339 timestamp, got %q: %v", endsAt, err)
	}

	annotations, ok := alerts[0]["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("alert annotations expected object, got %T", alerts[0]["annotations"])
	}
	if annotations == nil {
		t.Fatalf("alert annotations must not be nil")
	}

	receivers, ok := alerts[0]["receivers"].([]any)
	if !ok {
		t.Fatalf("alert receivers expected array, got %T", alerts[0]["receivers"])
	}
	if len(receivers) != 1 {
		t.Fatalf("expected exactly one receiver, got %d", len(receivers))
	}
	receiver, ok := receivers[0].(map[string]any)
	if !ok {
		t.Fatalf("receiver expected object, got %T", receivers[0])
	}
	if receiver["name"] != "default" {
		t.Fatalf("expected default receiver name, got %v", receiver["name"])
	}

	status, ok := alerts[0]["status"].(map[string]any)
	if !ok {
		t.Fatalf("alert status expected object, got %T", alerts[0]["status"])
	}
	if status["state"] != "active" {
		t.Fatalf("expected alert status.state=active, got %v", status["state"])
	}
	for _, field := range []string{"silencedBy", "inhibitedBy", "mutedBy"} {
		value, ok := status[field].([]any)
		if !ok {
			t.Fatalf("alert status.%s expected array, got %T", field, status[field])
		}
		if len(value) != 0 {
			t.Fatalf("alert status.%s expected empty array, got %v", field, value)
		}
	}
}

func TestPhase0AlertsFilterMatcherSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"CPUHigh","service":"api","severity":"critical"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"CPUMed","service":"api","severity":"warning"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"DiskHigh","service":"worker","severity":"critical"},
			"startsAt": "2026-02-25T00:02:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	serviceQuery := url.Values{}
	serviceQuery.Add("filter", `service="api"`)
	serviceReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+serviceQuery.Encode(), nil)
	serviceRec := httptest.NewRecorder()
	mux.ServeHTTP(serviceRec, serviceReq)
	if serviceRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with service filter expected 200, got %d", serviceRec.Code)
	}
	var serviceAlerts []map[string]any
	if err := json.Unmarshal(serviceRec.Body.Bytes(), &serviceAlerts); err != nil {
		t.Fatalf("failed to decode service filter response: %v", err)
	}
	if len(serviceAlerts) != 2 {
		t.Fatalf("expected 2 alerts for service=api, got %d", len(serviceAlerts))
	}

	regexQuery := url.Values{}
	regexQuery.Add("filter", `alertname=~"^CPU"`)
	regexReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+regexQuery.Encode(), nil)
	regexRec := httptest.NewRecorder()
	mux.ServeHTTP(regexRec, regexReq)
	if regexRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with regex filter expected 200, got %d", regexRec.Code)
	}
	var regexAlerts []map[string]any
	if err := json.Unmarshal(regexRec.Body.Bytes(), &regexAlerts); err != nil {
		t.Fatalf("failed to decode regex filter response: %v", err)
	}
	if len(regexAlerts) != 2 {
		t.Fatalf("expected 2 alerts for alertname=~^CPU, got %d", len(regexAlerts))
	}

	multiQuery := url.Values{}
	multiQuery.Add("filter", `service="api"`)
	multiQuery.Add("filter", `severity="critical"`)
	multiReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+multiQuery.Encode(), nil)
	multiRec := httptest.NewRecorder()
	mux.ServeHTTP(multiRec, multiReq)
	if multiRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with multi-filter expected 200, got %d", multiRec.Code)
	}
	var multiAlerts []map[string]any
	if err := json.Unmarshal(multiRec.Body.Bytes(), &multiAlerts); err != nil {
		t.Fatalf("failed to decode multi-filter response: %v", err)
	}
	if len(multiAlerts) != 1 {
		t.Fatalf("expected 1 alert for service=api AND severity=critical, got %d", len(multiAlerts))
	}
	labels, ok := multiAlerts[0]["labels"].(map[string]any)
	if !ok || labels["alertname"] != "CPUHigh" {
		t.Fatalf("expected CPUHigh for multi-filter, got %v", multiAlerts[0]["labels"])
	}
}

func TestPhase0AlertsStateFlagSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"FlagFiring","service":"api"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"FlagResolved","service":"api"},
			"startsAt": "2026-02-25T00:01:00Z",
			"endsAt": "2026-02-25T00:05:00Z",
			"status": "resolved"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	noneReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=false&unprocessed=false",
		nil,
	)
	noneRec := httptest.NewRecorder()
	mux.ServeHTTP(noneRec, noneReq)
	if noneRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with all flags false expected 200, got %d", noneRec.Code)
	}

	var noneAlerts []map[string]any
	if err := json.Unmarshal(noneRec.Body.Bytes(), &noneAlerts); err != nil {
		t.Fatalf("failed to decode all-false flags response: %v", err)
	}
	if len(noneAlerts) != 0 {
		t.Fatalf("expected no alerts when all state flags are false, got %d", len(noneAlerts))
	}

	resolvedReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?resolved=true&active=false&silenced=false&inhibited=false&unprocessed=false",
		nil,
	)
	resolvedRec := httptest.NewRecorder()
	mux.ServeHTTP(resolvedRec, resolvedReq)
	if resolvedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts resolved with all flags false expected 200, got %d", resolvedRec.Code)
	}

	var resolvedAlerts []map[string]any
	if err := json.Unmarshal(resolvedRec.Body.Bytes(), &resolvedAlerts); err != nil {
		t.Fatalf("failed to decode resolved+flags response: %v", err)
	}
	if len(resolvedAlerts) != 0 {
		t.Fatalf("expected no resolved snapshots when unprocessed=false, got %d", len(resolvedAlerts))
	}

	resolvedUnprocessedReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?resolved=true&active=false&silenced=false&inhibited=false&unprocessed=true",
		nil,
	)
	resolvedUnprocessedRec := httptest.NewRecorder()
	mux.ServeHTTP(resolvedUnprocessedRec, resolvedUnprocessedReq)
	if resolvedUnprocessedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts resolved with unprocessed=true expected 200, got %d", resolvedUnprocessedRec.Code)
	}

	var resolvedUnprocessedAlerts []map[string]any
	if err := json.Unmarshal(resolvedUnprocessedRec.Body.Bytes(), &resolvedUnprocessedAlerts); err != nil {
		t.Fatalf("failed to decode resolved+unprocessed response: %v", err)
	}
	if len(resolvedUnprocessedAlerts) != 1 {
		t.Fatalf("expected one resolved snapshot when unprocessed=true, got %d", len(resolvedUnprocessedAlerts))
	}
	status, ok := resolvedUnprocessedAlerts[0]["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolved alert status object, got %T", resolvedUnprocessedAlerts[0]["status"])
	}
	if status["state"] != "unprocessed" {
		t.Fatalf("expected resolved alert status.state=unprocessed, got %v", status["state"])
	}

	silenceReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/silences",
		bytes.NewBufferString(activeSilencePayloadForAlert(time.Now().UTC(), "FlagFiring")),
	)
	silenceRec := httptest.NewRecorder()
	mux.ServeHTTP(silenceRec, silenceReq)
	if silenceRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences for state-flag test expected 200, got %d", silenceRec.Code)
	}

	silencedOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=true&inhibited=false&unprocessed=false",
		nil,
	)
	silencedOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(silencedOnlyRec, silencedOnlyReq)
	if silencedOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts silenced-only expected 200, got %d", silencedOnlyRec.Code)
	}

	var silencedOnlyAlerts []map[string]any
	if err := json.Unmarshal(silencedOnlyRec.Body.Bytes(), &silencedOnlyAlerts); err != nil {
		t.Fatalf("failed to decode silenced-only alerts response: %v", err)
	}
	if len(silencedOnlyAlerts) != 1 {
		t.Fatalf("expected one silenced alert, got %d", len(silencedOnlyAlerts))
	}
	silencedLabels, ok := silencedOnlyAlerts[0]["labels"].(map[string]any)
	if !ok || silencedLabels["alertname"] != "FlagFiring" {
		t.Fatalf("expected FlagFiring in silenced-only response, got %v", silencedOnlyAlerts[0]["labels"])
	}
	silencedStatus, ok := silencedOnlyAlerts[0]["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected silenced alert status object, got %T", silencedOnlyAlerts[0]["status"])
	}
	if silencedStatus["state"] != "suppressed" {
		t.Fatalf("expected silenced alert status.state=suppressed, got %v", silencedStatus["state"])
	}
	silencedBy, ok := silencedStatus["silencedBy"].([]any)
	if !ok || len(silencedBy) == 0 {
		t.Fatalf("expected silenced alert status.silencedBy to be non-empty, got %v", silencedStatus["silencedBy"])
	}
	mutedBy, ok := silencedStatus["mutedBy"].([]any)
	if !ok || len(mutedBy) == 0 {
		t.Fatalf("expected silenced alert status.mutedBy to be non-empty, got %v", silencedStatus["mutedBy"])
	}

	activeOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=true&silenced=false&inhibited=true&unprocessed=true",
		nil,
	)
	activeOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(activeOnlyRec, activeOnlyReq)
	if activeOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts active-only expected 200, got %d", activeOnlyRec.Code)
	}

	var activeOnlyAlerts []map[string]any
	if err := json.Unmarshal(activeOnlyRec.Body.Bytes(), &activeOnlyAlerts); err != nil {
		t.Fatalf("failed to decode active-only alerts response: %v", err)
	}
	if len(activeOnlyAlerts) != 0 {
		t.Fatalf("expected no active alerts after silencing FlagFiring, got %d", len(activeOnlyAlerts))
	}
}

func TestPhase0AlertsInhibitedMetadataSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"FlagInhibited","service":"api"},
			"annotations": {"inhibitedBy":"rule-a,rule-b"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedOnlyRec, inhibitedOnlyReq)
	if inhibitedOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts inhibited-only expected 200, got %d", inhibitedOnlyRec.Code)
	}

	var inhibitedAlerts []map[string]any
	if err := json.Unmarshal(inhibitedOnlyRec.Body.Bytes(), &inhibitedAlerts); err != nil {
		t.Fatalf("failed to decode inhibited-only alerts response: %v", err)
	}
	if len(inhibitedAlerts) != 1 {
		t.Fatalf("expected one inhibited alert, got %d", len(inhibitedAlerts))
	}

	status, ok := inhibitedAlerts[0]["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected inhibited alert status object, got %T", inhibitedAlerts[0]["status"])
	}
	if status["state"] != "suppressed" {
		t.Fatalf("expected inhibited alert status.state=suppressed, got %v", status["state"])
	}

	inhibitedBy, ok := status["inhibitedBy"].([]any)
	if !ok || len(inhibitedBy) != 2 {
		t.Fatalf("expected two inhibitedBy entries, got %v", status["inhibitedBy"])
	}

	mutedBy, ok := status["mutedBy"].([]any)
	if !ok || len(mutedBy) != 2 {
		t.Fatalf("expected mutedBy to include inhibited entries, got %v", status["mutedBy"])
	}

	activeOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=true&silenced=false&inhibited=false&unprocessed=false",
		nil,
	)
	activeOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(activeOnlyRec, activeOnlyReq)
	if activeOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts active-only expected 200, got %d", activeOnlyRec.Code)
	}

	var activeAlerts []map[string]any
	if err := json.Unmarshal(activeOnlyRec.Body.Bytes(), &activeAlerts); err != nil {
		t.Fatalf("failed to decode active-only alerts response: %v", err)
	}
	if len(activeAlerts) != 0 {
		t.Fatalf("expected no active alerts when only inhibited alert exists, got %d", len(activeAlerts))
	}
}

func TestPhase0AlertsInhibitedByRulesSemantics(t *testing.T) {
	configPath := writeTestConfigFile(t, `
inhibit_rules:
  - name: "critical-inhibits-warning-same-service"
    source_match:
      severity: "critical"
    target_match:
      severity: "warning"
    equal:
      - service
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"RootCause","service":"api","severity":"critical"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"Symptom","service":"api","severity":"warning"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedOnlyRec, inhibitedOnlyReq)
	if inhibitedOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts inhibited-only expected 200, got %d", inhibitedOnlyRec.Code)
	}

	var inhibitedAlerts []map[string]any
	if err := json.Unmarshal(inhibitedOnlyRec.Body.Bytes(), &inhibitedAlerts); err != nil {
		t.Fatalf("failed to decode inhibited-only alerts response: %v", err)
	}
	if len(inhibitedAlerts) != 1 {
		t.Fatalf("expected one rule-inhibited alert, got %d", len(inhibitedAlerts))
	}

	labels, ok := inhibitedAlerts[0]["labels"].(map[string]any)
	if !ok || labels["alertname"] != "Symptom" {
		t.Fatalf("expected Symptom to be inhibited by rule, got %v", inhibitedAlerts[0]["labels"])
	}

	status, ok := inhibitedAlerts[0]["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected inhibited alert status object, got %T", inhibitedAlerts[0]["status"])
	}
	if status["state"] != "suppressed" {
		t.Fatalf("expected rule-inhibited alert status.state=suppressed, got %v", status["state"])
	}

	inhibitedBy, ok := status["inhibitedBy"].([]any)
	if !ok || len(inhibitedBy) == 0 {
		t.Fatalf("expected non-empty inhibitedBy for rule-inhibited alert, got %v", status["inhibitedBy"])
	}

	activeOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=true&silenced=false&inhibited=false&unprocessed=false",
		nil,
	)
	activeOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(activeOnlyRec, activeOnlyReq)
	if activeOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts active-only expected 200, got %d", activeOnlyRec.Code)
	}

	var activeAlerts []map[string]any
	if err := json.Unmarshal(activeOnlyRec.Body.Bytes(), &activeAlerts); err != nil {
		t.Fatalf("failed to decode active-only alerts response: %v", err)
	}
	if len(activeAlerts) != 1 {
		t.Fatalf("expected one active (source) alert after rule inhibition, got %d", len(activeAlerts))
	}
	activeLabels, ok := activeAlerts[0]["labels"].(map[string]any)
	if !ok || activeLabels["alertname"] != "RootCause" {
		t.Fatalf("expected RootCause in active-only response, got %v", activeAlerts[0]["labels"])
	}
}

func TestPhase0AlertsInhibitedByRulesRegexAndEqualSemantics(t *testing.T) {
	configPath := writeTestConfigFile(t, `
inhibit_rules:
  - name: "regex-critical-inhibits-warning-same-cluster"
    source_match_re:
      alertname: "^Root.*"
      severity: "critical|high"
    target_match_re:
      alertname: "^Symptom.*"
      severity: "warning|info"
    equal:
      - cluster
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"RootNodeDown","service":"api","severity":"critical","cluster":"prod-a"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"SymptomApiDown","service":"api","severity":"warning","cluster":"prod-a"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"SymptomWorkerDown","service":"api","severity":"warning","cluster":"prod-b"},
			"startsAt": "2026-02-25T00:02:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedOnlyRec, inhibitedOnlyReq)
	if inhibitedOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts inhibited-only expected 200, got %d", inhibitedOnlyRec.Code)
	}

	var inhibitedAlerts []map[string]any
	if err := json.Unmarshal(inhibitedOnlyRec.Body.Bytes(), &inhibitedAlerts); err != nil {
		t.Fatalf("failed to decode inhibited-only alerts response: %v", err)
	}
	if len(inhibitedAlerts) != 1 {
		t.Fatalf("expected one inhibited alert for regex+equal rule, got %d", len(inhibitedAlerts))
	}
	labels, ok := inhibitedAlerts[0]["labels"].(map[string]any)
	if !ok || labels["alertname"] != "SymptomApiDown" {
		t.Fatalf("expected SymptomApiDown to be inhibited, got %v", inhibitedAlerts[0]["labels"])
	}

	activeOnlyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=true&silenced=false&inhibited=false&unprocessed=false",
		nil,
	)
	activeOnlyRec := httptest.NewRecorder()
	mux.ServeHTTP(activeOnlyRec, activeOnlyReq)
	if activeOnlyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts active-only expected 200, got %d", activeOnlyRec.Code)
	}

	var activeAlerts []map[string]any
	if err := json.Unmarshal(activeOnlyRec.Body.Bytes(), &activeAlerts); err != nil {
		t.Fatalf("failed to decode active-only alerts response: %v", err)
	}
	if len(activeAlerts) != 2 {
		t.Fatalf("expected 2 active alerts (root + equal-mismatch symptom), got %d", len(activeAlerts))
	}
}

func TestPhase0ReloadAppliesRuntimeConfigChanges(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "initial-receiver"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"RootCause","service":"api","severity":"critical"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"Symptom","service":"api","severity":"warning"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedBeforeReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedBeforeRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedBeforeRec, inhibitedBeforeReq)
	if inhibitedBeforeRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts before reload expected 200, got %d", inhibitedBeforeRec.Code)
	}

	var inhibitedBefore []map[string]any
	if err := json.Unmarshal(inhibitedBeforeRec.Body.Bytes(), &inhibitedBefore); err != nil {
		t.Fatalf("failed to decode inhibited-before response: %v", err)
	}
	if len(inhibitedBefore) != 0 {
		t.Fatalf("expected no inhibited alerts before reload, got %d", len(inhibitedBefore))
	}

	updatedConfig := `
route:
  receiver: "team-runtime"
receivers:
  - name: "team-runtime"
inhibit_rules:
  - name: "critical-inhibits-warning"
    source_match:
      severity: "critical"
    target_match:
      severity: "warning"
    equal:
      - service
`
	if err := os.WriteFile(configPath, []byte(updatedConfig), 0o600); err != nil {
		t.Fatalf("failed to update runtime config: %v", err)
	}

	reloadReq := httptest.NewRequest(http.MethodPost, "/-/reload", bytes.NewBufferString(`{}`))
	reloadRec := httptest.NewRecorder()
	mux.ServeHTTP(reloadRec, reloadReq)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("POST /-/reload expected 200, got %d", reloadRec.Code)
	}
	if reloadRec.Body.Len() != 0 {
		t.Fatalf("POST /-/reload expected empty body on success, got %q", reloadRec.Body.String())
	}

	inhibitedAfterReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedAfterRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedAfterRec, inhibitedAfterReq)
	if inhibitedAfterRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts after reload expected 200, got %d", inhibitedAfterRec.Code)
	}

	var inhibitedAfter []map[string]any
	if err := json.Unmarshal(inhibitedAfterRec.Body.Bytes(), &inhibitedAfter); err != nil {
		t.Fatalf("failed to decode inhibited-after response: %v", err)
	}
	if len(inhibitedAfter) != 1 {
		t.Fatalf("expected one inhibited alert after reload, got %d", len(inhibitedAfter))
	}

	receiversReq := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	receiversRec := httptest.NewRecorder()
	mux.ServeHTTP(receiversRec, receiversReq)
	if receiversRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/receivers expected 200, got %d", receiversRec.Code)
	}

	var receivers []map[string]any
	if err := json.Unmarshal(receiversRec.Body.Bytes(), &receivers); err != nil {
		t.Fatalf("failed to decode receivers response: %v", err)
	}
	receiverSet := make(map[string]struct{}, len(receivers))
	for _, receiver := range receivers {
		name, _ := receiver["name"].(string)
		receiverSet[name] = struct{}{}
	}
	if _, ok := receiverSet["team-runtime"]; !ok {
		t.Fatalf("expected reloaded receiver team-runtime, got %v", receivers)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/status expected 200, got %d", statusRec.Code)
	}

	var statusPayload map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}
	configSection, ok := statusPayload["config"].(map[string]any)
	if !ok {
		t.Fatalf("status config expected object, got %T", statusPayload["config"])
	}
	configOriginal, _ := configSection["original"].(string)
	if !strings.Contains(configOriginal, "inhibit_rules") {
		t.Fatalf("expected status config.original to be refreshed after reload")
	}
}

func TestPhase0ConfigPostAppliesRuntimeConfigChanges(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "initial-receiver"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"RootCause","service":"api","severity":"critical"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"Symptom","service":"api","severity":"warning"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedBeforeReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedBeforeRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedBeforeRec, inhibitedBeforeReq)
	if inhibitedBeforeRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts before config post expected 200, got %d", inhibitedBeforeRec.Code)
	}

	var inhibitedBefore []map[string]any
	if err := json.Unmarshal(inhibitedBeforeRec.Body.Bytes(), &inhibitedBefore); err != nil {
		t.Fatalf("failed to decode inhibited-before response: %v", err)
	}
	if len(inhibitedBefore) != 0 {
		t.Fatalf("expected no inhibited alerts before config post, got %d", len(inhibitedBefore))
	}

	updatedConfig := `
route:
  receiver: "team-runtime"
receivers:
  - name: "team-runtime"
inhibit_rules:
  - name: "critical-inhibits-warning"
    source_match:
      severity: "critical"
    target_match:
      severity: "warning"
    equal:
      - service
`

	configPostReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(updatedConfig))
	configPostRec := httptest.NewRecorder()
	mux.ServeHTTP(configPostRec, configPostReq)
	if configPostRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/config expected 200, got %d", configPostRec.Code)
	}

	var configPostPayload map[string]any
	if err := json.Unmarshal(configPostRec.Body.Bytes(), &configPostPayload); err != nil {
		t.Fatalf("failed to decode config post response: %v", err)
	}
	if configPostPayload["status"] != "applied" {
		t.Fatalf("expected config post status=applied, got %v", configPostPayload["status"])
	}

	configOnDisk, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if !strings.Contains(string(configOnDisk), "team-runtime") {
		t.Fatalf("expected config file to be updated with team-runtime receiver")
	}

	inhibitedAfterReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false",
		nil,
	)
	inhibitedAfterRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedAfterRec, inhibitedAfterReq)
	if inhibitedAfterRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts after config post expected 200, got %d", inhibitedAfterRec.Code)
	}

	var inhibitedAfter []map[string]any
	if err := json.Unmarshal(inhibitedAfterRec.Body.Bytes(), &inhibitedAfter); err != nil {
		t.Fatalf("failed to decode inhibited-after response: %v", err)
	}
	if len(inhibitedAfter) != 1 {
		t.Fatalf("expected one inhibited alert after config post, got %d", len(inhibitedAfter))
	}

	receiversReq := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	receiversRec := httptest.NewRecorder()
	mux.ServeHTTP(receiversRec, receiversReq)
	if receiversRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/receivers expected 200, got %d", receiversRec.Code)
	}

	var receivers []map[string]any
	if err := json.Unmarshal(receiversRec.Body.Bytes(), &receivers); err != nil {
		t.Fatalf("failed to decode receivers response: %v", err)
	}
	receiverSet := make(map[string]struct{}, len(receivers))
	for _, receiver := range receivers {
		name, _ := receiver["name"].(string)
		receiverSet[name] = struct{}{}
	}
	if _, ok := receiverSet["team-runtime"]; !ok {
		t.Fatalf("expected runtime receiver team-runtime, got %v", receivers)
	}
}

func TestPhase0ConfigStatusTracksApplySourceAndResult(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	readStatus := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/config/status", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/status expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode config status response: %v", err)
		}
		return payload
	}
	readHistory := func(limit int, rawQuery string) []map[string]any {
		t.Helper()
		path := fmt.Sprintf("/api/v2/config/history?limit=%d", limit)
		if strings.TrimSpace(rawQuery) != "" {
			path += "&" + strings.TrimPrefix(rawQuery, "&")
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/config/history expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode config history response: %v", err)
		}
		rawEntries, ok := payload["entries"].([]any)
		if !ok {
			t.Fatalf("config history response expected entries array, got %T", payload["entries"])
		}
		entries := make([]map[string]any, 0, len(rawEntries))
		for _, raw := range rawEntries {
			entry, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("config history entry expected object, got %T", raw)
			}
			entries = append(entries, entry)
		}
		return entries
	}

	startupStatus := readStatus()
	if startupStatus["status"] != "ok" {
		t.Fatalf("expected startup config status=ok, got %v", startupStatus["status"])
	}
	if startupStatus["source"] != "startup" {
		t.Fatalf("expected startup config source=startup, got %v", startupStatus["source"])
	}
	appliedAt, ok := startupStatus["appliedAt"].(string)
	if !ok || strings.TrimSpace(appliedAt) == "" {
		t.Fatalf("expected startup appliedAt to be set")
	}
	startupHistory := readHistory(3, "")
	if len(startupHistory) == 0 {
		t.Fatalf("expected startup history entry")
	}
	if startupHistory[0]["source"] != "startup" || startupHistory[0]["status"] != "ok" {
		t.Fatalf("expected latest startup history entry, got %v", startupHistory[0])
	}

	configPayload := `
route:
  receiver: "team-api"
receivers:
  - name: "team-api"
`
	configPostReq := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(configPayload))
	configPostRec := httptest.NewRecorder()
	mux.ServeHTTP(configPostRec, configPostReq)
	if configPostRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/config expected 200, got %d", configPostRec.Code)
	}

	apiStatus := readStatus()
	if apiStatus["status"] != "ok" {
		t.Fatalf("expected api config status=ok, got %v", apiStatus["status"])
	}
	if apiStatus["source"] != "api" {
		t.Fatalf("expected api config source=api, got %v", apiStatus["source"])
	}
	if apiStatus["error"] != "" {
		t.Fatalf("expected api config error empty, got %v", apiStatus["error"])
	}

	if err := os.WriteFile(configPath, []byte("route: [\n"), 0o600); err != nil {
		t.Fatalf("failed to write invalid config: %v", err)
	}
	reloadReq := httptest.NewRequest(http.MethodPost, "/-/reload", bytes.NewBufferString(`{}`))
	reloadRec := httptest.NewRecorder()
	mux.ServeHTTP(reloadRec, reloadReq)
	if reloadRec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /-/reload with invalid config expected 500, got %d", reloadRec.Code)
	}

	failedStatus := readStatus()
	if failedStatus["status"] != "failed" {
		t.Fatalf("expected failed config status=failed, got %v", failedStatus["status"])
	}
	if failedStatus["source"] != "reload" {
		t.Fatalf("expected failed config source=reload, got %v", failedStatus["source"])
	}
	errText, _ := failedStatus["error"].(string)
	if strings.TrimSpace(errText) == "" {
		t.Fatalf("expected failed config error message")
	}

	history := readHistory(5, "")
	if len(history) < 3 {
		t.Fatalf("expected at least 3 history entries, got %d", len(history))
	}
	if history[0]["source"] != "reload" || history[0]["status"] != "failed" {
		t.Fatalf("expected latest history entry to be reload failure, got %v", history[0])
	}
	if history[1]["source"] != "api" || history[1]["status"] != "ok" {
		t.Fatalf("expected second history entry to be api success, got %v", history[1])
	}
	if history[2]["source"] != "startup" || history[2]["status"] != "ok" {
		t.Fatalf("expected third history entry to be startup success, got %v", history[2])
	}

	failedHistory := readHistory(5, "status=failed")
	if len(failedHistory) != 1 {
		t.Fatalf("expected exactly one failed history entry, got %d", len(failedHistory))
	}
	if failedHistory[0]["source"] != "reload" || failedHistory[0]["status"] != "failed" {
		t.Fatalf("expected failed history entry to be reload failure, got %v", failedHistory[0])
	}

	reloadHistory := readHistory(5, "source=reload")
	if len(reloadHistory) != 1 {
		t.Fatalf("expected exactly one reload history entry, got %d", len(reloadHistory))
	}
	if reloadHistory[0]["source"] != "reload" {
		t.Fatalf("expected reload history entry source=reload, got %v", reloadHistory[0]["source"])
	}
}

func TestPhase0ConfigRollbackRevertsPreviousRevision(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	postConfig := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
	}

	configA := `
route:
  receiver: "team-a"
receivers:
  - name: "team-a"
`
	configB := `
route:
  receiver: "team-b"
receivers:
  - name: "team-b"
`

	postConfig(configA)
	postConfig(configB)

	configOnDiskBeforeRollback, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config before rollback: %v", err)
	}
	if !strings.Contains(string(configOnDiskBeforeRollback), "team-b") {
		t.Fatalf("expected config before rollback to contain team-b receiver")
	}

	rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v2/config/rollback", bytes.NewBufferString(`{}`))
	rollbackRec := httptest.NewRecorder()
	mux.ServeHTTP(rollbackRec, rollbackReq)
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/config/rollback expected 200, got %d", rollbackRec.Code)
	}

	var rollbackPayload map[string]any
	if err := json.Unmarshal(rollbackRec.Body.Bytes(), &rollbackPayload); err != nil {
		t.Fatalf("failed to decode rollback response: %v", err)
	}
	if rollbackPayload["status"] != "rolled_back" {
		t.Fatalf("expected rollback status=rolled_back, got %v", rollbackPayload["status"])
	}

	configOnDiskAfterRollback, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config after rollback: %v", err)
	}
	if !strings.Contains(string(configOnDiskAfterRollback), "team-a") {
		t.Fatalf("expected config after rollback to contain team-a receiver")
	}
	if strings.Contains(string(configOnDiskAfterRollback), "team-b") {
		t.Fatalf("expected config after rollback to exclude team-b receiver")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/status", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/status expected 200, got %d", statusRec.Code)
	}

	var statusPayload map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("failed to decode config status response: %v", err)
	}
	if statusPayload["status"] != "ok" {
		t.Fatalf("expected config status after rollback to be ok, got %v", statusPayload["status"])
	}
	if statusPayload["source"] != "rollback" {
		t.Fatalf("expected config status source=rollback, got %v", statusPayload["source"])
	}

	receiversReq := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	receiversRec := httptest.NewRecorder()
	mux.ServeHTTP(receiversRec, receiversReq)
	if receiversRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/receivers expected 200, got %d", receiversRec.Code)
	}

	var receivers []map[string]any
	if err := json.Unmarshal(receiversRec.Body.Bytes(), &receivers); err != nil {
		t.Fatalf("failed to decode receivers response: %v", err)
	}
	receiverSet := make(map[string]struct{}, len(receivers))
	for _, receiver := range receivers {
		name, _ := receiver["name"].(string)
		receiverSet[name] = struct{}{}
	}
	if _, ok := receiverSet["team-a"]; !ok {
		t.Fatalf("expected runtime receiver team-a after rollback, got %v", receivers)
	}
	if _, ok := receiverSet["team-b"]; ok {
		t.Fatalf("expected runtime receiver team-b to be removed after rollback, got %v", receivers)
	}
}

func TestPhase0ConfigRollbackToSpecificHash(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	postConfig := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
	}

	configA := `
route:
  receiver: "team-hash-a"
receivers:
  - name: "team-hash-a"
`
	configB := `
route:
  receiver: "team-hash-b"
receivers:
  - name: "team-hash-b"
`

	postConfig(configA)
	postConfig(configB)

	hashA := configSHA256(configA)
	hashB := configSHA256(configB)

	rollbackToHashReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v2/config/rollback?configHash=%s", hashA),
		bytes.NewBufferString(`{}`),
	)
	rollbackToHashRec := httptest.NewRecorder()
	mux.ServeHTTP(rollbackToHashRec, rollbackToHashReq)
	if rollbackToHashRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/config/rollback by hash expected 200, got %d", rollbackToHashRec.Code)
	}

	var rollbackPayload map[string]any
	if err := json.Unmarshal(rollbackToHashRec.Body.Bytes(), &rollbackPayload); err != nil {
		t.Fatalf("failed to decode rollback by hash response: %v", err)
	}
	if rollbackPayload["status"] != "rolled_back" {
		t.Fatalf("expected rollback by hash status=rolled_back, got %v", rollbackPayload["status"])
	}
	if rollbackPayload["fromConfigHash"] != hashB {
		t.Fatalf("expected rollback by hash fromConfigHash=%s, got %v", hashB, rollbackPayload["fromConfigHash"])
	}
	if rollbackPayload["toConfigHash"] != hashA {
		t.Fatalf("expected rollback by hash toConfigHash=%s, got %v", hashA, rollbackPayload["toConfigHash"])
	}

	configOnDiskAfterRollback, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config after hash rollback: %v", err)
	}
	if !strings.Contains(string(configOnDiskAfterRollback), "team-hash-a") {
		t.Fatalf("expected config after hash rollback to contain team-hash-a receiver")
	}
	if strings.Contains(string(configOnDiskAfterRollback), "team-hash-b") {
		t.Fatalf("expected config after hash rollback to exclude team-hash-b receiver")
	}

	rollbackToSameHashReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v2/config/rollback?configHash=%s", hashA),
		bytes.NewBufferString(`{}`),
	)
	rollbackToSameHashRec := httptest.NewRecorder()
	mux.ServeHTTP(rollbackToSameHashRec, rollbackToSameHashReq)
	if rollbackToSameHashRec.Code != http.StatusConflict {
		t.Fatalf("POST /api/v2/config/rollback to current hash expected 409, got %d", rollbackToSameHashRec.Code)
	}
}

func TestPhase0ConfigRevisionsExposeUniqueHashesAndCurrentRevision(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	postConfig := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
	}

	configA := `
route:
  receiver: "team-revisions-a"
receivers:
  - name: "team-revisions-a"
`
	configB := `
route:
  receiver: "team-revisions-b"
receivers:
  - name: "team-revisions-b"
`

	postConfig(configA)
	postConfig(configB)

	hashA := configSHA256(configA)
	hashB := configSHA256(configB)

	rollbackReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v2/config/rollback?configHash=%s", hashA),
		bytes.NewBufferString(`{}`),
	)
	rollbackRec := httptest.NewRecorder()
	mux.ServeHTTP(rollbackRec, rollbackReq)
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/config/rollback by hash expected 200, got %d", rollbackRec.Code)
	}

	revisionsReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/revisions?limit=10", nil)
	revisionsRec := httptest.NewRecorder()
	mux.ServeHTTP(revisionsRec, revisionsReq)
	if revisionsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/revisions expected 200, got %d", revisionsRec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(revisionsRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode config revisions response: %v", err)
	}

	currentHash, ok := payload["currentConfigHash"].(string)
	if !ok {
		t.Fatalf("config revisions expected currentConfigHash string, got %T", payload["currentConfigHash"])
	}
	if currentHash != hashA {
		t.Fatalf("config revisions expected currentConfigHash=%s, got %s", hashA, currentHash)
	}

	rawRevisions, ok := payload["revisions"].([]any)
	if !ok {
		t.Fatalf("config revisions expected revisions array, got %T", payload["revisions"])
	}
	if len(rawRevisions) < 2 {
		t.Fatalf("config revisions expected at least 2 entries, got %d", len(rawRevisions))
	}

	seen := make(map[string]int, len(rawRevisions))
	var currentCount int
	for _, raw := range rawRevisions {
		revision, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("config revisions entry expected object, got %T", raw)
		}
		hash, _ := revision["configHash"].(string)
		seen[hash]++
		isCurrent, _ := revision["isCurrent"].(bool)
		if isCurrent {
			currentCount++
			if hash != hashA {
				t.Fatalf("config revisions expected current hash=%s, got %s", hashA, hash)
			}
		}
	}

	if seen[hashA] != 1 {
		t.Fatalf("config revisions expected hashA to appear once, got %d", seen[hashA])
	}
	if seen[hashB] != 1 {
		t.Fatalf("config revisions expected hashB to appear once, got %d", seen[hashB])
	}
	if currentCount != 1 {
		t.Fatalf("config revisions expected exactly one current revision, got %d", currentCount)
	}
}

func TestPhase0ConfigRevisionsPruneRemovesOldTargets(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	postConfig := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
	}

	configA := `
route:
  receiver: "team-prune-sem-a"
receivers:
  - name: "team-prune-sem-a"
`
	configB := `
route:
  receiver: "team-prune-sem-b"
receivers:
  - name: "team-prune-sem-b"
`
	configC := `
route:
  receiver: "team-prune-sem-c"
receivers:
  - name: "team-prune-sem-c"
`

	postConfig(configA)
	postConfig(configB)
	postConfig(configC)

	hashA := configSHA256(configA)
	hashC := configSHA256(configC)

	pruneReq := httptest.NewRequest(http.MethodDelete, "/api/v2/config/revisions/prune?keep=1", nil)
	pruneRec := httptest.NewRecorder()
	mux.ServeHTTP(pruneRec, pruneReq)
	if pruneRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/v2/config/revisions/prune expected 200, got %d", pruneRec.Code)
	}

	var prunePayload map[string]any
	if err := json.Unmarshal(pruneRec.Body.Bytes(), &prunePayload); err != nil {
		t.Fatalf("failed to decode prune response: %v", err)
	}
	if prunePayload["status"] != "pruned" {
		t.Fatalf("expected prune status=pruned, got %v", prunePayload["status"])
	}

	revisionsReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/revisions?limit=10", nil)
	revisionsRec := httptest.NewRecorder()
	mux.ServeHTTP(revisionsRec, revisionsReq)
	if revisionsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/revisions expected 200, got %d", revisionsRec.Code)
	}

	var revisionsPayload map[string]any
	if err := json.Unmarshal(revisionsRec.Body.Bytes(), &revisionsPayload); err != nil {
		t.Fatalf("failed to decode revisions response: %v", err)
	}
	rawRevisions, ok := revisionsPayload["revisions"].([]any)
	if !ok {
		t.Fatalf("config revisions expected revisions array, got %T", revisionsPayload["revisions"])
	}
	if len(rawRevisions) != 1 {
		t.Fatalf("config revisions after prune expected exactly 1 entry, got %d", len(rawRevisions))
	}

	revision, ok := rawRevisions[0].(map[string]any)
	if !ok {
		t.Fatalf("config revisions entry expected object, got %T", rawRevisions[0])
	}
	hash, _ := revision["configHash"].(string)
	if hash != hashC {
		t.Fatalf("config revisions after prune expected current hash %s, got %s", hashC, hash)
	}
	if isCurrent, _ := revision["isCurrent"].(bool); !isCurrent {
		t.Fatalf("config revisions after prune expected remaining revision to be current")
	}

	rollbackPrunedReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v2/config/rollback?configHash=%s", hashA),
		bytes.NewBufferString(`{}`),
	)
	rollbackPrunedRec := httptest.NewRecorder()
	mux.ServeHTTP(rollbackPrunedRec, rollbackPrunedReq)
	if rollbackPrunedRec.Code != http.StatusNotFound {
		t.Fatalf("rollback to pruned hash expected 404, got %d", rollbackPrunedRec.Code)
	}
}

func TestPhase0ConfigRollbackDryRunDoesNotMutate(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	postConfig := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
	}

	configA := `
route:
  receiver: "team-dryrun-a"
receivers:
  - name: "team-dryrun-a"
`
	configB := `
route:
  receiver: "team-dryrun-b"
receivers:
  - name: "team-dryrun-b"
`
	postConfig(configA)
	postConfig(configB)

	hashA := configSHA256(configA)

	beforeStatusReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/status", nil)
	beforeStatusRec := httptest.NewRecorder()
	mux.ServeHTTP(beforeStatusRec, beforeStatusReq)
	if beforeStatusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/status before dry-run expected 200, got %d", beforeStatusRec.Code)
	}

	var beforeStatus map[string]any
	if err := json.Unmarshal(beforeStatusRec.Body.Bytes(), &beforeStatus); err != nil {
		t.Fatalf("failed to decode status before dry-run: %v", err)
	}
	beforeSource, _ := beforeStatus["source"].(string)

	dryRunReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v2/config/rollback?configHash=%s&dryRun=true", hashA),
		bytes.NewBufferString(`{}`),
	)
	dryRunRec := httptest.NewRecorder()
	mux.ServeHTTP(dryRunRec, dryRunReq)
	if dryRunRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/config/rollback dry-run expected 200, got %d", dryRunRec.Code)
	}

	configOnDiskAfterDryRun, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config after rollback dry-run: %v", err)
	}
	if !strings.Contains(string(configOnDiskAfterDryRun), "team-dryrun-b") {
		t.Fatalf("expected config after rollback dry-run to keep team-dryrun-b receiver")
	}
	if strings.Contains(string(configOnDiskAfterDryRun), "team-dryrun-a") {
		t.Fatalf("expected config after rollback dry-run to not switch to team-dryrun-a receiver")
	}

	afterStatusReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/status", nil)
	afterStatusRec := httptest.NewRecorder()
	mux.ServeHTTP(afterStatusRec, afterStatusReq)
	if afterStatusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/status after dry-run expected 200, got %d", afterStatusRec.Code)
	}

	var afterStatus map[string]any
	if err := json.Unmarshal(afterStatusRec.Body.Bytes(), &afterStatus); err != nil {
		t.Fatalf("failed to decode status after dry-run: %v", err)
	}
	afterSource, _ := afterStatus["source"].(string)
	if afterSource != beforeSource {
		t.Fatalf("expected config status source to remain %q after dry-run, got %q", beforeSource, afterSource)
	}
}

func TestPhase0ConfigRevisionsPruneDryRunDoesNotMutate(t *testing.T) {
	configPath := writeTestConfigFile(t, validConfigPayload)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	postConfig := func(payload string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/config", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/config expected 200, got %d", rec.Code)
		}
	}

	configA := `
route:
  receiver: "team-prune-dryrun-a"
receivers:
  - name: "team-prune-dryrun-a"
`
	configB := `
route:
  receiver: "team-prune-dryrun-b"
receivers:
  - name: "team-prune-dryrun-b"
`
	configC := `
route:
  receiver: "team-prune-dryrun-c"
receivers:
  - name: "team-prune-dryrun-c"
`
	postConfig(configA)
	postConfig(configB)
	postConfig(configC)

	hashA := configSHA256(configA)

	beforeRevisionsReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/revisions?limit=20", nil)
	beforeRevisionsRec := httptest.NewRecorder()
	mux.ServeHTTP(beforeRevisionsRec, beforeRevisionsReq)
	if beforeRevisionsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/revisions before prune dry-run expected 200, got %d", beforeRevisionsRec.Code)
	}

	var beforePayload map[string]any
	if err := json.Unmarshal(beforeRevisionsRec.Body.Bytes(), &beforePayload); err != nil {
		t.Fatalf("failed to decode revisions before prune dry-run: %v", err)
	}
	beforeTotal, _ := beforePayload["total"].(float64)

	dryRunPruneReq := httptest.NewRequest(http.MethodDelete, "/api/v2/config/revisions/prune?keep=1&dryRun=true", nil)
	dryRunPruneRec := httptest.NewRecorder()
	mux.ServeHTTP(dryRunPruneRec, dryRunPruneReq)
	if dryRunPruneRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/v2/config/revisions/prune dry-run expected 200, got %d", dryRunPruneRec.Code)
	}

	var dryRunPayload map[string]any
	if err := json.Unmarshal(dryRunPruneRec.Body.Bytes(), &dryRunPayload); err != nil {
		t.Fatalf("failed to decode prune dry-run response: %v", err)
	}
	if dryRunPayload["status"] != "dry_run" {
		t.Fatalf("expected prune dry-run status=dry_run, got %v", dryRunPayload["status"])
	}

	afterRevisionsReq := httptest.NewRequest(http.MethodGet, "/api/v2/config/revisions?limit=20", nil)
	afterRevisionsRec := httptest.NewRecorder()
	mux.ServeHTTP(afterRevisionsRec, afterRevisionsReq)
	if afterRevisionsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/config/revisions after prune dry-run expected 200, got %d", afterRevisionsRec.Code)
	}

	var afterPayload map[string]any
	if err := json.Unmarshal(afterRevisionsRec.Body.Bytes(), &afterPayload); err != nil {
		t.Fatalf("failed to decode revisions after prune dry-run: %v", err)
	}
	afterTotal, _ := afterPayload["total"].(float64)
	if afterTotal != beforeTotal {
		t.Fatalf("expected revisions total unchanged after dry-run, before=%v after=%v", beforeTotal, afterTotal)
	}

	rollbackToOldReq := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v2/config/rollback?configHash=%s", hashA),
		bytes.NewBufferString(`{}`),
	)
	rollbackToOldRec := httptest.NewRecorder()
	mux.ServeHTTP(rollbackToOldRec, rollbackToOldReq)
	if rollbackToOldRec.Code != http.StatusOK {
		t.Fatalf("expected rollback to old hash after prune dry-run to still work, got %d", rollbackToOldRec.Code)
	}
}

func TestPhase0ReloadInvalidConfigReturns500(t *testing.T) {
	configPath := writeTestConfigFile(t, "route: [\n")
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	reloadReq := httptest.NewRequest(http.MethodPost, "/-/reload", bytes.NewBufferString(`{}`))
	reloadRec := httptest.NewRecorder()
	mux.ServeHTTP(reloadRec, reloadReq)

	if reloadRec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /-/reload with invalid config expected 500, got %d", reloadRec.Code)
	}
	if !strings.Contains(reloadRec.Body.String(), "failed to reload config") {
		t.Fatalf("reload error response expected failure prefix, got %q", reloadRec.Body.String())
	}
}

func TestPhase0AlertGroupsAndReceiversSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"HighCPU","service":"api","namespace":"prod","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"HighCPU","service":"api","namespace":"prod","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"HighMemory","service":"worker","namespace":"prod"},
			"startsAt": "2026-02-25T00:02:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups", nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", groupsRec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	for _, group := range groups {
		receiver, ok := group["receiver"].(map[string]any)
		if !ok {
			t.Fatalf("group receiver expected object, got %T", group["receiver"])
		}
		name, ok := receiver["name"].(string)
		if !ok || name == "" {
			t.Fatalf("group receiver.name expected non-empty string, got %v", receiver["name"])
		}
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?receiver=^team-ops$", nil)
	filteredRec := httptest.NewRecorder()
	mux.ServeHTTP(filteredRec, filteredReq)
	if filteredRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups with receiver filter expected 200, got %d", filteredRec.Code)
	}

	var filteredGroups []map[string]any
	if err := json.Unmarshal(filteredRec.Body.Bytes(), &filteredGroups); err != nil {
		t.Fatalf("failed to decode filtered groups response: %v", err)
	}
	if len(filteredGroups) != 1 {
		t.Fatalf("expected 1 filtered group for team-ops receiver, got %d", len(filteredGroups))
	}
	filteredReceiver, ok := filteredGroups[0]["receiver"].(map[string]any)
	if !ok || filteredReceiver["name"] != "team-ops" {
		t.Fatalf("expected filtered group receiver.name=team-ops, got %v", filteredGroups[0]["receiver"])
	}

	filterQuery := url.Values{}
	filterQuery.Add("filter", `alertname="HighCPU"`)
	filterReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+filterQuery.Encode(), nil)
	filterRec := httptest.NewRecorder()
	mux.ServeHTTP(filterRec, filterReq)
	if filterRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups with label filter expected 200, got %d", filterRec.Code)
	}

	var labelFilteredGroups []map[string]any
	if err := json.Unmarshal(filterRec.Body.Bytes(), &labelFilteredGroups); err != nil {
		t.Fatalf("failed to decode label-filtered groups response: %v", err)
	}
	if len(labelFilteredGroups) != 1 {
		t.Fatalf("expected 1 group for alertname=HighCPU filter, got %d", len(labelFilteredGroups))
	}

	receiversReq := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	receiversRec := httptest.NewRecorder()
	mux.ServeHTTP(receiversRec, receiversReq)
	if receiversRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/receivers expected 200, got %d", receiversRec.Code)
	}

	var receivers []map[string]any
	if err := json.Unmarshal(receiversRec.Body.Bytes(), &receivers); err != nil {
		t.Fatalf("failed to decode receivers response: %v", err)
	}
	if len(receivers) != 1 {
		t.Fatalf("expected exactly one configured receiver from runtime config, got %d", len(receivers))
	}
	if receivers[0]["name"] != "default" {
		t.Fatalf("expected configured receiver default, got %v", receivers[0]["name"])
	}
}

func TestPhase0ReceiversIncludeConfiguredNames(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
  routes:
    - receiver: "team-db"
      routes:
        - receiver: "team-nested"
receivers:
  - name: "team-default"
  - name: "team-email"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	receiversReq := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	receiversRec := httptest.NewRecorder()
	mux.ServeHTTP(receiversRec, receiversReq)
	if receiversRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/receivers expected 200, got %d", receiversRec.Code)
	}

	var receivers []map[string]any
	if err := json.Unmarshal(receiversRec.Body.Bytes(), &receivers); err != nil {
		t.Fatalf("failed to decode receivers response: %v", err)
	}

	receiverNames := make([]string, 0, len(receivers))
	receiverSet := make(map[string]struct{}, len(receivers))
	for _, receiver := range receivers {
		name, ok := receiver["name"].(string)
		if !ok {
			t.Fatalf("receiver.name expected string, got %T", receiver["name"])
		}
		receiverNames = append(receiverNames, name)
		receiverSet[name] = struct{}{}
	}

	required := []string{"team-default", "team-email"}
	for _, name := range required {
		if _, ok := receiverSet[name]; !ok {
			t.Fatalf("expected configured receiver %q in /api/v2/receivers, got %v", name, receiverNames)
		}
	}
	for _, excluded := range []string{"default", "team-db", "team-nested"} {
		if _, ok := receiverSet[excluded]; ok {
			t.Fatalf("did not expect non-receiver-list value %q in /api/v2/receivers, got %v", excluded, receiverNames)
		}
	}
}

func TestPhase0AlertGroupsNestedAlertShape(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"NestedShape","service":"api","namespace":"prod","receiver":"team-ops"},
			"annotations": {"summary":"nested check"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups", nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", groupsRec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected exactly one group, got %d", len(groups))
	}

	alerts, ok := groups[0]["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("group alerts expected one element, got %v", groups[0]["alerts"])
	}
	alert, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("nested alert expected object, got %T", alerts[0])
	}

	for _, field := range []string{"annotations", "receivers", "startsAt", "updatedAt", "endsAt", "fingerprint", "status"} {
		if _, ok := alert[field]; !ok {
			t.Fatalf("nested alert missing required field %q", field)
		}
	}

	status, ok := alert["status"].(map[string]any)
	if !ok {
		t.Fatalf("nested alert status expected object, got %T", alert["status"])
	}
	if status["state"] != "active" {
		t.Fatalf("nested alert status.state expected active, got %v", status["state"])
	}
}

func TestPhase0AlertGroupsStateFlagSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"GroupFiring","service":"api","namespace":"prod","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"GroupResolved","service":"api","namespace":"prod","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:01:00Z",
			"endsAt": "2026-02-25T00:05:00Z",
			"status": "resolved"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	noneReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=false&inhibited=false",
		nil,
	)
	noneRec := httptest.NewRecorder()
	mux.ServeHTTP(noneRec, noneReq)
	if noneRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups with active/silenced/inhibited false expected 200, got %d", noneRec.Code)
	}

	var noneGroups []map[string]any
	if err := json.Unmarshal(noneRec.Body.Bytes(), &noneGroups); err != nil {
		t.Fatalf("failed to decode all-false groups response: %v", err)
	}
	if len(noneGroups) != 0 {
		t.Fatalf("expected no groups when active/silenced/inhibited are false, got %d", len(noneGroups))
	}

	resolvedReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?resolved=true&active=false&silenced=false&inhibited=false&muted=false",
		nil,
	)
	resolvedRec := httptest.NewRecorder()
	mux.ServeHTTP(resolvedRec, resolvedReq)
	if resolvedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups resolved with state flags expected 200, got %d", resolvedRec.Code)
	}

	var resolvedGroups []map[string]any
	if err := json.Unmarshal(resolvedRec.Body.Bytes(), &resolvedGroups); err != nil {
		t.Fatalf("failed to decode resolved groups response: %v", err)
	}
	if len(resolvedGroups) != 1 {
		t.Fatalf("expected exactly one resolved-only group, got %d", len(resolvedGroups))
	}

	alerts, ok := resolvedGroups[0]["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("resolved group expected one alert, got %v", resolvedGroups[0]["alerts"])
	}
	alert, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("resolved group alert expected object, got %T", alerts[0])
	}
	alertStatus, ok := alert["status"].(map[string]any)
	if !ok || alertStatus["state"] != "unprocessed" {
		t.Fatalf("resolved group alert expected status.state=unprocessed, got %v", alert["status"])
	}
}

func TestPhase0AlertGroupsSilencedAndMutedSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"SilencedGroup","service":"api","namespace":"prod","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	silenceReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/silences",
		bytes.NewBufferString(activeSilencePayloadForAlert(time.Now().UTC(), "SilencedGroup")),
	)
	silenceRec := httptest.NewRecorder()
	mux.ServeHTTP(silenceRec, silenceReq)
	if silenceRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", silenceRec.Code)
	}

	silencedGroupsReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=true&inhibited=false&muted=true",
		nil,
	)
	silencedGroupsRec := httptest.NewRecorder()
	mux.ServeHTTP(silencedGroupsRec, silencedGroupsReq)
	if silencedGroupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups silenced-only expected 200, got %d", silencedGroupsRec.Code)
	}

	var silencedGroups []map[string]any
	if err := json.Unmarshal(silencedGroupsRec.Body.Bytes(), &silencedGroups); err != nil {
		t.Fatalf("failed to decode silenced groups response: %v", err)
	}
	if len(silencedGroups) != 1 {
		t.Fatalf("expected one silenced group, got %d", len(silencedGroups))
	}

	notMutedGroupsReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=true&inhibited=false&muted=false",
		nil,
	)
	notMutedGroupsRec := httptest.NewRecorder()
	mux.ServeHTTP(notMutedGroupsRec, notMutedGroupsReq)
	if notMutedGroupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups silenced-only muted=false expected 200, got %d", notMutedGroupsRec.Code)
	}

	var notMutedGroups []map[string]any
	if err := json.Unmarshal(notMutedGroupsRec.Body.Bytes(), &notMutedGroups); err != nil {
		t.Fatalf("failed to decode not-muted groups response: %v", err)
	}
	if len(notMutedGroups) != 0 {
		t.Fatalf("expected no groups when muted=false and only muted group exists, got %d", len(notMutedGroups))
	}
}

func TestPhase0AlertGroupsInhibitedAndMutedSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"InhibitedGroup","service":"api","namespace":"prod","receiver":"team-ops"},
			"annotations": {"inhibitedBy":"rule-a"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedGroupsReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=false&inhibited=true&muted=true",
		nil,
	)
	inhibitedGroupsRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedGroupsRec, inhibitedGroupsReq)
	if inhibitedGroupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups inhibited-only expected 200, got %d", inhibitedGroupsRec.Code)
	}

	var inhibitedGroups []map[string]any
	if err := json.Unmarshal(inhibitedGroupsRec.Body.Bytes(), &inhibitedGroups); err != nil {
		t.Fatalf("failed to decode inhibited groups response: %v", err)
	}
	if len(inhibitedGroups) != 1 {
		t.Fatalf("expected one inhibited group, got %d", len(inhibitedGroups))
	}

	alerts, ok := inhibitedGroups[0]["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("inhibited group expected one alert, got %v", inhibitedGroups[0]["alerts"])
	}
	alert, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("inhibited group alert expected object, got %T", alerts[0])
	}
	status, ok := alert["status"].(map[string]any)
	if !ok {
		t.Fatalf("inhibited group alert status expected object, got %T", alert["status"])
	}
	inhibitedBy, ok := status["inhibitedBy"].([]any)
	if !ok || len(inhibitedBy) != 1 {
		t.Fatalf("expected inhibited group alert to have inhibitedBy entry, got %v", status["inhibitedBy"])
	}

	notMutedReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=false&inhibited=true&muted=false",
		nil,
	)
	notMutedRec := httptest.NewRecorder()
	mux.ServeHTTP(notMutedRec, notMutedReq)
	if notMutedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups inhibited-only muted=false expected 200, got %d", notMutedRec.Code)
	}

	var notMutedGroups []map[string]any
	if err := json.Unmarshal(notMutedRec.Body.Bytes(), &notMutedGroups); err != nil {
		t.Fatalf("failed to decode inhibited muted=false groups response: %v", err)
	}
	if len(notMutedGroups) != 0 {
		t.Fatalf("expected no groups when muted=false and only inhibited group exists, got %d", len(notMutedGroups))
	}
}

func TestPhase0AlertGroupsInhibitedByRulesAndMutedSemantics(t *testing.T) {
	configPath := writeTestConfigFile(t, `
inhibit_rules:
  - name: "critical-inhibits-warning-same-service"
    source_match:
      severity: "critical"
    target_match:
      severity: "warning"
    equal:
      - service
      - namespace
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"RootCause","service":"api","namespace":"prod","severity":"critical","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"Symptom","service":"api","namespace":"prod","severity":"warning","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedGroupsReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=false&inhibited=true&muted=true",
		nil,
	)
	inhibitedGroupsRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedGroupsRec, inhibitedGroupsReq)
	if inhibitedGroupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups inhibited-only expected 200, got %d", inhibitedGroupsRec.Code)
	}

	var inhibitedGroups []map[string]any
	if err := json.Unmarshal(inhibitedGroupsRec.Body.Bytes(), &inhibitedGroups); err != nil {
		t.Fatalf("failed to decode inhibited groups response: %v", err)
	}
	if len(inhibitedGroups) != 1 {
		t.Fatalf("expected one inhibited group by rules, got %d", len(inhibitedGroups))
	}

	alerts, ok := inhibitedGroups[0]["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("inhibited group expected one alert, got %v", inhibitedGroups[0]["alerts"])
	}
	alert, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("inhibited group alert expected object, got %T", alerts[0])
	}
	labels, ok := alert["labels"].(map[string]any)
	if !ok || labels["alertname"] != "Symptom" {
		t.Fatalf("expected Symptom alert in inhibited group, got %v", alert["labels"])
	}

	notMutedReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=false&inhibited=true&muted=false",
		nil,
	)
	notMutedRec := httptest.NewRecorder()
	mux.ServeHTTP(notMutedRec, notMutedReq)
	if notMutedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups inhibited-only muted=false expected 200, got %d", notMutedRec.Code)
	}

	var notMutedGroups []map[string]any
	if err := json.Unmarshal(notMutedRec.Body.Bytes(), &notMutedGroups); err != nil {
		t.Fatalf("failed to decode inhibited muted=false groups response: %v", err)
	}
	if len(notMutedGroups) != 0 {
		t.Fatalf("expected no groups when muted=false and only inhibited-by-rule group exists, got %d", len(notMutedGroups))
	}
}

func TestPhase0AlertGroupsInhibitedByRulesRegexAndEqualSemantics(t *testing.T) {
	configPath := writeTestConfigFile(t, `
inhibit_rules:
  - name: "regex-critical-inhibits-warning-same-cluster"
    source_match_re:
      alertname: "^Root.*"
      severity: "critical|high"
    target_match_re:
      alertname: "^Symptom.*"
      severity: "warning|info"
    equal:
      - cluster
      - namespace
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"RootNodeDown","service":"api","namespace":"prod","severity":"critical","cluster":"prod-a","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"SymptomApiDown","service":"api","namespace":"prod","severity":"warning","cluster":"prod-a","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"SymptomWorkerDown","service":"api","namespace":"prod","severity":"warning","cluster":"prod-b","receiver":"team-ops"},
			"startsAt": "2026-02-25T00:02:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	inhibitedGroupsReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=false&silenced=false&inhibited=true&muted=true",
		nil,
	)
	inhibitedGroupsRec := httptest.NewRecorder()
	mux.ServeHTTP(inhibitedGroupsRec, inhibitedGroupsReq)
	if inhibitedGroupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups inhibited-only expected 200, got %d", inhibitedGroupsRec.Code)
	}

	var inhibitedGroups []map[string]any
	if err := json.Unmarshal(inhibitedGroupsRec.Body.Bytes(), &inhibitedGroups); err != nil {
		t.Fatalf("failed to decode inhibited groups response: %v", err)
	}
	if len(inhibitedGroups) != 1 {
		t.Fatalf("expected one inhibited group for regex+equal rule, got %d", len(inhibitedGroups))
	}
	alerts, ok := inhibitedGroups[0]["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("inhibited group expected one alert, got %v", inhibitedGroups[0]["alerts"])
	}
	alert, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("inhibited group alert expected object, got %T", alerts[0])
	}
	labels, ok := alert["labels"].(map[string]any)
	if !ok || labels["alertname"] != "SymptomApiDown" {
		t.Fatalf("expected SymptomApiDown in inhibited group, got %v", alert["labels"])
	}

	activeGroupsReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/alerts/groups?active=true&silenced=false&inhibited=false&muted=true",
		nil,
	)
	activeGroupsRec := httptest.NewRecorder()
	mux.ServeHTTP(activeGroupsRec, activeGroupsReq)
	if activeGroupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups active-only expected 200, got %d", activeGroupsRec.Code)
	}
	var activeGroups []map[string]any
	if err := json.Unmarshal(activeGroupsRec.Body.Bytes(), &activeGroups); err != nil {
		t.Fatalf("failed to decode active groups response: %v", err)
	}
	if len(activeGroups) != 2 {
		t.Fatalf("expected 2 active groups (source + equal-mismatch target), got %d", len(activeGroups))
	}
}

func TestPhase0SilenceAffectsAlertIngest(t *testing.T) {
	mux := newPhase0TestMux(t)

	now := time.Now().UTC()
	activeSilencePayload := fmt.Sprintf(`{
		"matchers": [{"name":"alertname","value":"TestAlert","isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "phase0-test",
		"comment": "suppress test alert"
	}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(1*time.Hour).Format(time.RFC3339))

	silenceReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(activeSilencePayload))
	silenceRec := httptest.NewRecorder()
	mux.ServeHTTP(silenceRec, silenceReq)
	if silenceRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", silenceRec.Code)
	}

	suppressedAlertReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(validAlertPayload))
	suppressedAlertRec := httptest.NewRecorder()
	mux.ServeHTTP(suppressedAlertRec, suppressedAlertReq)
	if suppressedAlertRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", suppressedAlertRec.Code)
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("expected silenced alert to be suppressed, got %d alerts", len(alerts))
	}

	unsilencedPayload := `[
		{
			"labels": {"alertname":"OtherAlert","service":"amp"},
			"startsAt": "2026-02-25T00:10:00Z",
			"status": "firing"
		}
	]`
	unsilencedReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(unsilencedPayload))
	unsilencedRec := httptest.NewRecorder()
	mux.ServeHTTP(unsilencedRec, unsilencedReq)
	if unsilencedRec.Code != http.StatusOK {
		t.Fatalf("POST unsilenced alert expected 200, got %d", unsilencedRec.Code)
	}

	alertsAfterReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	alertsAfterRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsAfterRec, alertsAfterReq)
	if alertsAfterRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsAfterRec.Code)
	}

	var alertsAfter []map[string]any
	if err := json.Unmarshal(alertsAfterRec.Body.Bytes(), &alertsAfter); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alertsAfter) != 1 {
		t.Fatalf("expected only unsilenced alert to be stored, got %d", len(alertsAfter))
	}
}

func TestPhase0SilencesStateSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(validSilencePayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", postRec.Code)
	}

	var postPayload map[string]any
	if err := json.Unmarshal(postRec.Body.Bytes(), &postPayload); err != nil {
		t.Fatalf("failed to decode silence post response: %v", err)
	}
	silenceID, _ := postPayload["silenceID"].(string)
	if silenceID == "" {
		t.Fatalf("expected non-empty silenceID")
	}

	getByIDReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/"+silenceID, nil)
	getByIDRec := httptest.NewRecorder()
	mux.ServeHTTP(getByIDRec, getByIDReq)
	if getByIDRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silence/{id} expected 200, got %d", getByIDRec.Code)
	}

	var silence map[string]any
	if err := json.Unmarshal(getByIDRec.Body.Bytes(), &silence); err != nil {
		t.Fatalf("failed to decode silence by id response: %v", err)
	}
	if gotID, _ := silence["id"].(string); gotID != silenceID {
		t.Fatalf("expected silence id %q, got %q", silenceID, gotID)
	}
	matchers, ok := silence["matchers"].([]any)
	if !ok || len(matchers) == 0 {
		t.Fatalf("expected non-empty matchers array in silence response")
	}
	firstMatcher, ok := matchers[0].(map[string]any)
	if !ok {
		t.Fatalf("expected matcher object, got %T", matchers[0])
	}
	if _, ok := firstMatcher["isRegex"]; !ok {
		t.Fatalf("expected matcher.isRegex to be present even for false value")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences expected 200, got %d", listRec.Code)
	}

	var silences []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &silences); err != nil {
		t.Fatalf("failed to decode silences list: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected 1 silence in list, got %d", len(silences))
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+silenceID, nil)
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/v2/silence/{id} expected 200, got %d", deleteRec.Code)
	}
	if deleteRec.Body.Len() != 0 {
		t.Fatalf("DELETE /api/v2/silence/{id} expected empty body, got %q", deleteRec.Body.String())
	}

	getAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/"+silenceID, nil)
	getAfterDeleteRec := httptest.NewRecorder()
	mux.ServeHTTP(getAfterDeleteRec, getAfterDeleteReq)
	if getAfterDeleteRec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v2/silence/{id} after delete expected 404, got %d", getAfterDeleteRec.Code)
	}
}

func TestPhase0SilencePostUpdateSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(validSilencePayload))
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences create expected 200, got %d", createRec.Code)
	}

	var createPayload map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}
	silenceID, _ := createPayload["silenceID"].(string)
	if silenceID == "" {
		t.Fatalf("expected non-empty silenceID")
	}

	now := time.Now().UTC()
	updatePayload := fmt.Sprintf(`{
		"id": %q,
		"matchers": [{"name":"alertname","value":"TestAlert","isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "phase0-test",
		"comment": "maintenance window updated"
	}`, silenceID, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(59*time.Minute).Format(time.RFC3339))

	updateReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(updatePayload))
	updateRec := httptest.NewRecorder()
	mux.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences update expected 200, got %d", updateRec.Code)
	}

	var updateResp map[string]any
	if err := json.Unmarshal(updateRec.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("failed to decode update response: %v", err)
	}
	if gotID, _ := updateResp["silenceID"].(string); gotID != silenceID {
		t.Fatalf("expected updated silenceID %q, got %q", silenceID, gotID)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/"+silenceID, nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silence/{id} after update expected 200, got %d", getRec.Code)
	}

	var updatedSilence map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &updatedSilence); err != nil {
		t.Fatalf("failed to decode silence after update: %v", err)
	}
	if comment, _ := updatedSilence["comment"].(string); comment != "maintenance window updated" {
		t.Fatalf("expected updated comment, got %q", comment)
	}

	unknownUpdatePayload := fmt.Sprintf(`{
		"id": %q,
		"matchers": [{"name":"alertname","value":"UnknownAlert","isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "phase0-test",
		"comment": "unknown id update"
	}`, "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(59*time.Minute).Format(time.RFC3339))

	unknownReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(unknownUpdatePayload))
	unknownRec := httptest.NewRecorder()
	mux.ServeHTTP(unknownRec, unknownReq)
	if unknownRec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v2/silences update for unknown id expected 404, got %d", unknownRec.Code)
	}
}

func TestPhase0SilencesFilterMatcherSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)

	posts := []string{
		`{
			"matchers": [{"name":"service","value":"api","isRegex":false}],
			"startsAt": "2099-01-01T00:00:00Z",
			"endsAt": "2099-01-01T01:00:00Z",
			"createdBy": "phase0-test",
			"comment": "silence-service-api"
		}`,
		`{
			"matchers": [{"name":"alertname","value":"^High.*","isRegex":true}],
			"startsAt": "2099-01-01T00:01:00Z",
			"endsAt": "2099-01-01T01:01:00Z",
			"createdBy": "phase0-test",
			"comment": "silence-alertname-regex"
		}`,
		`{
			"matchers": [{"name":"service","value":"api","isRegex":false,"isEqual":false}],
			"startsAt": "2099-01-01T00:02:00Z",
			"endsAt": "2099-01-01T01:02:00Z",
			"createdBy": "phase0-test",
			"comment": "silence-service-not-api"
		}`,
		`{
			"matchers": [
				{"name":"service","value":"api","isRegex":false},
				{"name":"alertname","value":"^High.*","isRegex":true}
			],
			"startsAt": "2099-01-01T00:03:00Z",
			"endsAt": "2099-01-01T01:03:00Z",
			"createdBy": "phase0-test",
			"comment": "silence-service-api-and-regex"
		}`,
	}

	for i, payload := range posts {
		postReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		postRec := httptest.NewRecorder()
		mux.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/silences #%d expected 200, got %d", i, postRec.Code)
		}
	}

	queryService := url.Values{}
	queryService.Add("filter", `service="api"`)
	serviceReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences?"+queryService.Encode(), nil)
	serviceRec := httptest.NewRecorder()
	mux.ServeHTTP(serviceRec, serviceReq)
	if serviceRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences with service filter expected 200, got %d", serviceRec.Code)
	}
	var serviceSilences []map[string]any
	if err := json.Unmarshal(serviceRec.Body.Bytes(), &serviceSilences); err != nil {
		t.Fatalf("failed to decode service-filter silences: %v", err)
	}
	if len(serviceSilences) != 2 {
		t.Fatalf("expected 2 silences for service=api, got %d", len(serviceSilences))
	}

	queryRegex := url.Values{}
	queryRegex.Add("filter", `alertname=~"^High.*"`)
	regexReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences?"+queryRegex.Encode(), nil)
	regexRec := httptest.NewRecorder()
	mux.ServeHTTP(regexRec, regexReq)
	if regexRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences with regex filter expected 200, got %d", regexRec.Code)
	}
	var regexSilences []map[string]any
	if err := json.Unmarshal(regexRec.Body.Bytes(), &regexSilences); err != nil {
		t.Fatalf("failed to decode regex-filter silences: %v", err)
	}
	if len(regexSilences) != 2 {
		t.Fatalf("expected 2 silences for alertname=~^High.*, got %d", len(regexSilences))
	}

	queryNotEqual := url.Values{}
	queryNotEqual.Add("filter", `service!="api"`)
	notEqualReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences?"+queryNotEqual.Encode(), nil)
	notEqualRec := httptest.NewRecorder()
	mux.ServeHTTP(notEqualRec, notEqualReq)
	if notEqualRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences with not-equal filter expected 200, got %d", notEqualRec.Code)
	}
	var notEqualSilences []map[string]any
	if err := json.Unmarshal(notEqualRec.Body.Bytes(), &notEqualSilences); err != nil {
		t.Fatalf("failed to decode not-equal-filter silences: %v", err)
	}
	if len(notEqualSilences) != 1 {
		t.Fatalf("expected 1 silence for service!=api, got %d", len(notEqualSilences))
	}

	queryMulti := url.Values{}
	queryMulti.Add("filter", `service="api"`)
	queryMulti.Add("filter", `alertname=~"^High.*"`)
	multiReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences?"+queryMulti.Encode(), nil)
	multiRec := httptest.NewRecorder()
	mux.ServeHTTP(multiRec, multiReq)
	if multiRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences with multi-filter expected 200, got %d", multiRec.Code)
	}
	var multiSilences []map[string]any
	if err := json.Unmarshal(multiRec.Body.Bytes(), &multiSilences); err != nil {
		t.Fatalf("failed to decode multi-filter silences: %v", err)
	}
	if len(multiSilences) != 1 {
		t.Fatalf("expected 1 silence for service=api AND alertname=~^High.*, got %d", len(multiSilences))
	}
	comment, _ := multiSilences[0]["comment"].(string)
	if comment != "silence-service-api-and-regex" {
		t.Fatalf("unexpected silence matched by multi-filter: %q", comment)
	}
}

func TestPhase0SilencesListOrderSemantics(t *testing.T) {
	mux := newPhase0TestMux(t)
	now := time.Now().UTC()

	payloads := []string{
		fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"PendingOrder","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "phase0-test",
			"comment": "pending-order"
		}`, now.Add(20*time.Minute).Format(time.RFC3339), now.Add(40*time.Minute).Format(time.RFC3339)),
		fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"ActiveLateOrder","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "phase0-test",
			"comment": "active-late-order"
		}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(50*time.Minute).Format(time.RFC3339)),
		fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"ActiveSoonOrder","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "phase0-test",
			"comment": "active-soon-order"
		}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339)),
	}

	for i, payload := range payloads {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/silences order payload #%d expected 200, got %d", i, rec.Code)
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences expected 200, got %d", listRec.Code)
	}

	var silences []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &silences); err != nil {
		t.Fatalf("failed to decode silences list: %v", err)
	}
	if len(silences) != 3 {
		t.Fatalf("expected 3 silences, got %d", len(silences))
	}

	comments := make([]string, 0, len(silences))
	for _, silence := range silences {
		comment, _ := silence["comment"].(string)
		comments = append(comments, comment)
	}

	expected := []string{"active-soon-order", "active-late-order", "pending-order"}
	for i := range expected {
		if comments[i] != expected[i] {
			t.Fatalf("unexpected silences order at index %d: got %q, want %q (full=%v)", i, comments[i], expected[i], comments)
		}
	}
}

func TestPhase0RuntimeStatePersistsAcrossRestart(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "runtime-state.json")
	mux1 := newPhase0TestMuxWithStateFile(t, stateFile)

	alertPost := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(validAlertPayload))
	alertPostRec := httptest.NewRecorder()
	mux1.ServeHTTP(alertPostRec, alertPost)
	if alertPostRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", alertPostRec.Code)
	}

	silencePost := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(validSilencePayload))
	silencePostRec := httptest.NewRecorder()
	mux1.ServeHTTP(silencePostRec, silencePost)
	if silencePostRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", silencePostRec.Code)
	}

	var silencePostPayload map[string]any
	if err := json.Unmarshal(silencePostRec.Body.Bytes(), &silencePostPayload); err != nil {
		t.Fatalf("failed to decode silence create response: %v", err)
	}
	silenceID, _ := silencePostPayload["silenceID"].(string)
	if silenceID == "" {
		t.Fatalf("expected non-empty silenceID")
	}

	// Simulate restart by creating a new mux with the same state file.
	mux2 := newPhase0TestMuxWithStateFile(t, stateFile)

	alertsGet := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	alertsGetRec := httptest.NewRecorder()
	mux2.ServeHTTP(alertsGetRec, alertsGet)
	if alertsGetRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsGetRec.Code)
	}
	var alerts []map[string]any
	if err := json.Unmarshal(alertsGetRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode restored alerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 restored alert, got %d", len(alerts))
	}

	silencesGet := httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil)
	silencesGetRec := httptest.NewRecorder()
	mux2.ServeHTTP(silencesGetRec, silencesGet)
	if silencesGetRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences expected 200, got %d", silencesGetRec.Code)
	}
	var silences []map[string]any
	if err := json.Unmarshal(silencesGetRec.Body.Bytes(), &silences); err != nil {
		t.Fatalf("failed to decode restored silences: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected 1 restored silence, got %d", len(silences))
	}

	silenceGet := httptest.NewRequest(http.MethodGet, "/api/v2/silence/"+silenceID, nil)
	silenceGetRec := httptest.NewRecorder()
	mux2.ServeHTTP(silenceGetRec, silenceGet)
	if silenceGetRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silence/{id} expected 200 after restart, got %d", silenceGetRec.Code)
	}
}

func TestPhase0WebhookProcessesAlertsIntoHistory(t *testing.T) {
	mux := newPhase0TestMux(t)

	webhookPayload := `{
		"alerts": [
			{
				"labels": {"alertname":"WebhookAlert","service":"amp"},
				"annotations": {"summary":"from webhook"},
				"startsAt": "2026-02-25T03:00:00Z",
				"status": "firing"
			}
		]
	}`

	postReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(webhookPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /webhook expected 200, got %d", postRec.Code)
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert ingested via webhook, got %d", len(alerts))
	}

	historyReq := httptest.NewRequest(http.MethodGet, "/history", nil)
	historyRec := httptest.NewRecorder()
	mux.ServeHTTP(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("GET /history expected 200, got %d", historyRec.Code)
	}

	var historyPayload map[string]any
	if err := json.Unmarshal(historyRec.Body.Bytes(), &historyPayload); err != nil {
		t.Fatalf("failed to decode history response: %v", err)
	}
	total, ok := historyPayload["total"].(float64)
	if !ok {
		t.Fatalf("history total has unexpected type: %T", historyPayload["total"])
	}
	if total < 1 {
		t.Fatalf("expected history total >= 1 after webhook ingest, got %.0f", total)
	}
}

func TestPhase0E2ESmoke_IngestSilenceAndHistoryRecent(t *testing.T) {
	mux := newPhase0TestMux(t)

	silenceReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/silences",
		bytes.NewBufferString(activeSilencePayloadForAlert(time.Now().UTC(), "MutedAlert")),
	)
	silenceRec := httptest.NewRecorder()
	mux.ServeHTTP(silenceRec, silenceReq)
	if silenceRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", silenceRec.Code)
	}

	mutedAlertPayload := `[
		{
			"labels": {"alertname":"MutedAlert","service":"amp"},
			"annotations": {"summary":"muted"},
			"startsAt": "2026-02-25T04:00:00Z",
			"status": "firing"
		}
	]`
	mutedAlertReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(mutedAlertPayload))
	mutedAlertRec := httptest.NewRecorder()
	mux.ServeHTTP(mutedAlertRec, mutedAlertReq)
	if mutedAlertRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts muted expected 200, got %d", mutedAlertRec.Code)
	}

	controlAlertPayload := `[
		{
			"labels": {"alertname":"ControlAlert","service":"amp"},
			"annotations": {"summary":"not muted"},
			"startsAt": "2026-02-25T04:01:00Z",
			"status": "firing"
		}
	]`
	controlAlertReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(controlAlertPayload))
	controlAlertRec := httptest.NewRecorder()
	mux.ServeHTTP(controlAlertRec, controlAlertReq)
	if controlAlertRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts control expected 200, got %d", controlAlertRec.Code)
	}

	recentReq := httptest.NewRequest(http.MethodGet, "/history/recent?status=firing&limit=10", nil)
	recentRec := httptest.NewRecorder()
	mux.ServeHTTP(recentRec, recentReq)
	if recentRec.Code != http.StatusOK {
		t.Fatalf("GET /history/recent expected 200, got %d", recentRec.Code)
	}

	var recentPayload map[string]any
	if err := json.Unmarshal(recentRec.Body.Bytes(), &recentPayload); err != nil {
		t.Fatalf("failed to decode history/recent response: %v", err)
	}

	total, ok := recentPayload["total"].(float64)
	if !ok {
		t.Fatalf("history/recent total has unexpected type: %T", recentPayload["total"])
	}
	if total != 1 {
		t.Fatalf("expected only non-muted alert in history/recent, got total %.0f", total)
	}

	alerts, ok := recentPayload["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("expected exactly one alert in history/recent, got %v", recentPayload["alerts"])
	}

	alertMap, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("history/recent alert has unexpected type: %T", alerts[0])
	}
	labels, ok := alertMap["labels"].(map[string]any)
	if !ok {
		t.Fatalf("history/recent alert labels has unexpected type: %T", alertMap["labels"])
	}
	if labels["alertname"] != "ControlAlert" {
		t.Fatalf("expected ControlAlert in history/recent, got %v", labels["alertname"])
	}
}
