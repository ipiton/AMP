package template

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ipiton/AMP/internal/core/domain"
)

// ================================================================================
// TN-155: Template API (CRUD) - Repository Layer
// ================================================================================
// Template persistence layer with dual-database support.
//
// Features:
// - Works with both PostgreSQL (Standard Profile) and SQLite (Lite Profile)
// - Full CRUD operations
// - Version control
// - Pagination and filtering
// - Transaction support
//
// Performance Targets:
// - Create: < 50ms p95
// - Get: < 100ms p95 (uncached)
// - List: < 200ms p95
// - Update: < 75ms p95
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// TemplateRepository defines the interface for template persistence
type TemplateRepository interface {
	// CRUD operations
	Create(ctx context.Context, template *domain.Template) error
	GetByName(ctx context.Context, name string) (*domain.Template, error)
	GetByID(ctx context.Context, id string) (*domain.Template, error)
	List(ctx context.Context, filters domain.ListFilters) ([]*domain.Template, int, error)
	Update(ctx context.Context, template *domain.Template) error
	Delete(ctx context.Context, name string, soft bool) error

	// Version operations
	CreateVersion(ctx context.Context, version *domain.TemplateVersion) error
	ListVersions(ctx context.Context, templateID string, filters domain.VersionFilters) ([]*domain.TemplateVersion, int, error)
	GetVersion(ctx context.Context, templateID string, versionNum int) (*domain.TemplateVersion, error)

	// Utility
	Exists(ctx context.Context, name string) (bool, error)
	CountByType(ctx context.Context) (map[string]int, error)
}

// ================================================================================

// DBInterface is a unified interface for both pgxpool.Pool and sql.DB
type DBInterface interface {
	QueryRow(ctx context.Context, query string, args ...interface{}) Row
	Query(ctx context.Context, query string, args ...interface{}) (Rows, error)
	Exec(ctx context.Context, query string, args ...interface{}) (Result, error)
	Begin(ctx context.Context) (Tx, error)
}

// Row interface for scanning results
type Row interface {
	Scan(dest ...interface{}) error
}

// Rows interface for iterating results
type Rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Close() error
	Err() error
}

// Result interface for exec results
type Result interface {
	RowsAffected() (int64, error)
}

// Tx interface for transactions
type Tx interface {
	QueryRow(ctx context.Context, query string, args ...interface{}) Row
	Query(ctx context.Context, query string, args ...interface{}) (Rows, error)
	Exec(ctx context.Context, query string, args ...interface{}) (Result, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// ================================================================================

// dbAdapter adapts different database drivers to common interface
type dbAdapter struct {
	pgPool  *pgxpool.Pool
	sqlDB   *sql.DB
	isPgx   bool
	isSqlDB bool
}

// newDBAdapter creates adapter from pgxpool.Pool or sql.DB
func newDBAdapter(db interface{}) (*dbAdapter, error) {
	adapter := &dbAdapter{}

	switch v := db.(type) {
	case *pgxpool.Pool:
		adapter.pgPool = v
		adapter.isPgx = true
		return adapter, nil
	case *sql.DB:
		adapter.sqlDB = v
		adapter.isSqlDB = true
		return adapter, nil
	default:
		return nil, fmt.Errorf("unsupported database type: %T (expected *pgxpool.Pool or *sql.DB)", db)
	}
}

func (a *dbAdapter) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	if a.isPgx {
		return &pgxRowAdapter{a.pgPool.QueryRow(ctx, query, args...)}
	}
	return &sqlRowAdapter{a.sqlDB.QueryRowContext(ctx, query, args...)}
}

func (a *dbAdapter) Query(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	if a.isPgx {
		rows, err := a.pgPool.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		return &pgxRowsAdapter{rows}, nil
	}
	rows, err := a.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRowsAdapter{rows}, nil
}

func (a *dbAdapter) Exec(ctx context.Context, query string, args ...interface{}) (Result, error) {
	if a.isPgx {
		tag, err := a.pgPool.Exec(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		return &pgxResultAdapter{tag}, nil
	}
	res, err := a.sqlDB.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlResultAdapter{res}, nil
}

func (a *dbAdapter) Begin(ctx context.Context) (Tx, error) {
	if a.isPgx {
		tx, err := a.pgPool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		return &pgxTxAdapter{tx}, nil
	}
	tx, err := a.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlTxAdapter{tx}, nil
}

// ================================================================================
// Adapter implementations for pgx
// ================================================================================

type pgxRowAdapter struct {
	row pgx.Row
}

func (r *pgxRowAdapter) Scan(dest ...interface{}) error {
	return r.row.Scan(dest...)
}

type pgxRowsAdapter struct {
	rows pgx.Rows
}

func (r *pgxRowsAdapter) Next() bool {
	return r.rows.Next()
}

func (r *pgxRowsAdapter) Scan(dest ...interface{}) error {
	return r.rows.Scan(dest...)
}

func (r *pgxRowsAdapter) Close() error {
	r.rows.Close()
	return nil
}

func (r *pgxRowsAdapter) Err() error {
	return r.rows.Err()
}

type pgxResultAdapter struct {
	tag pgconn.CommandTag
}

func (r *pgxResultAdapter) RowsAffected() (int64, error) {
	return r.tag.RowsAffected(), nil
}

type pgxTxAdapter struct {
	tx pgx.Tx
}

func (t *pgxTxAdapter) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	return &pgxRowAdapter{t.tx.QueryRow(ctx, query, args...)}
}

func (t *pgxTxAdapter) Query(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRowsAdapter{rows}, nil
}

func (t *pgxTxAdapter) Exec(ctx context.Context, query string, args ...interface{}) (Result, error) {
	tag, err := t.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &pgxResultAdapter{tag}, nil
}

func (t *pgxTxAdapter) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t *pgxTxAdapter) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

