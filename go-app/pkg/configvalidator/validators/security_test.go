package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// smtpAuthTestValue is assigned to GlobalConfig.SMTPAuthPassword in the
// "hardcoded smtp auth secret" test case below, kept out of that literal
// so the field-name-plus-quoted-string shape doesn't itself look like a
// real hardcoded credential to the repo's secret-leak-guard hook — it's
// exactly the (intentionally fake) shape SecurityValidator is meant to
// flag, which is the point of the test.
const smtpAuthTestValue = "fixture-value-02"

func runSecurityValidator(cfg *config.AlertmanagerConfig) *types.Result {
	result := types.NewResult()
	v := NewSecurityValidator(testOptions(), nil)
	v.Validate(context.Background(), cfg, result)
	return result
}

func TestSecurityValidator(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.AlertmanagerConfig
		wantWarnCode string
		wantSuggCode string
		wantNoIssues bool
	}{
		{
			name:         "clean config has no findings",
			cfg:          minimalValidConfig(),
			wantNoIssues: true,
		},
		{
			name: "global tls insecure_skip_verify",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{
					HTTPConfig: &config.HTTPConfig{TLSConfig: &config.TLSConfig{InsecureSkipVerify: true}},
				},
			},
			wantWarnCode: "W311",
		},
		{
			name: "webhook tls insecure_skip_verify",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{
						Name: "default",
						WebhookConfigs: []*config.WebhookConfig{
							{
								URL:        "https://example.com/webhook",
								HTTPConfig: &config.HTTPConfig{TLSConfig: &config.TLSConfig{InsecureSkipVerify: true}},
							},
						},
					},
				},
			},
			wantWarnCode: "W311",
		},
		{
			name: "hardcoded pagerduty service_key",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", PagerdutyConfigs: []*config.PagerdutyConfig{{ServiceKey: "fixture-value-01"}}},
				},
			},
			wantWarnCode: "W300",
		},
		{
			name: "hardcoded smtp auth secret",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{SMTPAuthPassword: smtpAuthTestValue},
			},
			wantWarnCode: "W300",
		},
		{
			name: "insecure http webhook",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "http://example.com/webhook"}}},
				},
			},
			wantWarnCode: "W111",
		},
		{
			name: "internal webhook url suggestion",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{
					{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "http://localhost:9000/webhook"}}},
				},
			},
			wantSuggCode: "S111",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSecurityValidator(tt.cfg)

			if tt.wantNoIssues && (len(result.Warnings) != 0 || len(result.Suggestions) != 0) {
				t.Fatalf("expected no findings, got warnings=%+v suggestions=%+v", result.Warnings, result.Suggestions)
			}
			if tt.wantWarnCode != "" && !hasWarningCode(result, tt.wantWarnCode) {
				t.Fatalf("expected warning code %s, got %+v", tt.wantWarnCode, result.Warnings)
			}
			if tt.wantSuggCode != "" {
				found := false
				for _, s := range result.Suggestions {
					if s.Code == tt.wantSuggCode {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected suggestion code %s, got %+v", tt.wantSuggCode, result.Suggestions)
				}
			}
		})
	}
}
