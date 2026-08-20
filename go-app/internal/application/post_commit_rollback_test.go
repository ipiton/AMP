package application

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	appconfig "github.com/ipiton/AMP/internal/config"
	pkglogger "github.com/ipiton/AMP/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// Post-commit applier failure rolls the whole reload back (fix-round I4)
// ================================================================================
// ServiceRegistry applies routing/templates/receivers/inhibition AFTER the
// coordinator has committed the config and after all five registered components
// adopted it. A failure there used to return "reload failed" to the operator
// while the new config was fully live everywhere — the inverse of the split
// state I3 covers, and about to become machine-readable via slice 2's
// /health/reload.

// newPostCommitRegistry builds a registry with a live coordinator over a real
// config file, so RollbackCommitted has something to roll back.
func newPostCommitRegistry(t *testing.T) (*ServiceRegistry, *appconfig.Config, *appconfig.Config) {
	t.Helper()

	write := func(level string) string {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := fmt.Sprintf("app:\n  name: test-app\nserver:\n  host: localhost\n  port: 8080\nlog:\n  level: %s\n  format: json\n", level)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	previous, err := appconfig.LoadConfig(write("info"))
	require.NoError(t, err)
	committedPath := write("debug")
	committed, err := appconfig.LoadConfig(committedPath)
	require.NoError(t, err)

	registry, err := NewServiceRegistry(committed, slog.Default())
	require.NoError(t, err)
	registry.restartWarnings = appconfig.NewRestartWarnings()

	reloader := appconfig.NewConfigReloader(slog.Default())
	registry.reloadCoordinator = appconfig.NewReloadCoordinator(
		committed, committedPath,
		appconfig.NewConfigValidator(), appconfig.NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)
	registry.reloadCoordinator.SetRestartWarnings(registry.restartWarnings)

	return registry, previous, committed
}

func TestRollbackPostCommit_RestoresTheConfigAndReportsTheStage(t *testing.T) {
	registry, previous, committed := newPostCommitRegistry(t)
	require.Equal(t, "debug", registry.Config().Log.Level)
	require.Equal(t, "debug", registry.reloadCoordinator.GetCurrentConfig().Log.Level)

	cause := fmt.Errorf("route tree build failed: receiver \"gone\" is not defined")
	err := registry.rollbackPostCommit(context.Background(), previous, "routing", cause)

	// The operator gets an error that says the reload was undone, not a bare
	// "reload failed" over a fully applied config.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "routing reload failed")
	assert.Contains(t, err.Error(), "rolled back to the previous config")
	assert.ErrorIs(t, err, cause, "the operator must still see the original cause")

	// Both views are back on the previous config.
	assert.Equal(t, "info", registry.Config().Log.Level)
	assert.Equal(t, "info", registry.reloadCoordinator.GetCurrentConfig().Log.Level)
	assert.NotSame(t, committed, registry.Config())

	// And the failed stage is queryable, not just logged.
	list := registry.RestartWarnings()
	require.Len(t, list, 1)
	assert.Equal(t, appconfig.WarnReloadPostCommitFailed, list[0].Code)
	assert.Equal(t, "service-registry", list[0].Component)
	assert.Equal(t, []string{"routing"}, list[0].Fields)
	assert.Contains(t, list[0].Reason, "rolled back")
}

func TestRollbackPostCommit_RollsRegisteredComponentsBackToo(t *testing.T) {
	registry, previous, _ := newPostCommitRegistry(t)

	// A live component that adopted the committed config must be driven back.
	handler, err := pkglogger.NewSwappableHandler(io.Discard, slog.LevelInfo, "json")
	require.NoError(t, err)
	require.NoError(t, handler.Swap(slog.LevelDebug, "json"))

	reloader := appconfig.NewConfigReloader(slog.Default())
	reloader.Register(appconfig.NewLoggerReloadable(handler, registry.Config(), registry.restartWarnings, slog.Default()))
	registry.reloadCoordinator = appconfig.NewReloadCoordinator(
		registry.Config(), "unused",
		appconfig.NewConfigValidator(), appconfig.NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)
	registry.reloadCoordinator.SetRestartWarnings(registry.restartWarnings)

	require.Equal(t, slog.LevelDebug, handler.Level())

	err = registry.rollbackPostCommit(context.Background(), previous, "routing", fmt.Errorf("boom"))
	require.Error(t, err)

	assert.Equal(t, slog.LevelInfo, handler.Level(),
		"the logger must be driven back to the previous level, not left on the rejected config")
}

func TestRollbackPostCommit_NilPreviousReportsTheSplitRatherThanPretending(t *testing.T) {
	registry, _, _ := newPostCommitRegistry(t)

	err := registry.rollbackPostCommit(context.Background(), nil, "routing", fmt.Errorf("boom"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no previous config was captured")
	assert.Equal(t, "debug", registry.Config().Log.Level, "nothing to restore, so nothing was touched")
}
