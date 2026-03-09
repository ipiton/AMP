//go:build futureparity
// +build futureparity

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const futureParityCompatibilityConfigYAML = `
profile: lite
storage:
  backend: filesystem
  filesystem_path: /tmp/futureparity.sqlite
server:
  host: 127.0.0.1
  port: 9093
publishing:
  enabled: false
`

func TestFutureParityHarnessRegistersCompatibilityRoutes(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "runtime-state.json")
	t.Setenv(runtimeStateFileEnv, stateFile)
	t.Setenv(runtimeConfigFileEnv, writeTestConfigFile(t, futureParityCompatibilityConfigYAML))

	initTemplates()

	mux := http.NewServeMux()
	registerRoutes(mux)

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	mux.ServeHTTP(healthRec, healthReq)

	if healthRec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d body=%q", healthRec.Code, http.StatusOK, healthRec.Body.String())
	}

	var healthPayload map[string]any
	if err := json.Unmarshal(healthRec.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("GET /health invalid json: %v", err)
	}
	if got := healthPayload["status"]; got != "healthy" && got != "degraded" {
		t.Fatalf("GET /health status field = %v, want healthy or degraded", got)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/ready", nil)
	readyRec := httptest.NewRecorder()
	mux.ServeHTTP(readyRec, readyReq)

	if readyRec.Code != http.StatusOK {
		t.Fatalf("GET /ready status = %d, want %d body=%q", readyRec.Code, http.StatusOK, readyRec.Body.String())
	}

	var readyPayload map[string]any
	if err := json.Unmarshal(readyRec.Body.Bytes(), &readyPayload); err != nil {
		t.Fatalf("GET /ready invalid json: %v", err)
	}
	if got := readyPayload["ready"]; got != true {
		t.Fatalf("GET /ready ready field = %v, want true", got)
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)

	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts status = %d, want %d body=%q", alertsRec.Code, http.StatusOK, alertsRec.Body.String())
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	dashboardRec := httptest.NewRecorder()
	mux.ServeHTTP(dashboardRec, dashboardReq)

	if dashboardRec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard status = %d, want %d body=%q", dashboardRec.Code, http.StatusOK, dashboardRec.Body.String())
	}
	if got := dashboardRec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("GET /dashboard content-type = %q, want html", got)
	}
	if body := dashboardRec.Body.String(); !strings.Contains(body, "Dashboard") {
		t.Fatalf("GET /dashboard body missing dashboard marker, body=%q", body)
	}
}

func TestFutureParityConfigHashIsDeterministic(t *testing.T) {
	configA := "route:\n  receiver: default\n"
	configB := "route:\n  receiver: secondary\n"

	hashA1 := configSHA256(configA)
	hashA2 := configSHA256(configA)
	hashB := configSHA256(configB)

	expectedSum := sha256.Sum256([]byte(configA))
	expectedHash := hex.EncodeToString(expectedSum[:])

	if hashA1 != hashA2 {
		t.Fatalf("configSHA256() must be deterministic: %q != %q", hashA1, hashA2)
	}
	if hashA1 != expectedHash {
		t.Fatalf("configSHA256() = %q, want %q", hashA1, expectedHash)
	}
	if hashA1 == hashB {
		t.Fatalf("configSHA256() must differ for different inputs: %q", hashA1)
	}
	if len(hashA1) != 64 {
		t.Fatalf("configSHA256() length = %d, want 64", len(hashA1))
	}
}
