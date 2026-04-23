package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// stubInvestigationRepo is a minimal in-memory stub for testing.
type stubInvestigationRepo struct {
	inv *core.Investigation
	err error
}

func (r *stubInvestigationRepo) Create(_ context.Context, _ *core.Investigation) error {
	return nil
}
func (r *stubInvestigationRepo) UpdateStatus(_ context.Context, _ string, _ core.InvestigationStatus) error {
	return nil
}
func (r *stubInvestigationRepo) SaveResult(_ context.Context, _ string, _ *core.InvestigationResult) error {
	return nil
}
func (r *stubInvestigationRepo) SaveError(_ context.Context, _ string, _ string, _ core.InvestigationErrorType) error {
	return nil
}
func (r *stubInvestigationRepo) GetLatestByFingerprint(_ context.Context, _ string) (*core.Investigation, error) {
	return r.inv, r.err
}
func (r *stubInvestigationRepo) MoveToDLQ(_ context.Context, _ string) error { return nil }
func (r *stubInvestigationRepo) SaveAgentResult(_ context.Context, _ string, _ *core.InvestigationResult, _ *core.AgentRunSummary) error {
	return nil
}

type stubInvestigationProvider struct {
	repo core.InvestigationRepository
}

func (p *stubInvestigationProvider) InvestigationRepository() core.InvestigationRepository {
	return p.repo
}

func TestInvestigationHandler_NotFound(t *testing.T) {
	provider := &stubInvestigationProvider{
		repo: &stubInvestigationRepo{inv: nil, err: nil},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/fp-missing/investigation", nil)
	rec := httptest.NewRecorder()

	InvestigationHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestInvestigationHandler_MethodNotAllowed(t *testing.T) {
	provider := &stubInvestigationProvider{
		repo: &stubInvestigationRepo{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/fp1/investigation", nil)
	rec := httptest.NewRecorder()

	InvestigationHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestInvestigationHandler_ServiceUnavailable(t *testing.T) {
	provider := &stubInvestigationProvider{repo: nil}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/fp1/investigation", nil)
	rec := httptest.NewRecorder()

	InvestigationHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestInvestigationHandler_ReturnsSteps(t *testing.T) {
	now := time.Now().UTC()
	stepsJSON := json.RawMessage(`[{"step_number":1,"type":"tool_call","tool_name":"echo"}]`)

	inv := &core.Investigation{
		ID:              "inv-1",
		Fingerprint:     "fp-abc",
		Status:          core.InvestigationCompleted,
		QueuedAt:        now,
		CreatedAt:       now,
		UpdatedAt:       now,
		Steps:           stepsJSON,
		IterationsCount: 2,
		ToolCallsCount:  1,
		Result: &core.InvestigationResult{
			Summary:    "all good",
			Confidence: 0.9,
		},
	}

	provider := &stubInvestigationProvider{
		repo: &stubInvestigationRepo{inv: inv},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/fp-abc/investigation", nil)
	rec := httptest.NewRecorder()

	InvestigationHandler(provider).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}

	if body["fingerprint"] != "fp-abc" {
		t.Errorf("fingerprint = %v, want %q", body["fingerprint"], "fp-abc")
	}
	if body["steps"] == nil {
		t.Error("expected steps in response, got nil")
	}
	if body["iterations_count"] == nil {
		t.Error("expected iterations_count in response")
	}
	if body["tool_calls_count"] == nil {
		t.Error("expected tool_calls_count in response")
	}
	if body["result"] == nil {
		t.Error("expected result in response")
	}
}

func TestExtractFingerprintFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/alerts/abc123/investigation", "abc123"},
		{"/api/v1/alerts/fp-with-dashes/investigation", "fp-with-dashes"},
		{"/api/v1/alerts/abc123/investigation/", "abc123"},
		{"/api/v1/alerts/", ""},
		{"/investigation", ""},
	}

	for _, tc := range cases {
		got := extractFingerprintFromPath(tc.path)
		if got != tc.want {
			t.Errorf("extractFingerprintFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
