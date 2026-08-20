package config

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLLMFixture() (*LLMReloadable, *llm.HTTPLLMClient, *RestartWarnings) {
	base := llm.DefaultConfig()
	base.Provider = "proxy"
	base.Model = "gpt-4o"
	base.BaseURL = "http://llm-old.internal"
	base.Timeout = 30 * time.Second
	base.CircuitBreaker.Enabled = false

	client := llm.NewHTTPLLMClient(base, slog.Default())
	warnings := NewRestartWarnings()
	return NewLLMReloadable(client, warnings, slog.Default()), client, warnings
}

func TestLLMReloadable_Contract(t *testing.T) {
	reloadable, _, _ := newLLMFixture()

	assert.Equal(t, "llm", reloadable.Name())
	assert.Equal(t, []string{"llm"}, reloadable.RelevantSections())
	assert.False(t, reloadable.IsCritical())
	assert.Equal(t, 50, reloadable.ReloadPriority())
}

func TestLLMReloadable_SwapsModelAndProvider(t *testing.T) {
	reloadable, client, warnings := newLLMFixture()

	oldCfg := &Config{LLM: LLMConfig{Enabled: true, Provider: "proxy", Model: "gpt-4o", BaseURL: "http://llm-old.internal"}}
	newCfg := &Config{LLM: LLMConfig{
		Enabled:    true,
		Provider:   "openai",
		Model:      "gpt-4o-mini",
		BaseURL:    "http://llm-new.internal",
		APIKey:     "rotated",
		MaxTokens:  2048,
		Timeout:    45 * time.Second,
		MaxRetries: 5,
	}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	live := client.GetConfig()
	assert.Equal(t, "openai", live.Provider)
	assert.Equal(t, "gpt-4o-mini", live.Model)
	assert.Equal(t, "http://llm-new.internal", live.BaseURL)
	assert.Equal(t, "rotated", live.APIKey)
	assert.Equal(t, 2048, live.MaxTokens)
	assert.Equal(t, 45*time.Second, live.Timeout)
	assert.Equal(t, 5, live.MaxRetries)
	assert.Empty(t, warnings.List(), "provider/model changes are real, so they must not warn")
}

func TestLLMReloadable_PreservesFieldsAMPConfigDoesNotOwn(t *testing.T) {
	// RetryDelay/RetryBackoff/EnableMetrics/CircuitBreaker are not in AMP's
	// llm.* YAML; a reload must not reset them to llm.DefaultConfig().
	base := llm.DefaultConfig()
	base.RetryDelay = 7 * time.Second
	base.RetryBackoff = 3.5
	base.EnableMetrics = false
	base.CircuitBreaker.Enabled = false
	client := llm.NewHTTPLLMClient(base, slog.Default())
	reloadable := NewLLMReloadable(client, NewRestartWarnings(), slog.Default())

	oldCfg := &Config{LLM: LLMConfig{Model: "gpt-4o"}}
	newCfg := &Config{LLM: LLMConfig{Model: "claude"}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	live := client.GetConfig()
	assert.Equal(t, "claude", live.Model)
	assert.Equal(t, 7*time.Second, live.RetryDelay)
	assert.InDelta(t, 3.5, live.RetryBackoff, 0.0001)
	assert.False(t, live.EnableMetrics)
}

func TestLLMReloadable_ZeroTimeoutAndRetriesKeepPreviousValues(t *testing.T) {
	reloadable, client, _ := newLLMFixture()
	before := client.GetConfig()

	oldCfg := &Config{LLM: LLMConfig{Model: "gpt-4o"}}
	newCfg := &Config{LLM: LLMConfig{Model: "gpt-5"}} // Timeout/MaxRetries left at zero

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	live := client.GetConfig()
	assert.Equal(t, before.Timeout, live.Timeout)
	assert.Equal(t, before.MaxRetries, live.MaxRetries)
}

func TestLLMReloadable_EnabledAndAgentModeWarnW604(t *testing.T) {
	reloadable, client, warnings := newLLMFixture()

	oldCfg := &Config{LLM: LLMConfig{Enabled: true, AgentMode: false, Model: "gpt-4o"}}
	newCfg := &Config{LLM: LLMConfig{Enabled: false, AgentMode: true, Model: "gpt-4o"}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnLLMRestartRequired, list[0].Code)
	assert.ElementsMatch(t, []string{"llm.enabled", "llm.agent_mode"}, list[0].Fields)

	// The transport half is still applied (the model did not change here, but
	// the client must not have been left in a broken state either).
	assert.Equal(t, "gpt-4o", client.GetConfig().Model)
}

func TestLLMReloadable_UnchangedSectionIsNoOp(t *testing.T) {
	reloadable, client, warnings := newLLMFixture()
	before := client.GetConfig()

	cfg := &Config{LLM: LLMConfig{Model: "gpt-4o"}}
	require.NoError(t, reloadable.Reload(context.Background(), cfg, cfg))

	assert.Equal(t, before.Model, client.GetConfig().Model)
	assert.Empty(t, warnings.List())
}

func TestLLMReloadable_NilClientWarnsInsteadOfPretending(t *testing.T) {
	warnings := NewRestartWarnings()
	reloadable := NewLLMReloadable(nil, warnings, slog.Default())

	oldCfg := &Config{LLM: LLMConfig{Model: "gpt-4o"}}
	newCfg := &Config{LLM: LLMConfig{Model: "gpt-5"}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnLLMRestartRequired, list[0].Code)
	assert.Contains(t, list[0].Reason, "no LLM client")
}

// TestLLMReloadable_ConcurrentReloadAndRead is the -race guard: the
// investigation pipeline reads the client's config on worker goroutines while
// a SIGHUP reload swaps it.
func TestLLMReloadable_ConcurrentReloadAndRead(t *testing.T) {
	reloadable, client, _ := newLLMFixture()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cfg := client.GetConfig()
				// A snapshot must always be internally coherent: these two
				// fields are always written together by the reload below.
				if cfg.Model == "model-b" {
					assert.Equal(t, "provider-b", cfg.Provider)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			a := &Config{LLM: LLMConfig{Provider: "provider-a", Model: "model-a"}}
			b := &Config{LLM: LLMConfig{Provider: "provider-b", Model: "model-b"}}
			_ = reloadable.Reload(context.Background(), a, b)
			_ = reloadable.Reload(context.Background(), b, a)
		}
	}()
	wg.Wait()
}
