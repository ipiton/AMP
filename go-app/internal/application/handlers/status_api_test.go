package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/buildinfo"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

type extendedFakeRegistry struct {
	alertStore     *memory.AlertStore
	silenceStore   *memory.SilenceStore
	processor      *services.AlertProcessor
	config         *appconfig.Config
	startTime      time.Time
	reloadErr      error
	clusterStatus  ClusterStatus
	routeEvaluator services.RouteEvaluator
}

func (r *extendedFakeRegistry) AlertStore() *memory.AlertStore     { return r.alertStore }
func (r *extendedFakeRegistry) SilenceStore() *memory.SilenceStore { return r.silenceStore }
func (r *extendedFakeRegistry) SilenceRepository() infrasilencing.SilenceRepository {
	return nil
}
func (r *extendedFakeRegistry) SilenceEventPublisher() infrasilencing.SilenceEventPublisher {
	return nil
}
func (r *extendedFakeRegistry) AlertProcessor() *services.AlertProcessor           { return r.processor }
func (r *extendedFakeRegistry) Config() *appconfig.Config                          { return r.config }
func (r *extendedFakeRegistry) StartTime() time.Time                               { return r.startTime }
func (r *extendedFakeRegistry) ReloadConfig(_ context.Context) error               { return r.reloadErr }
func (r *extendedFakeRegistry) InhibitionState() inhibition.InhibitionStateManager { return nil }

// RouteEvaluator returns nil unless a test injects one — nil is the
// lite/legacy posture (no `route:` section).
func (r *extendedFakeRegistry) RouteEvaluator() services.RouteEvaluator { return r.routeEvaluator }

func (r *extendedFakeRegistry) ClusterStatus(_ context.Context) ClusterStatus {
	if r.clusterStatus.Status == "" {
		return ClusterStatus{Status: "disabled"}
	}
	return r.clusterStatus
}

func TestStatusAPIHandler(t *testing.T) {
	// Create a temporary config file
	tmpFile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	configContent := "profile: lite\nserver:\n  port: 9093"
	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	_ = os.Setenv("AMP_CONFIG_FILE", tmpFile.Name())
	defer func() { _ = os.Unsetenv("AMP_CONFIG_FILE") }()

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

	// Final review finding 15: config.original must NOT be the raw config
	// file (it is served unauthenticated and would leak every credential in
	// it), and must be Alertmanager-shaped so amtool can re-parse it.
	if resp.Config.Original == configContent {
		t.Error("config.original returned the raw config file verbatim; it must be the Alertmanager-shaped redacted view")
	}
	if !strings.Contains(resp.Config.Original, "route:") {
		t.Errorf("config.original is not Alertmanager-shaped (no route:): %q", resp.Config.Original)
	}

	if resp.Uptime.Unix() != startTime.Unix() {
		t.Errorf("got uptime %v, want %v", resp.Uptime, startTime)
	}

	if resp.VersionInfo.Version != buildinfo.Version {
		t.Errorf("got version %q, want %q (buildinfo default)", resp.VersionInfo.Version, buildinfo.Version)
	}
	if resp.VersionInfo.GoVersion == "" {
		t.Error("got empty goVersion")
	}

	if resp.Cluster.Status != "disabled" {
		t.Errorf("got cluster.status %q, want %q (no clustering yet)", resp.Cluster.Status, "disabled")
	}

	// Verify the wire shape is actually nested (config: {original: ...}),
	// not the old flat "config.original" key.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to unmarshal raw response: %v", err)
	}
	configObj, ok := raw["config"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level \"config\" to be a nested object, got %T", raw["config"])
	}
	original, ok := configObj["original"].(string)
	if !ok {
		t.Fatalf("expected config.original to be a string, got %T", configObj["original"])
	}
	if strings.Contains(original, "profile:") {
		t.Errorf("config.original leaked non-Alertmanager config sections: %q", original)
	}
	if _, hasFlatKey := raw["config.original"]; hasFlatKey {
		t.Error("response still has the old flat \"config.original\" key")
	}
	clusterObj, ok := raw["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level \"cluster\" to be a nested object, got %T", raw["cluster"])
	}
	if clusterObj["status"] != "disabled" {
		t.Errorf("got cluster.status %v, want disabled", clusterObj["status"])
	}
}

// TestStatusAPIHandler_ClusterReadyWithPeers proves the standard-profile
// "ready" shape (task 6.5): status/name/peers all surface on the wire
// exactly as registry.ClusterStatus returns them, distinct from the
// lite-profile "disabled" shape covered by TestStatusAPIHandler above.
func TestStatusAPIHandler_ClusterReadyWithPeers(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config*.yaml")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, err = tmpFile.WriteString("profile: standard\n")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	_ = os.Setenv("AMP_CONFIG_FILE", tmpFile.Name())
	defer func() { _ = os.Unsetenv("AMP_CONFIG_FILE") }()

	registry := &extendedFakeRegistry{
		config: &appconfig.Config{},
		clusterStatus: ClusterStatus{
			Status: "ready",
			Name:   "amp-a",
			Peers: []ClusterPeer{
				{Name: "amp-a", Address: "10.0.0.1:8080"},
				{Name: "amp-b", Address: "10.0.0.2:8080"},
			},
		},
	}

	handler := StatusAPIHandler(registry)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp StatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, "ready", resp.Cluster.Status)
	assert.Equal(t, "amp-a", resp.Cluster.Name)
	require.Len(t, resp.Cluster.Peers, 2)
	assert.Equal(t, "amp-a", resp.Cluster.Peers[0].Name)
	assert.Equal(t, "10.0.0.1:8080", resp.Cluster.Peers[0].Address)
	assert.Equal(t, "amp-b", resp.Cluster.Peers[1].Name)
	assert.Equal(t, "10.0.0.2:8080", resp.Cluster.Peers[1].Address)
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

	// Final review finding 16: the failure body used to echo err.Error(), which
	// embeds the config file path (and, on validation failure, values from it)
	// on an unauthenticated endpoint.
	t.Run("FailureDoesNotEchoConfigPath", func(t *testing.T) {
		registry.reloadErr = errors.New("failed to read config file /etc/amp/secrets/config.yaml: permission denied")
		req := httptest.NewRequest(http.MethodPost, "/-/reload", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		body := rec.Body.String()
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, body, "/etc/amp/secrets/config.yaml", "the response must not echo the config path")
		assert.NotContains(t, body, "permission denied", "the response must not echo internal error detail")
		assert.Contains(t, body, "see server logs", "the response should point the operator at the logs")
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
