package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

type extendedFakeRegistry struct {
	alertStore   *memory.AlertStore
	silenceStore *memory.SilenceStore
	processor    *services.AlertProcessor
	config       *appconfig.Config
	startTime    time.Time
	reloadErr    error
}

func (r *extendedFakeRegistry) AlertStore() *memory.AlertStore           { return r.alertStore }
func (r *extendedFakeRegistry) SilenceStore() *memory.SilenceStore       { return r.silenceStore }
func (r *extendedFakeRegistry) AlertProcessor() *services.AlertProcessor { return r.processor }
func (r *extendedFakeRegistry) Config() *appconfig.Config               { return r.config }
func (r *extendedFakeRegistry) StartTime() time.Time                     { return r.startTime }
func (r *extendedFakeRegistry) ReloadConfig(_ context.Context) error     { return r.reloadErr }

func TestStatusAPIHandler(t *testing.T) {
	// Create a temporary config file
	tmpFile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	configContent := "profile: lite\nserver:\n  port: 9093"
	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	os.Setenv("AMP_CONFIG_FILE", tmpFile.Name())
	defer os.Unsetenv("AMP_CONFIG_FILE")

	startTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	registry := &extendedFakeRegistry{
		startTime: startTime,
		config:    &appconfig.Config{},
	}

	handler := StatusAPIHandler(registry)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/status status = %d, want 200", rec.Code)
	}

	var resp StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.ConfigOriginal != configContent {
		t.Errorf("got config content %q, want %q", resp.ConfigOriginal, configContent)
	}

	if resp.Uptime.Unix() != startTime.Unix() {
		t.Errorf("got uptime %v, want %v", resp.Uptime, startTime)
	}

	if resp.VersionInfo.Version != "0.0.1" {
		t.Errorf("got version %q, want 0.0.1", resp.VersionInfo.Version)
	}
}

func TestReloadHandler(t *testing.T) {
	registry := &extendedFakeRegistry{}

	handler := ReloadHandler(registry)

	t.Run("MethodNotAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/-/reload", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("got status %d, want 405", rec.Code)
		}
	})

	t.Run("Success", func(t *testing.T) {
		registry.reloadErr = nil
		req := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("got status %d, want 200", rec.Code)
		}
		if rec.Body.String() != "OK" {
			t.Errorf("got body %q, want OK", rec.Body.String())
		}
	})

	t.Run("Failure", func(t *testing.T) {
		registry.reloadErr = context.DeadlineExceeded
		req := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("got status %d, want 500", rec.Code)
		}
	})
}

func TestReceiversHandler(t *testing.T) {
	registry := &extendedFakeRegistry{
		config: &appconfig.Config{
			Receivers: []appconfig.ReceiverConfig{
				{Name: "pagerduty"},
				{Name: "slack"},
			},
		},
	}

	handler := ReceiversHandler(registry)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	var resp []appconfig.ReceiverConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if len(resp) != 2 {
		t.Errorf("got %d receivers, want 2", len(resp))
	}
	if resp[0].Name != "pagerduty" || resp[1].Name != "slack" {
		t.Errorf("got unexpected receivers: %v", resp)
	}
}
