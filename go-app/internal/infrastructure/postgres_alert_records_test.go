package infrastructure_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ipiton/AMP/internal/ampapi"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
	"github.com/ipiton/AMP/internal/database"
	dbpostgres "github.com/ipiton/AMP/internal/database/postgres"
	"github.com/ipiton/AMP/internal/infrastructure"
	"github.com/ipiton/AMP/internal/infrastructure/repository"
)

const (
	columnMigration int64 = 20261108000000
	indexMigration  int64 = 20261108000001
)

type testDB struct {
	pool  *dbpostgres.PostgresPool
	sqlDB *sql.DB
}

// newTestDB starts PostgreSQL 16, runs every migration as amp-upstream does
// and returns the pool. Skipped without Docker or in -short mode.
func newTestDB(t *testing.T) *testDB {
	t.Helper()
	if testing.Short() {
		t.Skip("testcontainers-backed test skipped in short mode")
	}
	ctx := context.Background()
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Skipf("no Docker client: %v", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingCtx); err != nil {
		_ = cli.Close()
		t.Skipf("Docker daemon not reachable: %v", err)
	}
	_ = cli.Close()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("amp"), tcpostgres.WithUsername("amp"), tcpostgres.WithPassword("amp"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	mapped, err := ctr.MappedPort(ctx, "5432")
	require.NoError(t, err)
	port, err := strconv.Atoi(mapped.Port())
	require.NoError(t, err)

	cfg := dbpostgres.DefaultConfig()
	cfg.Host, cfg.Port, cfg.Database, cfg.User, cfg.Password, cfg.SSLMode = host, port, "amp", "amp", "amp", "disable"
	pool := dbpostgres.NewPostgresPool(cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, pool.Connect(ctx))
	t.Cleanup(func() { _ = pool.Disconnect(ctx) })
	require.NoError(t, database.RunMigrations(ctx, pool, slog.New(slog.DiscardHandler)))

	sqlDB, err := sql.Open("pgx", cfg.DSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &testDB{pool: pool, sqlDB: sqlDB}
}

func (db *testDB) storage(t *testing.T) *infrastructure.PostgresStorageAdapter {
	t.Helper()
	s, err := infrastructure.NewPostgresStorageAdapter(db.pool.SharePool(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return s
}

func (db *testDB) scalar(t *testing.T, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	require.NoError(t, db.sqlDB.QueryRow(query, args...).Scan(&v))
	return v.String
}

func newAlert(labels map[string]string, at time.Time) *core.Alert {
	return &core.Alert{
		Fingerprint: alertconv.Fingerprint(labels),
		AlertName:   labels["alertname"],
		Status:      core.StatusFiring,
		Labels:      labels,
		Annotations: map[string]string{"summary": "s"},
		StartsAt:    at,
		Timestamp:   &at,
	}
}

// Both migrations go down and up again; the column and index come and go
// with them.
func TestUpstreamFingerprintMigrationsDownUp(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	provider, err := goose.NewProvider(goose.DialectPostgres, db.sqlDB, os.DirFS("../../migrations"))
	require.NoError(t, err)

	hasColumn := func() string {
		return db.scalar(t, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'alerts' AND column_name = 'upstream_fingerprint'`)
	}
	hasIndex := func() string {
		return db.scalar(t, `SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_alerts_upstream_fingerprint'`)
	}
	require.Equal(t, "1", hasColumn())
	require.Equal(t, "1", hasIndex())

	_, err = provider.DownTo(ctx, columnMigration-1)
	require.NoError(t, err)
	assert.Equal(t, "0", hasColumn())
	assert.Equal(t, "0", hasIndex())

	_, err = provider.UpTo(ctx, columnMigration)
	require.NoError(t, err)
	assert.Equal(t, "1", hasColumn())
	assert.Equal(t, "0", hasIndex())

	_, err = provider.Up(ctx)
	require.NoError(t, err)
	assert.Equal(t, "1", hasIndex())
	assert.Equal(t, strconv.FormatInt(indexMigration, 10), db.scalar(t, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`))
}

func TestUpstreamFingerprintWrittenAndFound(t *testing.T) {
	db := newTestDB(t)
	s := db.storage(t)
	ctx := context.Background()
	labels := map[string]string{"alertname": "Disk", "instance": "a"}
	upstreamFP := model.LabelSet{"alertname": "Disk", "instance": "a"}.Fingerprint().String()
	t0 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

	a := newAlert(labels, t0)
	require.NoError(t, s.SaveAlert(ctx, a))
	assert.Equal(t, upstreamFP, db.scalar(t, `SELECT upstream_fingerprint FROM alerts WHERE fingerprint = $1`, a.Fingerprint),
		"the column is api/v2's fingerprint")

	// UpdateAlert writes it too (a row left NULL by an older writer).
	_, err := db.sqlDB.Exec(`UPDATE alerts SET upstream_fingerprint = NULL`)
	require.NoError(t, err)
	t1 := t0.Add(time.Minute)
	a.Status, a.Timestamp = core.StatusResolved, &t1
	require.NoError(t, s.UpdateAlert(ctx, a))
	assert.Equal(t, upstreamFP, db.scalar(t, `SELECT upstream_fingerprint FROM alerts WHERE fingerprint = $1`, a.Fingerprint))

	for name, get := range map[string]func() (*core.AlertHistoryRecord, error){
		"amp":      func() (*core.AlertHistoryRecord, error) { return s.AlertRecordByFingerprint(ctx, a.Fingerprint) },
		"upstream": func() (*core.AlertHistoryRecord, error) { return s.AlertRecordByUpstreamFingerprint(ctx, upstreamFP) },
	} {
		rec, err := get()
		require.NoError(t, err, name)
		assert.Equal(t, a.Fingerprint, rec.Alert.Fingerprint, name)
		assert.Equal(t, core.StatusResolved, rec.Alert.Status, name)
		assert.Equal(t, labels, rec.Alert.Labels, name)
		assert.True(t, rec.Alert.StartsAt.Equal(t0), name)
		assert.False(t, rec.FirstSeenAt.IsZero(), name)
	}

	_, err = s.AlertRecordByFingerprint(ctx, strings.Repeat("0", 64))
	assert.ErrorIs(t, err, core.ErrAlertNotFound)
	_, err = s.AlertRecordByUpstreamFingerprint(ctx, "0123456789abcdef")
	assert.ErrorIs(t, err, core.ErrAlertNotFound)
}

// Two label sets sharing an FNV fingerprint: the most recent row wins.
func TestUpstreamFingerprintCollision(t *testing.T) {
	db := newTestDB(t)
	s := db.storage(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	older, newer := newAlert(map[string]string{"alertname": "A"}, t0), newAlert(map[string]string{"alertname": "B"}, t0.Add(time.Hour))
	require.NoError(t, s.SaveAlert(ctx, older))
	require.NoError(t, s.SaveAlert(ctx, newer))
	_, err := db.sqlDB.Exec(`UPDATE alerts SET upstream_fingerprint = 'feedfacefeedface'`)
	require.NoError(t, err)

	rec, err := s.AlertRecordByUpstreamFingerprint(ctx, "feedfacefeedface")
	require.NoError(t, err)
	assert.Equal(t, newer.Fingerprint, rec.Alert.Fingerprint)
}

func TestBackfillUpstreamFingerprints(t *testing.T) {
	db := newTestDB(t)
	s := db.storage(t)
	ctx := context.Background()

	// Legacy rows: written before the column, one with a NULL starts_at and
	// one whose labels do not parse as a label map.
	const n = 7
	want := map[string]string{}
	for i := range n {
		labels := map[string]string{"alertname": "Legacy", "i": strconv.Itoa(i)}
		lj, err := json.Marshal(labels)
		require.NoError(t, err)
		fp := alertconv.Fingerprint(labels)
		_, err = db.sqlDB.Exec(`INSERT INTO alerts (fingerprint, alert_name, status, labels) VALUES ($1, 'Legacy', 'resolved', $2)`, fp, lj)
		require.NoError(t, err)
		want[fp] = alertconv.UpstreamFingerprint(labels)
	}
	_, err := db.sqlDB.Exec(`INSERT INTO alerts (fingerprint, alert_name, status, labels) VALUES ($1, 'Bad', 'resolved', '{"n": 1}')`, strings.Repeat("b", 64))
	require.NoError(t, err)

	var plan strings.Builder
	rows, err := db.sqlDB.Query(`EXPLAIN SELECT id, labels FROM alerts WHERE upstream_fingerprint IS NULL AND id > 0 ORDER BY id LIMIT 500`)
	require.NoError(t, err)
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	require.NoError(t, rows.Close())
	t.Logf("backfill batch plan (deep-review F5):\n%s", plan.String())

	filled, err := s.BackfillUpstreamFingerprints(ctx, 3) // several batches
	require.NoError(t, err)
	assert.Equal(t, n, filled)
	for fp, upstream := range want {
		assert.Equal(t, upstream, db.scalar(t, `SELECT upstream_fingerprint FROM alerts WHERE fingerprint = $1`, fp))
	}
	assert.Equal(t, "", db.scalar(t, `SELECT upstream_fingerprint FROM alerts WHERE alert_name = 'Bad'`), "unparsable labels are skipped")

	filled, err = s.BackfillUpstreamFingerprints(ctx, 0)
	require.NoError(t, err)
	assert.Zero(t, filled, "idempotent")

	// A legacy row with NULL starts_at is readable by both keys.
	for fp, upstream := range want {
		rec, err := s.AlertRecordByUpstreamFingerprint(ctx, upstream)
		require.NoError(t, err)
		assert.Equal(t, fp, rec.Alert.Fingerprint)
		assert.True(t, rec.Alert.StartsAt.IsZero())
		break
	}
}

func TestUpstreamFingerprintIndexValid(t *testing.T) {
	db := newTestDB(t)
	s := db.storage(t)
	ctx := context.Background()

	valid, err := s.UpstreamFingerprintIndexValid(ctx)
	require.NoError(t, err)
	assert.True(t, valid)

	// What an interrupted CREATE INDEX CONCURRENTLY leaves behind.
	_, err = db.sqlDB.Exec(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'idx_alerts_upstream_fingerprint'::regclass`)
	require.NoError(t, err)
	valid, err = s.UpstreamFingerprintIndexValid(ctx)
	require.NoError(t, err)
	assert.False(t, valid, "INVALID")

	_, err = db.sqlDB.Exec(`DROP INDEX idx_alerts_upstream_fingerprint`)
	require.NoError(t, err)
	valid, err = s.UpstreamFingerprintIndexValid(ctx)
	require.NoError(t, err)
	assert.False(t, valid, "missing")
}

// ampapi on the real readers: the history row and the investigation by the
// api/v2 fingerprint.
func TestAMPAPIOnPostgres(t *testing.T) {
	db := newTestDB(t)
	s := db.storage(t)
	ctx := context.Background()
	labels := map[string]string{"alertname": "Disk", "instance": "a"}
	a := newAlert(labels, time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC))
	require.NoError(t, s.SaveAlert(ctx, a))

	invRepo := repository.NewPostgresInvestigationRepositoryWithRegisterer(db.pool.SharePool(), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, invRepo.Create(ctx, &core.Investigation{ID: "6f1b2c3d-0000-4000-8000-000000000001", Fingerprint: a.Fingerprint}))
	require.NoError(t, invRepo.SaveResult(ctx, "6f1b2c3d-0000-4000-8000-000000000001", &core.InvestigationResult{Summary: "disk full", Confidence: 0.9}))

	h := ampapi.New(ampapi.Options{Alerts: s, Investigations: invRepo, Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	upstreamFP := alertconv.UpstreamFingerprint(labels)
	get := func(path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var m map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), rec.Body.String())
		return rec.Code, m
	}

	code, m := get("/api/amp/v1/alerts/" + upstreamFP)
	require.Equal(t, http.StatusOK, code, m)
	assert.Equal(t, upstreamFP, m["fingerprint"])
	assert.Equal(t, a.Fingerprint, m["amp_fingerprint"])
	assert.Equal(t, "Disk", m["alert_name"])

	code, m = get("/api/amp/v1/alerts/" + upstreamFP + "/investigation")
	require.Equal(t, http.StatusOK, code, m)
	assert.Equal(t, upstreamFP, m["fingerprint"])
	assert.Equal(t, "completed", m["status"])
	assert.Equal(t, "disk full", m["result"].(map[string]any)["summary"])

	code, _ = get("/api/amp/v1/alerts/0123456789abcdef/investigation")
	assert.Equal(t, http.StatusNotFound, code)
}
