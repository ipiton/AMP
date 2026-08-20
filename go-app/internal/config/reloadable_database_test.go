package config

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func databaseConfigFixture(host string, maxConns int) DatabaseConfig {
	return DatabaseConfig{
		Host:           host,
		Port:           5432,
		Database:       "amp",
		Username:       "amp",
		SSLMode:        "disable",
		MaxConnections: maxConns,
	}
}

func TestDatabaseReloadable_Contract(t *testing.T) {
	reloadable := NewDatabaseReloadable(nil, NewRestartWarnings(), slog.Default())

	assert.Equal(t, "database", reloadable.Name())
	assert.Equal(t, []string{"database"}, reloadable.RelevantSections())
	assert.True(t, reloadable.IsCritical())
	// Storage last: the most disruptive reload runs after everything cheap.
	assert.Equal(t, 90, reloadable.ReloadPriority())
	assert.Greater(t, reloadable.ReloadPriority(), (&RedisReloadable{}).ReloadPriority())
}

func TestDatabaseReloadable_NilPoolWarnsW600InsteadOfPretending(t *testing.T) {
	warnings := NewRestartWarnings()
	reloadable := NewDatabaseReloadable(nil, warnings, slog.Default())

	oldCfg := &Config{Database: databaseConfigFixture("db-1", 10)}
	newCfg := &Config{Database: databaseConfigFixture("db-2", 10)}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnDatabaseRestartRequired, list[0].Code)
	assert.Equal(t, "database", list[0].Component)
	assert.Equal(t, []string{"database.host"}, list[0].Fields)
	assert.Contains(t, list[0].Reason, "lite runs on SQLite")
}

func TestDatabaseReloadable_SharedPoolWarnsW600(t *testing.T) {
	// SharePool is exactly what ServiceRegistry does for the storage adapter,
	// the silence repository, the investigation repository and the tools
	// *sql.DB. No database is needed to prove the refusal: Reload checks the
	// shared-handle flag before it dials anything.
	pool := postgres.NewPostgresPool(PostgresConfigFrom(databaseConfigFixture("db-1", 10)), slog.Default())
	require.Nil(t, pool.SharePool(), "not connected yet, but the handle is now marked shared")
	require.True(t, pool.IsPoolShared())

	warnings := NewRestartWarnings()
	reloadable := NewDatabaseReloadable(pool, warnings, slog.Default())

	oldCfg := &Config{Database: databaseConfigFixture("db-1", 10)}
	newCfg := &Config{Database: databaseConfigFixture("db-1", 25)}

	// Not an error: the rest of the config is still worth applying.
	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnDatabaseRestartRequired, list[0].Code)
	assert.Equal(t, []string{"database.max_connections"}, list[0].Fields)
	assert.Contains(t, list[0].Reason, "split writes across two databases")

	// The pool's config was NOT touched — nothing pretended to apply.
	assert.Equal(t, int32(10), pool.GetConfig().MaxConns)
}

func TestDatabaseReloadable_UnchangedSectionIsNoOp(t *testing.T) {
	pool := postgres.NewPostgresPool(PostgresConfigFrom(databaseConfigFixture("db-1", 10)), slog.Default())
	_ = pool.SharePool()

	warnings := NewRestartWarnings()
	reloadable := NewDatabaseReloadable(pool, warnings, slog.Default())

	cfg := &Config{Database: databaseConfigFixture("db-1", 10)}
	require.NoError(t, reloadable.Reload(context.Background(), cfg, cfg))
	assert.Empty(t, warnings.List(), "an unchanged section must not warn")
}

func TestDatabaseReloadable_UnreachableHostRejectsTheReload(t *testing.T) {
	// Pool handle NOT shared, so Reload really attempts a rebuild — and the
	// rebuild must fail loudly rather than warn, because a bad connection
	// string is an operator error, not an architectural limit.
	pool := postgres.NewPostgresPool(PostgresConfigFrom(databaseConfigFixture("db-1", 10)), slog.Default())
	require.False(t, pool.IsPoolShared())

	warnings := NewRestartWarnings()
	reloadable := NewDatabaseReloadable(pool, warnings, slog.Default())

	unreachable := databaseConfigFixture("127.0.0.1", 10)
	unreachable.Port = 1 // nothing listens here
	unreachable.ConnectTimeout = 500 * time.Millisecond

	oldCfg := &Config{Database: databaseConfigFixture("db-1", 10)}
	newCfg := &Config{Database: unreachable}

	err := reloadable.Reload(context.Background(), oldCfg, newCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database pool reload failed")
	assert.Empty(t, warnings.List())

	// Config untouched: no half-applied state after a failed verification.
	assert.Equal(t, "db-1", pool.GetConfig().Host)
}

func TestDatabaseReloadable_NilNewConfigIsAnError(t *testing.T) {
	reloadable := NewDatabaseReloadable(nil, nil, slog.Default())
	require.Error(t, reloadable.Reload(context.Background(), &Config{}, nil))
}

func TestPostgresConfigFrom_DefaultsForUnsetValues(t *testing.T) {
	defaults := postgres.DefaultConfig()

	// Only the connection identity is set; every pool/timeout knob must fall
	// back to the postgres package's own defaults rather than to zero, which
	// is the whole reason startup and reload share this function.
	got := PostgresConfigFrom(DatabaseConfig{
		Host:     "db",
		Port:     6432,
		Database: "amp",
		Username: "amp",
		SSLMode:  "require",
	})

	assert.Equal(t, "db", got.Host)
	assert.Equal(t, 6432, got.Port)
	assert.Equal(t, "amp", got.Database)
	assert.Equal(t, "amp", got.User)
	assert.Equal(t, "require", got.SSLMode)
	assert.Equal(t, defaults.MaxConns, got.MaxConns)
	assert.Equal(t, defaults.MinConns, got.MinConns)
	assert.Equal(t, defaults.MaxConnLifetime, got.MaxConnLifetime)
	assert.Equal(t, defaults.MaxConnIdleTime, got.MaxConnIdleTime)
	assert.Equal(t, defaults.ConnectTimeout, got.ConnectTimeout)
}

func TestPostgresConfigFrom_OverridesWhenSet(t *testing.T) {
	got := PostgresConfigFrom(DatabaseConfig{
		Host:            "db",
		MaxConnections:  42,
		MinConnections:  7,
		MaxConnLifetime: 2 * time.Hour,
		MaxConnIdleTime: 3 * time.Minute,
		ConnectTimeout:  4 * time.Second,
	})

	assert.Equal(t, int32(42), got.MaxConns)
	assert.Equal(t, int32(7), got.MinConns)
	assert.Equal(t, 2*time.Hour, got.MaxConnLifetime)
	assert.Equal(t, 3*time.Minute, got.MaxConnIdleTime)
	assert.Equal(t, 4*time.Second, got.ConnectTimeout)
}
