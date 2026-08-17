//go:build futureparity
// +build futureparity

// Historical wide-surface parity suite. Opt-in only until runtime restoration.

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
  group_by: ["alertname", "service", "namespace"]
receivers:
  - name: "default"
`

const unknownSilenceUUID = "00000000-0000-0000-0000-000000000001"

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

	// ADR-002: the route inventory documents the ACTIVE runtime surface
	// (internal/application/router.go + registerLegacyDashboardRoutes).
	// Endpoints removed by the controlled replacement (config API, history,
	// dashboard JSON API, webhook, pprof, classification, /script.js,
	// POST /api/v1/alerts alias) intentionally return 404.
	probes := []routeProbe{
		{name: "root", method: http.MethodGet, path: "/", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard", method: http.MethodGet, path: "/dashboard", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard alerts", method: http.MethodGet, path: "/dashboard/alerts", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard silences", method: http.MethodGet, path: "/dashboard/silences", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard llm", method: http.MethodGet, path: "/dashboard/llm", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "dashboard routing", method: http.MethodGet, path: "/dashboard/routing", allowedStatus: []int{http.StatusOK, http.StatusInternalServerError}},
		{name: "script js removed", method: http.MethodGet, path: "/script.js", allowedStatus: []int{http.StatusNotFound}},
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
		{name: "debug removed", method: http.MethodGet, path: "/debug/pprof/", allowedStatus: []int{http.StatusNotFound}},
		{name: "metrics", method: http.MethodGet, path: "/metrics", allowedStatus: []int{http.StatusOK}},
		{name: "alerts v1 post removed", method: http.MethodPost, path: "/api/v1/alerts", body: validAlertPayload, allowedStatus: []int{http.StatusNotFound}},
		{name: "alerts get", method: http.MethodGet, path: "/api/v2/alerts", allowedStatus: []int{http.StatusOK}},
		{name: "alerts post", method: http.MethodPost, path: "/api/v2/alerts", body: validAlertPayload, allowedStatus: []int{http.StatusOK}},
		{name: "alert groups get", method: http.MethodGet, path: "/api/v2/alerts/groups", allowedStatus: []int{http.StatusOK}},
		{name: "receivers get", method: http.MethodGet, path: "/api/v2/receivers", allowedStatus: []int{http.StatusOK}},
		{name: "inhibitions get", method: http.MethodGet, path: "/api/v2/inhibitions", allowedStatus: []int{http.StatusOK}},
		{name: "silences get", method: http.MethodGet, path: "/api/v2/silences", allowedStatus: []int{http.StatusOK}},
		{name: "silences post", method: http.MethodPost, path: "/api/v2/silences", body: validSilencePayload, allowedStatus: []int{http.StatusOK}},
		{name: "silence by id get", method: http.MethodGet, path: "/api/v2/silence/" + unknownSilenceUUID, allowedStatus: []int{http.StatusNotFound}},
		{name: "silence by id delete", method: http.MethodDelete, path: "/api/v2/silence/" + unknownSilenceUUID, allowedStatus: []int{http.StatusNotFound}},
		{name: "status get", method: http.MethodGet, path: "/api/v2/status", allowedStatus: []int{http.StatusOK}},
		{name: "config removed", method: http.MethodGet, path: "/api/v2/config", allowedStatus: []int{http.StatusNotFound}},
		{name: "config status removed", method: http.MethodGet, path: "/api/v2/config/status", allowedStatus: []int{http.StatusNotFound}},
		{name: "classification removed", method: http.MethodGet, path: "/api/v2/classification/stats", allowedStatus: []int{http.StatusNotFound}},
		{name: "history removed", method: http.MethodGet, path: "/history", allowedStatus: []int{http.StatusNotFound}},
		{name: "history recent removed", method: http.MethodGet, path: "/history/recent", allowedStatus: []int{http.StatusNotFound}},
		{name: "dashboard overview api removed", method: http.MethodGet, path: "/api/dashboard/overview", allowedStatus: []int{http.StatusNotFound}},
		{name: "webhook removed", method: http.MethodPost, path: "/webhook", body: validAlertPayload, allowedStatus: []int{http.StatusNotFound}},
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
	// ADR-002: the active liveness report exposes status healthy|degraded
	// (200 either way; optional components may be degraded in a lite test
	// environment) plus checks/initialized instead of the historical version
	// field.
	switch health["status"] {
	case "healthy", "degraded":
	default:
		t.Fatalf("GET /health expected status healthy or degraded, got %v", health["status"])
	}
	if _, ok := health["checks"].(map[string]any); !ok {
		t.Fatalf("GET /health expected checks object, got %T", health["checks"])
	}
	if initialized, ok := health["initialized"].(bool); !ok || !initialized {
		t.Fatalf("GET /health expected initialized=true, got %v", health["initialized"])
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
		// ADR-002: the active /api/v2/status payload is config.original +
		// versionInfo + uptime; cluster/stats/runtime were part of the
		// historical surface and are not exposed by the active runtime.
		if _, ok := payload["versionInfo"]; !ok {
			t.Fatalf("status response missing versionInfo field")
		}
		if _, ok := payload["uptime"]; !ok {
			t.Fatalf("status response missing uptime field")
		}
		if _, ok := payload["config.original"].(string); !ok {
			t.Fatalf("status response missing config.original string, got %T", payload["config.original"])
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

		uptimeRaw, ok := payload["uptime"].(string)
		if !ok {
			t.Fatalf("status uptime expected string, got %T", payload["uptime"])
		}
		if _, err := time.Parse(time.RFC3339, uptimeRaw); err != nil {
			t.Fatalf("status uptime expected RFC3339 timestamp, got %q: %v", uptimeRaw, err)
		}
	})

	t.Run("unknown api path returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/unknown-path", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// ADR-002: unknown /api/v2 paths fall through to the catch-all
		// dashboard handler, which answers with a plain-text 404 (the
		// historical runtime returned a JSON error object).
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v2/unknown-path expected 404, got %d", rec.Code)
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

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts with invalid resolved expected 200, got %d", rec.Code)
		}
	})

	t.Run("alerts get invalid receiver regex contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?receiver=[", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// PARITY-4.1: the active runtime now evaluates the upstream receiver
		// query parameter as a regex, so a malformed pattern is a 400.
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts with invalid receiver regex expected 400, got %d", rec.Code)
		}
	})

	t.Run("alerts get invalid state flag contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?active=not-bool", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// PARITY-4.1: active/silenced/inhibited/unprocessed are now validated
		// booleans, matching upstream's 400-on-malformed-query-param behavior.
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
		// ADR-002: active runtime error bodies are {"error": ...} objects.
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alerts invalid filter expected JSON object body, got %q (%v)", rec.Body.String(), err)
		}
		message, _ := payload["error"].(string)
		if !strings.Contains(message, "invalid matcher syntax") {
			t.Fatalf("alerts invalid filter expected invalid matcher syntax error, got %q", message)
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

	t.Run("alerts post date-only timestamps contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{"alertname":"DateOnly"},"startsAt":"2026-02-26","endsAt":"2026-03-01"}]`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// DTO-FRAGMENTATION consolidation: handler and memory store now share
		// one lenient parser (internal/core/alertconv.ParseAlertTime), which —
		// like upstream Alertmanager — accepts date-only YYYY-MM-DD
		// timestamps. The previous 400 documented parser divergence between
		// the two ingest layers; that divergence is gone.
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/alerts with date-only startsAt/endsAt expected 200 (lenient parser), got %d", rec.Code)
		}
	})

	t.Run("alerts post invalid payload contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/alerts with invalid payload expected 400, got %d", rec.Code)
		}
		// ADR-002: active error contract is {"error": ...} without an
		// upstream-style code/message envelope.
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alerts invalid payload expected JSON object body, got %q (%v)", rec.Body.String(), err)
		}
		if msg, _ := payload["error"].(string); !strings.Contains(msg, "invalid alert payload") {
			t.Fatalf("alerts invalid payload expected invalid alert payload error, got %v", payload["error"])
		}
	})

	t.Run("alerts post missing labels contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{}]`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// ADR-002: missing labels fail alertname validation with 400 (upstream
		// used 422 + code/message envelope).
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/alerts with missing labels expected 400, got %d", rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alerts missing labels expected JSON object body, got %q (%v)", rec.Body.String(), err)
		}
		if msg, _ := payload["error"].(string); !strings.Contains(msg, "missing required label alertname") {
			t.Fatalf("alerts missing labels expected alertname error, got %v", payload["error"])
		}
	})

	t.Run("alerts post empty labels contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{}}]`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/alerts with empty labels expected 400, got %d", rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alerts empty labels expected JSON object body, got %q (%v)", rec.Body.String(), err)
		}
		if msg, _ := payload["error"].(string); strings.TrimSpace(msg) == "" {
			t.Fatalf("alerts empty labels expected non-empty error message")
		}
	})

	t.Run("alerts post invalid generatorURL contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{"alertname":"A"},"generatorURL":":bad"}]`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// ADR-002: the active runtime does not validate generatorURL, so the
		// alert is accepted (upstream returned 422 code=601).
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/alerts with invalid generatorURL expected 200 (not validated), got %d", rec.Code)
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
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences invalid filter expected json object error, got %q (%v)", rec.Body.String(), err)
		}
		if msg, _ := payload["error"].(string); !strings.Contains(msg, "invalid matcher syntax") {
			t.Fatalf("silences invalid filter expected invalid matcher syntax error, got %v", payload["error"])
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

		// ADR-002: the active runtime answers invalid silence payloads with
		// 400 + {"error": ...} (upstream used 422 code/message envelopes).
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences with invalid payload expected 400, got %d", rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences invalid payload expected json object error, got %q (%v)", rec.Body.String(), err)
		}
		if message, _ := payload["error"].(string); strings.TrimSpace(message) == "" {
			t.Fatalf("silences invalid payload expected non-empty error message")
		}
	})

	t.Run("silences post no matchers contract", func(t *testing.T) {
		payload := `{
			"matchers": [],
			"startsAt": "2099-01-01T00:00:00Z",
			"endsAt": "2099-01-01T01:00:00Z",
			"createdBy": "phase0-test",
			"comment": "no matchers"
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences with empty matchers expected 400, got %d", rec.Code)
		}
		var errorPayload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &errorPayload); err != nil {
			t.Fatalf("silences empty matchers expected json object error, got %q (%v)", rec.Body.String(), err)
		}
		if message, _ := errorPayload["error"].(string); strings.TrimSpace(message) == "" {
			t.Fatalf("silences empty matchers expected non-empty error message")
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
			"matchers": [{"name":"","value":"value","isRegex":false}],
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
		var errorPayload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &errorPayload); err != nil {
			t.Fatalf("silences invalid matcher name expected json object error, got %q (%v)", rec.Body.String(), err)
		}
		if message, _ := errorPayload["error"].(string); strings.TrimSpace(message) == "" {
			t.Fatalf("silences invalid matcher name expected non-empty error message")
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

		// ADR-002: the active runtime reports unknown-id updates as 400
		// {"error":"silence not found"} (upstream used 404 + string body).
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences update with unknown id expected 400, got %d", rec.Code)
		}
		var errorPayload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &errorPayload); err != nil {
			t.Fatalf("silences unknown id expected json object error, got %q (%v)", rec.Body.String(), err)
		}
		if message, _ := errorPayload["error"].(string); !strings.Contains(message, "silence not found") {
			t.Fatalf("silences unknown id expected silence not found error, got %v", errorPayload["error"])
		}
	})

	t.Run("silences post update invalid id contract", func(t *testing.T) {
		payload := `{
			"id": "not-a-uuid",
			"matchers": [{"name":"alertname","value":"ContractInvalidID","isRegex":false}],
			"startsAt": "2099-01-01T00:00:00Z",
			"endsAt": "2099-01-01T01:00:00Z",
			"createdBy": "phase0-test",
			"comment": "invalid id update"
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// ADR-002: malformed update ids also surface as 400 "silence not
		// found" — the active runtime performs no UUID validation.
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences update with invalid id expected 400, got %d", rec.Code)
		}
		var errorPayload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &errorPayload); err != nil {
			t.Fatalf("silences invalid id expected json object error, got %q (%v)", rec.Body.String(), err)
		}
		if message, _ := errorPayload["error"].(string); strings.TrimSpace(message) == "" {
			t.Fatalf("silences invalid id expected non-empty error message")
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
		// ADR-002: the active runtime does not validate silence ids as UUIDs;
		// malformed ids are simply unknown and return 404 (upstream: 422).
		getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/not-a-uuid", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v2/silence/{id} with invalid id expected 404, got %d", getRec.Code)
		}

		delReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/not-a-uuid", nil)
		delRec := httptest.NewRecorder()
		mux.ServeHTTP(delRec, delReq)
		if delRec.Code != http.StatusNotFound {
			t.Fatalf("DELETE /api/v2/silence/{id} with invalid id expected 404, got %d", delRec.Code)
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
		if recResolved.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts/groups with invalid resolved expected 200, got %d", recResolved.Code)
		}

		// PARITY-4.1: the groups handler now evaluates the receiver parameter
		// as a regex, so a malformed pattern is a 400.
		reqReceiver := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?receiver=[", nil)
		recReceiver := httptest.NewRecorder()
		mux.ServeHTTP(recReceiver, reqReceiver)
		if recReceiver.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid receiver regex expected 400, got %d", recReceiver.Code)
		}

		// PARITY-4.1: active/silenced/inhibited/unprocessed are now validated
		// booleans on the groups endpoint too.
		reqActive := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?active=not-bool", nil)
		recActive := httptest.NewRecorder()
		mux.ServeHTTP(recActive, reqActive)
		if recActive.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/v2/alerts/groups with invalid active expected 400, got %d", recActive.Code)
		}

		// "muted" is not part of this parity slice (upstream's mutedBy is a
		// separate, unimplemented concept); it stays ignored.
		reqMuted := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?muted=not-bool", nil)
		recMuted := httptest.NewRecorder()
		mux.ServeHTTP(recMuted, reqMuted)
		if recMuted.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts/groups with invalid muted expected 200, got %d", recMuted.Code)
		}

		// ADR-002: the active groups handler does not parse the filter
		// parameter, so a broken matcher is not an error either.
		reqFilter := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?filter=broken-matcher", nil)
		recFilter := httptest.NewRecorder()
		mux.ServeHTTP(recFilter, reqFilter)
		if recFilter.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts/groups with invalid filter expected 200 (param ignored), got %d", recFilter.Code)
		}
	})

	t.Run("method contracts", func(t *testing.T) {
		// ADR-002: only the alerts/silences/silence-by-id/reload handlers
		// enforce methods in the active runtime; status/receivers/groups fall
		// through to the GET path (200) and removed endpoints answer 404.
		tests := []struct {
			name   string
			method string
			path   string
			status int
		}{
			{name: "alerts put not allowed", method: http.MethodPut, path: "/api/v2/alerts", status: http.StatusMethodNotAllowed},
			{name: "silences put not allowed", method: http.MethodPut, path: "/api/v2/silences", status: http.StatusMethodNotAllowed},
			{name: "silence by id post not allowed", method: http.MethodPost, path: "/api/v2/silence/any-id", status: http.StatusMethodNotAllowed},
			{name: "reload get not allowed", method: http.MethodGet, path: "/-/reload", status: http.StatusMethodNotAllowed},
			{name: "status post not allowed", method: http.MethodPost, path: "/api/v2/status", status: http.StatusMethodNotAllowed},
			{name: "receivers post not allowed", method: http.MethodPost, path: "/api/v2/receivers", status: http.StatusMethodNotAllowed},
			{name: "alert groups post not allowed", method: http.MethodPost, path: "/api/v2/alerts/groups", status: http.StatusMethodNotAllowed},
			{name: "config removed", method: http.MethodPut, path: "/api/v2/config", status: http.StatusNotFound},
			{name: "history removed", method: http.MethodPost, path: "/history", status: http.StatusNotFound},
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
			// NO-METHOD-ENFORCEMENT fixed: probes reject POST with 405; reload
			// still answers "OK" to POST.
			{name: "healthy post not allowed", method: http.MethodPost, path: "/-/healthy", status: http.StatusMethodNotAllowed, empty: true},
			{name: "ready post not allowed", method: http.MethodPost, path: "/-/ready", status: http.StatusMethodNotAllowed, empty: true},
			{name: "reload post", method: http.MethodPost, path: "/-/reload", body: `{}`, status: http.StatusOK, textOK: true},
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

	t.Run("upstream static compatibility contract", func(t *testing.T) {
		tests := []struct {
			name   string
			method string
			path   string
			status int
		}{
			// ADR-002: the upstream static compatibility assets were removed
			// (UI-PLACEHOLDER-REMOVAL); every method on these paths falls
			// through the catch-all dashboard handler and returns 404.
			{name: "script js removed", method: http.MethodGet, path: "/script.js", status: http.StatusNotFound},
			{name: "script js post removed", method: http.MethodPost, path: "/script.js", status: http.StatusNotFound},
			{name: "favicon get missing", method: http.MethodGet, path: "/favicon.ico", status: http.StatusNotFound},
			{name: "favicon post missing", method: http.MethodPost, path: "/favicon.ico", status: http.StatusNotFound},
			{name: "lib get missing", method: http.MethodGet, path: "/lib/nonexistent.js", status: http.StatusNotFound},
			{name: "lib post missing", method: http.MethodPost, path: "/lib/nonexistent.js", status: http.StatusNotFound},
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
		// ADR-002: the /api/v1/alerts ingest alias was removed; the exact path
		// is a deliberate 404 so the /api/v1/alerts/ investigation subtree
		// (PHASE-5B) owns the prefix.
		postReq := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewBufferString(`[]`))
		postRec := httptest.NewRecorder()
		mux.ServeHTTP(postRec, postReq)
		if postRec.Code != http.StatusNotFound {
			t.Fatalf("POST /api/v1/alerts expected 404, got %d", postRec.Code)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v1/alerts expected 404, got %d", getRec.Code)
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
		// ADR-002: the /history surface was removed from the active runtime;
		// resolved alerts remain queryable only via ?status=resolved above.
	})

	t.Run("invalid status filter is ignored", func(t *testing.T) {
		rec := get("/api/v2/alerts?status=broken")
		if rec.Code != http.StatusOK {
			t.Fatalf("invalid status filter expected 200, got %d", rec.Code)
		}
	})
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
	// ADR-002: the active runtime computes silencedBy from the silence store
	// (nil slice marshals as null when no silence matches); inhibitedBy and
	// mutedBy are not computed and marshal as null.
	for _, field := range []string{"silencedBy", "inhibitedBy", "mutedBy"} {
		if value, present := status[field]; present && value != nil {
			if arr, isArr := value.([]any); !isArr || len(arr) != 0 {
				t.Fatalf("alert status.%s expected null or empty array, got %v", field, value)
			}
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

	regexExactQuery := url.Values{}
	regexExactQuery.Add("filter", `alertname=~"^CPU"`)
	regexExactReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+regexExactQuery.Encode(), nil)
	regexExactRec := httptest.NewRecorder()
	mux.ServeHTTP(regexExactRec, regexExactReq)
	if regexExactRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with regex filter expected 200, got %d", regexExactRec.Code)
	}
	var regexExactAlerts []map[string]any
	if err := json.Unmarshal(regexExactRec.Body.Bytes(), &regexExactAlerts); err != nil {
		t.Fatalf("failed to decode regex exact filter response: %v", err)
	}
	// Upstream treats =~ filter as full-string match.
	if len(regexExactAlerts) != 0 {
		t.Fatalf("expected 0 alerts for alertname=~^CPU (full match semantics), got %d", len(regexExactAlerts))
	}

	regexPrefixQuery := url.Values{}
	regexPrefixQuery.Add("filter", `alertname=~"^CPU.*"`)
	regexPrefixReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+regexPrefixQuery.Encode(), nil)
	regexPrefixRec := httptest.NewRecorder()
	mux.ServeHTTP(regexPrefixRec, regexPrefixReq)
	if regexPrefixRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with regex prefix filter expected 200, got %d", regexPrefixRec.Code)
	}
	var regexPrefixAlerts []map[string]any
	if err := json.Unmarshal(regexPrefixRec.Body.Bytes(), &regexPrefixAlerts); err != nil {
		t.Fatalf("failed to decode regex prefix filter response: %v", err)
	}
	if len(regexPrefixAlerts) != 2 {
		t.Fatalf("expected 2 alerts for alertname=~^CPU.*, got %d", len(regexPrefixAlerts))
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

func TestPhase0ReceiversIncludeConfiguredNames(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-zeta"
  routes:
    - receiver: "team-db"
      routes:
        - receiver: "team-nested"
receivers:
  - name: "team-zeta"
  - name: "team-alpha"
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
		// RECEIVERS-JSON-CASE fixed: config.ReceiverConfig now carries a json
		// tag, so the field matches the upstream Alertmanager schema ("name").
		name, ok := receiver["name"].(string)
		if !ok {
			t.Fatalf("receiver.Name expected string, got %T", receiver["name"])
		}
		receiverNames = append(receiverNames, name)
		receiverSet[name] = struct{}{}
	}

	required := []string{"team-zeta", "team-alpha"}
	for _, name := range required {
		if _, ok := receiverSet[name]; !ok {
			t.Fatalf("expected configured receiver %q in /api/v2/receivers, got %v", name, receiverNames)
		}
	}
	if len(receiverNames) != 2 || receiverNames[0] != "team-zeta" || receiverNames[1] != "team-alpha" {
		t.Fatalf("expected receivers to preserve config order [team-zeta team-alpha], got %v", receiverNames)
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
	// ADR-002: the active runtime answers unknown-id updates with 400
	// {"error":"silence not found"} (upstream used 404).
	if unknownRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v2/silences update for unknown id expected 400, got %d", unknownRec.Code)
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
	// Upstream semantics: the filter matches silence-matcher VALUES. All three
	// silences carrying service=api pass; the regex-only silence does not.
	if len(serviceSilences) != 3 {
		t.Fatalf("expected 3 silences with a service matcher, got %d", len(serviceSilences))
	}

	queryRegex := url.Values{}
	// The filter regex runs against the silence matcher's raw VALUE string
	// ("^High.*"), so match by substring rather than by anchor.
	queryRegex.Add("filter", `alertname=~".*High.*"`)
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
		t.Fatalf("expected 2 silences for alertname=~.*High.*, got %d", len(regexSilences))
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
	// Upstream semantics: != excludes silences whose service matcher value is
	// "api"; a silence without a service matcher counts as empty value and
	// therefore matches.
	if len(notEqualSilences) != 1 {
		t.Fatalf("expected 1 silence for service!=api, got %d", len(notEqualSilences))
	}

	queryMulti := url.Values{}
	queryMulti.Add("filter", `service="api"`)
	queryMulti.Add("filter", `alertname=~".*High.*"`)
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

	// ADR-002: /history/recent was removed; the active runtime drops silenced
	// firing alerts at ingest, so only the control alert is listed.
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
		t.Fatalf("expected only the non-muted alert to be ingested, got %d", len(alerts))
	}

	labels, ok := alerts[0]["labels"].(map[string]any)
	if !ok {
		t.Fatalf("alert labels has unexpected type: %T", alerts[0]["labels"])
	}
	if labels["alertname"] != "ControlAlert" {
		t.Fatalf("expected ControlAlert to remain, got %v", labels["alertname"])
	}
}
