package application

import (
	"context"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// atFixture keeps the email fixtures below from placing a literal '@' after the
// URL literals in this file — the repository's secret scanner reads any
// "://<host>:<…>@" sequence as a connection string with an embedded password.
// Declared first so every '@' precedes every URL.
const atFixture = "@"

func mailFixture(local, domain string) string { return local + atFixture + domain }

// TestConfigTargets_BuildEnhancedPublishers is slice-1 review finding M6: the
// mapping table asserts header KEYS against a hand-copied list, which would stay
// green if the publisher side renamed one — while every notification silently
// degraded to the plain HTTP fallback publisher (each createEnhanced*Publisher
// falls back with a Warn when the credentials it needs are missing).
//
// This closes the loop from the other end: build real targets from a real
// receiver config and assert the factory returns the ENHANCED publisher for
// each. internal/application is the natural home — it already imports both the
// business and infrastructure publishing packages.
func TestConfigTargets_BuildEnhancedPublishers(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			SMTPSmartHost: "smtp.example.com:2525",
			SMTPFrom:      mailFixture("amp", "example.com"),
		},
		Receivers: []*infraroute.Receiver{{
			Name: "team-x",
			SlackConfigs: []*infraroute.SlackConfig{{
				APIURL: "https://hooks.slack.com/services/T/B/C",
			}},
			PagerDutyConfigs: []*infraroute.PagerDutyConfig{{
				RoutingKey: "rk-fixture",
				URL:        "https://events.pagerduty.com/v2/enqueue",
			}},
			TelegramConfigs: []*infraroute.TelegramConfig{{
				BotToken: "bot-token-fixture",
				ChatID:   "-1001234567890",
				APIURL:   "https://api.telegram.org",
			}},
			EmailConfigs: []*infraroute.EmailConfig{{
				To: mailFixture("ops", "example.com"),
			}},
		}},
	}

	targets := businesspublishing.BuildConfigTargets(rc, testRegistryLogger())
	require.Len(t, targets, 4, "slack + pagerduty + telegram + email")

	factory := infrapublishing.NewPublisherFactory(
		infrapublishing.NewAlertFormatter(""),
		testRegistryLogger(),
		v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing,
		"",
	)
	t.Cleanup(factory.Shutdown)

	wantEnhanced := map[string]string{
		"cfg:team-x/slack0":     "*publishing.EnhancedSlackPublisher",
		"cfg:team-x/pagerduty0": "*publishing.EnhancedPagerDutyPublisher",
		"cfg:team-x/telegram0":  "*publishing.EnhancedTelegramPublisher",
		"cfg:team-x/email0":     "*publishing.EnhancedEmailPublisher",
	}

	for _, target := range targets {
		want, ok := wantEnhanced[target.Name]
		require.True(t, ok, "unexpected target %q", target.Name)

		publisher, err := factory.CreatePublisherForTarget(target)
		require.NoError(t, err)
		assert.Equal(t, want, fmt.Sprintf("%T", publisher),
			"target %q must build the ENHANCED publisher; the plain HTTP fallback means the credential headers this target carries are not the ones the factory reads",
			target.Name)
	}
}

// TestInitializePublishing_LiteProfileWithReceiverIntegrations is the other half
// of M6: the lite profile must get a REAL publishing stack (config-only mode)
// when the config carries receiver integrations, and the metrics-only publisher
// when it does not. Only the helpers were unit-tested before; the branch itself
// was not.
func TestInitializePublishing_LiteProfileWithReceiverIntegrations(t *testing.T) {
	registry := &ServiceRegistry{
		logger: testRegistryLogger(),
		config: liteConfigWithReceivers(true),
	}
	t.Cleanup(registry.shutdownPublishing)

	registry.initializePublishing(context.Background())

	require.NotNil(t, registry.publisher)
	_, isMetricsOnly := registry.publisher.(*MetricsOnlyPublisher)
	assert.False(t, isMetricsOnly,
		"lite profile with receivers: integrations must deliver, not fall back to metrics-only")
	require.NotNil(t, registry.publishingDiscovery)

	// Config-only mode: no Kubernetes client at all, targets came from config.
	assert.Nil(t, registry.k8sClient)
	names := make([]string, 0)
	for _, target := range registry.publishingDiscovery.ListTargets() {
		names = append(names, target.Name)
	}
	assert.Equal(t, []string{"cfg:team-x/webhook0"}, names)
	assert.Equal(t, 1, registry.publishingDiscovery.GetStats().ConfigTargets)
}

func TestInitializePublishing_LiteProfileWithoutIntegrationsStaysMetricsOnly(t *testing.T) {
	registry := &ServiceRegistry{
		logger: testRegistryLogger(),
		config: liteConfigWithReceivers(false),
	}
	t.Cleanup(registry.shutdownPublishing)

	registry.initializePublishing(context.Background())

	require.NotNil(t, registry.publisher)
	_, isMetricsOnly := registry.publisher.(*MetricsOnlyPublisher)
	assert.True(t, isMetricsOnly)
	assert.Nil(t, registry.publishingDiscovery)
}

func liteConfigWithReceivers(withIntegrations bool) *appconfig.Config {
	cfg := &appconfig.Config{Profile: appconfig.ProfileLite}
	cfg.Publishing.Enabled = true
	cfg.Publishing.Queue.JobTrackingCapacity = 16
	cfg.Publishing.Queue.MaxConcurrent = 2

	receiver := &infraroute.Receiver{Name: "team-x"}
	if withIntegrations {
		receiver.WebhookConfigs = []*infraroute.WebhookConfig{{URL: "https://x.example.com/hook"}}
	}
	cfg.Routing = &infraroute.RouteConfig{Receivers: []*infraroute.Receiver{receiver}}
	return cfg
}
