package config

import (
	"context"
	"fmt"
	"log/slog"

	pkglogger "github.com/ipiton/AMP/pkg/logger"
)

// ================================================================================
// LoggerReloadable (INF-A slice 1)
// ================================================================================

// loggerReloadPriority is deliberately the lowest number in the registry: the
// logger must be swapped BEFORE any other component reloads, so that every
// subsequent reload line is emitted at the operator's new level and in the new
// format. An operator who sets log.level=debug and SIGHUPs specifically wants
// to see the rest of that reload in debug.
const loggerReloadPriority = 10

// loggerRestartRequiredFields are the log.* fields that cannot be applied to a
// running process: they select or configure the output SINK, not the
// formatting. Swapping the sink under a live handler would leave the previous
// file handle open (lumberjack owns it) and could drop records mid-write, so
// the honest answer is a restart.
var loggerRestartRequiredFields = map[string]bool{
	"log.output":      true,
	"log.filename":    true,
	"log.max_size":    true,
	"log.max_backups": true,
	"log.max_age":     true,
	"log.compress":    true,
}

// LoggerReloadable hot-reloads the log level and format.
//
// Level and format are genuinely hot-swappable and are applied for real: the
// process installs a pkglogger.SwappableHandler at startup, and this component
// rebuilds its delegate over the same writer and publishes it atomically.
// Loggers derived with With()/WithGroup() follow the swap too (see
// SwappableHandler).
//
// Output sink and file-rotation settings are restart-required (W602) — see
// loggerRestartRequiredFields.
type LoggerReloadable struct {
	handler  *pkglogger.SwappableHandler
	logger   *slog.Logger
	warnings *RestartWarnings
}

// NewLoggerReloadable wires a LoggerReloadable over the process's swappable
// handler. A nil handler is legal (a process that installed a fixed handler);
// in that case a log.* change is reported as restart-required.
func NewLoggerReloadable(
	handler *pkglogger.SwappableHandler,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *LoggerReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggerReloadable{handler: handler, logger: logger, warnings: warnings}
}

// Name implements Reloadable.
func (l *LoggerReloadable) Name() string { return "logger" }

// RelevantSections implements Reloadable.
func (l *LoggerReloadable) RelevantSections() []string { return []string{"log"} }

// IsCritical implements Reloadable: losing a level change is a degradation,
// not an outage. (The reload is still rejected on an error — IsCritical only
// grades severity.)
func (l *LoggerReloadable) IsCritical() bool { return false }

// ReloadPriority implements OrderedReloadable.
func (l *LoggerReloadable) ReloadPriority() int { return loggerReloadPriority }

// Reload implements Reloadable.
func (l *LoggerReloadable) Reload(_ context.Context, oldCfg, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("logger reload: nil config")
	}

	var fields []string
	if oldCfg != nil {
		fields = changedFields("log", oldCfg.Log, newCfg.Log)
		if len(fields) == 0 {
			return nil
		}
	}

	// Sink/rotation changes: warn, and keep going — the level/format part of
	// the same edit is still applied below.
	sinkFields := make([]string, 0, len(fields))
	for _, field := range fields {
		if loggerRestartRequiredFields[field] {
			sinkFields = append(sinkFields, field)
		}
	}
	if len(sinkFields) > 0 {
		warnRestartRequired(l.logger, l.warnings, RestartRequiredWarning{
			Code:      WarnLoggerRestartRequired,
			Component: l.Name(),
			Fields:    sinkFields,
			Reason:    "the log output sink and its rotation settings are bound to the writer installed at startup; the file handle cannot be replaced without dropping buffered records — restart to apply (log.level and log.format ARE applied live)",
		})
	}

	if l.handler == nil {
		// Nothing to swap. Only warn about level/format if they actually
		// changed, so a sink-only edit does not produce two warnings.
		if levelOrFormatChanged(fields) {
			warnRestartRequired(l.logger, l.warnings, RestartRequiredWarning{
				Code:      WarnLoggerRestartRequired,
				Component: l.Name(),
				Fields:    fields,
				Reason:    "this process installed a fixed slog handler rather than a swappable one; restart to apply the new level/format",
			})
		}
		return nil
	}

	if !levelOrFormatChanged(fields) {
		return nil
	}

	level := pkglogger.ParseLevel(newCfg.Log.Level)
	if err := l.handler.Swap(level, newCfg.Log.Format); err != nil {
		// An unsupported format is an operator error worth rejecting the whole
		// reload for: the alternative is silently keeping the old format while
		// reporting success. The previous handler is still live.
		return fmt.Errorf("logger reload failed: %w", err)
	}

	l.logger.Info("logger reloaded from config",
		"level", level.String(),
		"format", l.handler.Format(),
		"fields", fields,
	)
	return nil
}

// levelOrFormatChanged reports whether the hot-swappable half of log.* moved.
// A nil/empty field list means "no previous config known", i.e. apply.
func levelOrFormatChanged(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	for _, field := range fields {
		if field == "log.level" || field == "log.format" {
			return true
		}
	}
	return false
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*LoggerReloadable)(nil)
	_ OrderedReloadable = (*LoggerReloadable)(nil)
)
