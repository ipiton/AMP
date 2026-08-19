package application

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

func testRegistryLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func webhookReceiver(name, url string) *infraroute.Receiver {
	return &infraroute.Receiver{
		Name:           name,
		WebhookConfigs: []*infraroute.WebhookConfig{{URL: url}},
	}
}

func targetNames(t *testing.T, discovery businesspublishing.TargetDiscoveryManager) []string {
	t.Helper()
	names := make([]string, 0)
	for _, target := range discovery.ListTargets() {
		names = append(names, target.Name)
	}
	return names
}

// TestApplyConfigTargets_StartupAndReload is slice 1 item 3: config targets are
// provisioned from the loaded config, and a receivers-only edit followed by a
// reload swaps them — which is what makes the reload path (routing fingerprint)
// change delivery and not just routing.
func TestApplyConfigTargets_StartupAndReload(t *testing.T) {
	discovery := businesspublishing.NewConfigOnlyTargetDiscoveryManager(testRegistryLogger(), nil)

	registry := &ServiceRegistry{
		logger:              testRegistryLogger(),
		publishingDiscovery: discovery,
		config: &appconfig.Config{
			Routing: &infraroute.RouteConfig{
				Receivers: []*infraroute.Receiver{
					webhookReceiver("team-x", "https://x.example.com/hook"),
				},
			},
		},
	}

	// Startup.
	registry.applyConfigTargets()
	assert.Equal(t, []string{"cfg:team-x/webhook0"}, targetNames(t, discovery))

	// Receivers-only edit + reload: a second receiver appears, the first one's
	// endpoint changes.
	registry.config = &appconfig.Config{
		Routing: &infraroute.RouteConfig{
			Receivers: []*infraroute.Receiver{
				webhookReceiver("team-x", "https://x2.example.com/hook"),
				{
					Name:            "team-y",
					TelegramConfigs: []*infraroute.TelegramConfig{{BotToken: "tok", ChatID: "-100"}},
				},
			},
		},
	}
	registry.applyConfigTargets()

	assert.ElementsMatch(t, []string{"cfg:team-x/webhook0", "cfg:team-y/telegram0"}, targetNames(t, discovery))

	updated, err := discovery.GetTarget("cfg:team-x/webhook0")
	require.NoError(t, err)
	assert.Equal(t, "https://x2.example.com/hook", updated.URL, "reload must swap in the new endpoint")

	tg, err := discovery.GetTarget("cfg:team-y/telegram0")
	require.NoError(t, err)
	assert.Equal(t, []string{"team-y"}, tg.Receivers)
	assert.Equal(t, "tok", tg.Headers["bot_token"])

	// All receivers removed -> the set is cleared, not left stale.
	registry.config = &appconfig.Config{}
	registry.applyConfigTargets()
	assert.Empty(t, targetNames(t, discovery))
}

// TestApplyConfigTargets_NoDiscoveryIsNoop covers publishing disabled /
// metrics-only: no discovery manager wired at all.
func TestApplyConfigTargets_NoDiscoveryIsNoop(t *testing.T) {
	registry := &ServiceRegistry{
		logger: testRegistryLogger(),
		config: &appconfig.Config{
			Routing: &infraroute.RouteConfig{
				Receivers: []*infraroute.Receiver{webhookReceiver("team-x", "https://x.example.com")},
			},
		},
	}

	require.NotPanics(t, registry.applyConfigTargets)
}

// TestHasConfigProvisionedTargets decides whether a lite-profile deployment
// gets a real publishing stack (config-only mode) or the metrics-only
// publisher.
func TestHasConfigProvisionedTargets(t *testing.T) {
	tests := []struct {
		name string
		cfg  *appconfig.Config
		want bool
	}{
		{name: "nil config", cfg: nil, want: false},
		{name: "no route tree", cfg: &appconfig.Config{}, want: false},
		{
			name: "receivers without integrations",
			cfg: &appconfig.Config{Routing: &infraroute.RouteConfig{
				Receivers: []*infraroute.Receiver{{Name: "team-x"}},
			}},
			want: false,
		},
		{
			name: "receiver with a webhook integration",
			cfg: &appconfig.Config{Routing: &infraroute.RouteConfig{
				Receivers: []*infraroute.Receiver{webhookReceiver("team-x", "https://x.example.com")},
			}},
			want: true,
		},
		{
			name: "nil receiver entry is skipped",
			cfg: &appconfig.Config{Routing: &infraroute.RouteConfig{
				Receivers: []*infraroute.Receiver{nil},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasConfigProvisionedTargets(tt.cfg))
		})
	}
}
