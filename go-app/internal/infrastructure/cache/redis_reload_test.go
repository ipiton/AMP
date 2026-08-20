package cache

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// RedisCache.Reload — client recreation with PING verification (INF-A slice 1)
// ================================================================================

func configFor(server *miniredis.Miniredis, poolSize int) *CacheConfig {
	return &CacheConfig{
		Addr:        server.Addr(),
		PoolSize:    poolSize,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}
}

func TestRedisCache_Reload_SwapsClientAndClosesTheOldOne(t *testing.T) {
	oldServer, err := miniredis.Run()
	require.NoError(t, err)
	defer oldServer.Close()

	newServer, err := miniredis.Run()
	require.NoError(t, err)
	defer newServer.Close()

	cache, err := NewRedisCache(configFor(oldServer, 4), slog.Default())
	require.NoError(t, err)
	defer func() { _ = cache.Close() }()

	replaced := cache.GetClient() // short-lived borrow, must not block Reload
	require.False(t, cache.IsClientShared())

	require.NoError(t, cache.Reload(context.Background(), configFor(newServer, 8)))

	assert.Equal(t, newServer.Addr(), cache.Config().Addr)
	assert.Equal(t, 8, cache.Config().PoolSize)
	assert.NotSame(t, replaced, cache.GetClient(), "Reload must publish a new client")

	// New traffic goes to the new server.
	require.NoError(t, cache.Set(context.Background(), "key", "value", time.Minute))
	assert.True(t, newServer.Exists("key"))
	assert.False(t, oldServer.Exists("key"))

	// The replaced client was closed (no leak).
	require.Error(t, replaced.Ping(context.Background()).Err())
}

func TestRedisCache_Reload_FailedPingKeepsOldClient(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cache, err := NewRedisCache(configFor(server, 4), slog.Default())
	require.NoError(t, err)
	defer func() { _ = cache.Close() }()

	err = cache.Reload(context.Background(), &CacheConfig{
		Addr:        "127.0.0.1:1", // nothing listens here
		PoolSize:    4,
		DialTimeout: 200 * time.Millisecond,
		ReadTimeout: 200 * time.Millisecond,
	})
	require.Error(t, err, "a client that cannot PING must never be published")

	assert.Equal(t, server.Addr(), cache.Config().Addr)
	require.NoError(t, cache.Set(context.Background(), "still-works", "1", time.Minute))
	assert.True(t, server.Exists("still-works"))
}

func TestRedisCache_Reload_RefusesWhenClientShared(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cache, err := NewRedisCache(configFor(server, 4), slog.Default())
	require.NoError(t, err)
	defer func() { _ = cache.Close() }()

	shared := cache.ShareClient()
	require.NotNil(t, shared)
	require.True(t, cache.IsClientShared())

	err = cache.Reload(context.Background(), configFor(server, 9))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrClientHandleShared))

	// The shared client is untouched and still usable.
	require.NoError(t, shared.Ping(context.Background()).Err())
	assert.Equal(t, 4, cache.Config().PoolSize)
}

func TestRedisCache_Reload_RejectsInvalidConfig(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cache, err := NewRedisCache(configFor(server, 4), slog.Default())
	require.NoError(t, err)
	defer func() { _ = cache.Close() }()

	require.Error(t, cache.Reload(context.Background(), nil))
	// Empty Addr fails CacheConfig.Validate.
	require.Error(t, cache.Reload(context.Background(), &CacheConfig{PoolSize: 4, DialTimeout: time.Second}))
}
