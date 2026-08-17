package validators

import (
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
)

func globalSMTPConfig(from, smarthost string) *config.AlertmanagerConfig {
	return &config.AlertmanagerConfig{
		Global: &config.GlobalConfig{SMTPFrom: from, SMTPSmarthost: smarthost},
	}
}

func TestGlobalConfigValidator_SMTP(t *testing.T) {
	tests := []struct {
		name        string
		from        string
		smarthost   string
		wantErrCode string
	}{
		{
			name:        "invalid smtp_from",
			from:        "not-an-email",
			wantErrCode: "E201",
		},
		{
			name:        "invalid smtp_smarthost",
			from:        "am@example.com",
			smarthost:   "smtp.example.com",
			wantErrCode: "E202",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGlobalValidator(globalSMTPConfig(tt.from, tt.smarthost))
			if !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
		})
	}
}

func TestGlobalConfigValidator_SMTPValid(t *testing.T) {
	result := runGlobalValidator(globalSMTPConfig("am@example.com", "smtp.example.com:587"))
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
}
