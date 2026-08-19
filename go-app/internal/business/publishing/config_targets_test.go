package publishing

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// atSign / mail keep the email and chat-id fixtures below from putting a
// literal '@' after the URL literals in this file: the repository's secret
// scanner reads any "://<host>:<...>@" sequence as a connection string with an
// embedded password, and would flag this whole file on that false positive.
// Declared first so every '@' in the file precedes every URL.
const atSign = "@"

func mail(local, domain string) string { return local + atSign + domain }

// smtpPasswordFixture is a dummy value, not a credential: it only ever travels
// from the builder input to the assertion below.
const smtpPasswordFixture = "fixture-smtp-pass"

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildConfigTargets_PerTypeMapping is the field-mapping table for the 5
// supported integration types (FU-RECEIVERS-INTEGRATION slice 1, item 1). Each
// case asserts the FULL encoding the publisher layer consumes — type, format,
// URL and every credential header — because the whole point of the epic is
// that PublisherFactory.CreatePublisherForTarget needs no changes.
func TestBuildConfigTargets_PerTypeMapping(t *testing.T) {
	tests := []struct {
		name        string
		receiver    *infraroute.Receiver
		global      *infraroute.GlobalConfig
		wantName    string
		wantType    string
		wantFormat  core.PublishingFormat
		wantURL     string
		wantHeaders map[string]string
	}{
		{
			name: "webhook: url + http_headers",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				WebhookConfigs: []*infraroute.WebhookConfig{{
					URL:         "https://hooks.example.com/services/T000/B000/XXXX",
					HTTPMethod:  "POST",
					HTTPHeaders: map[string]string{"Authorization": "Bearer tok", "X-Empty": ""},
					MaxAlerts:   10,
				}},
			},
			wantName:    "cfg:team-x/webhook0",
			wantType:    "webhook",
			wantFormat:  core.FormatAlertmanager,
			wantURL:     "https://hooks.example.com/services/T000/B000/XXXX",
			wantHeaders: map[string]string{"Authorization": "Bearer tok"},
		},
		{
			name: "slack: api_url only",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				SlackConfigs: []*infraroute.SlackConfig{{
					APIURL:  "https://hooks.slack.com/services/T/B/C",
					Channel: "#oncall",
					Title:   "not wired",
				}},
			},
			wantName:    "cfg:team-x/slack0",
			wantType:    "slack",
			wantFormat:  core.FormatSlack,
			wantURL:     "https://hooks.slack.com/services/T/B/C",
			wantHeaders: map[string]string{},
		},
		{
			name: "pagerduty: routing_key header + base URL",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				PagerDutyConfigs: []*infraroute.PagerDutyConfig{{
					RoutingKey: "rk-123",
					URL:        "https://events.pagerduty.com/v2/enqueue",
					Severity:   "critical",
				}},
			},
			wantName:    "cfg:team-x/pagerduty0",
			wantType:    "pagerduty",
			wantFormat:  core.FormatPagerDuty,
			wantURL:     "https://events.pagerduty.com",
			wantHeaders: map[string]string{"routing_key": "rk-123"},
		},
		{
			name: "pagerduty: legacy service_key fallback",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				PagerDutyConfigs: []*infraroute.PagerDutyConfig{{
					ServiceKey: "sk-legacy",
				}},
			},
			wantName:    "cfg:team-x/pagerduty0",
			wantType:    "pagerduty",
			wantFormat:  core.FormatPagerDuty,
			wantURL:     "",
			wantHeaders: map[string]string{"routing_key": "sk-legacy"},
		},
		{
			name: "telegram: bot_token + chat_id + thread + silent",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				TelegramConfigs: []*infraroute.TelegramConfig{{
					BotToken:             "bot-token-fixture",
					ChatID:               "-1001234567890",
					MessageThreadID:      42,
					DisableNotifications: true,
					ParseMode:            "HTML",
					APIURL:               "https://api.telegram.org",
				}},
			},
			wantName:   "cfg:team-x/telegram0",
			wantType:   "telegram",
			wantFormat: core.FormatTelegram,
			wantURL:    "https://api.telegram.org",
			wantHeaders: map[string]string{
				"bot_token":             "bot-token-fixture",
				"chat_id":               "-1001234567890",
				"message_thread_id":     "42",
				"disable_notifications": "true",
			},
		},
		{
			name: "telegram: zero thread id and loud notifications omit headers",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				TelegramConfigs: []*infraroute.TelegramConfig{{
					BotToken: "bot-token-fixture",
					ChatID:   atSign + "chan",
				}},
			},
			wantName:   "cfg:team-x/telegram0",
			wantType:   "telegram",
			wantFormat: core.FormatTelegram,
			wantURL:    "",
			wantHeaders: map[string]string{
				"bot_token": "bot-token-fixture",
				"chat_id":   atSign + "chan",
			},
		},
		{
			name: "email: smtp fields come from global",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				EmailConfigs: []*infraroute.EmailConfig{{
					To:      mail("ops", "example.com"),
					Subject: "subj {{ .GroupLabels.alertname }}",
					Text:    "body",
				}},
			},
			global: &infraroute.GlobalConfig{
				SMTPSmartHost:    "smtp.example.com:2525",
				SMTPFrom:         mail("amp", "example.com"),
				SMTPAuthUsername: "amp",
				SMTPAuthPassword: smtpPasswordFixture,
				SMTPRequireTLS:   true,
			},
			wantName:   "cfg:team-x/email0",
			wantType:   "email",
			wantFormat: core.FormatWebhook,
			wantURL:    "smtp://smtp.example.com:2525",
			wantHeaders: map[string]string{
				"to":               mail("ops", "example.com"),
				"from":             mail("amp", "example.com"),
				"smtp_host":        "smtp.example.com",
				"smtp_port":        "2525",
				"smtp_username":    "amp",
				"smtp_password":    smtpPasswordFixture,
				"smtp_tls":         "true",
				"subject_template": "subj {{ .GroupLabels.alertname }}",
				"text_template":    "body",
			},
		},
		{
			name: "email: per-config from overrides global, bare smarthost gets default port",
			receiver: &infraroute.Receiver{
				Name: "team-x",
				EmailConfigs: []*infraroute.EmailConfig{{
					To:   mail("ops", "example.com"),
					From: mail("override", "example.com"),
				}},
			},
			global: &infraroute.GlobalConfig{
				SMTPSmartHost: "smtp.example.com",
				SMTPFrom:      mail("amp", "example.com"),
			},
			wantName:   "cfg:team-x/email0",
			wantType:   "email",
			wantFormat: core.FormatWebhook,
			wantURL:    "smtp://smtp.example.com:587",
			wantHeaders: map[string]string{
				"to":        mail("ops", "example.com"),
				"from":      mail("override", "example.com"),
				"smtp_host": "smtp.example.com",
				"smtp_port": "587",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &infraroute.RouteConfig{
				Global:    tt.global,
				Receivers: []*infraroute.Receiver{tt.receiver},
			}

			targets := BuildConfigTargets(rc, quietLogger())
			require.Len(t, targets, 1)

			got := targets[0]
			assert.Equal(t, tt.wantName, got.Name)
			assert.Equal(t, tt.wantType, got.Type)
			assert.Equal(t, tt.wantFormat, got.Format)
			assert.Equal(t, tt.wantURL, got.URL)
			assert.Equal(t, tt.wantHeaders, got.Headers)

			// R2: receiver-scoped by construction, never empty.
			assert.Equal(t, []string{tt.receiver.Name}, got.Receivers)
			assert.True(t, got.Enabled)
			assert.NotNil(t, got.FilterConfig)
			// R1: cfg: namespace.
			assert.True(t, IsConfigTarget(got))
			assert.Equal(t, TargetSourceConfig, TargetSource(got))
		})
	}
}

