package config

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/stretchr/testify/assert"
)

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
