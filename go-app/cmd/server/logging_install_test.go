package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ipiton/AMP/internal/config"
	pkglogger "github.com/ipiton/AMP/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/natefinch/lumberjack.v2"
)

// ================================================================================
// installLogging — cfg.Log actually reaches the process logger (fix-round I1)
// ================================================================================
// Before INF-A slice 1 the server hardcoded stdout/json/info and never read
// cfg.Log at all. The slice made level/format live but still never called
// SetupWriter, so log.output/log.filename remained dead config while
// LoggerReloadable's W602 told operators to restart to apply them. These tests
// pin the fix: a restart genuinely does apply the sink.

func bootstrapHandler(t *testing.T) (*slog.Logger, *pkglogger.SwappableHandler) {
	t.Helper()
	handler, err := pkglogger.NewSwappableHandler(&bytes.Buffer{}, slog.LevelInfo, "json")
	require.NoError(t, err)
	return slog.New(handler), handler
}

func TestInstallLogging_HonoursFileOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "amp.log")
	bootLogger, bootHandler := bootstrapHandler(t)

	logger, handler := installLogging(bootLogger, bootHandler, &config.Config{
		Log: config.LogConfig{Level: "debug", Format: "text", Output: "file", Filename: logPath},
	})

	require.NotSame(t, bootHandler, handler, "a file sink needs a new handler, not a level swap")
	assert.Equal(t, slog.LevelDebug, handler.Level())
	assert.Equal(t, "text", handler.Format())

	logger.Debug("written-to-file")

	content, err := os.ReadFile(logPath) //nolint:gosec // path built by t.TempDir
	require.NoError(t, err, "log.output=file must create the file the operator named")
	assert.Contains(t, string(content), "written-to-file")
}

func TestInstallLogging_HonoursLevelAndFormatOnStdout(t *testing.T) {
	bootLogger, bootHandler := bootstrapHandler(t)

	_, handler := installLogging(bootLogger, bootHandler, &config.Config{
		Log: config.LogConfig{Level: "warn", Format: "text"},
	})

	assert.Equal(t, slog.LevelWarn, handler.Level())
	assert.Equal(t, "text", handler.Format())
}

func TestInstallLogging_BadFormatKeepsTheBootstrapLogger(t *testing.T) {
	bootLogger, bootHandler := bootstrapHandler(t)

	logger, handler := installLogging(bootLogger, bootHandler, &config.Config{
		Log: config.LogConfig{Level: "debug", Format: "yaml"},
	})

	// One YAML typo must not cost the process its logging.
	assert.Same(t, bootLogger, logger)
	assert.Same(t, bootHandler, handler)
	assert.Equal(t, slog.LevelInfo, handler.Level())
}

func TestInstallLogging_NilConfigKeepsTheBootstrapLogger(t *testing.T) {
	bootLogger, bootHandler := bootstrapHandler(t)

	logger, handler := installLogging(bootLogger, bootHandler, nil)

	assert.Same(t, bootLogger, logger)
	assert.Same(t, bootHandler, handler)
}

// TestLoggerConfigFrom_CarriesEverySinkField guards the mapping that makes
// W602 honest: every field LoggerReloadable reports as restart-required must be
// one this function actually hands to SetupWriter.
func TestLoggerConfigFrom_CarriesEverySinkField(t *testing.T) {
	got := config.LoggerConfigFrom(config.LogConfig{
		Level:      "debug",
		Format:     "text",
		Output:     "file",
		Filename:   "/var/log/amp.log",
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   true,
	})

	assert.Equal(t, "debug", got.Level)
	assert.Equal(t, "text", got.Format)
	assert.Equal(t, "file", got.Output)
	assert.Equal(t, "/var/log/amp.log", got.Filename)
	assert.Equal(t, 10, got.MaxSize)
	assert.Equal(t, 3, got.MaxBackups)
	assert.Equal(t, 7, got.MaxAge)
	assert.True(t, got.Compress)

	// And the writer really is a rotating file writer for output=file, which
	// is what makes those rotation fields mean anything.
	writer := pkglogger.SetupWriter(got)
	require.NotNil(t, writer)
	_, isLumberjack := writer.(*lumberjack.Logger)
	assert.True(t, isLumberjack, "output=file must go through lumberjack, got %T", writer)
}
