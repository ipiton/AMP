package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// Reloadable registry + coordinator integration (INF-A slice 1)
// ================================================================================

// fakeReloadable is a scriptable Reloadable used to assert selection, ordering
// and failure handling without touching real infrastructure.
type fakeReloadable struct {
	name     string
	sections []string
	critical bool
	priority int
	err      error

	// failForwardOnly makes err apply to the FIRST call only, which is what a
	// realistic component does: the new value is unusable, the previous one
	// still works, so the rollback pass succeeds.
	failForwardOnly bool

	mu    sync.Mutex
	calls []reloadCall
}

type reloadCall struct {
	oldCfg *Config
	newCfg *Config
}

func (f *fakeReloadable) Name() string               { return f.name }
func (f *fakeReloadable) RelevantSections() []string { return f.sections }
func (f *fakeReloadable) IsCritical() bool           { return f.critical }
func (f *fakeReloadable) ReloadPriority() int        { return f.priority }

func (f *fakeReloadable) Reload(_ context.Context, oldCfg, newCfg *Config) error {
	f.mu.Lock()
	f.calls = append(f.calls, reloadCall{oldCfg: oldCfg, newCfg: newCfg})
	first := len(f.calls) == 1
	f.mu.Unlock()

	if f.failForwardOnly && !first {
		return nil
	}
	return f.err
}

func (f *fakeReloadable) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// orderRecorder captures the order components were reloaded in.
type orderRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *orderRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, name)
}

func (r *orderRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

type recordingReloadable struct {
	fakeReloadable
	recorder *orderRecorder
}

func (r *recordingReloadable) Reload(ctx context.Context, oldCfg, newCfg *Config) error {
	r.recorder.record(r.name)
	return r.fakeReloadable.Reload(ctx, oldCfg, newCfg)
}

func TestConfigReloader_SelectsOnlyComponentsWhoseSectionChanged(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())

	logger := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	database := &fakeReloadable{name: "database", sections: []string{"database"}, priority: 90, critical: true}
	reloader.Register(logger)
	reloader.Register(database)

	oldCfg := &Config{Log: LogConfig{Level: "info"}, Database: databaseConfigFixture("db-1", 10)}
	newCfg := &Config{Log: LogConfig{Level: "debug"}, Database: databaseConfigFixture("db-1", 10)}

	assert.Equal(t, []string{"logger"}, reloader.SelectComponents(oldCfg, newCfg, nil))

	errs := reloader.ReloadAll(context.Background(), oldCfg, newCfg, nil)
	assert.Empty(t, errs)
	assert.Equal(t, 1, logger.callCount())
	assert.Equal(t, 0, database.callCount(), "an untouched section must not reload")
}

func TestConfigReloader_NameGateNarrowsSelection(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())

	logger := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	metrics := &fakeReloadable{name: "metrics", sections: []string{"metrics"}, priority: 20}
	reloader.Register(logger)
	reloader.Register(metrics)

	oldCfg := &Config{Log: LogConfig{Level: "info"}, Metrics: MetricsConfig{Enabled: true}}
	newCfg := &Config{Log: LogConfig{Level: "debug"}, Metrics: MetricsConfig{Enabled: false}}

	// Both sections changed, but the caller narrowed to one component.
	errs := reloader.ReloadAll(context.Background(), oldCfg, newCfg, []string{"metrics"})
	assert.Empty(t, errs)
	assert.Equal(t, 0, logger.callCount())
	assert.Equal(t, 1, metrics.callCount())
}

func TestConfigReloader_EmptySectionsMeansAlwaysRelevant(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())
	always := &fakeReloadable{name: "always", priority: 50}
	reloader.Register(always)

	cfg := &Config{Log: LogConfig{Level: "info"}}
	errs := reloader.ReloadAll(context.Background(), cfg, cfg, nil)

	assert.Empty(t, errs)
	assert.Equal(t, 1, always.callCount())
}

