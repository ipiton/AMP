package config

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	infracache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRedisFixture(t *testing.T) (*infracache.RedisCache, *miniredis.Miniredis) {
	t.Helper()

	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)

	cache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        server.Addr(),
		PoolSize:    4,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	return cache, server
}

func redisConfigFor(server *miniredis.Miniredis, poolSize int) RedisConfig {
	return RedisConfig{
		Addr:        server.Addr(),
		PoolSize:    poolSize,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}
}

func TestRedisReloadable_Contract(t *testing.T) {
	cache, _ := newRedisFixture(t)
	reloadable := NewRedisReloadable(cache, NewRestartWarnings(), slog.Default())

	assert.Equal(t, "redis", reloadable.Name())
	assert.Equal(t, []string{"redis"}, reloadable.RelevantSections())
	assert.True(t, reloadable.IsCritical())
	assert.Equal(t, 80, reloadable.ReloadPriority())
	// Redis reloads before the database, after the cheap in-process swaps.
	assert.Greater(t, reloadable.ReloadPriority(), (&LLMReloadable{}).ReloadPriority())
	assert.Less(t, reloadable.ReloadPriority(), (&DatabaseReloadable{}).ReloadPriority())
}

func TestRedisReloadable_SwapsClientWhenHandleIsNotShared(t *testing.T) {
	cache, oldServer := newRedisFixture(t)
	warnings := NewRestartWarnings()
	reloadable := NewRedisReloadable(cache, warnings, slog.Default())

	newServer, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(newServer.Close)

	oldCfg := &Config{Redis: redisConfigFor(oldServer, 4)}
	newCfg := &Config{Redis: redisConfigFor(newServer, 9)}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))
	assert.Empty(t, warnings.List(), "a swap that really happened must not warn")

	// The cache now talks to the NEW server: writes land there, not on the old.
	require.NoError(t, cache.Set(context.Background(), "post-reload", "value", time.Minute))
	assert.True(t, newServer.Exists("post-reload"), "write must land on the reloaded server")
	assert.False(t, oldServer.Exists("post-reload"), "old server must no longer receive writes")

	assert.Equal(t, newServer.Addr(), cache.Config().Addr)
	assert.Equal(t, 9, cache.Config().PoolSize)
}

func TestRedisReloadable_SharedHandleWarnsW601AndKeepsTheOldClient(t *testing.T) {
	cache, oldServer := newRedisFixture(t)
	warnings := NewRestartWarnings()
	reloadable := NewRedisReloadable(cache, warnings, slog.Default())

	// This is what ServiceRegistry does for grouping/nflog/leader election.
	shared := cache.ShareClient()
	require.NotNil(t, shared)
	require.True(t, cache.IsClientShared())

	newServer, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(newServer.Close)

	oldCfg := &Config{Redis: redisConfigFor(oldServer, 4)}
	newCfg := &Config{Redis: redisConfigFor(newServer, 4)}

	// The reload as a whole is NOT failed — the rest of the config is valid.
	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnRedisRestartRequired, list[0].Code)
	assert.Equal(t, "redis", list[0].Component)
	assert.Equal(t, []string{"redis.addr"}, list[0].Fields)
	assert.Contains(t, list[0].Reason, "delivered-state")

	// The shared handle is still usable and still pointed at the old server —
	// nothing was closed under the consumers that hold it.
	require.NoError(t, shared.Set(context.Background(), "still-alive", "1", time.Minute).Err())
	assert.True(t, oldServer.Exists("still-alive"))
	assert.Equal(t, oldServer.Addr(), cache.Config().Addr)
}

func TestRedisReloadable_UnreachableAddrRejectsTheReload(t *testing.T) {
	cache, oldServer := newRedisFixture(t)
	warnings := NewRestartWarnings()
	reloadable := NewRedisReloadable(cache, warnings, slog.Default())

	oldCfg := &Config{Redis: redisConfigFor(oldServer, 4)}
	newCfg := &Config{Redis: RedisConfig{
		Addr:        "127.0.0.1:1", // nothing listens here
		PoolSize:    4,
		DialTimeout: 200 * time.Millisecond,
		ReadTimeout: 200 * time.Millisecond,
	}}

	err := reloadable.Reload(context.Background(), oldCfg, newCfg)
	require.Error(t, err, "an unverifiable client must reject the reload, not warn")
	assert.Contains(t, err.Error(), "redis client reload failed")
	assert.Empty(t, warnings.List())

	// Previous client still live and still on the old server.
	assert.Equal(t, oldServer.Addr(), cache.Config().Addr)
	require.NoError(t, cache.Set(context.Background(), "after-failed-reload", "1", time.Minute))
	assert.True(t, oldServer.Exists("after-failed-reload"))
}

func TestRedisReloadable_UnchangedSectionIsNoOp(t *testing.T) {
	cache, server := newRedisFixture(t)
	warnings := NewRestartWarnings()
	reloadable := NewRedisReloadable(cache, warnings, slog.Default())

	cfg := &Config{Redis: redisConfigFor(server, 4)}
	require.NoError(t, reloadable.Reload(context.Background(), cfg, cfg))
	assert.Empty(t, warnings.List())
}

func TestRedisReloadable_NilCacheWarnsInsteadOfPretending(t *testing.T) {
	warnings := NewRestartWarnings()
	reloadable := NewRedisReloadable(nil, warnings, slog.Default())

	oldCfg := &Config{Redis: RedisConfig{Addr: "old:6379"}}
	newCfg := &Config{Redis: RedisConfig{Addr: "new:6379"}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnRedisRestartRequired, list[0].Code)
	assert.Contains(t, list[0].Reason, "no Redis cache backend")
}

func TestCacheConfigFrom_CarriesEveryConnectionField(t *testing.T) {
	const secretFixture = "redis-auth-fixture" // #nosec G101 -- test fixture, not a real credential

	src := RedisConfig{
		Addr:            "redis:6379",
		Password:        secretFixture,
		DB:              3,
		PoolSize:        11,
		MinIdleConns:    2,
		DialTimeout:     time.Second,
		ReadTimeout:     2 * time.Second,
		WriteTimeout:    3 * time.Second,
		MaxRetries:      4,
		MinRetryBackoff: 5 * time.Millisecond,
		MaxRetryBackoff: 6 * time.Millisecond,
	}

	got := CacheConfigFrom(src)

	assert.Equal(t, src.Addr, got.Addr)
	assert.Equal(t, src.Password, got.Password)
	assert.Equal(t, src.DB, got.DB)
	assert.Equal(t, src.PoolSize, got.PoolSize)
	assert.Equal(t, src.MinIdleConns, got.MinIdleConns)
	assert.Equal(t, src.DialTimeout, got.DialTimeout)
	assert.Equal(t, src.ReadTimeout, got.ReadTimeout)
	assert.Equal(t, src.WriteTimeout, got.WriteTimeout)
	assert.Equal(t, src.MaxRetries, got.MaxRetries)
	assert.Equal(t, src.MinRetryBackoff, got.MinRetryBackoff)
	assert.Equal(t, src.MaxRetryBackoff, got.MaxRetryBackoff)
}
