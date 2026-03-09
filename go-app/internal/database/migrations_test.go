package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMigrationsDir(t *testing.T) {
	dir, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("resolveMigrationsDir() error = %v", err)
	}

	if filepath.Base(dir) != "migrations" {
		t.Fatalf("resolveMigrationsDir() = %q, want path ending with migrations", dir)
	}

	if _, err := os.Stat(filepath.Join(dir, "20250911094416_initial_schema.sql")); err != nil {
		t.Fatalf("expected initial migration file in %q: %v", dir, err)
	}
}

func TestPgxDriverRegistered(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://amp:amp@127.0.0.1:1/amp?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open(pgx) error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
}
