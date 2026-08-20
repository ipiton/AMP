package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/database/postgres"
	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// Restart-required durability (fix-round I2) and rollback completeness (I3)
// ================================================================================

// ---------------------------------------------------------------- I2: durability

// TestDeclinedChange_SurvivesAnUnrelatedReload is the fix-round I2 case: a
// declined database change must still be reported after a later reload that
// touched a completely different section.
//
// It used to disappear twice over — the coordinator commits the declined config
// so the next diff sees no `database` change (section gate skips the component),
// and ReloadConfig cleared the whole warning set on every attempt.
func TestDeclinedChange_SurvivesAnUnrelatedReload(t *testing.T) {
	pool := postgres.NewPostgresPool(PostgresConfigFrom(databaseConfigFixture("db-1", 10)), slog.Default())
	_ = pool.SharePool() // what ServiceRegistry does: pool cannot be swapped

	warnings := NewRestartWarnings()
	bootCfg := &Config{
		Database: databaseConfigFixture("db-1", 10),
		Log:      LogConfig{Level: "info", Format: "json"},
	}

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(NewDatabaseReloadable(pool, bootCfg, warnings, slog.Default()))
	reloader.Register(&fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10})

	// Attempt 1: the operator repoints the database. Declined with W600.
	declined := &Config{
		Database: databaseConfigFixture("db-2", 10),
		Log:      bootCfg.Log,
	}
	require.Empty(t, reloader.ReloadAll(context.Background(), bootCfg, declined, []string{"database"}))

	list := warnings.List()
	require.Len(t, list, 1)
	require.Equal(t, WarnDatabaseRestartRequired, list[0].Code)
	assert.Equal(t, []string{"database.host"}, list[0].Fields)

	// Attempt 2: somebody edits `log` only. The coordinator's diff flags just
	// "logger", and `database` looks unchanged since attempt 1 — both gates
	// would drop the database component before this fix.
	unrelated := &Config{
		Database: declined.Database,
		Log:      LogConfig{Level: "debug", Format: "json"},
	}
	selected := reloader.SelectComponents(declined, unrelated, []string{"logger"})
	assert.Contains(t, selected, "database",
		"a component whose live state diverges must be selected regardless of the diff")

	require.Empty(t, reloader.ReloadAll(context.Background(), declined, unrelated, []string{"logger"}))

	list = warnings.List()
	require.Len(t, list, 1, "the restart-required fact must outlive an unrelated reload")
	assert.Equal(t, WarnDatabaseRestartRequired, list[0].Code)
	assert.Equal(t, []string{"database.host"}, list[0].Fields,
		"and it must still name the field that is not live")
}

// TestDeclinedChange_ResolvesWhenTheOperatorRevertsIt is the other half: the
// warning is derived from the live state, so putting the config back clears it
// without a restart.
func TestDeclinedChange_ResolvesWhenTheOperatorRevertsIt(t *testing.T) {
	pool := postgres.NewPostgresPool(PostgresConfigFrom(databaseConfigFixture("db-1", 10)), slog.Default())
	_ = pool.SharePool()

	warnings := NewRestartWarnings()
	bootCfg := &Config{Database: databaseConfigFixture("db-1", 10)}
	reloadable := NewDatabaseReloadable(pool, bootCfg, warnings, slog.Default())

	declined := &Config{Database: databaseConfigFixture("db-2", 10)}
	require.NoError(t, reloadable.Reload(context.Background(), bootCfg, declined))
	require.Len(t, warnings.List(), 1)
	assert.True(t, reloadable.NeedsResync(declined))

	// Operator reverts.
	require.NoError(t, reloadable.Reload(context.Background(), declined, bootCfg))
	assert.Empty(t, warnings.List(), "reverting the edit must clear the warning")
	assert.False(t, reloadable.NeedsResync(bootCfg))
}

// TestDeclinedRedisChange_IsDurableToo mirrors I2 for Redis, against a real
// miniredis with a shared handle.
func TestDeclinedRedisChange_IsDurableToo(t *testing.T) {
	cache, server := newRedisFixture(t)
	_ = cache.ShareClient()

	warnings := NewRestartWarnings()
	bootCfg := &Config{Redis: redisConfigFor(server, 4)}
	reloadable := NewRedisReloadable(cache, bootCfg, warnings, slog.Default())

	declined := &Config{Redis: redisConfigFor(server, 32)}
	require.NoError(t, reloadable.Reload(context.Background(), bootCfg, declined))
	require.Len(t, warnings.List(), 1)

	// A second attempt that changes nothing since the first still re-raises.
	require.NoError(t, reloadable.Reload(context.Background(), declined, declined))
	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnRedisRestartRequired, list[0].Code)
	assert.Equal(t, []string{"redis.pool_size"}, list[0].Fields)
}

