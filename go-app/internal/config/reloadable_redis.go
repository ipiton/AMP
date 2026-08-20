package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
// changed fields, and returns nil.
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
}

// NewRedisReloadable wires a RedisReloadable over an existing Redis cache.
// A nil cache is legal (lite profile, or a standard profile that degraded to
// the in-memory cache); in that case a redis.* change is reported as
// restart-required.
func NewRedisReloadable(
	redisCache *infracache.RedisCache,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *RedisReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisReloadable{cache: redisCache, logger: logger, warnings: warnings}
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

// Reload implements Reloadable.
func (r *RedisReloadable) Reload(ctx context.Context, oldCfg, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("redis reload: nil config")
	}

	var fields []string
	if oldCfg != nil {
		fields = changedFields("redis", oldCfg.Redis, newCfg.Redis)
		if len(fields) == 0 {
			return nil
		}
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

	r.logger.Info("redis client reloaded from config", "fields", fields)
	return nil
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*RedisReloadable)(nil)
	_ OrderedReloadable = (*RedisReloadable)(nil)
)
