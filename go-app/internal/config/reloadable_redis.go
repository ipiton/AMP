package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	infracache "github.com/ipiton/AMP/internal/infrastructure/cache"
)

// ================================================================================
// RedisReloadable (INF-A slice 1)
// ================================================================================

// redisReloadPriority: after the cheap in-process swaps (logger, metrics, LLM)
// and before the database, so the most disruptive step is still last.
const redisReloadPriority = 80

// CacheConfigFrom maps AMP's redis config section onto the cache package's own
// config. Single source of truth shared by startup
// (ServiceRegistry.initializeCache) and hot reload, so a reload cannot
// accidentally change a knob the operator never edited.
func CacheConfigFrom(cfg RedisConfig) *infracache.CacheConfig {
	return &infracache.CacheConfig{
		Addr:            cfg.Addr,
		Password:        cfg.Password,
		DB:              cfg.DB,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		MaxRetries:      cfg.MaxRetries,
		MinRetryBackoff: cfg.MinRetryBackoff,
		MaxRetryBackoff: cfg.MaxRetryBackoff,
	}
}

// RedisReloadable hot-reloads the Redis cache client.
//
// What it does when it CAN act (client handle not shared — see
// cache.RedisCache.ShareClient): builds a new client from the new redis.*
// section, PING-verifies it, swaps it in atomically and closes the replaced
// one.
//
// What it does when it CANNOT act: raises W601 (restart required) naming the
// fields that differ from what the live client is running, and returns nil.
// The warning is re-raised on every subsequent reload attempt while the
// divergence lasts, and resolved the moment the config matches the live client
// again (fix-round I2).
//
// In the standard profile the raw *redis.Client is handed at construction time
// to leader election (silence GC claim TTLs), the cluster heartbeat registry,
// the silence event bus, the Redis group storage, the notification log
// (delivered state) and the Redis timer storage. None of them can follow a
// swap, so every redis.* edit is W601 there today — including pool sizing,
// because go-redis fixes PoolSize at client construction and there is no
// resize API to apply to the client those components already hold.
//
// This is the honest reading of the brief's fallback clause: rather than
// "wire pool-size reload and document that addr needs a restart", the wiring
// says pool size is no more reachable than addr is. A wave-3/4 consumer that
// kept using the pre-reload client would hold claim TTLs and delivered-state
// in a different keyspace from the cache, which is a split-brain, not a
// degradation. Fixing it means giving those consumers a handle that follows
// the swap (FU-REDIS-LIVE-CLIENT-HANDLE); the swap machinery is implemented
// and tested here so that change becomes a wiring change only.
type RedisReloadable struct {
	cache    *infracache.RedisCache
	logger   *slog.Logger
	warnings *RestartWarnings

	// mu guards applied — the redis config the live client is ACTUALLY
	// running, not the one the coordinator has committed. See
	// DatabaseReloadable.applied for why the distinction matters (fix-round
	// I2).
	mu      sync.Mutex
	applied RedisConfig
}

// NewRedisReloadable wires a RedisReloadable over an existing Redis cache.
// A nil cache is legal (lite profile, or a standard profile that degraded to
// the in-memory cache); in that case a redis.* change is reported as
// restart-required.
func NewRedisReloadable(
	redisCache *infracache.RedisCache,
	bootCfg *Config,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *RedisReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	reloadable := &RedisReloadable{cache: redisCache, logger: logger, warnings: warnings}
	if bootCfg != nil {
		reloadable.applied = bootCfg.Redis
	}
	return reloadable
}

// Name implements Reloadable.
func (r *RedisReloadable) Name() string { return "redis" }

// RelevantSections implements Reloadable.
func (r *RedisReloadable) RelevantSections() []string { return []string{"redis"} }

// IsCritical implements Reloadable: Redis backs distributed locking, grouping
// state and the notification log.
func (r *RedisReloadable) IsCritical() bool { return true }

// ReloadPriority implements OrderedReloadable.
func (r *RedisReloadable) ReloadPriority() int { return redisReloadPriority }

// NeedsResync implements ResyncReloadable: true while the requested redis
// config differs from what the live client is running.
func (r *RedisReloadable) NeedsResync(newCfg *Config) bool {
	if newCfg == nil {
		return false
	}
	return len(r.drift(newCfg.Redis)) > 0
}

// drift returns the field paths where the requested config differs from what
// the live client is running.
func (r *RedisReloadable) drift(requested RedisConfig) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return changedFields("redis", r.applied, requested)
}

// Reload implements Reloadable.
func (r *RedisReloadable) Reload(ctx context.Context, _, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("redis reload: nil config")
	}

	fields := r.drift(newCfg.Redis)
	if len(fields) == 0 {
		// Live client already matches; drop any stale warning.
		r.warnings.Resolve(WarnRedisRestartRequired, r.Name())
		return nil
	}

	if r.cache == nil {
		warnRestartRequired(r.logger, r.warnings, RestartRequiredWarning{
			Code:      WarnRedisRestartRequired,
			Component: r.Name(),
			Fields:    fields,
			Reason:    "no Redis cache backend in this process (lite profile, or the standard profile degraded to the in-memory cache at startup); restart to pick up the new Redis settings",
		})
		return nil
	}

	if err := r.cache.Reload(ctx, CacheConfigFrom(newCfg.Redis)); err != nil {
		if errors.Is(err, infracache.ErrClientHandleShared) {
			warnRestartRequired(r.logger, r.warnings, RestartRequiredWarning{
				Code:      WarnRedisRestartRequired,
				Component: r.Name(),
				Fields:    fields,
				Reason:    "the Redis client is held directly by leader election, the cluster heartbeat, the silence event bus, group storage, the notification log and timer storage; replacing it under them would split claim TTLs and delivered-state across two keyspaces — restart to apply",
			})
			return nil
		}
		// A real failure: the new settings do not produce a working client
		// (unreachable addr, wrong password, invalid pool config). Reject the
		// reload so the previous config stays active.
		return fmt.Errorf("redis client reload failed: %w", err)
	}

	r.mu.Lock()
	r.applied = newCfg.Redis
	r.mu.Unlock()

	r.warnings.Resolve(WarnRedisRestartRequired, r.Name())
	r.logger.Info("redis client reloaded from config", "fields", fields)
	return nil
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*RedisReloadable)(nil)
	_ OrderedReloadable = (*RedisReloadable)(nil)
	_ ResyncReloadable  = (*RedisReloadable)(nil)
)
