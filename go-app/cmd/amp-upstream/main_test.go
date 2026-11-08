package main

import (
	"flag"
	"io"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/upstreamcore"
)

func parseClusterFlags(t *testing.T, args ...string) upstreamcore.Options {
	t.Helper()
	fs := flag.NewFlagSet("amp-upstream", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := clusterFlags(fs)
	require.NoError(t, fs.Parse(args))
	return *o
}

func TestClusterFlagsDefaults(t *testing.T) {
	o := parseClusterFlags(t)
	assert.Equal(t, "0.0.0.0:9094", o.ClusterBindAddr, "upstream binary default: the cluster is on")
	assert.Empty(t, o.Peers)
	assert.Equal(t, 15*time.Second, o.PeerTimeout)
	assert.Equal(t, cluster.DefaultPushPullInterval, o.SettleTimeout)
	assert.NoError(t, validateClusterFlags(o))
}

func TestClusterFlagsRepeatablePeer(t *testing.T) {
	o := parseClusterFlags(t,
		"--cluster.peer=am-0.am:9094",
		"--cluster.peer=am-1.am:9094",
		"--cluster.peer-timeout=5s",
		"--cluster.listen-address=",
	)
	assert.Equal(t, []string{"am-0.am:9094", "am-1.am:9094"}, o.Peers)
	assert.Equal(t, 5*time.Second, o.PeerTimeout)
	assert.Empty(t, o.ClusterBindAddr)
}

// As upstream app.Options.validate: non-positive cluster durations fail
// only with the cluster on, and settle-timeout=0 is valid (settle at once).
func TestValidateClusterFlags(t *testing.T) {
	assert.NoError(t, validateClusterFlags(parseClusterFlags(t, "--cluster.settle-timeout=0s")))
	assert.ErrorContains(t, validateClusterFlags(parseClusterFlags(t, "--cluster.gossip-interval=0s")),
		"--cluster.gossip-interval must be positive")
	assert.ErrorContains(t, validateClusterFlags(parseClusterFlags(t, "--cluster.reconnect-timeout=-1s")),
		"--cluster.reconnect-timeout must be positive")
	assert.NoError(t, validateClusterFlags(parseClusterFlags(t, "--cluster.listen-address=", "--cluster.peer-timeout=0s")))
}
