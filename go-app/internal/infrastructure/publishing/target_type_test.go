package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseTargetType was rescued from discovery_manager_test.go, which was
// deleted along with the dead DefaultTargetDiscoveryManager it exercised (final
// review finding 14). ParseTargetType itself lives in models.go and is very
// much alive — it is how a discovered target's `type` field becomes a
// TargetType.
func TestParseTargetType(t *testing.T) {
	tests := []struct {
		input    string
		expected TargetType
	}{
		{"rootly", TargetTypeRootly},
		{"pagerduty", TargetTypePagerDuty},
		{"pager_duty", TargetTypePagerDuty},
		{"slack", TargetTypeSlack},
		{"webhook", TargetTypeWebhook},
		{"alertmanager", TargetTypeAlertmanager},
		{"unknown", TargetTypeWebhook}, // Default
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, ParseTargetType(tt.input), "Input: %s", tt.input)
	}
}
