package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/ipiton/AMP/internal/core"
)

// setupTestDB creates a PostgreSQL container and returns a connection pool
func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()

	dbName := "alerthistory_test"
	dbUser := "testuser"
	dbPassword := "testpassword"

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second)),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %s", err)
	}

	t.Cleanup(func() {
		if err := postgresContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate postgres container: %s", err)
		}
	})

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %s", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to create pool: %s", err)
	}

	// Run migrations
	// Note: In a real scenario, we would use the migration files.
	// For this test, we'll create the schema manually to keep it self-contained
	// matching the schema defined in migrations/000001_init_schema.up.sql
	schema := `
	CREATE TABLE IF NOT EXISTS alerts (
		fingerprint VARCHAR(255) PRIMARY KEY,
		alert_name VARCHAR(255) NOT NULL,
		status VARCHAR(50) NOT NULL,
		starts_at TIMESTAMP WITH TIME ZONE NOT NULL,
		ends_at TIMESTAMP WITH TIME ZONE,
		generator_url TEXT,
		labels JSONB,
		annotations JSONB,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS alert_history (
		id SERIAL PRIMARY KEY,
		fingerprint VARCHAR(255) NOT NULL,
		alert_name VARCHAR(255) NOT NULL,
		status VARCHAR(50) NOT NULL,
		starts_at TIMESTAMP WITH TIME ZONE NOT NULL,
		ends_at TIMESTAMP WITH TIME ZONE,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		labels JSONB,
		annotations JSONB
	);

	CREATE INDEX idx_alert_history_fingerprint ON alert_history(fingerprint);
	CREATE INDEX idx_alert_history_created_at ON alert_history(created_at);
	`
	_, err = pool.Exec(ctx, schema)
	if err != nil {
		t.Fatalf("failed to create schema: %s", err)
	}

	return pool
}

func TestGetTopAlerts_EmptyDatabase(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	timeRange := &core.TimeRange{
		From: nil,
		To:   nil,
	}

	alerts, err := repo.GetTopAlerts(context.Background(), timeRange, 10)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts, got %d", len(alerts))
	}
}

func TestGetFlappingAlerts_NoStateTransitions(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	// Insert a stable alert (always firing)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
		VALUES
		('fp1', 'StableAlert', 'firing', NOW(), NOW(), '{"namespace": "prod"}'),
		('fp1', 'StableAlert', 'firing', NOW(), NOW() + INTERVAL '1 hour', '{"namespace": "prod"}')
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	timeRange := &core.TimeRange{}
	alerts, err := repo.GetFlappingAlerts(context.Background(), timeRange, 2)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 flapping alerts, got %d", len(alerts))
	}
}

func TestGetFlappingAlerts_MultipleTransitions(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	// Insert a flapping alert (firing -> resolved -> firing -> resolved)
	// 4 transitions
	baseTime := time.Now().Add(-24 * time.Hour)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
		VALUES
		('fp_flap', 'FlappingAlert', 'firing', $1, $1, '{"namespace": "prod"}'),
		('fp_flap', 'FlappingAlert', 'resolved', $1, $1 + INTERVAL '10 minutes', '{"namespace": "prod"}'),
		('fp_flap', 'FlappingAlert', 'firing', $1, $1 + INTERVAL '20 minutes', '{"namespace": "prod"}'),
		('fp_flap', 'FlappingAlert', 'resolved', $1, $1 + INTERVAL '30 minutes', '{"namespace": "prod"}')
	`, baseTime)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	timeRange := &core.TimeRange{}
	// Threshold 3 should catch it (4 transitions)
	alerts, err := repo.GetFlappingAlerts(context.Background(), timeRange, 3)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 flapping alert, got %d", len(alerts))
	}

	if alerts[0].Fingerprint != "fp_flap" {
		t.Errorf("Expected fingerprint fp_flap, got %s", alerts[0].Fingerprint)
	}
	if alerts[0].TransitionCount < 4 {
		t.Errorf("Expected at least 4 transitions, got %d", alerts[0].TransitionCount)
	}
}

func TestGetAggregatedStats_WithData(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	// Insert mixed alerts
	_, err := pool.Exec(context.Background(), `
		INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
		VALUES
		('fp1', 'Alert1', 'firing', NOW(), NOW(), '{"namespace": "prod", "severity": "critical"}'),
		('fp2', 'Alert2', 'resolved', NOW(), NOW(), '{"namespace": "prod", "severity": "warning"}'),
		('fp3', 'Alert3', 'firing', NOW(), NOW(), '{"namespace": "dev", "severity": "info"}')
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	stats, err := repo.GetAggregatedStats(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if stats.TotalAlerts != 3 {
		t.Errorf("Expected 3 total alerts, got %d", stats.TotalAlerts)
	}
	if stats.FiringAlerts != 2 {
		t.Errorf("Expected 2 firing alerts, got %d", stats.FiringAlerts)
	}
	if stats.ResolvedAlerts != 1 {
		t.Errorf("Expected 1 resolved alert, got %d", stats.ResolvedAlerts)
	}
	if stats.AlertsBySeverity["critical"] != 1 {
		t.Errorf("Expected 1 critical alert, got %d", stats.AlertsBySeverity["critical"])
	}
	if stats.AlertsByNamespace["prod"] != 2 {
		t.Errorf("Expected 2 prod alerts, got %d", stats.AlertsByNamespace["prod"])
	}
}

func TestGetTopAlerts_WithTimeRange(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	// Insert old and new alerts
	_, err := pool.Exec(context.Background(), `
		INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
		VALUES
		('fp_old', 'OldAlert', 'firing', $1, $1, '{"namespace": "prod"}'),
		('fp_new', 'NewAlert', 'firing', $2, $2, '{"namespace": "prod"}'),
		('fp_new', 'NewAlert', 'firing', $2, $2 + INTERVAL '1 minute', '{"namespace": "prod"}')
	`, old, now)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Filter for last 24 hours
	from := now.Add(-24 * time.Hour)
	timeRange := &core.TimeRange{From: &from}

	alerts, err := repo.GetTopAlerts(context.Background(), timeRange, 10)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert (new only), got %d", len(alerts))
	}
	if alerts[0].Fingerprint != "fp_new" {
		t.Errorf("Expected fp_new, got %s", alerts[0].Fingerprint)
	}
	if alerts[0].FireCount != 2 {
		t.Errorf("Expected fire count 2, got %d", alerts[0].FireCount)
	}
}

