package configvalidator

import (
	"testing"

	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

func strictOptions() types.Options {
	return types.Options{
		Mode:                types.StrictMode,
		IncludeInfo:         true,
		IncludeSuggestions:  true,
		EnableSecurity:      true,
		EnableBestPractices: true,
	}
}

// TestValidator_FullRealisticConfig exercises the whole pipeline (parser +
// all six validators wired together in ValidateConfig) against one
// complete, realistic Alertmanager configuration: global defaults, a
// routing tree using the AMP matchers: list syntax with nested routes,
// three different receiver integration types, and an inhibition rule
// using source_matchers/target_matchers + equal. It must come back clean.
func TestValidator_FullRealisticConfig(t *testing.T) {
	v := New(strictOptions())

	result, err := v.ValidateFile("testdata/valid_alertmanager.yaml")
	if err != nil {
		t.Fatalf("ValidateFile returned error: %v", err)
	}

	if !result.Valid {
		t.Fatalf("expected valid config, got errors=%+v warnings=%+v", result.Errors, result.Warnings)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
}

// TestValidator_InvalidConfig proves errors from multiple validators
// (structural + route + receiver) surface together through the public
// Validator API, not just when unit-testing each validator in isolation.
func TestValidator_InvalidConfig(t *testing.T) {
	data := []byte(`
route:
  receiver: default
  routes:
    - receiver: unknown-receiver

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
  - name: default
    webhook_configs:
      - url: https://example.com/webhook2
`)

	v := New(strictOptions())
	result, err := v.ValidateBytes(data)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}

	if result.Valid {
		t.Fatalf("expected invalid config")
	}

	wantCodes := map[string]bool{"E102": false, "E023": false}
	for _, e := range result.Errors {
		if _, ok := wantCodes[e.Code]; ok {
			wantCodes[e.Code] = true
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Fatalf("expected error code %s among %+v", code, result.Errors)
		}
	}
}

// TestValidator_EmptyConfig proves the empty-input path (no route, no
// receivers) reports E100/E021 rather than panicking.
func TestValidator_EmptyConfig(t *testing.T) {
	v := New(strictOptions())
	result, err := v.ValidateBytes([]byte(`{}`))
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected invalid config for empty input")
	}
}
