package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ipiton/AMP/internal/database/postgres"
)

// ================================================================================
// DatabaseReloadable (INF-A slice 1)
// ================================================================================

// databaseReloadPriority puts the database last: a pool rebuild is the most
// expensive and most disruptive step, so everything cheap (logger, metrics,
// LLM) is already applied by the time we touch it, and a pool failure rejects
// the reload before it can be followed by other side effects.
const databaseReloadPriority = 90

// PostgresConfigFrom maps AMP's database config section onto the postgres
// package's own config, applying postgres.DefaultConfig() for every value the
// operator left at zero.
//
// Single source of truth on purpose: startup (ServiceRegistry.initializeDatabase)
// and hot reload (DatabaseReloadable) must produce byte-identical pool configs
// from the same YAML, otherwise a reload would silently change knobs the
// operator never edited.
func PostgresConfigFrom(cfg DatabaseConfig) *postgres.PostgresConfig {
	dbCfg := postgres.DefaultConfig()
	dbCfg.Host = cfg.Host
	dbCfg.Port = cfg.Port
	dbCfg.Database = cfg.Database
	dbCfg.User = cfg.Username
	dbCfg.Password = cfg.Password
	dbCfg.SSLMode = cfg.SSLMode
	if cfg.MaxConnections > 0 {
		dbCfg.MaxConns = int32(cfg.MaxConnections) //nolint:gosec // config-bounded, validated by ConfigValidator
	}
	if cfg.MinConnections > 0 {
		dbCfg.MinConns = int32(cfg.MinConnections) //nolint:gosec // config-bounded, validated by ConfigValidator
	}
	if cfg.MaxConnLifetime > 0 {
		dbCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		dbCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.ConnectTimeout > 0 {
		dbCfg.ConnectTimeout = cfg.ConnectTimeout
	}
	return dbCfg
}

// DatabaseReloadable hot-reloads the PostgreSQL connection pool.
//
// What it does when it CAN act (pool handle not shared — see
// postgres.PostgresPool.SharePool): rebuilds the pool from the new
// database.* section, Ping-verifies it, swaps it in atomically, and drains the
// replaced pool for 5s before closing it, so queries already holding a
// connection finish on the old pool instead of being cut off.
//
// What it does when it CANNOT act: raises W600 (restart required) naming the
// fields that differ from what the live pool is running, and returns nil. It
// does NOT fail the reload — the rest of the config is still valid and worth
// applying — and it does NOT pretend to have applied anything.
//
// The standard profile hands the raw *pgxpool.Pool to the storage adapter, the
// silence repository, the investigation repository and the investigation tools
// *sql.DB at construction time. Those holders cannot follow a swap, so in that
// profile every database.* edit is W600 today. That is the honest state of the
// wiring, not a placeholder: closing a pool those components still query would
// take the process down, and swapping only this wrapper's own pool while they
// keep the old one would split writes across two servers on a host change.
// Fixing it means giving them a handle that follows the swap
// (FU-DB-LIVE-POOL-HANDLE).
type DatabaseReloadable struct {
	pool     *postgres.PostgresPool
	logger   *slog.Logger
	warnings *RestartWarnings

	// mu guards applied, which is the database config the live pool is
	// ACTUALLY running — not the config the coordinator has committed. Every
	// decision this component makes is taken against it, which is what keeps
	// a declined change reported for as long as it is unapplied (fix-round
	// I2): the coordinator commits the new config regardless, so comparing
	// against oldCfg would make the divergence invisible from the next reload
	// onwards.
	mu      sync.Mutex
	applied DatabaseConfig
}

// NewDatabaseReloadable wires a DatabaseReloadable over an existing pool.
// A nil pool is legal (lite profile runs on SQLite and never builds one); in
// that case a database.* change is reported as restart-required.
//
// bootCfg is the config the process started with — the state the live pool is
// in before any reload.
func NewDatabaseReloadable(
	pool *postgres.PostgresPool,
	bootCfg *Config,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *DatabaseReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	reloadable := &DatabaseReloadable{pool: pool, logger: logger, warnings: warnings}
	if bootCfg != nil {
		reloadable.applied = bootCfg.Database
	}
	return reloadable
}

// Name implements Reloadable.
func (d *DatabaseReloadable) Name() string { return "database" }

// RelevantSections implements Reloadable.
func (d *DatabaseReloadable) RelevantSections() []string { return []string{"database"} }

// IsCritical implements Reloadable: losing the database is an outage.
func (d *DatabaseReloadable) IsCritical() bool { return true }

// ReloadPriority implements OrderedReloadable.
func (d *DatabaseReloadable) ReloadPriority() int { return databaseReloadPriority }

// NeedsResync implements ResyncReloadable: true while the requested database
// config differs from what the live pool is running.
func (d *DatabaseReloadable) NeedsResync(newCfg *Config) bool {
	if newCfg == nil {
		return false
	}
	return len(d.drift(newCfg.Database)) > 0
}

// drift returns the field paths where the requested config differs from what
// the live pool is running.
func (d *DatabaseReloadable) drift(requested DatabaseConfig) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return changedFields("database", d.applied, requested)
}

// Reload implements Reloadable.
func (d *DatabaseReloadable) Reload(ctx context.Context, _, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("database reload: nil config")
	}

	fields := d.drift(newCfg.Database)
	if len(fields) == 0 {
		// Live pool already matches: nothing to do, and any previous
		// restart-required warning no longer describes reality.
		d.warnings.Resolve(WarnDatabaseRestartRequired, d.Name())
		return nil
	}

	if d.pool == nil {
		warnRestartRequired(d.logger, d.warnings, RestartRequiredWarning{
			Code:      WarnDatabaseRestartRequired,
			Component: d.Name(),
			Fields:    fields,
			Reason:    "no PostgreSQL pool in this deployment profile (lite runs on SQLite, wired at startup); restart to pick up the new database settings",
		})
		return nil
	}

	if err := d.pool.Reload(ctx, PostgresConfigFrom(newCfg.Database)); err != nil {
		if errors.Is(err, postgres.ErrPoolHandleShared) {
			warnRestartRequired(d.logger, d.warnings, RestartRequiredWarning{
				Code:      WarnDatabaseRestartRequired,
				Component: d.Name(),
				Fields:    fields,
				Reason:    "the connection pool is held directly by the storage adapter, silence repository and investigation repository; replacing it under them would either close a pool they are querying or split writes across two databases — restart to apply",
			})
			return nil
		}
		// A real failure: the new settings do not produce a working pool.
		// Reject the reload so the previous config stays active.
		return fmt.Errorf("database pool reload failed: %w", err)
	}

	d.mu.Lock()
	d.applied = newCfg.Database
	d.mu.Unlock()

	d.warnings.Resolve(WarnDatabaseRestartRequired, d.Name())
	d.logger.Info("database pool reloaded from config", "fields", fields)
	return nil
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*DatabaseReloadable)(nil)
	_ OrderedReloadable = (*DatabaseReloadable)(nil)
	_ ResyncReloadable  = (*DatabaseReloadable)(nil)
)
