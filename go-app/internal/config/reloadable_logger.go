package config

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

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

// LoggerConfigFrom maps AMP's log config section onto pkg/logger's own config.
// Single source of truth shared by startup (cmd/server/main.go's
// installLogging) and this component, so the sink a restart installs is built
// from exactly the fields this component reports on.
func LoggerConfigFrom(cfg LogConfig) pkglogger.Config {
	return pkglogger.Config{
		Level:      cfg.Level,
		Format:     cfg.Format,
		Output:     cfg.Output,
		Filename:   cfg.Filename,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   cfg.Compress,
	}
}

// loggerRestartRequiredFields are the log.* fields that cannot be applied to a
// running process: they select or configure the output SINK, not the
// formatting. Swapping the sink under a live handler would leave the previous
// file handle open (lumberjack owns it) and could drop records mid-write, so
// the honest answer is a restart.
//
// A restart genuinely DOES apply them (fix-round I1): cmd/server/main.go now
// builds the handler over pkglogger.SetupWriter(LoggerConfigFrom(cfg.Log)).
// Before that it never called SetupWriter at all, so these fields were dead
// config and W602's "restart to apply" was false — the same defect W603
// correctly reports for metrics.path/metrics.port.
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
// loggerRestartRequiredFields. That warning is re-raised on every reload
// attempt while the config asks for a sink the live handler is not using, and
// resolved when they agree again (fix-round I2).
type LoggerReloadable struct {
	handler  *pkglogger.SwappableHandler
	logger   *slog.Logger
	warnings *RestartWarnings

	// mu guards applied — the log config the live handler is ACTUALLY using.
	// Level/Format move when a swap succeeds; the sink fields only ever move
	// on a restart, which is precisely why comparing against this (rather
	// than against the previous config) keeps W602 alive for as long as it is
	// true.
	mu      sync.Mutex
	applied LogConfig
}

// NewLoggerReloadable wires a LoggerReloadable over the process's swappable
// handler. A nil handler is legal (a process that installed a fixed handler);
// in that case a log.* change is reported as restart-required.
//
// bootCfg is the config the process started with — what the live handler was
// built from.
func NewLoggerReloadable(
	handler *pkglogger.SwappableHandler,
	bootCfg *Config,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *LoggerReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	reloadable := &LoggerReloadable{handler: handler, logger: logger, warnings: warnings}
	if bootCfg != nil {
		reloadable.applied = bootCfg.Log
	}
	return reloadable
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

// NeedsResync implements ResyncReloadable: true while the requested log config
// differs from what the live handler is using.
func (l *LoggerReloadable) NeedsResync(newCfg *Config) bool {
	if newCfg == nil {
		return false
	}
	return len(l.drift(newCfg.Log)) > 0
}

// drift returns the field paths where the requested config differs from what
// the live handler is using.
func (l *LoggerReloadable) drift(requested LogConfig) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return changedFields("log", l.applied, requested)
}

// Reload implements Reloadable.
func (l *LoggerReloadable) Reload(_ context.Context, _, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("logger reload: nil config")
	}

	fields := l.drift(newCfg.Log)
	if len(fields) == 0 {
		l.warnings.Resolve(WarnLoggerRestartRequired, l.Name())
		return nil
	}

	// Apply the half that CAN be applied first, so the warning below reports
	// only what is genuinely still diverging.
	if l.handler != nil && levelOrFormatChanged(fields) {
		level := pkglogger.ParseLevel(newCfg.Log.Level)
		if err := l.handler.Swap(level, newCfg.Log.Format); err != nil {
			// An unsupported format is an operator error worth rejecting the
			// whole reload for: the alternative is silently keeping the old
			// format while reporting success. The previous handler is still
			// live, and `applied` is untouched, so the next attempt sees the
			// same divergence.
			return fmt.Errorf("logger reload failed: %w", err)
		}

		l.mu.Lock()
		l.applied.Level = newCfg.Log.Level
		l.applied.Format = newCfg.Log.Format
		l.mu.Unlock()

		l.logger.Info("logger reloaded from config",
			"level", level.String(),
			"format", l.handler.Format(),
		)
	}

	// Whatever is left after the swap needs a restart. ONE warning per
	// component per attempt: RestartWarnings is keyed by code+component, so
	// two Record calls in one pass would silently overwrite each other
	// (fix-round I2 side-fix).
	remaining := l.drift(newCfg.Log)
	if len(remaining) == 0 {
		l.warnings.Resolve(WarnLoggerRestartRequired, l.Name())
		return nil
	}

	reason := "the log output sink and its rotation settings are bound to the writer installed at startup; the file handle cannot be replaced without dropping buffered records — restart to apply (log.level and log.format ARE applied live)"
	if l.handler == nil {
		reason = "this process installed a fixed slog handler rather than a swappable one; restart to apply the new log settings"
	} else if !onlySinkFields(remaining) {
		// Should not happen: with a live handler, level/format are always
		// applied above. Say so rather than blaming the sink.
		reason = "the live handler did not adopt these fields; restart to apply"
	}

	warnRestartRequired(l.logger, l.warnings, RestartRequiredWarning{
		Code:      WarnLoggerRestartRequired,
		Component: l.Name(),
		Fields:    remaining,
		Reason:    reason,
	})
	return nil
}

// levelOrFormatChanged reports whether the hot-swappable half of log.* moved.
// A nil/empty field list means "no previous state known", i.e. apply.
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

// onlySinkFields reports whether every field is a sink/rotation field.
func onlySinkFields(fields []string) bool {
	for _, field := range fields {
		if !loggerRestartRequiredFields[field] {
			return false
		}
	}
	return true
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*LoggerReloadable)(nil)
	_ OrderedReloadable = (*LoggerReloadable)(nil)
	_ ResyncReloadable  = (*LoggerReloadable)(nil)
)