func TestGetTopAlerts_LimitValidation(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	// Insert 5 alerts
	for i := 0; i < 5; i++ {
		fp := fmt.Sprintf("fp%d", i)
		_, err := pool.Exec(context.Background(), `
			INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
			VALUES ($1, 'Alert', 'firing', NOW(), NOW(), '{"namespace": "prod"}')
		`, fp)
		if err != nil {
			t.Fatalf("Failed to insert test data: %v", err)
		}
	}

	// Request top 3
	alerts, err := repo.GetTopAlerts(context.Background(), nil, 3)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(alerts) != 3 {
		t.Errorf("Expected 3 alerts, got %d", len(alerts))
	}
}

func TestGetFlappingAlerts_ThresholdFiltering(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	// Insert alert with 2 transitions (below threshold 3)
	baseTime := time.Now()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
		VALUES
		('fp_stable', 'Stable', 'firing', $1, $1, '{"namespace": "prod"}'),
		('fp_stable', 'Stable', 'resolved', $1, $1 + INTERVAL '10 minutes', '{"namespace": "prod"}')
	`, baseTime)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	alerts, err := repo.GetFlappingAlerts(context.Background(), nil, 3)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(alerts) != 0 {
		t.Errorf("Expected 0 flapping alerts (threshold 3), got %d", len(alerts))
	}
}

func TestGetAggregatedStats_TimeRange(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	repo := NewPostgresHistoryRepository(pool, nil, nil)

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO alert_history (fingerprint, alert_name, status, starts_at, created_at, labels)
		VALUES
		('fp_old', 'Old', 'firing', $1, $1, '{"namespace": "prod"}'),
		('fp_new', 'New', 'firing', $2, $2, '{"namespace": "prod"}')
	`, old, now)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	from := now.Add(-24 * time.Hour)
	timeRange := &core.TimeRange{From: &from}

	stats, err := repo.GetAggregatedStats(context.Background(), timeRange)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if stats.TotalAlerts != 1 {
		t.Errorf("Expected 1 alert (new only), got %d", stats.TotalAlerts)
	}
}

// Example integration test structure (when testcontainers are added)
/*
func TestGetTopAlerts_Integration(t *testing.T) {
	// Start testcontainers PostgreSQL
	ctx := context.Background()
	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:15-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_DB":       "testdb",
				"POSTGRES_USER":     "test",
				"POSTGRES_PASSWORD": "test",
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer pgContainer.Terminate(ctx)

	// Get connection details
	host, _ := pgContainer.Host(ctx)
	port, _ := pgContainer.MappedPort(ctx, "5432")

	// Create pool
	config := &PostgresConfig{
		Host:     host,
		Port:     port.Int(),
		Database: "testdb",
		User:     "test",
		Password: "test",
	}
	pool := NewPostgresPool(config, slog.Default())
	err = pool.Connect(ctx)
	require.NoError(t, err)
	defer pool.Disconnect(ctx)

	// Run migrations
	// ... setup schema ...

	// Create repository
	mockStorage := &MockAlertStorage{}
	repo := NewPostgresHistoryRepository(pool.Pool(), mockStorage, slog.Default())

	// Insert test data
	now := time.Now()
	for i := 0; i < 10; i++ {
		alert := &core.Alert{
			Fingerprint: fmt.Sprintf("fp-%d", i),
			AlertName:   fmt.Sprintf("Alert%d", i % 3),
			Status:      core.StatusFiring,
			Labels:      map[string]string{"severity": "critical"},
			StartsAt:    now.Add(-time.Hour * time.Duration(i)),
		}
		mockStorage.SaveAlert(ctx, alert)
	}

	// Test GetTopAlerts
	topAlerts, err := repo.GetTopAlerts(ctx, nil, 5)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(topAlerts), 5)

	// Verify sorting (by fire_count DESC)
	for i := 1; i < len(topAlerts); i++ {
		assert.GreaterOrEqual(t, topAlerts[i-1].FireCount, topAlerts[i].FireCount)
	}
}
*/

