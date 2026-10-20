package routing

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SLACK-COLOR-TEMPLATE-REJECTED: since TEMPLATES-EPIC slice 2 these
// presentation fields are rendered by the template engine at send time, but
// their struct tags still checked the LITERAL format, so a templated value
// (upstream's own slack color default among them) failed the whole config
// load. A template is now accepted as is; a literal is still checked.

// configWithSlackField returns a config whose only slack_config sets field
// (a YAML key) to value.
func configWithSlackField(field, value string) string {
	return fmt.Sprintf(`
route:
  receiver: slack
receivers:
  - name: slack
    slack_configs:
      - api_url: https://hooks.slack.com/services/T/B/C
        %s: %q
`, field, value)
}

// configWithPagerDutySeverity returns a config whose only pagerduty_config
// sets severity to value.
func configWithPagerDutySeverity(value string) string {
	return fmt.Sprintf(`
route:
  receiver: pd
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key: 0123456789abcdef0123456789abcdef
        severity: %q
`, value)
}

func TestTemplatedPresentationFields_AreAcceptedAtLoad(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value string
		got   func(*RouteConfig) string
	}{
		{"color upstream default", "color", `{{ if eq .Status "firing" }}danger{{ else }}good{{ end }}`, slackColor},
		{"color sema-prod", "color", `{{ if eq .Status "firing" }}warning{{ else }}good{{ end }}`, slackColor},
		{"color named template", "color", `{{ template "slack.default.color" . }}`, slackColor},
		{"channel", "channel", `{{ .CommonLabels.channel }}`, func(c *RouteConfig) string { return c.Receivers[0].SlackConfigs[0].Channel }},
		{"channel with literal prefix", "channel", `#alerts-{{ .CommonLabels.team }}`, func(c *RouteConfig) string { return c.Receivers[0].SlackConfigs[0].Channel }},
		{"icon_emoji", "icon_emoji", `{{ if eq .Status "firing" }}:fire:{{ else }}:ok:{{ end }}`, func(c *RouteConfig) string { return c.Receivers[0].SlackConfigs[0].IconEmoji }},
		{"icon_url", "icon_url", `{{ template "slack.default.iconurl" . }}`, func(c *RouteConfig) string { return c.Receivers[0].SlackConfigs[0].IconURL }},
		{"title_link", "title_link", `{{ template "slack.default.titlelink" . }}`, func(c *RouteConfig) string { return c.Receivers[0].SlackConfigs[0].TitleLink }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewRouteConfigParser().Parse([]byte(configWithSlackField(tc.field, tc.value)))
			require.NoError(t, err, "templated %s must load", tc.field)
			assert.Equal(t, tc.value, tc.got(cfg), "the expression must reach the runtime verbatim")
		})
	}

	t.Run("pagerduty severity", func(t *testing.T) {
		value := `{{ .CommonLabels.severity }}`
		cfg, err := NewRouteConfigParser().Parse([]byte(configWithPagerDutySeverity(value)))
		require.NoError(t, err, "templated severity must load")
		assert.Equal(t, value, cfg.Receivers[0].PagerDutyConfigs[0].Severity)
	})
}

func slackColor(c *RouteConfig) string { return c.Receivers[0].SlackConfigs[0].Color }

func TestTemplatedPresentationFields_LiteralsAreStillValidated(t *testing.T) {
	invalid := []struct {
		field, value, namespace string
	}{
		{"color", "red", "Color"},
		{"color", "#12345", "Color"},
		{"color", "{ not a template }", "Color"},
		{"channel", "ops", "Channel"},
		{"icon_emoji", "fire", "IconEmoji"},
		{"icon_url", "not a url", "IconURL"},
		{"title_link", "nope", "TitleLink"},
	}
	for _, tc := range invalid {
		t.Run("rejects "+tc.field+"="+tc.value, func(t *testing.T) {
			_, err := NewRouteConfigParser().Parse([]byte(configWithSlackField(tc.field, tc.value)))
			require.Error(t, err, "literal %s=%q must still be rejected", tc.field, tc.value)
			assert.Contains(t, err.Error(), "SlackConfigs[0]."+tc.namespace)
		})
	}

	t.Run("rejects severity=bogus", func(t *testing.T) {
		_, err := NewRouteConfigParser().Parse([]byte(configWithPagerDutySeverity("bogus")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PagerDutyConfigs[0].Severity")
	})

	valid := []struct{ field, value string }{
		{"color", "danger"},
		{"color", "#FF0000"},
		{"channel", "#ops"},
		{"channel", "@oncall"},
		{"icon_emoji", ":fire:"},
		{"icon_url", "https://example.com/icon.png"},
		{"title_link", "https://example.com/alerts"},
	}
	for _, tc := range valid {
		t.Run("accepts "+tc.field+"="+tc.value, func(t *testing.T) {
			_, err := NewRouteConfigParser().Parse([]byte(configWithSlackField(tc.field, tc.value)))
			require.NoError(t, err)
		})
	}

	t.Run("accepts severity=critical", func(t *testing.T) {
		_, err := NewRouteConfigParser().Parse([]byte(configWithPagerDutySeverity("critical")))
		require.NoError(t, err)
	})
}
