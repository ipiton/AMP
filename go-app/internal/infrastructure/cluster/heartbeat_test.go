package cluster

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHeartbeatTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})
	return client, mr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestHeartbeatRegistry_StartRegistersImmediately proves Start's initial
// SET happens synchronously: IsRegistered() is true and the peer is
// visible via Peers() the instant Start returns, without waiting for any
// refresh tick.
func TestHeartbeatRegistry_StartRegistersImmediately(t *testing.T) {
	client, _ := setupHeartbeatTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewHeartbeatRegistry(client, "replica-a", "10.0.0.1:8080", 5*time.Second, time.Second, discardLogger())
	require.NoError(t, reg.Start(ctx))
	defer func() { _ = reg.Stop(context.Background()) }()

	assert.True(t, reg.IsRegistered())

	peers, err := reg.Peers(ctx)
	require.NoError(t, err)
	require.Len(t, peers, 1)
	assert.Equal(t, "replica-a", peers[0].Name)
	assert.Equal(t, "10.0.0.1:8080", peers[0].Address)
}

// TestHeartbeatRegistry_RefreshKeepsKeyAlive proves the background refresh
// loop re-registers before the TTL elapses: fast-forwarding past the
// original TTL (but staying inside the refresh cadence) must NOT expire
// the key.
func TestHeartbeatRegistry_RefreshKeepsKeyAlive(t *testing.T) {
	client, mr := setupHeartbeatTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ttl := 300 * time.Millisecond
	refresh := 50 * time.Millisecond
	reg := NewHeartbeatRegistry(client, "replica-a", "", ttl, refresh, discardLogger())
	require.NoError(t, reg.Start(ctx))
	defer func() { _ = reg.Stop(context.Background()) }()

	// Advance well past the original TTL in small steps so miniredis' clock
	// and the registry's real-time refresh ticks interleave; sleep briefly
	// between fast-forwards so the refresh goroutine actually gets to run.
	for i := 0; i < 6; i++ {
		mr.FastForward(ttl / 2)
		time.Sleep(20 * time.Millisecond)
	}

	peers, err := reg.Peers(ctx)
	require.NoError(t, err)
	require.Len(t, peers, 1, "key should have been refreshed before expiry")
	assert.Equal(t, "replica-a", peers[0].Name)
}

// TestHeartbeatRegistry_ExpiredPeerExcludedFromList proves Peers() relies
// entirely on Redis TTL expiry: once a peer's key has actually expired
// (no more refreshes happening — registry stopped), it stops appearing,
// with no separate staleness filtering needed on the read side.
func TestHeartbeatRegistry_ExpiredPeerExcludedFromList(t *testing.T) {
	client, mr := setupHeartbeatTestRedis(t)
	ctx := context.Background()

	ttl := 200 * time.Millisecond
	regA := NewHeartbeatRegistry(client, "replica-a", "", ttl, time.Hour, discardLogger())
	regB := NewHeartbeatRegistry(client, "replica-b", "", ttl, time.Hour, discardLogger())

	require.NoError(t, regA.Start(ctx))
	require.NoError(t, regB.Start(ctx))
	// Refresh interval is deliberately long (1h) so only the initial SET
	// matters here — this test is about expiry, not refresh.

	peers, err := regA.Peers(ctx)
	require.NoError(t, err)
	require.Len(t, peers, 2, "both peers should be visible before either expires")

	// Fast-forward miniredis past the TTL without either registry refreshing.
	mr.FastForward(ttl * 2)

	peers, err = regA.Peers(ctx)
	require.NoError(t, err)
	assert.Empty(t, peers, "both peer keys should have expired out of the list")
}

