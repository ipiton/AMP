package upstreamtargets

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/prometheus/alertmanager/config"
	amcommoncfg "github.com/prometheus/alertmanager/config/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

func defaultGlobal() *config.GlobalConfig {
	g := config.DefaultGlobalConfig()
	return &g
}

func mustConvert(t *testing.T, target *core.PublishingTarget) integration {
	t.Helper()
	in, err := convert(target, defaultGlobal())
	require.NoError(t, err)
	return in
}

func skipReason(t *testing.T, err error) string {
	t.Helper()
	var se *skipError
	require.True(t, errors.As(err, &se), "want a skipError, got %v", err)
	return se.reason
}

func TestConvertWebhook(t *testing.T) {
	in := mustConvert(t, &core.PublishingTarget{Name: "hook", Type: "webhook", URL: "https://hooks.example/a"})
	require.NotNil(t, in.webhook)
	assert.Equal(t, "https://hooks.example/a", string(in.webhook.URL))
	assert.True(t, in.webhook.SendResolved())
	// Upstream's default, from the global http_config.
	require.NotNil(t, in.webhook.HTTPConfig)
	assert.True(t, in.webhook.HTTPConfig.FollowRedirects)
}

func TestConvertRootly(t *testing.T) {
	in := mustConvert(t, &core.PublishingTarget{
		Name: "rootly", Type: "rootly",
		URL:     "https://webhooks.rootly.com/webhooks/incoming/alertmanager_webhooks",
		Headers: map[string]string{"Authorization": "Bearer s3cret"},
	})
	require.NotNil(t, in.webhook)
	require.NotNil(t, in.webhook.HTTPConfig.Authorization)
	assert.Equal(t, "Bearer", in.webhook.HTTPConfig.Authorization.Type)
	assert.Equal(t, "s3cret", string(in.webhook.HTTPConfig.Authorization.Credentials))

	for _, u := range []string{"https://api.rootly.com/v1/incidents", "https://API.rootly.io/v1/alerts"} {
		_, err := convert(&core.PublishingTarget{Name: "r", Type: "rootly", URL: u}, defaultGlobal())
		assert.Equal(t, reasonRootlyREST, skipReason(t, err), u)
	}
}

func TestConvertSlack(t *testing.T) {
	in := mustConvert(t, &core.PublishingTarget{
		Name: "slack", Type: "slack", URL: "https://hooks.slack.com/services/T/B/X",
		Templates: map[string]string{"channel": "#ops", "title": "{{ .CommonLabels.alertname }}", "unknown": "x"},
	})
	require.NotNil(t, in.slack)
	assert.Equal(t, "https://hooks.slack.com/services/T/B/X", in.slack.APIURL.String())
	assert.Equal(t, "#ops", in.slack.Channel)
	assert.Equal(t, "{{ .CommonLabels.alertname }}", in.slack.Title)
	// Upstream's default template for a field the target leaves out.
	assert.NotEmpty(t, in.slack.Text)
	require.NotNil(t, in.slack.HTTPConfig)
}

func TestConvertPagerDuty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		url     string
		headers map[string]string
		key     string
	}{
		{"base URL", "https://events.pagerduty.com", map[string]string{"routing_key": "rk"}, "rk"},
		{"trailing slash", "https://events.pagerduty.com/", map[string]string{"routing_key": "rk"}, "rk"},
		{"legacy events path", "https://events.pagerduty.com/v2/events", map[string]string{"routing_key": "rk"}, "rk"},
		{"enqueue path", "https://events.pagerduty.com/v2/enqueue/", map[string]string{"routing_key": "rk"}, "rk"},
		{"change path", "https://events.pagerduty.com/v2/change/enqueue", map[string]string{"routing_key": "rk"}, "rk"},
		{"empty URL uses the global", "", map[string]string{"routing_key": "rk"}, "rk"},
		{"Bearer Authorization", "https://events.pagerduty.com", map[string]string{"Authorization": "Bearer ak"}, "ak"},
		{"raw Authorization", "https://events.pagerduty.com", map[string]string{"authorization": "ak"}, "ak"},
		{"routing_key wins over Authorization", "https://events.pagerduty.com", map[string]string{"routing_key": "rk", "Authorization": "raw-key"}, "rk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := mustConvert(t, &core.PublishingTarget{Name: "pd", Type: "pagerduty", URL: tc.url, Headers: tc.headers})
			require.NotNil(t, in.pagerduty)
			assert.Equal(t, "https://events.pagerduty.com/v2/enqueue", in.pagerduty.URL.String())
			assert.Equal(t, tc.key, string(in.pagerduty.RoutingKey))
			// The routing key never goes out as an HTTP header.
			assert.Nil(t, in.pagerduty.HTTPConfig.Authorization)
			assert.Empty(t, in.pagerduty.HTTPConfig.HTTPHeaders)
		})
	}

	t.Run("templates and details", func(t *testing.T) {
		in := mustConvert(t, &core.PublishingTarget{
			Name: "pd", Type: "pagerduty", URL: "https://events.pagerduty.com",
			Headers:   map[string]string{"routing_key": "rk"},
			Templates: map[string]string{"severity": "critical", "details.team": "db", "details.": "ignored"},
		})
		assert.Equal(t, "critical", in.pagerduty.Severity)
		assert.Equal(t, "db", in.pagerduty.Details["team"])
	})

	t.Run("no routing key", func(t *testing.T) {
		_, err := convert(&core.PublishingTarget{Name: "pd", Type: "pagerduty", URL: "https://events.pagerduty.com"}, defaultGlobal())
		assert.Equal(t, reasonInvalid, skipReason(t, err))
	})
}