// ================================================================================
// Adapter implementations for database/sql
// ================================================================================

type sqlRowAdapter struct {
	row *sql.Row
}

func (r *sqlRowAdapter) Scan(dest ...interface{}) error {
	return r.row.Scan(dest...)
}

type sqlRowsAdapter struct {
	rows *sql.Rows
}

func (r *sqlRowsAdapter) Next() bool {
	return r.rows.Next()
}

func (r *sqlRowsAdapter) Scan(dest ...interface{}) error {
	return r.rows.Scan(dest...)
}

func (r *sqlRowsAdapter) Close() error {
	return r.rows.Close()
}

func (r *sqlRowsAdapter) Err() error {
	return r.rows.Err()
}

type sqlResultAdapter struct {
	result sql.Result
}

func (r *sqlResultAdapter) RowsAffected() (int64, error) {
	return r.result.RowsAffected()
}

type sqlTxAdapter struct {
	tx *sql.Tx
}

func (t *sqlTxAdapter) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	return &sqlRowAdapter{t.tx.QueryRowContext(ctx, query, args...)}
}

func (t *sqlTxAdapter) Query(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRowsAdapter{rows}, nil
}

func (t *sqlTxAdapter) Exec(ctx context.Context, query string, args ...interface{}) (Result, error) {
	res, err := t.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlResultAdapter{res}, nil
}

func (t *sqlTxAdapter) Commit(ctx context.Context) error {
	return t.tx.Commit()
}

func (t *sqlTxAdapter) Rollback(ctx context.Context) error {
	return t.tx.Rollback()
}

// ================================================================================

// DefaultTemplateRepository implements TemplateRepository
// Works with both PostgreSQL (Standard Profile) and SQLite (Lite Profile)
type DefaultTemplateRepository struct {
	db     DBInterface
	logger *slog.Logger
}

// NewTemplateRepository creates a new repository
// Accepts either *pgxpool.Pool (PostgreSQL) or *sql.DB (SQLite)
func NewTemplateRepository(db interface{}, logger *slog.Logger) (TemplateRepository, error) {
	if logger == nil {
		logger = slog.Default()
	}

	adapter, err := newDBAdapter(db)
	if err != nil {
		return nil, err
	}

	return &DefaultTemplateRepository{
		db:     adapter,
		logger: logger,
	}, nil
}

// ================================================================================
// Helper functions
// ================================================================================

// scanTemplate scans a row into Template struct
func scanTemplate(row Row) (*domain.Template, error) {
	var t domain.Template
	var metadataJSON []byte
	var deletedAt sql.NullTime

	err := row.Scan(
		&t.ID,
		&t.Name,
		&t.Type,
		&t.Content,
		&t.Description,
		&metadataJSON,
		&t.Version,
		&t.CreatedAt,
		&t.UpdatedAt,
		&t.CreatedBy,
		&t.UpdatedBy,
		&deletedAt,
	)
	if err != nil {
		return nil, err
	}

	// Parse metadata JSON
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &t.Metadata); err != nil {
			return nil, fmt.Errorf("failed to parse metadata: %w", err)
		}
	}

	// Set deleted_at
	if deletedAt.Valid {
		t.DeletedAt = &deletedAt.Time
	}

	return &t, nil
}

// scanTemplateVersion scans a row into TemplateVersion struct
func scanTemplateVersion(row Row) (*domain.TemplateVersion, error) {
	var v domain.TemplateVersion
	var metadataJSON []byte

	err := row.Scan(
		&v.ID,
		&v.TemplateID,
		&v.Version,
		&v.Content,
		&v.Description,
		&metadataJSON,
		&v.CreatedAt,
		&v.CreatedBy,
		&v.ChangeSummary,
	)
	if err != nil {
		return nil, err
	}

	// Parse metadata JSON
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &v.Metadata); err != nil {
			return nil, fmt.Errorf("failed to parse metadata: %w", err)
		}
	}

	return &v, nil
}

// ================================================================================
// Error types
// ================================================================================

var (
	// ErrTemplateNotFound is returned when template doesn't exist
	ErrTemplateNotFound = errors.New("template not found")

	// ErrTemplateExists is returned when template name already exists
	ErrTemplateExists = errors.New("template with this name already exists")

	// ErrVersionNotFound is returned when version doesn't exist
	ErrVersionNotFound = errors.New("template version not found")

	// ErrInvalidFilter is returned when filter parameters are invalid
	ErrInvalidFilter = errors.New("invalid filter parameters")
)

// isNotFoundError checks if error is "not found" error
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows)
}

// ================================================================================
