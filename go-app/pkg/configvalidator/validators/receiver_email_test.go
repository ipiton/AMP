package validators

import (
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
)

func receiverConfigWithEmail(to, from, smarthost string) *config.AlertmanagerConfig {
	return &config.AlertmanagerConfig{
		Receivers: []*config.Receiver{
			{
				Name: "default",
				EmailConfigs: []*config.EmailConfig{
					{To: to, From: from, Smarthost: smarthost},
				},
			},
		},
	}
}

func TestReceiverValidator_EmailConfigs(t *testing.T) {
	tests := []struct {
		name        string
		to          string
		from        string
		smarthost   string
		wantErrCode string
	}{
		{
			name:        "to required",
			from:        "am@example.com",
			wantErrCode: "E118",
		},
		{
			name:        "to invalid",
			to:          "not-an-email",
			from:        "am@example.com",
			wantErrCode: "E119",
		},
		{
			name:        "smarthost missing port",
			to:          "ops@example.com",
			from:        "am@example.com",
			smarthost:   "smtp.example.com",
			wantErrCode: "E121",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := receiverConfigWithEmail(tt.to, tt.from, tt.smarthost)
			result := runReceiverValidator(cfg)
			if !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
		})
	}
}

func TestReceiverValidator_EmailConfigValid(t *testing.T) {
	cfg := receiverConfigWithEmail("ops@example.com", "am@example.com", "smtp.example.com:587")
	result := runReceiverValidator(cfg)
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
}
