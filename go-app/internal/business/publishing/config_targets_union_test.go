package publishing

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// syncBuffer is a mutex-guarded log sink (slog handlers may be written from
// several goroutines in these tests).
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newBufferLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// k8sTarget builds a Secret-shaped target (DNS-1123 name, no cfg: prefix).
func k8sTarget(name string, targetType string, receivers []string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:         name,
		Type:         targetType,
		URL:          "https://" + name + ".example.com/hook",
		Enabled:      true,
		Format:       core.FormatWebhook,
		Headers:      map[string]string{},
		FilterConfig: map[string]any{},
		Receivers:    receivers,
	}
}

func configOnlyManager(t *testing.T) *DefaultTargetDiscoveryManager {
	t.Helper()
	return NewConfigOnlyTargetDiscoveryManager(quietLogger(), nil)
}

// TestUnionView_ListGetByTypeAndStats is slice 1 item 2: both sources land in
// the SAME discovery view (R3).
func TestUnionView_ListGetByTypeAndStats(t *testing.T) {
	manager := configOnlyManager(t)

	// K8s-sourced half, injected straight into the Secret cache the way
	// DiscoverTargets would.
	manager.cache.Set([]*core.PublishingTarget{
		k8sTarget("legacy-webhook", "webhook", nil),
		k8sTarget("slack-prod", "slack", []string{"team-x"}),
	})
	manager.mu.Lock()
	manager.stats.TotalTargets = 2
	manager.stats.ValidTargets = 2
	manager.mu.Unlock()

	// Config-sourced half.
	configTargets := []*core.PublishingTarget{
		{Name: ConfigTargetName("team-x", configKindWebhook, 0), Type: "webhook", URL: "https://a.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
		{Name: ConfigTargetName("team-y", configKindSlack, 0), Type: "slack", URL: "https://hooks.slack.com/y", Enabled: true, Format: core.FormatSlack, Receivers: []string{"team-y"}},
	}
	manager.SetConfigTargets(configTargets)

	// ListTargets: the union.
	names := map[string]bool{}
	for _, target := range manager.ListTargets() {
		names[target.Name] = true
	}
	assert.Equal(t, map[string]bool{
		"legacy-webhook":      true,
		"slack-prod":          true,
		"cfg:team-x/webhook0": true,
		"cfg:team-y/slack0":   true,
	}, names)

	// GetTarget: reachable from both halves; unknown names still 404.
	got, err := manager.GetTarget("cfg:team-y/slack0")
	require.NoError(t, err)
	assert.Equal(t, "slack", got.Type)
	got, err = manager.GetTarget("legacy-webhook")
	require.NoError(t, err)
	assert.Equal(t, "webhook", got.Type)
	_, err = manager.GetTarget("cfg:nope/webhook0")
	assert.Error(t, err)

	// GetTargetsByType: both halves filtered.
	webhooks := manager.GetTargetsByType("webhook")
	require.Len(t, webhooks, 2)
	slacks := manager.GetTargetsByType("slack")
	require.Len(t, slacks, 2)

	// Stats: K8s counters keep their Secret-only meaning, config targets are
	// reported separately.
	stats := manager.GetStats()
	assert.Equal(t, 2, stats.ValidTargets)
	assert.Equal(t, 2, stats.ConfigTargets)

	// Collector surfaces the source split.
	metrics, err := NewDiscoveryMetricsCollector(manager).Collect(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2.0, metrics[`targets_by_source{source="config"}`])
	assert.Equal(t, 2.0, metrics[`targets_by_source{source="k8s"}`])
	assert.Equal(t, 2.0, metrics["targets_config"])
}

// TestDiscoverTargets_ConfigOnlyModeKeepsConfigTargets guards the exact hazard
// that motivated a second cache: a K8s refresh (or the config-only no-op) must
// not wipe the config-provisioned set.
func TestDiscoverTargets_ConfigOnlyModeKeepsConfigTargets(t *testing.T) {
	manager := configOnlyManager(t)
	require.True(t, manager.IsConfigOnly())

	manager.SetConfigTargets([]*core.PublishingTarget{
		{Name: ConfigTargetName("team-x", configKindWebhook, 0), Type: "webhook", URL: "https://a.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
	})

	require.NoError(t, manager.DiscoverTargets(context.Background()))
	require.NoError(t, manager.Health(context.Background()))

	targets := manager.ListTargets()
	require.Len(t, targets, 1)
	assert.Equal(t, "cfg:team-x/webhook0", targets[0].Name)
	assert.False(t, manager.GetStats().LastDiscovery.IsZero())
}

// TestSetConfigTargets_ReloadSwapIsAtomic is slice 1 item 4: a reload must
// never expose a window with zero config targets. A reader goroutine hammers
// ListTargets while writers swap the set; every observation must be a complete
// generation, never empty and never a mix.
func TestSetConfigTargets_ReloadSwapIsAtomic(t *testing.T) {
	manager := configOnlyManager(t)

	genA := []*core.PublishingTarget{
		{Name: ConfigTargetName("team-x", configKindWebhook, 0), Type: "webhook", URL: "https://a1.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
		{Name: ConfigTargetName("team-x", configKindWebhook, 1), Type: "webhook", URL: "https://a2.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
	}
	genB := []*core.PublishingTarget{
		{Name: ConfigTargetName("team-x", configKindWebhook, 0), Type: "webhook", URL: "https://b1.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
		{Name: ConfigTargetName("team-x", configKindWebhook, 1), Type: "webhook", URL: "https://b2.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
	}
	manager.SetConfigTargets(genA)

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				manager.SetConfigTargets(genB)
			} else {
				manager.SetConfigTargets(genA)
			}
		}
	}()

	observations := make([]int, 0, iterations)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			targets := manager.ListTargets()
			observations = append(observations, len(targets))
			// Every observed generation must be internally consistent: both
			// URLs from the same generation.
			if len(targets) == 2 {
				a := targets[0].URL[:len("https://a")]
				b := targets[1].URL[:len("https://a")]
				assert.Equal(t, a, b, "observed a mixed generation: %s / %s", targets[0].URL, targets[1].URL)
			}
		}
	}()

	wg.Wait()

	for i, count := range observations {
		require.Equal(t, 2, count, "observation %d saw %d targets: a reload exposed an incomplete set", i, count)
	}
}

// TestSetConfigTargets_ClearsWhenReceiversRemoved covers the other reload
// direction: all integrations deleted from the config.
func TestSetConfigTargets_ClearsWhenReceiversRemoved(t *testing.T) {
	manager := configOnlyManager(t)
	manager.SetConfigTargets([]*core.PublishingTarget{
		{Name: ConfigTargetName("team-x", configKindWebhook, 0), Type: "webhook", URL: "https://a.example.com", Enabled: true, Format: core.FormatAlertmanager, Receivers: []string{"team-x"}},
	})
	require.Len(t, manager.ListTargets(), 1)

	manager.SetConfigTargets(nil)
	assert.Empty(t, manager.ListTargets())
	assert.Equal(t, 0, manager.GetStats().ConfigTargets)
	_, err := manager.GetTarget("cfg:team-x/webhook0")
	assert.Error(t, err)
}

// TestSetConfigTargets_DoesNotLogSecrets: the update log line names targets
// only — their URLs and credential headers must never reach the log.
func TestSetConfigTargets_DoesNotLogSecrets(t *testing.T) {
	var buf syncBuffer
	manager := NewConfigOnlyTargetDiscoveryManager(newBufferLogger(&buf), nil)

	const botToken = "bot-token-fixture-value"
	const url = "https://api.telegram.example/secret-path"
	manager.SetConfigTargets([]*core.PublishingTarget{{
		Name:      ConfigTargetName("team-x", configKindTelegram, 0),
		Type:      "telegram",
		URL:       url,
		Enabled:   true,
		Format:    core.FormatTelegram,
		Headers:   map[string]string{"bot_token": botToken, "chat_id": "-100"},
		Receivers: []string{"team-x"},
	}})

	logged := buf.String()
	assert.Contains(t, logged, "cfg:team-x/telegram0", "target names are safe and needed for triage")
	assert.NotContains(t, logged, botToken)
	assert.NotContains(t, logged, url)
}