func TestConfigReloader_ReloadsInPriorityOrderRegardlessOfRegistrationOrder(t *testing.T) {
	recorder := &orderRecorder{}
	reloader := NewConfigReloader(slog.Default())

	// Registered in reverse priority order on purpose.
	for _, spec := range []struct {
		name     string
		priority int
	}{
		{"database", 90},
		{"redis", 80},
		{"llm", 50},
		{"metrics", 20},
		{"logger", 10},
	} {
		reloader.Register(&recordingReloadable{
			fakeReloadable: fakeReloadable{name: spec.name, priority: spec.priority},
			recorder:       recorder,
		})
	}

	assert.Equal(t,
		[]string{"logger", "metrics", "llm", "redis", "database"},
		reloader.GetRegisteredComponents(),
	)

	cfg := &Config{}
	require.Empty(t, reloader.ReloadAll(context.Background(), cfg, cfg, nil))
	assert.Equal(t, []string{"logger", "metrics", "llm", "redis", "database"}, recorder.snapshot())
}

func TestConfigReloader_DefaultPriorityForUnorderedComponents(t *testing.T) {
	recorder := &orderRecorder{}
	reloader := NewConfigReloader(slog.Default())

	// plainReloadable does NOT implement OrderedReloadable.
	reloader.Register(&recordingReloadable{
		fakeReloadable: fakeReloadable{name: "database", priority: 90},
		recorder:       recorder,
	})
	reloader.Register(plainReloadable{name: "unordered", recorder: recorder})
	reloader.Register(&recordingReloadable{
		fakeReloadable: fakeReloadable{name: "logger", priority: 10},
		recorder:       recorder,
	})

	cfg := &Config{}
	require.Empty(t, reloader.ReloadAll(context.Background(), cfg, cfg, nil))
	// defaultReloadPriority (100) puts the unordered one after the database.
	assert.Equal(t, []string{"logger", "database", "unordered"}, recorder.snapshot())
}

// plainReloadable implements Reloadable but not OrderedReloadable.
type plainReloadable struct {
	name     string
	recorder *orderRecorder
}

func (p plainReloadable) Name() string               { return p.name }
func (p plainReloadable) RelevantSections() []string { return nil }
func (p plainReloadable) IsCritical() bool           { return false }
func (p plainReloadable) Reload(_ context.Context, _, _ *Config) error {
	p.recorder.record(p.name)
	return nil
}

func TestConfigReloader_FailFastStopsLaterComponents(t *testing.T) {
	recorder := &orderRecorder{}
	reloader := NewConfigReloader(slog.Default())

	first := &recordingReloadable{fakeReloadable: fakeReloadable{name: "logger", priority: 10}, recorder: recorder}
	failing := &recordingReloadable{
		fakeReloadable: fakeReloadable{name: "metrics", priority: 20, err: fmt.Errorf("boom")},
		recorder:       recorder,
	}
	later := &recordingReloadable{fakeReloadable: fakeReloadable{name: "database", priority: 90, critical: true}, recorder: recorder}
	reloader.Register(first)
	reloader.Register(failing)
	reloader.Register(later)

	cfg := &Config{}
	errs := reloader.ReloadAll(context.Background(), cfg, cfg, nil)

	require.Len(t, errs, 1, "fail-fast means at most one reported error")
	assert.Equal(t, "metrics", errs[0].Component)
	assert.Contains(t, errs[0].Error, "boom")
	assert.Equal(t, []string{"logger", "metrics"}, recorder.snapshot(),
		"no component after the failure may apply changes")
	assert.Equal(t, 0, later.callCount())
}

func TestConfigReloader_RegisterIsIdempotentByName(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())
	reloader.Register(&fakeReloadable{name: "logger", priority: 10})
	reloader.Register(&fakeReloadable{name: "logger", priority: 90})
	reloader.Register(nil)

	assert.Equal(t, []string{"logger"}, reloader.GetRegisteredComponents())
}

func TestConfigReloader_UnknownSectionIsTreatedAsAlwaysRelevant(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())
	typo := &fakeReloadable{name: "typo", sections: []string{"loggg"}, priority: 10}
	reloader.Register(typo)

	oldCfg := &Config{Log: LogConfig{Level: "info"}}
	newCfg := &Config{Log: LogConfig{Level: "debug"}}

	require.Empty(t, reloader.ReloadAll(context.Background(), oldCfg, newCfg, nil))
	assert.Equal(t, 1, typo.callCount(), "a typo must reload loudly, not silently skip")
}