// TestHeartbeatRegistry_StopDeletesKeyImmediately proves Stop's
// best-effort DEL makes the peer disappear right away rather than waiting
// out the TTL, and that IsRegistered() flips to false.
func TestHeartbeatRegistry_StopDeletesKeyImmediately(t *testing.T) {
	client, _ := setupHeartbeatTestRedis(t)
	ctx := context.Background()

	regA := NewHeartbeatRegistry(client, "replica-a", "", 10*time.Second, time.Hour, discardLogger())
	regB := NewHeartbeatRegistry(client, "replica-b", "", 10*time.Second, time.Hour, discardLogger())
	require.NoError(t, regA.Start(ctx))
	require.NoError(t, regB.Start(ctx))

	peers, err := regB.Peers(ctx)
	require.NoError(t, err)
	require.Len(t, peers, 2)

	require.NoError(t, regA.Stop(context.Background()))
	assert.False(t, regA.IsRegistered())

	peers, err = regB.Peers(ctx)
	require.NoError(t, err)
	require.Len(t, peers, 1, "replica-a's key should be gone immediately, not after a TTL wait")
	assert.Equal(t, "replica-b", peers[0].Name)
}

// TestHeartbeatRegistry_TwoReplicasSeeEachOther proves Peers() reflects
// registrations from a different HeartbeatRegistry/client sharing the same
// Redis — the multi-replica case the status endpoint's "peers" list exists
// for.
func TestHeartbeatRegistry_TwoReplicasSeeEachOther(t *testing.T) {
	client, _ := setupHeartbeatTestRedis(t)
	ctx := context.Background()

	regA := NewHeartbeatRegistry(client, "replica-a", "10.0.0.1:8080", 10*time.Second, time.Hour, discardLogger())
	regB := NewHeartbeatRegistry(client, "replica-b", "10.0.0.2:8080", 10*time.Second, time.Hour, discardLogger())
	require.NoError(t, regA.Start(ctx))
	require.NoError(t, regB.Start(ctx))
	defer func() { _ = regA.Stop(context.Background()) }()
	defer func() { _ = regB.Stop(context.Background()) }()

	peersFromA, err := regA.Peers(ctx)
	require.NoError(t, err)
	require.Len(t, peersFromA, 2)
	assert.Equal(t, "replica-a", peersFromA[0].Name)
	assert.Equal(t, "replica-b", peersFromA[1].Name)
}

// TestHeartbeatRegistry_StartTwiceErrors mirrors
// TestLeaderElector_StartTwiceErrors (task 6.4) for the same reason: Start
// is not meant to be called concurrently/repeatedly on one registry.
func TestHeartbeatRegistry_StartTwiceErrors(t *testing.T) {
	client, _ := setupHeartbeatTestRedis(t)
	ctx := context.Background()

	reg := NewHeartbeatRegistry(client, "replica-a", "", 10*time.Second, time.Hour, discardLogger())
	require.NoError(t, reg.Start(ctx))
	defer func() { _ = reg.Stop(context.Background()) }()

	err := reg.Start(ctx)
	assert.Error(t, err)
}

// TestHeartbeatRegistry_SelfIDGeneratedWhenEmpty proves an empty selfID
// gets a non-empty generated one, and that two independently-generated IDs
// don't collide (hostname + random suffix).
func TestHeartbeatRegistry_SelfIDGeneratedWhenEmpty(t *testing.T) {
	client, _ := setupHeartbeatTestRedis(t)

	regA := NewHeartbeatRegistry(client, "", "", 10*time.Second, time.Hour, discardLogger())
	regB := NewHeartbeatRegistry(client, "", "", 10*time.Second, time.Hour, discardLogger())

	assert.NotEmpty(t, regA.SelfID())
	assert.NotEmpty(t, regB.SelfID())
	assert.NotEqual(t, regA.SelfID(), regB.SelfID())
}

// TestHeartbeatRegistry_StopBeforeStartIsNoop proves Stop is safe to call
// on a registry that was never started (mirrors LeaderElector.Stop's same
// guarantee).
func TestHeartbeatRegistry_StopBeforeStartIsNoop(t *testing.T) {
	client, _ := setupHeartbeatTestRedis(t)
	reg := NewHeartbeatRegistry(client, "replica-a", "", 10*time.Second, time.Hour, discardLogger())
	assert.NoError(t, reg.Stop(context.Background()))
	assert.False(t, reg.IsRegistered())
}