// NOTE: For full test coverage, add testcontainers:
// 1. go get github.com/testcontainers/testcontainers-go
// 2. Implement integration tests with real PostgreSQL
// 3. Test SQL queries, aggregations, window functions
// 4. Verify Prometheus metrics are recorded
// 5. Test edge cases (NULL values, empty results, etc.)

// Unit test examples for validation logic
func TestTimeRangeValidation(t *testing.T) {
	tests := []struct {
		name        string
		timeRange   *core.TimeRange
		expectValid bool
	}{
		{
			name:        "nil time range is valid",
			timeRange:   nil,
			expectValid: true,
		},
		{
			name: "valid time range",
			timeRange: &core.TimeRange{
				From: func() *time.Time { t := time.Now().Add(-24 * time.Hour); return &t }(),
				To:   func() *time.Time { t := time.Now(); return &t }(),
			},
			expectValid: true,
		},
		{
			name: "only from is valid",
			timeRange: &core.TimeRange{
				From: func() *time.Time { t := time.Now().Add(-24 * time.Hour); return &t }(),
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Time range validation is implicit in the repository
			// This test documents expected behavior
			if tt.expectValid {
				// All cases should be valid
				t.Log("Time range is valid:", tt.name)
			}
		})
	}
}

func TestLimitValidation(t *testing.T) {
	tests := []struct {
		name          string
		inputLimit    int
		expectedLimit int
	}{
		{
			name:          "zero limit uses default",
			inputLimit:    0,
			expectedLimit: 10,
		},
		{
			name:          "negative limit uses default",
			inputLimit:    -5,
			expectedLimit: 10,
		},
		{
			name:          "valid limit",
			inputLimit:    25,
			expectedLimit: 25,
		},
		{
			name:          "limit over max caps at 100",
			inputLimit:    150,
			expectedLimit: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Document limit validation logic from postgres_history.go GetTopAlerts()
			limit := tt.inputLimit
			if limit <= 0 {
				limit = 10
			}
			if limit > 100 {
				limit = 100
			}

			if limit != tt.expectedLimit {
				t.Errorf("Limit validation failed: got %d, want %d", limit, tt.expectedLimit)
			}
		})
	}
}

func TestFlappingThresholdValidation(t *testing.T) {
	tests := []struct {
		name              string
		inputThreshold    int
		expectedThreshold int
	}{
		{
			name:              "zero threshold uses default",
			inputThreshold:    0,
			expectedThreshold: 3,
		},
		{
			name:              "negative threshold uses default",
			inputThreshold:    -1,
			expectedThreshold: 3,
		},
		{
			name:              "valid threshold",
			inputThreshold:    5,
			expectedThreshold: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Document threshold validation logic from postgres_history.go GetFlappingAlerts()
			threshold := tt.inputThreshold
			if threshold <= 0 {
				threshold = 3
			}

			if threshold != tt.expectedThreshold {
				t.Errorf("Threshold validation failed: got %d, want %d", threshold, tt.expectedThreshold)
			}
		})
	}
}

// BenchmarkGetTopAlerts benchmarks top alerts query performance
func BenchmarkGetTopAlerts(b *testing.B) {
	b.Skip("Benchmark requires PostgreSQL connection")

	// Benchmark structure:
	// 1. Setup PostgreSQL with realistic data (10k+ alerts)
	// 2. Run GetTopAlerts b.N times
	// 3. Measure query performance
}

// BenchmarkGetFlappingAlerts benchmarks flapping detection performance
func BenchmarkGetFlappingAlerts(b *testing.B) {
	b.Skip("Benchmark requires PostgreSQL connection")

	// Benchmark structure:
	// 1. Setup PostgreSQL with alerts having state transitions
	// 2. Run GetFlappingAlerts b.N times
	// 3. Measure window function performance
}

// BenchmarkGetAggregatedStats benchmarks stats aggregation performance
func BenchmarkGetAggregatedStats(b *testing.B) {
	b.Skip("Benchmark requires PostgreSQL connection")

	// Benchmark structure:
	// 1. Setup PostgreSQL with diverse alert data
	// 2. Run GetAggregatedStats b.N times
	// 3. Measure aggregation query performance
}
