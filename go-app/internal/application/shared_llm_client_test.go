package application

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// One LLM client per process (fix-round C1)
// ================================================================================
// initializeClassification and initializeInvestigation used to build their own
// identically-configured *HTTPLLMClient, and only the investigation one was
// registered for hot reload. An llm.model / llm.api_key edit therefore reported
// success while every alert classification — one call per alert, the
// higher-traffic path — kept the old model and the old credential.

func TestSharedLLMClient_IsBuiltOnceAndReused(t *testing.T) {
	registry, err := NewServiceRegistry(&appconfig.Config{
		Profile: appconfig.ProfileLite,
		LLM:     appconfig.LLMConfig{Enabled: true, Provider: "openai", Model: "gpt-4o", BaseURL: "http://llm.internal"},
	}, slog.Default())
	require.NoError(t, err)

	first := registry.sharedLLMClient()
	second := registry.sharedLLMClient()

	require.NotNil(t, first)
	assert.Same(t, first, second, "there must be exactly one LLM client per process")
	assert.Same(t, first, registry.llmClient, "and it must be the one registerReloadables wires")

	// It carries the operator's config, not llm.DefaultConfig().
	assert.Equal(t, "gpt-4o", first.GetConfig().Model)
	assert.Equal(t, "http://llm.internal", first.GetConfig().BaseURL)
}

// TestClassificationUsesTheSharedLLMClient_AndFollowsAReload is the end-to-end
// proof: the classification service's LLM traffic must land on whatever
// provider the LAST reload configured, not on the one it started with.
func TestClassificationUsesTheSharedLLMClient_AndFollowsAReload(t *testing.T) {
	var oldHits, newHits atomic.Int64

	oldProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oldHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer oldProvider.Close()

	newProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		newHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer newProvider.Close()

	bootCfg := &appconfig.Config{
		Profile: appconfig.ProfileLite,
		LLM: appconfig.LLMConfig{
			Enabled: true, Provider: "openai", Model: "gpt-4o",
			BaseURL: oldProvider.URL, Timeout: 5 * time.Second,
		},
		Metrics: appconfig.MetricsConfig{Enabled: true},
	}

	registry, err := NewServiceRegistry(bootCfg, slog.Default())
	require.NoError(t, err)
	registry.restartWarnings = appconfig.NewRestartWarnings()
	registry.metricsGate = nil

	ctx := context.Background()

	// Build the classification service the way Initialize does (it creates its
	// own in-memory cache when r.cache is nil).
	require.NoError(t, registry.initializeClassification(ctx))
	require.NotNil(t, registry.classificationSvc)
	require.NotNil(t, registry.llmClient, "classification must have taken the shared client")

	// Health() routes through the classification service's OWN llm client
	// reference — the only externally observable proof of which client it holds.
	_ = registry.classificationSvc.Health(ctx)
	require.Positive(t, oldHits.Load(), "classification must be talking to the boot provider")

	// Now reload just the LLM section, through the real registry + reloader.
	newCfg := *bootCfg
	newCfg.LLM.BaseURL = newProvider.URL
	newCfg.LLM.Model = "gpt-4o-mini"

	reloader := appconfig.NewConfigReloader(slog.Default())
	registry.registerReloadables(reloader)
	require.Empty(t, reloader.ReloadAll(ctx, bootCfg, &newCfg, nil))

	before := newHits.Load()
	_ = registry.classificationSvc.Health(ctx)

	assert.Greater(t, newHits.Load(), before,
		"after an llm.base_url reload the CLASSIFICATION path must use the new provider")
	assert.Equal(t, "gpt-4o-mini", registry.llmClient.GetConfig().Model)
	assert.Empty(t, registry.RestartWarnings(),
		"a provider/model swap is applied for real, so it must not warn")
}
