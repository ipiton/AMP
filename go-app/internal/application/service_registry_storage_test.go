package application

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

func withIsolatedPrometheusRegistry(t *testing.T) {
	t.Helper()

	registry := prometheus.NewRegistry()
	originalRegisterer := prometheus.DefaultRegisterer
	originalGatherer := prometheus.DefaultGatherer
	prometheus.DefaultRegisterer = registry
	prometheus.DefaultGatherer = registry

	t.Cleanup(func() {
		prometheus.DefaultRegisterer = originalRegisterer
		prometheus.DefaultGatherer = originalGatherer
	})
}

func TestServiceRegistryInitialize_LiteCreatesStorageRuntime(t *testing.T) {
	t.Helper()
	withIsolatedPrometheusRegistry(t)

	cfg := &appconfig.Config{
		Profile: appconfig.ProfileLite,
		Storage: appconfig.StorageConfig{
			Backend:        appconfig.StorageBackendFilesystem,
			FilesystemPath: filepath.Join(t.TempDir(), "alerts.db"),
		},
		Database: appconfig.DatabaseConfig{
			MaxConnections: 1,
			MinConnections: 1,
		},
		Redis: appconfig.RedisConfig{
			Addr:            "127.0.0.1:1",
			DB:              0,
			PoolSize:        1,
			MinIdleConns:    1,
			DialTimeout:     10 * time.Millisecond,
			ReadTimeout:     10 * time.Millisecond,
			WriteTimeout:    10 * time.Millisecond,
			MaxRetries:      1,
			MinRetryBackoff: time.Millisecond,
			MaxRetryBackoff: 2 * time.Millisecond,
		},
		Publishing: appconfig.PublishingConfig{
			Enabled: false,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := NewServiceRegistry(cfg, logger)
	if err != nil {
		t.Fatalf("NewServiceRegistry() error = %v", err)
	}

	ctx := context.Background()
	if err := registry.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	t.Cleanup(func() {
		_ = registry.Shutdown(ctx)
	})

	if registry.Storage() == nil {
		t.Fatalf("Storage() must not be nil after successful lite bootstrap")
	}
	if registry.storageRuntime == nil {
		t.Fatalf("storageRuntime must not be nil after successful lite bootstrap")
	}
	if err := registry.Liveness(ctx); err != nil {
		t.Fatalf("Liveness() error = %v", err)
	}
	if err := registry.Readiness(ctx); err != nil {
		t.Fatalf("Readiness() error = %v", err)
	}

	report := registry.ReadinessReport(ctx)
	if got := report["status"]; got != "degraded" {
		t.Fatalf("ReadinessReport status = %v, want degraded", got)
	}
	degradedReasons, ok := report["degraded_reasons"].([]string)
	if !ok || len(degradedReasons) == 0 {
		t.Fatalf("ReadinessReport degraded_reasons must contain cache fallback details, got %#v", report["degraded_reasons"])
	}
}

func TestServiceRegistryInitializeStorage_StandardRequiresDatabase(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{
			Profile: appconfig.ProfileStandard,
			Storage: appconfig.StorageConfig{
				Backend: appconfig.StorageBackendPostgres,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := registry.initializeStorage(context.Background())
	if err == nil {
		t.Fatalf("initializeStorage() expected error for standard profile without database")
	}
	if !strings.Contains(err.Error(), "postgres database is not initialized") {
		t.Fatalf("initializeStorage() error = %v, want postgres database initialization failure", err)
	}
}

func TestServiceRegistryInitialize_StandardFailsFastOnDatabaseConnect(t *testing.T) {
	t.Helper()
	withIsolatedPrometheusRegistry(t)

	cfg := &appconfig.Config{
		Profile: appconfig.ProfileStandard,
		Storage: appconfig.StorageConfig{
			Backend: appconfig.StorageBackendPostgres,
		},
		Database: appconfig.DatabaseConfig{
			Host:            "127.0.0.1",
			Port:            1,
			Database:        "amp",
			Username:        "amp",
			Password:        "amp",
			SSLMode:         "disable",
			MaxConnections:  1,
			MinConnections:  1,
			ConnectTimeout:  20 * time.Millisecond,
			MaxConnLifetime: time.Second,
			MaxConnIdleTime: time.Second,
		},
		Publishing: appconfig.PublishingConfig{
			Enabled: false,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := NewServiceRegistry(cfg, logger)
	if err != nil {
		t.Fatalf("NewServiceRegistry() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	err = registry.Initialize(ctx)
	if err == nil {
		t.Fatalf("Initialize() expected database connection failure")
	}
	if !strings.Contains(err.Error(), "failed to connect to PostgreSQL") {
		t.Fatalf("Initialize() error = %v, want PostgreSQL connection failure", err)
	}
	if registry.initialized {
		t.Fatalf("registry must remain uninitialized after database bootstrap failure")
	}
	if registry.Storage() != nil {
		t.Fatalf("Storage() must stay nil after failed standard bootstrap")
	}
	if registry.storageRuntime != nil {
		t.Fatalf("storageRuntime must stay nil after failed standard bootstrap")
	}
}

func TestServiceRegistryReadiness_FailsWithoutStorageRuntime(t *testing.T) {
	registry := &ServiceRegistry{
		config: &appconfig.Config{
			Profile: appconfig.ProfileLite,
			Storage: appconfig.StorageConfig{
				Backend: appconfig.StorageBackendFilesystem,
			},
		},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		initialized: true,
	}

	if err := registry.Readiness(context.Background()); err == nil {
		t.Fatalf("Readiness() expected error when storage runtime is missing")
	}
	if err := registry.Liveness(context.Background()); err != nil {
		t.Fatalf("Liveness() error = %v, want nil for initialized registry", err)
	}
}
