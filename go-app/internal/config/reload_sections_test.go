package config

import (
	"io"
	"log/slog"
	"testing"
	"time"

	pkglogger "github.com/ipiton/AMP/pkg/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnownSections_CoversEveryReloadableSection(t *testing.T) {
	// The section names the shipped Reloadable components declare must all be
	// real Config sections; a typo here is the failure mode Register warns
	// about and this test forbids outright.
	for _, section := range []string{"database", "redis", "log", "metrics", "llm"} {
		assert.True(t, IsKnownSection(section), "section %q must exist in Config", section)
	}

	known := KnownSections()
	assert.Contains(t, known, "server")
	assert.Contains(t, known, "publishing")
	// mapstructure:"-" fields are not sections.
	assert.NotContains(t, known, "routing")
	// Sorted output, so callers can compare deterministically.
	for i := 1; i < len(known); i++ {
		assert.LessOrEqual(t, known[i-1], known[i], "KnownSections must be sorted")
	}
}

func TestSectionChanged(t *testing.T) {
	base := &Config{
		Log:      LogConfig{Level: "info", Format: "json"},
		Database: DatabaseConfig{Host: "db-1", MaxConnections: 10},
	}

	t.Run("unchanged section reports false", func(t *testing.T) {
		other := *base
		assert.False(t, SectionChanged(base, &other, "log"))
		assert.False(t, SectionChanged(base, &other, "database"))
	})

	t.Run("changed section reports true", func(t *testing.T) {
		other := *base
		other.Log = LogConfig{Level: "debug", Format: "json"}
		assert.True(t, SectionChanged(base, &other, "log"))
		assert.False(t, SectionChanged(base, &other, "database"))
	})

	t.Run("nil config reports true", func(t *testing.T) {
		assert.True(t, SectionChanged(nil, base, "log"))
		assert.True(t, SectionChanged(base, nil, "log"))
	})

	t.Run("unknown section reports true so a typo cannot silently skip", func(t *testing.T) {
		other := *base
		assert.True(t, SectionChanged(base, &other, "logg"))
	})
}

func TestChangedSections(t *testing.T) {
	oldCfg := &Config{
		Log:      LogConfig{Level: "info"},
		Metrics:  MetricsConfig{Enabled: true},
		Database: DatabaseConfig{Host: "db-1"},
	}
	newCfg := &Config{
		Log:      LogConfig{Level: "debug"},
		Metrics:  MetricsConfig{Enabled: false},
		Database: DatabaseConfig{Host: "db-1"},
	}

	assert.Equal(t, []string{"log", "metrics"}, ChangedSections(oldCfg, newCfg))
	assert.Empty(t, ChangedSections(oldCfg, oldCfg))
	assert.Equal(t, KnownSections(), ChangedSections(nil, newCfg))
}

func TestChangedFields_NamesOnlyNeverValues(t *testing.T) {
	const oldCredential = "credential-before" // #nosec G101 -- test fixture, not a credential
	const newCredential = "credential-after"  // #nosec G101 -- test fixture, not a credential

	oldCfg := DatabaseConfig{Host: "db-1", Password: oldCredential, MaxConnLifetime: time.Minute}
	newCfg := DatabaseConfig{Host: "db-2", Password: newCredential, MaxConnLifetime: time.Minute}

	fields := changedFields("database", oldCfg, newCfg)

	require.Equal(t, []string{"database.host", "database.password"}, fields)
	for _, field := range fields {
		assert.NotContains(t, field, "credential", "field lists must never carry values")
	}
}

func TestChangedFields_TypeMismatchIsSafe(t *testing.T) {
	assert.Nil(t, changedFields("database", DatabaseConfig{}, RedisConfig{}))
	assert.Nil(t, changedFields("database", "not-a-struct", "also-not"))
}

// TestLogConfigFormat_ValidationMatchesTheHandler pins re-review residual 1:
// the reload validator must accept EXACTLY what pkg/logger accepts, in both
// directions.
//
// Too strict and a config that boots cannot be reloaded — which is what was
// actually broken here: `level: ""` / `format: ""` are legal for the logger
// (they mean info/json) but were rejected by the validator, so such a config
// started fine and then failed every SIGHUP. The config-reloader sidecar turns
// that into one failed reload per poll.
//
// Too lax and the bad value reaches the APPLY phase, where LoggerReloadable
// rejects it — correct, but a rejected reload rather than a rejected config.
func TestLogConfigFormat_ValidationMatchesTheHandler(t *testing.T) {
	validator := NewConfigValidator()

	logFieldErrors := func(cfg *Config) map[string]string {
		found := make(map[string]string)
		for _, err := range validator.Validate(cfg, []string{"log"}) {
			found[err.Field] = err.Code
		}
		return found
	}

	t.Run("accepts every value pkg/logger accepts", func(t *testing.T) {
		for _, format := range []string{"json", "text", "JSON", ""} {
			for _, level := range []string{"debug", "info", "warn", "error", "WARN", ""} {
				cfg := &Config{Log: LogConfig{Level: level, Format: format}}

				// Sanity: pkg/logger really does accept these.
				_, err := pkglogger.NewSwappableHandler(io.Discard, pkglogger.ParseLevel(level), format)
				require.NoError(t, err, "pkg/logger must accept level=%q format=%q", level, format)

				errs := logFieldErrors(cfg)
				assert.NotContains(t, errs, "log.format", "format %q must validate", format)
				assert.NotContains(t, errs, "log.level", "level %q must validate", level)
			}
		}
	})

	t.Run("rejects every value pkg/logger rejects", func(t *testing.T) {
		for _, format := range []string{"toml", "yaml", "logfmt"} {
			cfg := &Config{Log: LogConfig{Level: "info", Format: format}}

			// Sanity: pkg/logger really does reject these.
			_, err := pkglogger.NewSwappableHandler(io.Discard, slog.LevelInfo, format)
			require.Error(t, err, "pkg/logger must reject format %q", format)

			errs := logFieldErrors(cfg)
			assert.Contains(t, errs, "log.format",
				"format %q must be rejected before the config is committed", format)
		}
	})

	t.Run("rejects an unparseable level", func(t *testing.T) {
		// ParseLevel silently falls back to info, so this one is validator-only:
		// a typo must not be silently downgraded to the default.
		errs := logFieldErrors(&Config{Log: LogConfig{Level: "trace", Format: "json"}})
		assert.Contains(t, errs, "log.level")
	})
}
