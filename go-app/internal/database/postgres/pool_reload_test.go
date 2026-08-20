package postgres

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ================================================================================
// PostgresPool.Reload — pool recreation without in-flight query loss (INF-A slice 1)
// ================================================================================

func requireDockerForReload(t *testing.T) {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Skipping testcontainers-backed test: cannot create Docker client: %v", err)
	}
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("Skipping testcontainers-backed test: Docker daemon not reachable: %v", err)
	}
}

// startPostgres boots a throwaway PostgreSQL and returns a config pointed at it.
func startPostgres(t *testing.T) *PostgresConfig {
	t.Helper()

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("amp_reload_test"),
		tcpostgres.WithUsername("amp"),
		tcpostgres.WithPassword("amp"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %s", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("failed to terminate postgres container: %s", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %s", err)
	}
	mapped, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get mapped port: %s", err)
	}
	port, err := strconv.Atoi(mapped.Port())
	if err != nil {
		t.Fatalf("failed to parse mapped port: %s", err)
	}

	cfg := DefaultConfig()
	cfg.Host = host
	cfg.Port = port
	cfg.Database = "amp_reload_test"
	cfg.User = "amp"
	cfg.Password = "amp"
	cfg.SSLMode = "disable"
	cfg.MaxConns = 5
	cfg.MinConns = 1
	return cfg
}

// TestPostgresPool_Reload_NoInFlightQueryLoss is the brief's "test with a held
// connection": a caller that already holds a connection from the OLD pool must
// keep being able to use it across the swap and the drain window, while new
// callers get the new pool.
func TestPostgresPool_Reload_NoInFlightQueryLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping testcontainers-backed test in short mode")
	}
	requireDockerForReload(t)

	ctx := context.Background()
	cfg := startPostgres(t)

	pool := NewPostgresPool(cfg, slog.Default())
	// Short but non-zero grace so the test observes the drain window instead of
	// waiting 5s for the production default.
	pool.SetDrainGrace(750 * time.Millisecond)

	if err := pool.Connect(ctx); err != nil {
		t.Fatalf("failed to connect: %s", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	oldPool := pool.Pool()
	if oldPool == nil {
		t.Fatal("expected a live pool after Connect")
	}

	// Hold a connection, as an in-flight query would.
	held, err := oldPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire a connection: %s", err)
	}

	// Reload with a different pool shape (same DSN).
	newCfg := *cfg
	newCfg.MaxConns = 9
	newCfg.MinConns = 2
	if err := pool.Reload(ctx, &newCfg); err != nil {
		t.Fatalf("reload failed: %s", err)
	}

	// The swap really happened.
	if pool.Pool() == oldPool {
		t.Fatal("expected Reload to publish a NEW pgxpool")
	}
	if got := pool.GetConfig().MaxConns; got != 9 {
		t.Fatalf("expected the reloaded config to be live, got MaxConns=%d", got)
	}

	// The held connection still works AFTER the swap — this is the "no
	// in-flight query loss" guarantee.
	var one int
	if err := held.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("held connection broke across the swap: %s", err)
	}
	if one != 1 {
		t.Fatalf("unexpected result from held connection: %d", one)
	}

	// New work goes to the new pool and succeeds.
	if err := pool.QueryRow(ctx, "SELECT 2").Scan(&one); err != nil {
		t.Fatalf("query on the reloaded pool failed: %s", err)
	}
	if one != 2 {
		t.Fatalf("unexpected result from reloaded pool: %d", one)
	}

	// The held connection is STILL usable while the drain window is open.
	if err := held.QueryRow(ctx, "SELECT 3").Scan(&one); err != nil {
		t.Fatalf("held connection broke during the drain window: %s", err)
	}
	held.Release()

	// After the grace window the replaced pool is closed, so it stops
	// answering. A closed pgxpool returns an error from Ping.
	deadline := time.Now().Add(5 * time.Second)
	for oldPool.Ping(ctx) == nil {
		if time.Now().After(deadline) {
			t.Fatal("replaced pool was never closed after the drain window")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPostgresPool_Reload_FailedVerificationKeepsOldPool proves the
// build->verify->swap order: an unusable new config must leave the live pool
// untouched rather than half-applied.
func TestPostgresPool_Reload_FailedVerificationKeepsOldPool(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping testcontainers-backed test in short mode")
	}
	requireDockerForReload(t)

	ctx := context.Background()
	cfg := startPostgres(t)

	pool := NewPostgresPool(cfg, slog.Default())
	if err := pool.Connect(ctx); err != nil {
		t.Fatalf("failed to connect: %s", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	livePool := pool.Pool()

	bad := *cfg
	bad.Port = 1 // nothing listens here
	bad.ConnectTimeout = 500 * time.Millisecond
	if err := pool.Reload(ctx, &bad); err == nil {
		t.Fatal("expected reload to fail against an unreachable server")
	}

	if pool.Pool() != livePool {
		t.Fatal("a failed reload must not replace the live pool")
	}
	if pool.GetConfig().Port == 1 {
		t.Fatal("a failed reload must not publish the rejected config")
	}

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("pool unusable after a failed reload: %s", err)
	}
}

// TestPostgresPool_Reload_RefusesWhenHandleShared needs no database: the
// shared-handle check runs before anything is dialed.
func TestPostgresPool_Reload_RefusesWhenHandleShared(t *testing.T) {
	pool := NewPostgresPool(DefaultConfig(), slog.Default())
	_ = pool.SharePool()

	if !pool.IsPoolShared() {
		t.Fatal("SharePool must mark the handle as shared")
	}
	err := pool.Reload(context.Background(), DefaultConfig())
	if !errors.Is(err, ErrPoolHandleShared) {
		t.Fatalf("expected ErrPoolHandleShared, got %v", err)
	}
}

// TestPostgresPool_Reload_RejectsInvalidConfig also needs no database.
func TestPostgresPool_Reload_RejectsInvalidConfig(t *testing.T) {
	pool := NewPostgresPool(DefaultConfig(), slog.Default())

	if err := pool.Reload(context.Background(), nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for a nil config, got %v", err)
	}

	invalid := DefaultConfig()
	invalid.Host = ""
	if err := pool.Reload(context.Background(), invalid); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for an empty host, got %v", err)
	}
}

// TestPostgresPool_PoolVsSharePool documents the distinction the reload
// machinery depends on: a short-lived borrow does not block Reload, a
// long-lived handle does.
func TestPostgresPool_PoolVsSharePool(t *testing.T) {
	pool := NewPostgresPool(DefaultConfig(), slog.Default())

	_ = pool.Pool()
	if pool.IsPoolShared() {
		t.Fatal("Pool() must not mark the handle as shared")
	}

	_ = pool.SharePool()
	if !pool.IsPoolShared() {
		t.Fatal("SharePool() must mark the handle as shared")
	}
}