func TestConfigReloader_UnregisterRemovesComponent(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())
	reloader.Register(&fakeReloadable{name: "logger", priority: 10})
	reloader.Unregister("logger")
	reloader.Unregister("absent") // must not panic

	assert.Empty(t, reloader.GetRegisteredComponents())
}

func TestConfigReloader_ComponentsReceiveBothConfigs(t *testing.T) {
	reloader := NewConfigReloader(slog.Default())
	component := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	reloader.Register(component)

	oldCfg := &Config{Log: LogConfig{Level: "info"}}
	newCfg := &Config{Log: LogConfig{Level: "debug"}}
	require.Empty(t, reloader.ReloadAll(context.Background(), oldCfg, newCfg, nil))

	component.mu.Lock()
	defer component.mu.Unlock()
	require.Len(t, component.calls, 1)
	assert.Same(t, oldCfg, component.calls[0].oldCfg)
	assert.Same(t, newCfg, component.calls[0].newCfg)
}

// ================================================================================
// Coordinator-level integration
// ================================================================================

// writeReloadConfigFile writes a config file with the given log level and
// metrics toggle, the two sections used by the coordinator tests below.
func writeReloadConfigFile(t *testing.T, logLevel string, metricsEnabled bool) string {
	t.Helper()

	content := fmt.Sprintf(`
app:
  name: test-app
  environment: test
server:
  host: localhost
  port: 8080
log:
  level: %s
  format: json
metrics:
  enabled: %t
`, logLevel, metricsEnabled)

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestReloadCoordinator_TwoComponentsReload is the brief's coordinator-level
// case, happy path: a config change touching two sections reloads both
// components.
func TestReloadCoordinator_TwoComponentsReload(t *testing.T) {
	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)

	newPath := writeReloadConfigFile(t, "debug", false)

	logger := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	metrics := &fakeReloadable{name: "metrics", sections: []string{"metrics"}, priority: 20}

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(logger)
	reloader.Register(metrics)

	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{}, NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)

	result, err := coordinator.ReloadFromFile(context.Background(), newPath)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t, result.Success)
	assert.False(t, result.RolledBack)
	assert.Equal(t, 1, logger.callCount())
	assert.Equal(t, 1, metrics.callCount())

	names := make([]string, 0, len(result.ComponentsReloaded))
	for _, component := range result.ComponentsReloaded {
		assert.True(t, component.Success)
		names = append(names, component.Name)
	}
	assert.ElementsMatch(t, []string{"logger", "metrics"}, names)

	// New config is live.
	assert.Equal(t, "debug", coordinator.GetCurrentConfig().Log.Level)
	assert.False(t, coordinator.GetCurrentConfig().Metrics.Enabled)
}

// TestReloadCoordinator_OneComponentFailureRejectsTheWholeReload is the brief's
// coordinator-level case, failure path: one of the two components fails, so the
// reload is rejected, the failing component is named, and the OLD config stays
// active.
func TestReloadCoordinator_OneComponentFailureRejectsTheWholeReload(t *testing.T) {
	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)

	newPath := writeReloadConfigFile(t, "debug", false)

	logger := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	metrics := &fakeReloadable{
		name:            "metrics",
		sections:        []string{"metrics"},
		priority:        20,
		err:             fmt.Errorf("exposition gate is wedged"),
		failForwardOnly: true,
	}

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(logger)
	reloader.Register(metrics)

	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{}, NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)

	versionBefore, _, _ := coordinator.GetReloadStatus()

	result, err := coordinator.ReloadFromFile(context.Background(), newPath)
	require.NoError(t, err, "a rejected reload is a reported result, not a transport error")
	require.NotNil(t, result)

	assert.False(t, result.Success)
	assert.True(t, result.RolledBack)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "metrics",
		"the rejection must name the component that failed")

	// A NON-critical component failing still rejects the whole reload: partial
	// application is worse than rejection. Two calls: the failed forward pass
	// plus the rollback pass that put it back on the previous config.
	assert.Equal(t, 2, metrics.callCount())

	// OLD config is intact, and the version did not advance.
	live := coordinator.GetCurrentConfig()
	assert.Equal(t, "info", live.Log.Level)
	assert.True(t, live.Metrics.Enabled)

	versionAfter, status, _ := coordinator.GetReloadStatus()
	assert.Equal(t, versionBefore, versionAfter)
	assert.Equal(t, "rolled_back", status)
}

