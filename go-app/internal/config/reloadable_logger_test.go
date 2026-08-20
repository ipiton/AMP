package config

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	pkglogger "github.com/ipiton/AMP/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLoggerFixture(t *testing.T, bootCfg *Config) (*LoggerReloadable, *pkglogger.SwappableHandler, *RestartWarnings, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	handler, err := pkglogger.NewSwappableHandler(buf, slog.LevelInfo, "json")
	require.NoError(t, err)

	warnings := NewRestartWarnings()
	return NewLoggerReloadable(handler, bootCfg, warnings, slog.New(handler)), handler, warnings, buf
}

// loggerBootCfg matches what newLoggerFixture's handler was built from.
func loggerBootCfg() *Config {
	return &Config{Log: LogConfig{Level: "info", Format: "json"}}
}

func TestLoggerReloadable_Contract(t *testing.T) {
	reloadable, _, _, _ := newLoggerFixture(t, loggerBootCfg())

	assert.Equal(t, "logger", reloadable.Name())
	assert.Equal(t, []string{"log"}, reloadable.RelevantSections())
	assert.False(t, reloadable.IsCritical())
	// Lowest priority number in the registry: the logger reloads first.
	assert.Equal(t, 10, reloadable.ReloadPriority())
	assert.Less(t, reloadable.ReloadPriority(), (&MetricsReloadable{}).ReloadPriority())
}

func TestLoggerReloadable_AppliesLevelAndFormat(t *testing.T) {
	oldCfg := &Config{Log: LogConfig{Level: "info", Format: "json"}}
	reloadable, handler, warnings, buf := newLoggerFixture(t, oldCfg)

	newCfg := &Config{Log: LogConfig{Level: "debug", Format: "text"}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	assert.Equal(t, slog.LevelDebug, handler.Level())
	assert.Equal(t, "text", handler.Format())
	assert.Empty(t, warnings.List(), "a level/format change must not warn")

	logger := slog.New(handler)
	logger.Debug("visible-now")
	assert.Contains(t, buf.String(), "visible-now")
}

func TestLoggerReloadable_UnchangedSectionIsNoOp(t *testing.T) {
	cfg := &Config{Log: LogConfig{Level: "info", Format: "json"}}
	reloadable, handler, warnings, _ := newLoggerFixture(t, cfg)

	require.NoError(t, reloadable.Reload(context.Background(), cfg, cfg))

	assert.Equal(t, slog.LevelInfo, handler.Level())
	assert.Empty(t, warnings.List())
}

func TestLoggerReloadable_SinkChangeWarnsW602ButStillAppliesLevel(t *testing.T) {
	oldCfg := &Config{Log: LogConfig{Level: "info", Format: "json", Output: "stdout"}}
	reloadable, handler, warnings, _ := newLoggerFixture(t, oldCfg)

	newCfg := &Config{Log: LogConfig{Level: "warn", Format: "json", Output: "file", Filename: "amp.log"}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	// The half that CAN be applied was applied...
	assert.Equal(t, slog.LevelWarn, handler.Level())

	// ...and the half that cannot is a codified, field-named warning.
	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnLoggerRestartRequired, list[0].Code)
	assert.Equal(t, "logger", list[0].Component)
	assert.ElementsMatch(t, []string{"log.output", "log.filename"}, list[0].Fields)
	assert.NotEmpty(t, list[0].Reason)
	assert.False(t, list[0].At.IsZero())
}

func TestLoggerReloadable_SinkOnlyChangeDoesNotSwap(t *testing.T) {
	oldCfg := &Config{Log: LogConfig{Level: "info", Format: "json", MaxBackups: 3}}
	reloadable, handler, warnings, _ := newLoggerFixture(t, oldCfg)

	newCfg := &Config{Log: LogConfig{Level: "info", Format: "json", MaxBackups: 7}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	assert.Equal(t, slog.LevelInfo, handler.Level())
	require.Len(t, warnings.List(), 1)
	assert.Equal(t, []string{"log.max_backups"}, warnings.List()[0].Fields)
}

func TestLoggerReloadable_BadFormatRejectsTheReload(t *testing.T) {
	oldCfg := &Config{Log: LogConfig{Level: "info", Format: "json"}}
	reloadable, handler, _, _ := newLoggerFixture(t, oldCfg)

	newCfg := &Config{Log: LogConfig{Level: "info", Format: "toml"}}

	err := reloadable.Reload(context.Background(), oldCfg, newCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logger reload failed")

	// Previous handler still live — no half-applied state.
	assert.Equal(t, "json", handler.Format())
}

func TestLoggerReloadable_NilHandlerWarnsInsteadOfPretending(t *testing.T) {
	warnings := NewRestartWarnings()
	oldCfg := &Config{Log: LogConfig{Level: "info"}}
	newCfg := &Config{Log: LogConfig{Level: "debug"}}
	reloadable := NewLoggerReloadable(nil, oldCfg, warnings, slog.Default())

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnLoggerRestartRequired, list[0].Code)
	assert.Contains(t, list[0].Reason, "fixed slog handler")
}

func TestLoggerReloadable_NilNewConfigIsAnError(t *testing.T) {
	reloadable, _, _, _ := newLoggerFixture(t, loggerBootCfg())
	require.Error(t, reloadable.Reload(context.Background(), &Config{}, nil))
}

func TestRestartWarnings_DedupesAndClears(t *testing.T) {
	warnings := NewRestartWarnings()

	warnings.Record(RestartRequiredWarning{Code: WarnLoggerRestartRequired, Component: "logger", Reason: "first"})
	warnings.Record(RestartRequiredWarning{Code: WarnLoggerRestartRequired, Component: "logger", Reason: "second"})
	warnings.Record(RestartRequiredWarning{Code: WarnRedisRestartRequired, Component: "redis", Reason: "third"})

	list := warnings.List()
	require.Len(t, list, 2, "same code+component must refresh one entry, not append")
	// Sorted by code: redis is W601, logger is W602.
	assert.Equal(t, "redis", list[0].Component)
	assert.Equal(t, "logger", list[1].Component)
	assert.Equal(t, "second", list[1].Reason, "a repeat must refresh the entry, keeping the latest reason")

	warnings.Clear()
	assert.Empty(t, warnings.List())
}

func TestRestartWarnings_NilReceiverIsSafe(t *testing.T) {
	var warnings *RestartWarnings
	warnings.Record(RestartRequiredWarning{Code: "W600"})
	assert.Nil(t, warnings.List())
	warnings.Clear()
}
