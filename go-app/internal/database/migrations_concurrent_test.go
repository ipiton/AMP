package database

import (
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	dbpostgres "github.com/ipiton/AMP/internal/database/postgres"
)

// TestRunMigrations_ConcurrentReplicas_FreshDB is the regression test for the
// 2-replica e2e race documented in deploy/e2e-ha/docker-compose.yml: two
// replicas starting RunMigrations against the same brand-new database used
// to collide creating goose's own goose_db_version tracking table (23505
// duplicate key on pg_type). With the session advisory lock in place, one
// replica must win the race, the other blocks until the first is done, then
// no-ops because the migrations are already applied -- both succeed and end
// up with an identical, fully migrated schema.
func TestRunMigrations_ConcurrentReplicas_FreshDB(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping testcontainers-backed test in short mode")
	}

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("amp_migrate_test"),
		postgres.WithUsername("amp"),
		postgres.WithPassword("amp"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %s", err)
	}
	t.Cleanup(func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate postgres container: %s", err)
		}
	})

	host, err := pgContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %s", err)
	}
	mappedPort, err := pgContainer.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get mapped port: %s", err)
	}
	port, err := strconv.Atoi(mappedPort.Port())
	if err != nil {
		t.Fatalf("failed to parse mapped port: %s", err)
	}

	newPool := func() *dbpostgres.PostgresPool {
		cfg := dbpostgres.DefaultConfig()
		cfg.Host = host
		cfg.Port = port
		cfg.Database = "amp_migrate_test"
		cfg.User = "amp"
		cfg.Password = "amp"
		cfg.SSLMode = "disable"
		return dbpostgres.NewPostgresPool(cfg, slog.Default())
	}

	const replicas = 3
	pools := make([]*dbpostgres.PostgresPool, replicas)
	for i := range pools {
		p := newPool()
		if err := p.Connect(ctx); err != nil {
			t.Fatalf("replica %d: failed to connect: %s", i, err)
		}
		pools[i] = p
		t.Cleanup(func(p *dbpostgres.PostgresPool) func() {
			return func() { _ = p.Disconnect(ctx) }
		}(p))
	}

	// Fire all replicas at RunMigrations simultaneously, exactly like N
	// application instances booting against a fresh DB at the same time.
	var wg sync.WaitGroup
	errs := make([]error, replicas)
	start := make(chan struct{})
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = RunMigrations(ctx, pools[i], slog.Default())
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d: RunMigrations returned error: %s", i, err)
		}
	}

	// All replicas must agree on the final migration version, and it must
	// match the number of .sql files on disk (no duplicate-apply, no
	// skipped migration).
	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("resolveMigrationsDir() error = %s", err)
	}
	entries, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		t.Fatalf("failed to list migration files: %s", err)
	}
	var wantVersion int64
	for _, e := range entries {
		base := filepath.Base(e)
		versionStr := base[:strings.Index(base, "_")]
		v, err := strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			t.Fatalf("failed to parse migration version from %q: %s", base, err)
		}
		if v > wantVersion {
			wantVersion = v
		}
	}

	verifyPool := newPool()
	if err := verifyPool.Connect(ctx); err != nil {
		t.Fatalf("verify pool: failed to connect: %s", err)
	}
	t.Cleanup(func() { _ = verifyPool.Disconnect(ctx) })

	var gotVersion int64
	var rowCount int64
	row := verifyPool.QueryRow(ctx, "SELECT COUNT(*), COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = true")
	if err := row.Scan(&rowCount, &gotVersion); err != nil {
		t.Fatalf("failed to query goose_db_version: %s", err)
	}
	if gotVersion != wantVersion {
		t.Fatalf("goose_db_version max applied version = %d, want %d (no duplicate/skip)", gotVersion, wantVersion)
	}
	// rowCount should equal wantVersion + 1 (goose's own version-0 bootstrap
	// row), and must NOT be replicas*(wantVersion+1) -- that would mean each
	// replica re-applied migrations instead of the lock making later
	// replicas no-op.
	wantRowCount := int64(len(entries)) + 1 // +1 for goose's own version-0 bootstrap row
	if rowCount != wantRowCount {
		t.Fatalf("goose_db_version applied row count = %d, want %d (each migration applied exactly once across %d concurrent replicas)", rowCount, wantRowCount, replicas)
	}
}
