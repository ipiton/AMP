package config

import (
	"log/slog"
	"reflect"
	"sort"
	"sync"
	"time"
)

// ================================================================================
// Restart-required warnings (INF-A slice 1, honesty rule)
// ================================================================================
// Some config changes cannot be applied to a running process. The rule for
// this codebase is that such a change produces a LOUD, codified,
// operator-visible warning and no state change — never a Reload that returns
// nil while the new value is in effect nowhere.
//
// W6xx is reserved for this class of warning. It is deliberately disjoint from
// pkg/configvalidator's W1xx/W2xx/W3xx codes: those are emitted while
// validating a config file (the value is wrong or risky), these are emitted
// while applying one (the value is fine, the process simply cannot adopt it
// without a restart).
const (
	// WarnDatabaseRestartRequired: database.* changed but the pool cannot be
	// replaced under its long-lived consumers.
	WarnDatabaseRestartRequired = "W600"

	// WarnRedisRestartRequired: redis.* changed but the client cannot be
	// replaced under its long-lived consumers.
	WarnRedisRestartRequired = "W601"

	// WarnLoggerRestartRequired: log.* changed in a way the live handler
	// cannot adopt (output sink / file rotation settings).
	WarnLoggerRestartRequired = "W602"

	// WarnMetricsRestartRequired: metrics.* changed in a way that needs the
	// HTTP surface rebuilt (path/port).
	WarnMetricsRestartRequired = "W603"

	// WarnLLMRestartRequired: llm.* changed in a way that needs the
	// investigation pipeline rebuilt (enable/disable, agent mode).
	WarnLLMRestartRequired = "W604"
)

// RestartRequiredWarning is a single "this change needs a restart" finding.
type RestartRequiredWarning struct {
	// Code is the W6xx code (stable, greppable, documentable).
	Code string `json:"code"`

	// Component is the Reloadable that refused the change ("database", ...).
	Component string `json:"component"`

	// Fields are the config field paths the operator changed, e.g.
	// ["database.host", "database.port"]. Field NAMES only — never values,
	// so a rotated password never reaches a log line or an API response.
	Fields []string `json:"fields"`

	// Reason explains why the change cannot be applied live.
	Reason string `json:"reason"`

	// At is when the warning was last raised.
	At time.Time `json:"at"`
}

// RestartWarnings collects the restart-required warnings raised by the last
// reload attempts. Keyed by code+component, so repeatedly SIGHUP-ing the same
// unappliable change refreshes one entry instead of growing the list forever.
//
// Slice 2 exposes this through /health/reload; until then it is what makes the
// warnings assertable in tests rather than log-scraping.
type RestartWarnings struct {
	mu    sync.Mutex
	byKey map[string]RestartRequiredWarning
}

// NewRestartWarnings creates an empty collector.
func NewRestartWarnings() *RestartWarnings {
	return &RestartWarnings{byKey: make(map[string]RestartRequiredWarning)}
}

// Record stores (or refreshes) a warning. Nil-receiver safe: components take a
// *RestartWarnings that may legitimately be nil in unit tests.
func (w *RestartWarnings) Record(warning RestartRequiredWarning) {
	if w == nil {
		return
	}
	if warning.At.IsZero() {
		warning.At = time.Now().UTC()
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.byKey == nil {
		w.byKey = make(map[string]RestartRequiredWarning)
	}
	w.byKey[warning.Code+"|"+warning.Component] = warning
}

// List returns the collected warnings, sorted by code then component.
func (w *RestartWarnings) List() []RestartRequiredWarning {
	if w == nil {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	out := make([]RestartRequiredWarning, 0, len(w.byKey))
	for _, warning := range w.byKey {
		out = append(out, warning)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Component < out[j].Component
	})
	return out
}

// Clear drops every collected warning. Used when a reload applied cleanly, so
// a stale "restart required" cannot outlive the change that caused it.
func (w *RestartWarnings) Clear() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.byKey = make(map[string]RestartRequiredWarning)
}

// warnRestartRequired logs a restart-required warning and records it. This is
// the ONLY sanctioned way for a Reloadable to decline a change: it must be
// visible in the log AND queryable afterwards, because an operator who edited
// a ConfigMap and got HTTP 200 back has no other way to learn the value is
// not live.
func warnRestartRequired(
	logger *slog.Logger,
	sink *RestartWarnings,
	warning RestartRequiredWarning,
) {
	if warning.At.IsZero() {
		warning.At = time.Now().UTC()
	}
	sink.Record(warning)

	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("config change requires a restart; it is NOT active",
		"code", warning.Code,
		"component", warning.Component,
		"fields", warning.Fields,
		"reason", warning.Reason,
	)
}

// changedFields reports the `mapstructure` field names that differ between two
// values of the same struct type, prefixed with the section name
// ("database.host"). Values are never included — see
// RestartRequiredWarning.Fields.
//
// Nested structs are compared as a whole (reported as "section.field"), which
// is the right granularity for a warning: the operator needs to know which
// knob they touched, not which leaf inside it.
func changedFields(section string, oldValue, newValue any) []string {
	oldRV := reflect.ValueOf(oldValue)
	newRV := reflect.ValueOf(newValue)
	if oldRV.Kind() != reflect.Struct || newRV.Kind() != reflect.Struct || oldRV.Type() != newRV.Type() {
		return nil
	}

	t := oldRV.Type()
	changed := make([]string, 0, 2)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		name := sectionNameOf(field)
		if name == "" {
			continue
		}
		if !reflect.DeepEqual(oldRV.Field(i).Interface(), newRV.Field(i).Interface()) {
			changed = append(changed, section+"."+name)
		}
	}
	sort.Strings(changed)
	return changed
}
