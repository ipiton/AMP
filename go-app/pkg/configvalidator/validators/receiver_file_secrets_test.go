package validators

import (
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
)

// TestReceiverValidator_FileSecrets covers the *_file twins (FU7-B) in the
// required-value checks E113/E115/E122 (HELM-EXTRA-VOLUMES, review F1): a
// path to a file holding the secret counts as the value, so a config that
// keeps its webhook URLs and keys in a mounted Secret is not rejected before
// the loader ever reads the files. With neither the value nor its file, the
// errors stay.
func TestReceiverValidator_FileSecrets(t *testing.T) {
	const path = "/etc/amp/secrets/value"

	tests := []struct {
		name        string
		cfg         *config.AlertmanagerConfig
		wantErrCode string // "" = no errors at all
	}{
		{
			name: "webhook url_file",
			cfg: &config.AlertmanagerConfig{Receivers: []*config.Receiver{
				{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URLFile: path}}},
			}},
		},
		{
			name: "webhook neither url nor url_file",
			cfg: &config.AlertmanagerConfig{Receivers: []*config.Receiver{
				{Name: "default", WebhookConfigs: []*config.WebhookConfig{{}}},
			}},
			wantErrCode: "E113",
		},
		{
			name: "slack api_url_file",
			cfg: &config.AlertmanagerConfig{Receivers: []*config.Receiver{
				{Name: "default", SlackConfigs: []*config.SlackConfig{{APIURLFile: path}}},
			}},
		},
		{
			name: "slack falls back to global.slack_api_url_file",
			cfg: &config.AlertmanagerConfig{
				Global:    &config.GlobalConfig{SlackAPIURLFile: path},
				Receivers: []*config.Receiver{{Name: "default", SlackConfigs: []*config.SlackConfig{{}}}},
			},
		},
		{
			name: "slack neither api_url nor any file, here or in global",
			cfg: &config.AlertmanagerConfig{
				Global:    &config.GlobalConfig{},
				Receivers: []*config.Receiver{{Name: "default", SlackConfigs: []*config.SlackConfig{{}}}},
			},
			wantErrCode: "E115",
		},
		{
			name: "pagerduty routing_key_file",
			cfg: &config.AlertmanagerConfig{Receivers: []*config.Receiver{
				{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{RoutingKeyFile: path}}},
			}},
		},
		{
			name: "pagerduty service_key_file",
			cfg: &config.AlertmanagerConfig{Receivers: []*config.Receiver{
				{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{ServiceKeyFile: path}}},
			}},
		},
		{
			name: "pagerduty no key and no key file",
			cfg: &config.AlertmanagerConfig{Receivers: []*config.Receiver{
				{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{}}},
			}},
			wantErrCode: "E122",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runReceiverValidator(tt.cfg)

			if tt.wantErrCode == "" {
				if len(result.Errors) != 0 {
					t.Fatalf("expected no errors, got %+v", result.Errors)
				}
				return
			}
			if !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
		})
	}
}