func TestConvertUnsupportedType(t *testing.T) {
	_, err := convert(&core.PublishingTarget{Name: "tg", Type: "telegram", URL: "https://t.example"}, defaultGlobal())
	assert.Equal(t, reasonUnsupportedType, skipReason(t, err))
}

func TestSendResolved(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{nil, true}, {true, true}, {false, false},
		{"false", false}, {"FALSE", false}, {"0", false}, {" f ", false},
		{"true", true}, {"nonsense", true},
		{float64(0), false}, {float64(1), true}, {0, false}, {2, true},
		{[]any{}, true},
	} {
		target := &core.PublishingTarget{FilterConfig: map[string]any{}}
		if tc.value != nil {
			target.FilterConfig["send_resolved"] = tc.value
		}
		assert.Equal(t, tc.want, sendResolved(target), "%#v", tc.value)
	}

	in := mustConvert(t, &core.PublishingTarget{
		Name: "hook", Type: "webhook", URL: "https://hooks.example/a",
		FilterConfig: map[string]any{"send_resolved": "false"},
	})
	assert.False(t, in.webhook.SendResolved())
}

func TestConvertHeaders(t *testing.T) {
	t.Run("plain headers become secret http_headers", func(t *testing.T) {
		in := mustConvert(t, &core.PublishingTarget{
			Name: "hook", Type: "webhook", URL: "https://hooks.example/a",
			Headers: map[string]string{"X-Api-Key": "k1"},
		})
		h := in.webhook.HTTPConfig.HTTPHeaders
		require.NotNil(t, h)
		require.Contains(t, h.Headers, "X-Api-Key")
		require.Len(t, h.Headers["X-Api-Key"].Secrets, 1)
		assert.Equal(t, "k1", string(h.Headers["X-Api-Key"].Secrets[0]))
		assert.Empty(t, in.dropped)
	})

	t.Run("reserved headers are dropped, the target kept", func(t *testing.T) {
		in := mustConvert(t, &core.PublishingTarget{
			Name: "hook", Type: "webhook", URL: "https://hooks.example/a",
			Headers: map[string]string{"content-type": "application/json", "User-Agent": "amp", "X-Ok": "1"},
		})
		assert.Equal(t, []string{"Content-Type", "User-Agent"}, in.dropped)
		assert.Contains(t, in.webhook.HTTPConfig.HTTPHeaders.Headers, "X-Ok")
	})

	t.Run("Basic Authorization becomes basic_auth", func(t *testing.T) {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
			in := mustConvert(t, &core.PublishingTarget{
				Name: "hook", Type: "webhook", URL: "https://hooks.example/a",
				Headers: map[string]string{"Authorization": "Basic " + enc.EncodeToString([]byte("user:pa:ss1"))},
			})
			ba := in.webhook.HTTPConfig.BasicAuth
			require.NotNil(t, ba)
			assert.Equal(t, "user", ba.Username)
			assert.Equal(t, "pa:ss1", string(ba.Password))
			assert.Nil(t, in.webhook.HTTPConfig.Authorization)
		}
	})

	t.Run("http_config fields map to upstream", func(t *testing.T) {
		follow := false
		in := mustConvert(t, &core.PublishingTarget{
			Name: "hook", Type: "webhook", URL: "https://hooks.example/a",
			HTTPConfig: &core.HTTPClientConfig{
				FollowRedirects: &follow,
				Authorization:   &core.AuthorizationConfig{Type: "Bearer", Credentials: "tok"},
			},
		})
		assert.False(t, in.webhook.HTTPConfig.FollowRedirects)
		assert.Equal(t, "tok", string(in.webhook.HTTPConfig.Authorization.Credentials))
	})

	for name, target := range map[string]*core.PublishingTarget{
		"Authorization without a scheme": {Headers: map[string]string{"Authorization": "token"}},
		"Authorization and http_config authorization": {
			Headers:    map[string]string{"Authorization": "Bearer a"},
			HTTPConfig: &core.HTTPClientConfig{Authorization: &core.AuthorizationConfig{Type: "Bearer", Credentials: "b"}},
		},
		"Basic not base64":    {Headers: map[string]string{"Authorization": "Basic !!!"}},
		"Basic without colon": {Headers: map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("user"))}},
	} {
		t.Run(name+" is skipped", func(t *testing.T) {
			target.Name, target.Type, target.URL = "hook", "webhook", "https://hooks.example/a"
			_, err := convert(target, defaultGlobal())
			assert.Equal(t, reasonInvalid, skipReason(t, err))
		})
	}
}

func TestConvertUsesGlobals(t *testing.T) {
	g := defaultGlobal()
	appURL, err := amcommoncfg.ParseURL("https://slack.example/app")
	require.NoError(t, err)
	g.SlackAppURL = appURL
	in, err := convert(&core.PublishingTarget{Name: "s", Type: "slack", URL: "https://hooks.slack.com/x"}, g)
	require.NoError(t, err)
	assert.Equal(t, "https://slack.example/app", in.slack.AppURL.String())
	// A copy: the notifier must not write into the global http_config.
	assert.NotSame(t, g.HTTPConfig, in.slack.HTTPConfig)
}

func TestRedact(t *testing.T) {
	target := &core.PublishingTarget{
		URL: "https://events.pagerduty.com/v2/events",
		Headers: map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("user:hunter2")),
			"X-Key":         "k-value",
		},
		HTTPConfig: &core.HTTPClientConfig{ProxyURL: "http://proxy.example:3128"},
	}
	msg := redact("url https://events.pagerduty.com/v2/enqueue, password hunter2, key k-value, proxy http://proxy.example:3128", target)
	for _, secret := range []string{"events.pagerduty.com", "hunter2", "k-value", "proxy.example"} {
		assert.NotContains(t, msg, secret)
	}
}
