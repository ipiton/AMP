package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testHealthProvider struct {
	livenessErr     error
	readinessErr    error
	livenessReport  map[string]any
	readinessReport map[string]any
}

func (p *testHealthProvider) Liveness(context.Context) error {
	return p.livenessErr
}

func (p *testHealthProvider) Readiness(context.Context) error {
	return p.readinessErr
}

func (p *testHealthProvider) LivenessReport(context.Context) map[string]any {
	return p.livenessReport
}

func (p *testHealthProvider) ReadinessReport(context.Context) map[string]any {
	return p.readinessReport
}

func TestHealthHandler_ReturnsDegradedJSONBody(t *testing.T) {
	provider := &testHealthProvider{
		livenessReport: map[string]any{
			"status": "degraded",
			"checks": map[string]any{
				"bootstrap": map[string]any{"status": "healthy"},
			},
			"degraded_reasons": []string{"cache backend unavailable"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	HealthHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HealthHandler() status = %d, want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("HealthHandler() invalid JSON body: %v", err)
	}
	if got := payload["status"]; got != "degraded" {
		t.Fatalf("HealthHandler() body status = %v, want degraded", got)
	}
}

func TestReadyHandler_ReturnsUnavailableWhenNotReady(t *testing.T) {
	provider := &testHealthProvider{
		readinessErr: errors.New("storage unavailable"),
		readinessReport: map[string]any{
			"status": "unhealthy",
			"ready":  false,
			"checks": map[string]any{
				"storage": map[string]any{"status": "unhealthy", "error": "storage unavailable"},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	ReadyHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ReadyHandler() status = %d, want %d body=%q", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ReadyHandler() invalid JSON body: %v", err)
	}
	if got := payload["ready"]; got != false {
		t.Fatalf("ReadyHandler() body ready = %v, want false", got)
	}
}

func TestReadyHandler_ReturnsDegradedJSONBodyWhenReady(t *testing.T) {
	provider := &testHealthProvider{
		readinessReport: map[string]any{
			"status": "degraded",
			"ready":  true,
			"checks": map[string]any{
				"storage": map[string]any{"status": "healthy"},
			},
			"degraded_reasons": []string{"cache backend unavailable"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	ReadyHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ReadyHandler() status = %d, want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("ReadyHandler() invalid JSON body: %v", err)
	}
	if got := payload["status"]; got != "degraded" {
		t.Fatalf("ReadyHandler() body status = %v, want degraded", got)
	}
	if got := payload["ready"]; got != true {
		t.Fatalf("ReadyHandler() body ready = %v, want true", got)
	}
}

func TestAlertmanagerHealthHandlers_TextContract(t *testing.T) {
	provider := &testHealthProvider{
		readinessErr: errors.New("storage unavailable"),
	}

	liveReq := httptest.NewRequest(http.MethodGet, "/-/healthy", nil)
	liveRec := httptest.NewRecorder()
	AlertmanagerHealthyHandler(provider).ServeHTTP(liveRec, liveReq)

	if liveRec.Code != http.StatusOK {
		t.Fatalf("AlertmanagerHealthyHandler() status = %d, want %d", liveRec.Code, http.StatusOK)
	}
	if body := liveRec.Body.String(); body != "OK" {
		t.Fatalf("AlertmanagerHealthyHandler() body = %q, want OK", body)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
	readyRec := httptest.NewRecorder()
	AlertmanagerReadyHandler(provider).ServeHTTP(readyRec, readyReq)

	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("AlertmanagerReadyHandler() status = %d, want %d", readyRec.Code, http.StatusServiceUnavailable)
	}
	if body := readyRec.Body.String(); body != "NOT READY" {
		t.Fatalf("AlertmanagerReadyHandler() body = %q, want NOT READY", body)
	}
}
