package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// SMTP-related (email address) test cases live in global_smtp_test.go,
// split out for the same secret-leak-guard false-positive reason as
// receiver_email_test.go: this file already carries several fake
// integration URLs.

func runGlobalValidator(cfg *config.AlertmanagerConfig) *types.Result {
	result := types.NewResult()
	v := NewGlobalConfigValidator(testOptions(), nil)
	v.Validate(context.Background(), cfg, result)
	return result
}

func TestGlobalConfigValidator(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.AlertmanagerConfig
		wantErrCode  string
		wantInfoCode string
		wantNoIssues bool
	}{
		{
			name:         "nil global is fine, notes default",
			cfg:          &config.AlertmanagerConfig{},
			wantInfoCode: "I200",
		},
		{
			name: "valid global config",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{
					ResolveTimeout: 300000000000, // 5m in nanoseconds
					SlackAPIURL:    "https://example.com/slack-webhook",
					PagerdutyURL:   "https://example.com/pagerduty-events",
					OpsGenieAPIURL: "https://example.com/opsgenie-api",
					HTTPConfig:     &config.HTTPConfig{ProxyURL: "https://example.com/proxy"},
				},
			},
			wantNoIssues: true,
		},
		{
			name: "negative resolve_timeout",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{ResolveTimeout: -1},
			},
			wantErrCode: "E200",
		},
		{
			name: "slack url malformed",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{SlackAPIURL: "not-a-url"},
			},
			wantErrCode: "E203",
		},
		{
			name: "slack url must be https",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{SlackAPIURL: "http://example.com/slack-webhook"},
			},
			wantErrCode: "E204",
		},
		{
			name: "pagerduty url malformed",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{PagerdutyURL: "not-a-url"},
			},
			wantErrCode: "E205",
		},
		{
			name: "pagerduty url must be https",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{PagerdutyURL: "http://example.com/pagerduty-events"},
			},
			wantErrCode: "E206",
		},
		{
			name: "opsgenie url malformed",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{OpsGenieAPIURL: "not-a-url"},
			},
			wantErrCode: "E207",
		},
		{
			name: "opsgenie url must be https",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{OpsGenieAPIURL: "http://example.com/opsgenie-api"},
			},
			wantErrCode: "E208",
		},
		{
			name: "proxy url malformed",
			cfg: &config.AlertmanagerConfig{
				Global: &config.GlobalConfig{HTTPConfig: &config.HTTPConfig{ProxyURL: "not-a-url"}},
			},
			wantErrCode: "E209",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGlobalValidator(tt.cfg)

			if tt.wantNoIssues && len(result.Errors) != 0 {
				t.Fatalf("expected no errors, got %+v", result.Errors)
			}
			if tt.wantErrCode != "" && !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
			if tt.wantInfoCode != "" && !hasInfoCode(result, tt.wantInfoCode) {
				t.Fatalf("expected info code %s, got %+v", tt.wantInfoCode, result.Info)
			}
		})
	}
}
