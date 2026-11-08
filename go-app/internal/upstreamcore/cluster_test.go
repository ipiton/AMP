package upstreamcore

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clusterStatus struct {
	Cluster *struct {
		Status string `json:"status"`
		Peers  []struct {
			Name    string `json:"name"`
			Address string `json:"address"`
		} `json:"peers"`
	} `json:"cluster"`
}

func (tc *testCore) status(t *testing.T) clusterStatus {
	t.Helper()
	code, body := tc.do(t, http.MethodGet, "/api/v2/status", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	var st clusterStatus
	require.NoError(t, json.Unmarshal(body, &st))
	return st
}

func clusterEnabledMetric(t *testing.T, tc *testCore) float64 {
	t.Helper()
	mfs, err := tc.registry.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == "alertmanager_cluster_enabled" {
			return mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatal("alertmanager_cluster_enabled is not registered")
	return 0
}

// clusterOptions keeps the gossip on loopback with a random port and short
// timings, so two cores in one test settle and dedup within seconds.
func clusterOptions(peers ...string) func(*Options) {
	return func(o *Options) {
		o.ClusterBindAddr = "127.0.0.1:0"
		o.Peers = peers
		o.PeerTimeout = time.Second
		o.GossipInterval = 20 * time.Millisecond
		o.PushPullInterval = time.Second
		o.SettleTimeout = 0
	}
}

func TestCoreWithoutCluster(t *testing.T) {
	hook, _ := webhookReceiver(t)
	tc := newTestCore(t, hook.URL)

	// Upstream reports a disabled cluster rather than leaving the section out.
	st := tc.status(t)
	require.NotNil(t, st.Cluster)
	assert.Equal(t, "disabled", st.Cluster.Status)
	assert.Empty(t, st.Cluster.Peers)
	assert.Zero(t, clusterEnabledMetric(t, tc))
}

// Two cores in one memberlist cluster: silences are gossiped, and an alert
// sent to both is notified once (the shared notification log, with the
// second core waiting PeerTimeout per position).
func TestCoreClusterOfTwo(t *testing.T) {
	hook, got := webhookReceiver(t)

	a := newTestCoreWith(t, hook.URL, clusterOptions())
	stA := a.status(t)
	require.NotNil(t, stA.Cluster)
	require.Len(t, stA.Cluster.Peers, 1)
	b := newTestCoreWith(t, hook.URL, clusterOptions(stA.Cluster.Peers[0].Address))

	for _, tc := range []*testCore{a, b} {
		require.Eventually(t, func() bool {
			st := tc.status(t)
			return st.Cluster != nil && st.Cluster.Status == "ready" && len(st.Cluster.Peers) == 2
		}, 10*time.Second, 50*time.Millisecond)
		assert.Equal(t, 1.0, clusterEnabledMetric(t, tc))
	}

	now := time.Now()
	code, body := a.do(t, http.MethodPost, "/api/v2/silences", map[string]any{
		"matchers":  []map[string]any{{"name": "alertname", "value": "Muted", "isRegex": false, "isEqual": true}},
		"startsAt":  now.Add(-time.Minute),
		"endsAt":    now.Add(time.Hour),
		"createdBy": "test",
		"comment":   "gossip",
	})
	require.Equal(t, http.StatusOK, code, string(body))
	var created struct {
		SilenceID string `json:"silenceID"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.Eventually(t, func() bool {
		code, _ := b.do(t, http.MethodGet, "/api/v2/silence/"+created.SilenceID, nil)
		return code == http.StatusOK
	}, 10*time.Second, 50*time.Millisecond, "silence created on A never reached B")

	for _, name := range []string{"Muted", "Both"} {
		for _, tc := range []*testCore{a, b} {
			code, body := tc.do(t, http.MethodPost, "/api/v2/alerts", []map[string]any{
				{"labels": map[string]string{"alertname": name}},
			})
			require.Equal(t, http.StatusOK, code, string(body))
		}
	}

	// One notification for "Both"; none for "Muted". The quiet period runs
	// past the second core's PeerTimeout, so a duplicate would arrive in it.
	var names []string
	deadline := time.After(10 * time.Second)
	quiet := time.After(4 * time.Second)
	for done := false; !done; {
		select {
		case msg := <-got:
			labels, _ := msg["commonLabels"].(map[string]any)
			names = append(names, labels["alertname"].(string))
		case <-quiet:
			done = len(names) > 0
			quiet = time.After(time.Second)
		case <-deadline:
			done = true
		}
	}
	assert.Equal(t, []string{"Both"}, names)
}
