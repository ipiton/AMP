package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newPhase0TestMux(t *testing.T) *http.ServeMux {
	t.Helper()

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
		{name: "health", method: http.MethodGet, path: "/health", allowedStatus: []int{http.StatusOK}},
		{name: "ready", method: http.MethodGet, path: "/ready", allowedStatus: []int{http.StatusOK}},
		{name: "healthz alias", method: http.MethodGet, path: "/healthz", allowedStatus: []int{http.StatusOK}},
		{name: "readyz alias", method: http.MethodGet, path: "/readyz", allowedStatus: []int{http.StatusOK}},
		{name: "metrics", method: http.MethodGet, path: "/metrics", allowedStatus: []int{http.StatusOK}},
		{name: "alerts get", method: http.MethodGet, path: "/api/v2/alerts", allowedStatus: []int{http.StatusOK}},
		{name: "alerts post", method: http.MethodPost, path: "/api/v2/alerts", body: "{}", allowedStatus: []int{http.StatusOK}},
		{name: "silences get", method: http.MethodGet, path: "/api/v2/silences", allowedStatus: []int{http.StatusOK}},
		{name: "status get", method: http.MethodGet, path: "/api/v2/status", allowedStatus: []int{http.StatusOK}},
		{name: "dashboard overview api", method: http.MethodGet, path: "/api/dashboard/overview", allowedStatus: []int{http.StatusOK}},
		{name: "dashboard recent alerts api", method: http.MethodGet, path: "/api/dashboard/alerts/recent", allowedStatus: []int{http.StatusOK}},
		{name: "webhook post", method: http.MethodPost, path: "/webhook", body: "{}", allowedStatus: []int{http.StatusOK}},
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

		// Active runtime registers "/" as catch-all dashboard handler, so unknown routes
		// currently resolve to dashboard rendering path and return 500 on template fallback.
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 for unknown route due catch-all handler, got %d", rec.Code)
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
	})

	t.Run("alerts get contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/alerts expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("alerts get response is not valid json: %v", err)
		}
		if payload["status"] != "success" {
			t.Fatalf("alerts get expected status=success, got %v", payload["status"])
		}
	})

	t.Run("alerts post contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/alerts expected 200, got %d", rec.Code)
		}
	})

	t.Run("silences get contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v2/silences expected 200, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("silences response is not valid json: %v", err)
		}
		if payload["status"] != "success" {
			t.Fatalf("silences get expected status=success, got %v", payload["status"])
		}
	})

	t.Run("webhook method contract", func(t *testing.T) {
		postReq := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{}`))
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
}
