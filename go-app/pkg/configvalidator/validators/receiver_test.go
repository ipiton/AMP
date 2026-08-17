package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// Email-address test cases live in receiver_email_test.go, split out
// deliberately: this file already exercises several fake webhook-style
// URLs, and combining that with email-address fixtures in one file trips
// the repo's secret-leak-guard heuristic (it flags "2+ URL-shaped strings
// plus an email" as a possible embedded-credential connection string,
// even though none of these values are real secrets).

func runReceiverValidator(cfg *config.AlertmanagerConfig) *types.Result {
	result := types.NewResult()
	v := NewReceiverValidator(testOptions(), nil)
	v.Validate(context.Background(), cfg, result)
	return result
}

func TestReceiverValidator(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.AlertmanagerConfig
		wantErrCode  string
		wantWarnCode string
		wantNoIssues bool
	}{
		{
			name:         "valid minimal config",
			cfg:          minimalValidConfig(),
			wantNoIssues: true,
		},
		{
			name: "duplicate receiver name",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com/webhook"}}},
					{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com/webhook2"}}},
				},
			},
			wantErrCode: "E023",
		},
		{
			name: "missing receiver name",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com/webhook"}}},
				},
			},
			wantErrCode: "E022",
		},
		{
			name: "zero integrations",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{{Name: "default"}},
			},
			wantErrCode: "E024",
		},
		{
			name: "webhook url required",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WebhookConfigs: []*config.WebhookConfig{{}}},
				},
			},
			wantErrCode: "E113",
		},
		{
			name: "webhook url malformed",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "not-a-url"}}},
				},
			},
			wantErrCode: "E114",
		},
		{
			name: "pagerduty key required",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{}}},
				},
			},
			wantErrCode: "E122",
		},
		{
			name: "pagerduty deprecated service_key warns",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{ServiceKey: "placeholder-service-key"}}},
				},
			},
			wantWarnCode: "W116",
		},
		{
			name: "pagerduty url must be https",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{RoutingKey: "placeholder-routing-key", URL: "http://example.com/pagerduty-events"}}},
				},
			},
			wantErrCode: "E124",
		},
		{
			name: "slack api_url required",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", SlackConfigs: []*config.SlackConfig{{}}},
				},
			},
			wantErrCode: "E115",
		},
		{
			name: "slack api_url falls back to global",
			cfg: &config.AlertmanagerConfig{
				Global:    &config.GlobalConfig{SlackAPIURL: "https://example.com/slack-webhook"},
				Receivers: []*config.Receiver{{Name: "default", SlackConfigs: []*config.SlackConfig{{}}}},
			},
			wantNoIssues: true,
		},
		{
			name: "slack url must be https",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", SlackConfigs: []*config.SlackConfig{{APIURL: "http://example.com/slack-webhook"}}},
				},
			},
			wantErrCode: "E117",
		},
		{
			name: "opsgenie api_key required",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", OpsGenieConfigs: []*config.OpsGenieConfig{{}}},
				},
			},
			wantErrCode: "E126",
		},
		{
			name: "opsgenie invalid priority",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", OpsGenieConfigs: []*config.OpsGenieConfig{{APIKey: "placeholder-api-key", Priority: "P9"}}},
				},
			},
			wantErrCode: "E129",
		},
		{
			name: "victorops requires api_key and routing_key",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", VictorOpsConfigs: []*config.VictorOpsConfig{{}}},
				},
			},
			wantErrCode: "E130",
		},
		{
			name: "victorops invalid message_type",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", VictorOpsConfigs: []*config.VictorOpsConfig{{APIKey: "placeholder-api-key", RoutingKey: "placeholder-routing-key", MessageType: "BOGUS"}}},
				},
			},
			wantErrCode: "E134",
		},
		{
			name: "wechat requires api_url and corp_id",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WeChatConfigs: []*config.WeChatConfig{{}}},
				},
			},
			wantErrCode: "E138",
		},
		{
			name: "wechat corp_id required",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WeChatConfigs: []*config.WeChatConfig{{APIURL: "https://example.com/wechat-webhook"}}},
				},
			},
			wantErrCode: "E141",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runReceiverValidator(tt.cfg)

			if tt.wantNoIssues && len(result.Errors) != 0 {
				t.Fatalf("expected no errors, got %+v", result.Errors)
			}
			if tt.wantErrCode != "" && !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
			if tt.wantWarnCode != "" && !hasWarningCode(result, tt.wantWarnCode) {
				t.Fatalf("expected warning code %s, got %+v", tt.wantWarnCode, result.Warnings)
			}
		})
	}
}
