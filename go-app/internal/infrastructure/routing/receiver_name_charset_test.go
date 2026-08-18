package routing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 7: receiver names were validated with
// validateAlphanumHyphen ([a-zA-Z0-9_-] only), which rejects names upstream
// Alertmanager accepts and real configs use. A config migrated from upstream
// failed to load, reading as "AMP doesn't support my config" rather than the
// cosmetic restriction it actually was. Only '/' remains reserved, because
// group keys are "receiver=<name>/<generated-key>" and the receiver is
// recovered by splitting on the first '/'.

func configWithReceiverName(name string) string {
	return fmt.Sprintf(`
route:
  receiver: %q
  group_by: [alertname]
  routes:
    - receiver: %q
      matchers: ['severity="critical"']
receivers:
  - name: %q
    webhook_configs:
      - url: https://example.com/webhook
`, name, name, name)
}

func TestReceiverName_UpstreamLegalNamesAccepted(t *testing.T) {
	names := []string{
		"team.dba",      // dotted — extremely common upstream
		"email:sre",     // colon
		"ops team",      // space
		"équipe-réseau", // non-ASCII
		"pagerduty_p1",  // underscore (already worked)
		"default",       // plain (already worked)
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			cfg, err := NewRouteConfigParser().Parse([]byte(configWithReceiverName(name)))
			require.NoError(t, err, "upstream-legal receiver name %q must parse", name)
			require.NotNil(t, cfg)

			receiver, ok := cfg.GetReceiver(name)
			require.True(t, ok, "receiver %q must be resolvable by name after parse", name)
			assert.Equal(t, name, receiver.Name)

			// The route tree must reference it too, so routing actually lands
			// on this receiver rather than silently falling back.
			assert.Equal(t, name, cfg.Route.Receiver)
			require.Len(t, cfg.Route.Routes, 1)
			assert.Equal(t, name, cfg.Route.Routes[0].Receiver)
		})
	}
}

func TestReceiverName_SlashRejectedWithActionableError(t *testing.T) {
	_, err := NewRouteConfigParser().Parse([]byte(configWithReceiverName("team/dba")))
	require.Error(t, err, "'/' must stay reserved — it is the group-key separator")

	msg := err.Error()
	assert.Contains(t, msg, "receiver_name", "the failing rule must be identifiable")
	assert.Contains(t, msg, "reserved",
		"the error must explain WHICH character is the problem and why; got: %s", msg)
}

func TestReceiverName_EmptyStillRejected(t *testing.T) {
	_, err := NewRouteConfigParser().Parse([]byte(`
route:
  receiver: default
  group_by: [alertname]
receivers:
  - name: ""
    webhook_configs:
      - url: https://example.com/webhook
`))
	require.Error(t, err, "an empty receiver name must still be rejected (required tag)")
}

// TestSuggestionForTag_ReceiverNameNamesTheReservedChar pins the operator-facing
// text: the raw go-playground message only says the 'receiver_name' tag failed.
func TestSuggestionForTag_ReceiverNameNamesTheReservedChar(t *testing.T) {
	hint := suggestionForTag("receiver_name")
	require.NotEmpty(t, hint)
	assert.Contains(t, hint, string(ReceiverNameReservedChar))
	assert.True(t, strings.Contains(hint, "reserved"), "hint should explain the reservation: %s", hint)

	assert.Empty(t, suggestionForTag("required"), "tags with self-explanatory messages need no hint")
}
