package grouping

import (
	"context"
	"fmt"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 7's other half: relaxing receiver-name validation to
// "anything but '/'" is only safe if the group-key round trip
// (AlertProcessor.groupKeyFor writes "receiver=<name>/<key>",
// receiverFromGroupKey splits on the FIRST '/') survives the newly-legal
// characters. Dots, colons, spaces and non-ASCII must all come back intact,
// and the receiver must reach the publisher unmangled.
func TestReceiverFromGroupKey_UpstreamLegalNames(t *testing.T) {
	cases := []string{
		"team.dba",
		"email:sre",
		"ops team",
		"équipe-réseau",
		"pagerduty_p1",
		"default",
	}

	for _, receiver := range cases {
		t.Run(receiver, func(t *testing.T) {
			key := GroupKey(fmt.Sprintf("receiver=%s/alertname=HighCPU,severity=critical", receiver))
			assert.Equal(t, receiver, receiverFromGroupKey(key))
		})
	}
}

func TestReceiverFromGroupKey_EdgeCases(t *testing.T) {
	// No "receiver=" prefix: "" means "no receiver-scoped filtering" (see the
	// function's doc comment), not an error.
	assert.Empty(t, receiverFromGroupKey(GroupKey("alertname=HighCPU")))

	// No trailing group-key part at all (an empty group_by list).
	assert.Equal(t, "team.dba", receiverFromGroupKey(GroupKey("receiver=team.dba")))

	// A '/' inside the generated key part must not be mistaken for the
	// separator — the FIRST one wins, so the receiver is still exact.
	assert.Equal(t, "team.dba", receiverFromGroupKey(GroupKey("receiver=team.dba/path=/var/log")))
}

// TestPublishGroupAlerts_PassesExoticReceiverNameThrough closes the loop: the
// receiver recovered from the key is what actually reaches the publisher.
func TestPublishGroupAlerts_PassesExoticReceiverNameThrough(t *testing.T) {
	const receiver = "team.dba"

	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=" + receiver + "/alertname=ExoticReceiverAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "ExoticReceiverAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group)

	require.Len(t, pub.calls(), 1)
	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.receivers, 1)
	assert.Equal(t, receiver, pub.receivers[0],
		"a dotted receiver name must reach the publisher unmangled")
}