// TestPermanentlyUnappliableFields_KeepWarning covers the same durability rule
// for the three components that DO apply their live half: the unappliable
// remainder must keep being reported.
func TestPermanentlyUnappliableFields_KeepWarning(t *testing.T) {
	t.Run("metrics path/port", func(t *testing.T) {
		warnings := NewRestartWarnings()
		bootCfg := &Config{Metrics: MetricsConfig{Enabled: true, Path: "/metrics"}}
		gate := metricsv2.NewExpositionGate(bootCfg.Metrics.Enabled)
		reloadable := NewMetricsReloadable(gate, bootCfg, warnings, slog.Default())

		asked := &Config{Metrics: MetricsConfig{Enabled: false, Path: "/prom"}}
		require.NoError(t, reloadable.Reload(context.Background(), bootCfg, asked))
		assert.False(t, gate.Enabled(), "the appliable half must be applied")

		list := warnings.List()
		require.Len(t, list, 1)
		assert.Equal(t, []string{"metrics.path"}, list[0].Fields,
			"only the unappliable remainder is reported")

		// Re-attempting the same config keeps the warning alive.
		require.NoError(t, reloadable.Reload(context.Background(), asked, asked))
		require.Len(t, warnings.List(), 1)
	})

	t.Run("llm enabled/agent_mode", func(t *testing.T) {
		oldCfg := &Config{LLM: LLMConfig{Enabled: true, Model: "gpt-4o"}}
		reloadable, client, warnings := newLLMFixture(oldCfg)

		asked := &Config{LLM: LLMConfig{Enabled: false, Model: "gpt-5"}}
		require.NoError(t, reloadable.Reload(context.Background(), oldCfg, asked))
		assert.Equal(t, "gpt-5", client.GetConfig().Model, "the appliable half must be applied")

		list := warnings.List()
		require.Len(t, list, 1)
		assert.Equal(t, []string{"llm.enabled"}, list[0].Fields)

		require.NoError(t, reloadable.Reload(context.Background(), asked, asked))
		require.Len(t, warnings.List(), 1)
	})

	t.Run("logger sink", func(t *testing.T) {
		bootCfg := &Config{Log: LogConfig{Level: "info", Format: "json"}}
		reloadable, handler, warnings, _ := newLoggerFixture(t, bootCfg)

		asked := &Config{Log: LogConfig{Level: "debug", Format: "json", Output: "file", Filename: "amp.log"}}
		require.NoError(t, reloadable.Reload(context.Background(), bootCfg, asked))
		assert.Equal(t, slog.LevelDebug, handler.Level())

		list := warnings.List()
		require.Len(t, list, 1)
		assert.ElementsMatch(t, []string{"log.output", "log.filename"}, list[0].Fields)

		require.NoError(t, reloadable.Reload(context.Background(), asked, asked))
		require.Len(t, warnings.List(), 1)
	})
}

// ---------------------------------------------------------------- I3: rollback

// TestRollback_AttemptsEveryComponentAndReportsTheSplit is the fix-round I3
// case: when a rollback leg fails, the remaining legs must still be rolled back
// and the split state must be reported loudly, not left in one log line.
func TestRollback_AttemptsEveryComponentAndReportsTheSplit(t *testing.T) {
	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)
	newPath := writeReloadConfigFile(t, "debug", false)

	recorder := &orderRecorder{}

	// logger (10) rolls back fine.
	logger := &recordingReloadable{
		fakeReloadable: fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10},
		recorder:       recorder,
	}
	// metrics (20) fails BOTH ways: it rejects the new config, then fails to go
	// back — the leg that used to abandon everything after it.
	metrics := &recordingReloadable{
		fakeReloadable: fakeReloadable{
			name:     "metrics",
			sections: []string{"metrics"},
			priority: 20,
			err:      fmt.Errorf("gate wedged"),
		},
		recorder: recorder,
	}
	// database (90) is always relevant, so it must still be attempted after
	// metrics' rollback leg failed.
	database := &recordingReloadable{
		fakeReloadable: fakeReloadable{name: "database", priority: 90, critical: true},
		recorder:       recorder,
	}

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(logger)
	reloader.Register(metrics)
	reloader.Register(database)

	warnings := NewRestartWarnings()
	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{}, NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)
	coordinator.SetRestartWarnings(warnings)

	_, err = coordinator.ReloadFromFile(context.Background(), newPath)
	require.Error(t, err, "a reload whose rollback failed is an error, not a result")
	assert.Contains(t, err.Error(), "rollback")

	// Every leg after the failing one was still attempted.
	order := recorder.snapshot()
	assert.Equal(t,
		[]string{"logger", "metrics", "logger", "metrics", "database"},
		order,
		"forward pass stops at metrics; rollback pass attempts logger, metrics AND database")

	// And the split state is queryable, not just logged.
	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnReloadRollbackIncomplete, list[0].Code)
	assert.Equal(t, []string{"metrics"}, list[0].Fields,
		"W610 names the components still running the rejected config")
	assert.Contains(t, list[0].Reason, "restart to converge")

	// The coordinator still reports the previous config.
	assert.Equal(t, "info", coordinator.GetCurrentConfig().Log.Level)
}

