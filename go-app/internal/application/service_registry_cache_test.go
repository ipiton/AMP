package application

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appconfig "github.com/ipiton/AMP/internal/config"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
)

// AMP-CACHE-NO-RECONNECT: initializeCache retries an unreachable cache for
// redis.startup_wait in the standard profile instead of settling on the
// in-memory cache after one attempt.

// syncBuffer lets the test read the log while nothing else writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// freeAddr returns a loopback address nothing listens on: connections to it
// are refused at once, like a cache Service whose pod is not up yet.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func newCacheTestRegistry(profile appconfig.DeploymentProfile, addr string, wait time.Duration) (*ServiceRegistry, *syncBuffer) {
	logs := &syncBuffer{}
	return &ServiceRegistry{
		config: &appconfig.Config{
			Profile: profile,
			Redis: appconfig.RedisConfig{
				Addr:        addr,
				PoolSize:    2,
				DialTimeout: 200 * time.Millisecond,
				StartupWait: wait,
			},
		},
		logger:                   slog.New(slog.NewTextHandler(logs, nil)),
		cacheRetryInitialBackoff: 20 * time.Millisecond,
		cacheRetryMaxBackoff:     50 * time.Millisecond,
	}, logs
}

func TestInitializeCache_StandardWaitsForLateCache(t *testing.T) {
	addr := freeAddr(t)
	registry, logs := newCacheTestRegistry(appconfig.ProfileStandard, addr, 10*time.Second)

	mr := miniredis.NewMiniRedis()
	t.Cleanup(mr.Close)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = mr.StartAddr(addr)
	}()

	require.NoError(t, registry.initializeCache(context.Background()))

	redisCache, ok := registry.cache.(*infrastructurecache.RedisCache)
	require.True(t, ok, "cache = %T, want *RedisCache once the cache came up within the window", registry.cache)
	t.Cleanup(func() { _ = redisCache.Close() })
	assert.Empty(t, registry.degradedReasons)
	assert.Contains(t, logs.String(), "Redis cache not reachable yet, retrying")
	assert.Contains(t, logs.String(), "Redis cache initialized")
	assert.NotContains(t, logs.String(), "attempts=1 ", "the cache came up late, so the first attempt cannot have succeeded")
}

func TestInitializeCache_StandardFallsBackAfterWindow(t *testing.T) {
	registry, logs := newCacheTestRegistry(appconfig.ProfileStandard, freeAddr(t), 300*time.Millisecond)

	started := time.Now()
	require.NoError(t, registry.initializeCache(context.Background()))
	elapsed := time.Since(started)

	_, isMemory := registry.cache.(*infrastructurecache.MemoryCache)
	assert.True(t, isMemory, "cache = %T, want the in-memory fallback", registry.cache)
	require.Len(t, registry.degradedReasons, 1)
	assert.True(t, strings.HasPrefix(registry.degradedReasons[0], "cache backend unavailable: "), registry.degradedReasons[0])
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "fell back before the window was spent")
	assert.Less(t, elapsed, 2*time.Second)
	assert.Contains(t, logs.String(), "Redis cache unavailable, falling back to in-memory cache")
	assert.Contains(t, logs.String(), "waited=")
	assert.NotContains(t, logs.String(), "attempts=1 ")
}

// Lite and a zero window keep the pre-fix behavior: one attempt, no delay.
func TestInitializeCache_SingleAttempt(t *testing.T) {
	tests := []struct {
		name    string
		profile appconfig.DeploymentProfile
		wait    time.Duration
	}{
		{"lite ignores the window", appconfig.ProfileLite, 10 * time.Second},
		{"standard with zero window", appconfig.ProfileStandard, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry, logs := newCacheTestRegistry(tt.profile, freeAddr(t), tt.wait)

			started := time.Now()
			require.NoError(t, registry.initializeCache(context.Background()))

			assert.Less(t, time.Since(started), time.Second)
			_, isMemory := registry.cache.(*infrastructurecache.MemoryCache)
			assert.True(t, isMemory, "cache = %T", registry.cache)
			assert.NotContains(t, logs.String(), "retrying")
			assert.Contains(t, logs.String(), "attempts=1 ")
		})
	}
}

func TestInitializeCache_ReachableCacheFirstAttempt(t *testing.T) {
	mr := miniredis.RunT(t)
	registry, logs := newCacheTestRegistry(appconfig.ProfileStandard, mr.Addr(), time.Minute)

	require.NoError(t, registry.initializeCache(context.Background()))

	redisCache, ok := registry.cache.(*infrastructurecache.RedisCache)
	require.True(t, ok, "cache = %T", registry.cache)
	t.Cleanup(func() { _ = redisCache.Close() })
	assert.Empty(t, registry.degradedReasons)
	assert.NotContains(t, logs.String(), "retrying")
	assert.Contains(t, logs.String(), "attempts=1")
}

func TestInitializeCache_CanceledContextStopsWaiting(t *testing.T) {
	registry, _ := newCacheTestRegistry(appconfig.ProfileStandard, freeAddr(t), time.Minute)
	registry.cacheRetryInitialBackoff = 5 * time.Second
	registry.cacheRetryMaxBackoff = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	started := time.Now()
	require.NoError(t, registry.initializeCache(ctx))

	assert.Less(t, time.Since(started), 2*time.Second)
	_, isMemory := registry.cache.(*infrastructurecache.MemoryCache)
	assert.True(t, isMemory, "cache = %T", registry.cache)
	assert.Len(t, registry.degradedReasons, 1)
}
