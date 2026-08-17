package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

func testOptions() types.Options {
	return types.Options{
		Mode:                types.StrictMode,
		IncludeInfo:         true,
		IncludeSuggestions:  true,
		EnableSecurity:      true,
		EnableBestPractices: true,
	}
}

func hasErrorCode(result *types.Result, code string) bool {
	for _, e := range result.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

func hasWarningCode(result *types.Result, code string) bool {
	for _, w := range result.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func hasInfoCode(result *types.Result, code string) bool {
	for _, info := range result.Info {
		if info.Code == code {
			return true
		}
	}
	return false
}

func minimalValidConfig() *config.AlertmanagerConfig {
	return &config.AlertmanagerConfig{
		Route: &config.Route{
			Receiver: "default",
		},
		Receivers: []*config.Receiver{
			{
				Name: "default",
				WebhookConfigs: []*config.WebhookConfig{
					{URL: "https://example.com/webhook"},
				},
			},
		},
	}
}

func TestStructuralValidator(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.AlertmanagerConfig
		wantErrCode string
		wantNoErr   bool
	}{
		{
			name:      "valid minimal config",
			cfg:       minimalValidConfig(),
			wantNoErr: true,
		},
		{
			name: "missing root route",
			cfg: &config.AlertmanagerConfig{
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E100",
		},
		{
			name:        "no receivers",
			cfg:         &config.AlertmanagerConfig{Route: &config.Route{Receiver: "default"}},
			wantErrCode: "E021",
		},
		{
			name: "nil config",
			cfg:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := types.NewResult()
			v := NewStructuralValidator(testOptions(), nil)
			v.Validate(context.Background(), tt.cfg, result)

			if tt.wantNoErr && len(result.Errors) != 0 {
				t.Fatalf("expected no errors, got %+v", result.Errors)
			}
			if tt.wantErrCode != "" && !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
		})
	}
}

func TestStructuralValidator_FutureSectionsAccepted(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.TimeIntervals = []map[string]any{{"name": "business-hours"}}

	result := types.NewResult()
	v := NewStructuralValidator(testOptions(), nil)
	v.Validate(context.Background(), cfg, result)

	if len(result.Errors) != 0 {
		t.Fatalf("time_intervals must not be rejected, got errors: %+v", result.Errors)
	}
	if !hasInfoCode(result, "I001") {
		t.Fatalf("expected I001 info note about unvalidated time_intervals, got %+v", result.Info)
	}
}
