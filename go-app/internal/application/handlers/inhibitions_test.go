package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// fakeInhibitionRegistry implements InhibitionsRegistryProvider for testing.
type fakeInhibitionRegistry struct {
	stateManager inhibition.InhibitionStateManager
}

func (r *fakeInhibitionRegistry) InhibitionState() inhibition.InhibitionStateManager {
	return r.stateManager
}

// fakeStateManager is a minimal InhibitionStateManager for testing.
type fakeStateManager struct {
	inhibitions []*inhibition.InhibitionState
	err         error
}

func (m *fakeStateManager) RecordInhibition(_ context.Context, _ *inhibition.InhibitionState) error {
	return nil
}

func (m *fakeStateManager) RemoveInhibition(_ context.Context, _ string) error {
	return nil
}

func (m *fakeStateManager) GetActiveInhibitions(_ context.Context) ([]*inhibition.InhibitionState, error) {
	return m.inhibitions, m.err
}

func (m *fakeStateManager) GetInhibitedAlerts(_ context.Context) ([]string, error) {
	return nil, nil
}

func (m *fakeStateManager) IsInhibited(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *fakeStateManager) GetInhibitionState(_ context.Context, _ string) (*inhibition.InhibitionState, error) {
	return nil, nil
}

func TestInhibitionsHandler_GetNilStateManager_ReturnsEmptyList(t *testing.T) {
	registry := &fakeInhibitionRegistry{stateManager: nil}
	handler := InhibitionsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/inhibitions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var result []inhibitionResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestInhibitionsHandler_GetWithActiveInhibitions_ReturnsInhibitions(t *testing.T) {
	inhibitedAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	states := []*inhibition.InhibitionState{
		{
			TargetFingerprint: "target-abc",
			SourceFingerprint: "source-xyz",
			RuleName:          "node-down-inhibits-instance-down",
			InhibitedAt:       inhibitedAt,
			ExpiresAt:         &expiresAt,
		},
		{
			TargetFingerprint: "target-def",
			SourceFingerprint: "source-ghi",
			RuleName:          "critical-inhibits-warnings",
			InhibitedAt:       inhibitedAt,
			ExpiresAt:         nil,
		},
	}

	registry := &fakeInhibitionRegistry{
		stateManager: &fakeStateManager{inhibitions: states},
	}
	handler := InhibitionsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/inhibitions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var result []inhibitionResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}

	first := result[0]
	if first.TargetFingerprint != "target-abc" {
		t.Errorf("TargetFingerprint = %q, want %q", first.TargetFingerprint, "target-abc")
	}
	if first.SourceFingerprint != "source-xyz" {
		t.Errorf("SourceFingerprint = %q, want %q", first.SourceFingerprint, "source-xyz")
	}
	if first.RuleName != "node-down-inhibits-instance-down" {
		t.Errorf("RuleName = %q, want %q", first.RuleName, "node-down-inhibits-instance-down")
	}
	if first.ExpiresAt == nil {
		t.Errorf("ExpiresAt: got nil, want non-nil")
	}

	second := result[1]
	if second.ExpiresAt != nil {
		t.Errorf("ExpiresAt: got %v, want nil", *second.ExpiresAt)
	}
}

func TestInhibitionsHandler_GetStateManagerError_Returns500(t *testing.T) {
	registry := &fakeInhibitionRegistry{
		stateManager: &fakeStateManager{err: errors.New("redis unavailable")},
	}
	handler := InhibitionsHandler(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/inhibitions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestInhibitionsHandler_PostMethodNotAllowed(t *testing.T) {
	registry := &fakeInhibitionRegistry{stateManager: nil}
	handler := InhibitionsHandler(registry)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/inhibitions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestInhibitionsHandler_PutMethodNotAllowed(t *testing.T) {
	registry := &fakeInhibitionRegistry{stateManager: nil}
	handler := InhibitionsHandler(registry)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/inhibitions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
