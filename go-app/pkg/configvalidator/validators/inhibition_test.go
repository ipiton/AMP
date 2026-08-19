package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

func runInhibitionValidator(cfg *config.AlertmanagerConfig) *types.Result {
	result := types.NewResult()
	v := NewInhibitionValidator(testOptions(), nil)
	v.Validate(context.Background(), cfg, result)
	return result
}

func TestInhibitionValidator(t *testing.T) {
	tests := []struct {
		name           string
		cfg            *config.AlertmanagerConfig
		wantErrCode    string
		wantErrCodes   []string // when a case needs to assert more than one error code
		wantWarnCode   string
		dontWantErrors bool // assert zero errors without requiring zero warnings too
		wantNoIssues   bool
	}{
		{
			name: "valid rule with equal labels",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatch: map[string]string{"alertname": "NodeDown"},
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
						Equal:       []string{"node"},
					},
				},
			},
			wantNoIssues: true,
		},
		{
			// Wave 7 (FU-INHIBIT-MATCHERS): the runtime inhibition loader
			// now implements source_matchers/target_matchers for real
			// (internal/infrastructure/inhibition.InhibitionRule.
			// CompileMatchers), so the matchers-form list alone must
			// satisfy E150/E151 with no errors and no warnings.
			name: "matchers list syntax alone is sufficient (wired at runtime)",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatchers: []string{"alertname=NodeDown"},
						TargetMatchers: []string{"alertname=InstanceDown"},
						Equal:          []string{"node"},
					},
				},
			},
			wantNoIssues: true,
		},
		{
			// Matchers list alongside the legacy maps: both forms combine
			// as AND at runtime, no errors, no warnings either.
			name: "matchers list alongside legacy fields is fine, no warning",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatch:    map[string]string{"alertname": "NodeDown"},
						SourceMatchers: []string{"alertname=NodeDown"},
						TargetMatch:    map[string]string{"alertname": "InstanceDown"},
						Equal:          []string{"node"},
					},
				},
			},
			wantNoIssues: true,
		},
		{
			name: "missing source matchers",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{TargetMatch: map[string]string{"alertname": "InstanceDown"}},
				},
			},
			wantErrCode: "E150",
		},
		{
			name: "missing target matchers",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{SourceMatch: map[string]string{"alertname": "NodeDown"}},
				},
			},
			wantErrCode: "E151",
		},
		{
			name: "invalid equal label name",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatch: map[string]string{"alertname": "NodeDown"},
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
						Equal:       []string{"not a label!"},
					},
				},
			},
			wantErrCode: "E152",
		},
		{
			name: "bad source matcher syntax",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatchers: []string{"nodown"},
						TargetMatch:    map[string]string{"alertname": "InstanceDown"},
						Equal:          []string{"node"},
					},
				},
			},
			wantErrCode: "E153",
		},
		{
			name: "bad target match_re regex",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatch:   map[string]string{"alertname": "NodeDown"},
						TargetMatchRE: map[string]string{"alertname": "("},
						Equal:         []string{"node"},
					},
				},
			},
			wantErrCode: "E154",
		},
		{
			name: "no equal labels warns broad rule",
			cfg: &config.AlertmanagerConfig{
				InhibitRules: []*config.InhibitRule{
					{
						SourceMatch: map[string]string{"alertname": "NodeDown"},
						TargetMatch: map[string]string{"alertname": "InstanceDown"},
					},
				},
			},
			wantWarnCode: "W154",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runInhibitionValidator(tt.cfg)

			if tt.wantNoIssues && (len(result.Errors) != 0 || len(result.Warnings) != 0) {
				t.Fatalf("expected no issues, got errors=%+v warnings=%+v", result.Errors, result.Warnings)
			}
			if tt.wantErrCode != "" && !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
			for _, code := range tt.wantErrCodes {
				if !hasErrorCode(result, code) {
					t.Fatalf("expected error code %s, got %+v", code, result.Errors)
				}
			}
			if tt.dontWantErrors && len(result.Errors) != 0 {
				t.Fatalf("expected no errors, got %+v", result.Errors)
			}
			if tt.wantWarnCode != "" && !hasWarningCode(result, tt.wantWarnCode) {
				t.Fatalf("expected warning code %s, got %+v", tt.wantWarnCode, result.Warnings)
			}
		})
	}
}
