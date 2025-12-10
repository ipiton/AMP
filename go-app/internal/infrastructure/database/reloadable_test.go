package database

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/config"
)

// ================================================================================
// Unit Tests for Reloadable Database Pool
// ================================================================================

func TestReloadableDatabasePool_Name(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(cfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	assert.Equal(t, "database", pool.Name())
}

func TestReloadableDatabasePool_IsCritical(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(cfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	assert.True(t, pool.IsCritical(), "database should be critical component")
}

func TestReloadableDatabasePool_Reload_NoChange(t *testing.T) {
	// Skip if no database available
	if !isDatabaseAvailable() {
		t.Skip("Skipping test: no database available")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dbCfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(dbCfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	// Create config with same database config
	cfg := &config.Config{
		Database: *dbCfg,
	}

	// Reload with same config (should be no-op)
	ctx := context.Background()
	startTime := time.Now()

	err = pool.Reload(ctx, cfg)
	duration := time.Since(startTime)

	assert.NoError(t, err)
	assert.Less(t, duration, 50*time.Millisecond, "no-change reload should be fast")
}

func TestReloadableDatabasePool_Reload_MaxConnsChange(t *testing.T) {
	// Skip if no database available
	if !isDatabaseAvailable() {
		t.Skip("Skipping test: no database available")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dbCfg := testDatabaseConfig()
	dbCfg.MaxConns = 10

	pool, err := NewReloadableDatabasePool(dbCfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	// Change max connections
	newCfg := &config.Config{
		Database: *dbCfg,
	}
	newCfg.Database.MaxConns = 20

	// Reload
	ctx := context.Background()
	err = pool.Reload(ctx, newCfg)

	assert.NoError(t, err)

	// Verify new config is applied
	assert.Equal(t, 20, pool.config.MaxConns)
}

func TestReloadableDatabasePool_Reload_InvalidHost(t *testing.T) {
	// Skip if no database available
	if !isDatabaseAvailable() {
		t.Skip("Skipping test: no database available")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dbCfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(dbCfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	// Change to invalid host
	newCfg := &config.Config{
		Database: *dbCfg,
	}
	newCfg.Database.Host = "invalid-host-that-does-not-exist"

	// Reload (should fail)
	ctx := context.Background()
	err = pool.Reload(ctx, newCfg)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create new pool")

	// Verify old config still active
	assert.Equal(t, dbCfg.Host, pool.config.Host)
}

func TestReloadableDatabasePool_Reload_Concurrent(t *testing.T) {
	// Skip if no database available
	if !isDatabaseAvailable() {
		t.Skip("Skipping test: no database available")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dbCfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(dbCfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	// Concurrent reload attempts
	done := make(chan error, 5)

	for i := 0; i < 5; i++ {
		go func(iteration int) {
			cfg := &config.Config{
				Database: *dbCfg,
			}
			cfg.Database.MaxConns = 10 + iteration

			ctx := context.Background()
			done <- pool.Reload(ctx, cfg)
		}(i)
	}

	// Collect results
	for i := 0; i < 5; i++ {
		err := <-done
		// Some reloads may succeed, some may be no-ops
		// Important: no panics or race conditions
		t.Logf("Reload %d: %v", i, err)
	}

	// Verify pool still functional
	assert.NotNil(t, pool.Pool())
}

func TestReloadableDatabasePool_Pool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(cfg, logger)
	require.NoError(t, err)
	defer pool.Close()

	// Get pool (thread-safe)
	p := pool.Pool()
	assert.NotNil(t, p)
}

func TestReloadableDatabasePool_Close(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(cfg, logger)
	require.NoError(t, err)

	// Close pool
	pool.Close()

	// Verify pool is nil
	assert.Nil(t, pool.Pool())

	// Close again (should be safe)
	pool.Close()
}

// ================================================================================
// Test Helpers
// ================================================================================

// testDatabaseConfig returns a test database configuration
func testDatabaseConfig() *config.DatabaseConfig {
	// Use environment variables if set, otherwise use defaults for testing
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		host = "localhost"
	}

	port := 5432
	user := os.Getenv("TEST_DB_USER")
	if user == "" {
		user = "postgres"
	}

	password := os.Getenv("TEST_DB_PASSWORD")
	if password == "" {
		password = "postgres"
	}

	database := os.Getenv("TEST_DB_NAME")
	if database == "" {
		database = "amp_test"
	}

	return &config.DatabaseConfig{
		Host:              host,
		Port:              port,
		User:              user,
		Password:          password,
		Database:          database,
		SSLMode:           "disable",
		MaxConns:          10,
		MinConns:          2,
		MaxConnLifetime:   1 * time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: 1 * time.Minute,
	}
}

// isDatabaseAvailable checks if database is available for testing
func isDatabaseAvailable() bool {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := testDatabaseConfig()

	pool, err := createPool(cfg, logger)
	if err != nil {
		return false
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	return pool.Ping(ctx) == nil
}

// ================================================================================
// Benchmark Tests
// ================================================================================

func BenchmarkReloadableDatabasePool_Reload_NoChange(b *testing.B) {
	if !isDatabaseAvailable() {
		b.Skip("Skipping benchmark: no database available")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dbCfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(dbCfg, logger)
	require.NoError(b, err)
	defer pool.Close()

	cfg := &config.Config{
		Database: *dbCfg,
	}
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = pool.Reload(ctx, cfg)
	}
}

func BenchmarkReloadableDatabasePool_Pool(b *testing.B) {
	if !isDatabaseAvailable() {
		b.Skip("Skipping benchmark: no database available")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := testDatabaseConfig()

	pool, err := NewReloadableDatabasePool(cfg, logger)
	require.NoError(b, err)
	defer pool.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = pool.Pool()
	}
}