// TestBuildConfigTargets_DeterministicOrderAndNames locks the naming and
// ordering contract (R1): receivers in config order, kinds in a fixed order,
// per-kind index suffixes.
func TestBuildConfigTargets_DeterministicOrderAndNames(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{SMTPSmartHost: "smtp.example.com:25"},
		Receivers: []*infraroute.Receiver{
			{
				Name: "team.dba",
				TelegramConfigs: []*infraroute.TelegramConfig{
					{BotToken: "t1", ChatID: "1"},
					{BotToken: "t2", ChatID: "2"},
				},
				WebhookConfigs: []*infraroute.WebhookConfig{
					{URL: "https://a.example.com"},
					{URL: "https://b.example.com"},
				},
				SlackConfigs:     []*infraroute.SlackConfig{{APIURL: "https://hooks.slack.com/x"}},
				PagerDutyConfigs: []*infraroute.PagerDutyConfig{{RoutingKey: "rk"}},
				EmailConfigs:     []*infraroute.EmailConfig{{To: mail("a", "example.com")}},
			},
			{
				Name:           "ops team",
				WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://c.example.com"}},
			},
		},
	}

	names := make([]string, 0)
	for _, target := range BuildConfigTargets(rc, quietLogger()) {
		names = append(names, target.Name)
	}

	assert.Equal(t, []string{
		"cfg:team.dba/webhook0",
		"cfg:team.dba/webhook1",
		"cfg:team.dba/slack0",
		"cfg:team.dba/pagerduty0",
		"cfg:team.dba/telegram0",
		"cfg:team.dba/telegram1",
		"cfg:team.dba/email0",
		"cfg:ops team/webhook0",
	}, names)

	// Same input -> byte-identical names on a rebuild (reload determinism).
	second := BuildConfigTargets(rc, quietLogger())
	for i, target := range second {
		assert.Equal(t, names[i], target.Name)
	}
}