// TestRollbackCommitted_UndoesAPostCommitFailure covers the coordinator half of
// fix-round I4: a caller that discovers a failure after commit can drive
// everything back.
func TestRollbackCommitted_UndoesAPostCommitFailure(t *testing.T) {
	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)
	newPath := writeReloadConfigFile(t, "debug", true)

	logger := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	reloader := NewConfigReloader(slog.Default())
	reloader.Register(logger)

	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{}, NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)

	result, err := coordinator.ReloadFromFile(context.Background(), newPath)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "debug", coordinator.GetCurrentConfig().Log.Level)

	// The caller's own post-commit stage failed: undo the whole thing.
	require.NoError(t, coordinator.RollbackCommitted(context.Background(), initial))

	assert.Equal(t, "info", coordinator.GetCurrentConfig().Log.Level)
	_, status, _ := coordinator.GetReloadStatus()
	assert.Equal(t, "rolled_back", status)

	logger.mu.Lock()
	defer logger.mu.Unlock()
	require.Len(t, logger.calls, 2)
	assert.Equal(t, "debug", logger.calls[1].oldCfg.Log.Level)
	assert.Equal(t, "info", logger.calls[1].newCfg.Log.Level)
}

func TestRollbackCommitted_RejectsNilPrevious(t *testing.T) {
	coordinator := NewReloadCoordinator(
		&Config{}, "/tmp/config.yaml",
		&MockConfigValidator{}, &MockConfigComparator{}, NewConfigReloader(slog.Default()),
		nil, nil, slog.Default(),
	)
	require.Error(t, coordinator.RollbackCommitted(context.Background(), nil))
}

func TestRestartWarnings_Resolve(t *testing.T) {
	warnings := NewRestartWarnings()
	warnings.Record(RestartRequiredWarning{Code: WarnDatabaseRestartRequired, Component: "database", At: time.Now()})
	warnings.Record(RestartRequiredWarning{Code: WarnRedisRestartRequired, Component: "redis"})
	require.Len(t, warnings.List(), 2)

	warnings.Resolve(WarnDatabaseRestartRequired, "database")
	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, "redis", list[0].Component)

	// Resolving something absent is a no-op, and a nil receiver is safe.
	warnings.Resolve("W999", "nobody")
	assert.Len(t, warnings.List(), 1)

	var nilWarnings *RestartWarnings
	nilWarnings.Resolve(WarnRedisRestartRequired, "redis")
}

// TestSplitStateWarning_CarriesNoConfigValues is the re-review I5 guard: the
// W610 warning is served verbatim by the UNAUTHENTICATED /health/reload, so the
// component errors it is built from must not leak into it.
//
// The failing component here returns a pgx-shaped error, which is exactly what
// a real database rejection produces: it embeds the user, the database name and
// every dialed host:port (pgx redacts only the password).
func TestSplitStateWarning_CarriesNoConfigValues(t *testing.T) {
	const leakyError = `failed to connect to ` +
		"`user=amp_prod database=amp_prod`" +
		`: [::1]:5432 (db-prod-1.internal): dial error: connection refused`

	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)
	newPath := writeReloadConfigFile(t, "debug", false)

	// Fails in BOTH directions, so the rollback leg fails and W610 is raised.
	breaker := &fakeReloadable{
		name:     "database",
		priority: 90,
		critical: true,
		err:      fmt.Errorf("%s", leakyError),
	}

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(breaker)

	warnings := NewRestartWarnings()
	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{}, NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)
	coordinator.SetRestartWarnings(warnings)

	_, err = coordinator.ReloadFromFile(context.Background(), newPath)
	require.Error(t, err)

	list := warnings.List()
	require.Len(t, list, 1)
	warning := list[0]
	require.Equal(t, WarnReloadRollbackIncomplete, warning.Code)

	// The component name IS reported — that is the actionable part.
	assert.Equal(t, []string{"database"}, warning.Fields)

	// Nothing else from the error is. Assert on the whole serialized warning,
	// which is what the endpoint actually writes.
	serialized, err := json.Marshal(warning)
	require.NoError(t, err)
	body := string(serialized)

	for _, leak := range []string{
		"user=", "database=", "amp_prod", "db-prod-1.internal", "5432",
		"connection refused", "dial error",
	} {
		assert.NotContains(t, body, leak,
			"the unauthenticated /health/reload body must not echo config values or error text")
	}
}