func TestReloadCoordinator_RollbackDrivesComponentsBackToTheOldConfig(t *testing.T) {
	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)

	newPath := writeReloadConfigFile(t, "debug", true)

	// The logger succeeds, a second always-relevant component fails, so the
	// logger must be called again during rollback — this time with the NEW
	// config as "old" and the previous config as "new".
	logger := &fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10}
	breaker := &fakeReloadable{name: "rejects-new-config", priority: 50, err: fmt.Errorf("nope"), failForwardOnly: true}

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(logger)
	reloader.Register(breaker)

	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{}, NewConfigComparator(), reloader,
		nil, nil, slog.Default(),
	)

	result, err := coordinator.ReloadFromFile(context.Background(), newPath)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.RolledBack)

	logger.mu.Lock()
	defer logger.mu.Unlock()
	require.Len(t, logger.calls, 2, "the logger must be reloaded again on rollback")

	forward := logger.calls[0]
	assert.Equal(t, "info", forward.oldCfg.Log.Level)
	assert.Equal(t, "debug", forward.newCfg.Log.Level)

	back := logger.calls[1]
	assert.Equal(t, "debug", back.oldCfg.Log.Level, "rollback passes the rejected config as old")
	assert.Equal(t, "info", back.newCfg.Log.Level, "rollback drives components back to the previous config")
}

func TestReloadCoordinator_ComponentResultsOnlyNameSelectedComponents(t *testing.T) {
	// A diff can flag sections that no Reloadable owns (routing/receivers are
	// applied by ServiceRegistry, not through the registry). Those must not be
	// reported as successfully reloaded components.
	initialPath := writeReloadConfigFile(t, "info", true)
	initial, err := LoadConfig(initialPath)
	require.NoError(t, err)

	newPath := writeReloadConfigFile(t, "debug", true)

	reloader := NewConfigReloader(slog.Default())
	reloader.Register(&fakeReloadable{name: "logger", sections: []string{"log"}, priority: 10})

	coordinator := NewReloadCoordinator(
		initial, newPath,
		&MockConfigValidator{},
		&MockConfigComparator{
			CompareFunc: func(_ *Config, _ *Config, _ []string) (*ConfigDiff, error) {
				return &ConfigDiff{
					Added: map[string]interface{}{},
					Modified: map[string]DiffEntry{
						"log.level":      {OldValue: "info", NewValue: "debug"},
						"route.receiver": {OldValue: "a", NewValue: "b"},
					},
					Deleted:  []string{},
					Affected: []string{"logger", "routing"},
				}, nil
			},
		},
		reloader,
		nil, nil, slog.Default(),
	)

	result, err := coordinator.ReloadFromFile(context.Background(), newPath)
	require.NoError(t, err)
	require.True(t, result.Success)

	require.Len(t, result.ComponentsReloaded, 1)
	assert.Equal(t, "logger", result.ComponentsReloaded[0].Name)
}

func TestReloadCoordinator_IdentifyAffectedComponents_CoversReloadableSections(t *testing.T) {
	coordinator := NewReloadCoordinator(
		&Config{}, "/tmp/config.yaml",
		&MockConfigValidator{}, &MockConfigComparator{}, NewConfigReloader(slog.Default()),
		nil, nil, slog.Default(),
	)

	// Every registered component name must be reachable, and Added/Deleted
	// paths must count as well as Modified.
	affected := coordinator.identifyAffectedComponents(&ConfigDiff{
		Modified: map[string]DiffEntry{"log.level": {}},
		Added:    map[string]interface{}{"metrics.enabled": true},
		Deleted:  []string{"redis.addr"},
	})

	assert.ElementsMatch(t, []string{"logger", "metrics", "redis"}, affected)
}