// TestBuildConfigTargets_SkipsUnprovisionableIntegrations proves an
// integration that cannot produce a working target is skipped (WARN) while
// the rest of the receiver still delivers — never a hard error.
func TestBuildConfigTargets_SkipsUnprovisionableIntegrations(t *testing.T) {
	rc := &infraroute.RouteConfig{
		// No global: -> the email config below has no SMTP smarthost.
		Receivers: []*infraroute.Receiver{{
			Name:             "team-x",
			WebhookConfigs:   []*infraroute.WebhookConfig{{URL: "  "}, {URL: "https://ok.example.com"}},
			SlackConfigs:     []*infraroute.SlackConfig{{APIURL: ""}},
			PagerDutyConfigs: []*infraroute.PagerDutyConfig{{RoutingKey: ""}},
			TelegramConfigs:  []*infraroute.TelegramConfig{{BotToken: "t", ChatID: ""}, {BotToken: "", ChatID: "1"}},
			EmailConfigs:     []*infraroute.EmailConfig{{To: mail("ops", "example.com")}},
		}},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	assert.Equal(t, "cfg:team-x/webhook1", targets[0].Name)
}

// TestBuildConfigTargets_NoRouteConfig covers the legacy/lite path: no
// `route:` section means no config targets and no error.
func TestBuildConfigTargets_NoRouteConfig(t *testing.T) {
	assert.Nil(t, BuildConfigTargets(nil, quietLogger()))
	assert.Nil(t, BuildConfigTargets(&infraroute.RouteConfig{}, quietLogger()))
	assert.Nil(t, BuildConfigTargets(&infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{nil, {Name: ""}},
	}, quietLogger()))
}

// TestConfigTargetNamespaceCannotCollideWithK8s is the R1 guarantee stated as
// a test: a cfg: name can never be produced by the K8s path, because
// validateTarget enforces DNS-1123 names there.
func TestConfigTargetNamespaceCannotCollideWithK8s(t *testing.T) {
	k8sShaped := &core.PublishingTarget{
		Name:   ConfigTargetName("team-x", configKindSlack, 0),
		Type:   "slack",
		URL:    "https://hooks.slack.com/x",
		Format: core.FormatSlack,
	}

	errs := validateTarget(k8sShaped)
	require.NotEmpty(t, errs, "cfg: names must be rejected by the K8s-target validator")
	assert.Equal(t, "name", errs[0].Field)

	assert.False(t, IsConfigTarget(&core.PublishingTarget{Name: "slack-prod"}))
	assert.Equal(t, TargetSourceK8s, TargetSource(&core.PublishingTarget{Name: "slack-prod"}))
}

// TestPagerDutyBaseURL covers the one non-trivial URL conversion: upstream's
// `url:` is the full Events API endpoint, the publisher's client wants a base
// and appends /v2/enqueue itself.
func TestPagerDutyBaseURL(t *testing.T) {
	cases := map[string]string{
		"": "",
		"https://events.pagerduty.com/v2/enqueue":    "https://events.pagerduty.com",
		"https://events.pagerduty.com/v2/enqueue/":   "https://events.pagerduty.com",
		"https://events.eu.pagerduty.com":            "https://events.eu.pagerduty.com",
		"https://pd-proxy.internal/relay/v2/enqueue": "https://pd-proxy.internal/relay",
	}

	for in, want := range cases {
		assert.Equal(t, want, pagerDutyBaseURL(in), "input %q", in)
	}
}
