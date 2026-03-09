//go:build futureparity
// +build futureparity

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	application "github.com/ipiton/AMP/internal/application"
	appconfig "github.com/ipiton/AMP/internal/config"
)

const (
	runtimeStateFileEnv               = "AMP_RUNTIME_STATE_FILE"
	runtimeClusterListenAddressEnv    = "AMP_CLUSTER_LISTEN_ADDRESS"
	runtimeClusterAdvertiseAddressEnv = "AMP_CLUSTER_ADVERTISE_ADDRESS"
	runtimeClusterNameEnv             = "AMP_CLUSTER_NAME"
)

var (
	futureParityRegistryOnce sync.Once
	futureParityRegistry     *application.ServiceRegistry
	futureParityRegistryErr  error
)

type runtimeClusterContext struct {
	status      string
	name        string
	settleUntil time.Time
	peers       []map[string]string
}

// registerRoutes preserves the historical test seam for futureparity-only suites.
// The compatibility owner stays build-tagged so production main/router ownership
// does not drift back toward the historical wide-surface tests.
func registerRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}

	registry, err := futureParityCompatibilityRegistry()
	if err != nil {
		registerFutureParityBootstrapFailure(mux, err)
		return
	}

	application.NewRouter(registry).SetupRoutes(mux)
	registerLegacyDashboardRoutes(mux, registry)
}

func configSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func buildRuntimeClusterStatusPayload(clusterCtx *runtimeClusterContext, now time.Time) map[string]any {
	payload := map[string]any{
		"status": "disabled",
		"name":   "",
		"peers":  []map[string]string{},
	}
	if clusterCtx == nil {
		return payload
	}

	status := strings.TrimSpace(clusterCtx.status)
	if status == "" {
		status = "disabled"
	}
	if status == "ready" && !clusterCtx.settleUntil.IsZero() && now.Before(clusterCtx.settleUntil) {
		status = "settling"
	}

	peers := make([]map[string]string, 0, len(clusterCtx.peers))
	for _, peer := range clusterCtx.peers {
		clone := make(map[string]string, len(peer))
		for key, value := range peer {
			clone[key] = value
		}
		peers = append(peers, clone)
	}

	payload["status"] = status
	payload["name"] = strings.TrimSpace(clusterCtx.name)
	payload["peers"] = peers
	return payload
}

func futureParityCompatibilityRegistry() (*application.ServiceRegistry, error) {
	futureParityRegistryOnce.Do(func() {
		futureParityRegistry, futureParityRegistryErr = newFutureParityCompatibilityRegistry()
	})

	return futureParityRegistry, futureParityRegistryErr
}

func newFutureParityCompatibilityRegistry() (*application.ServiceRegistry, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := loadFutureParityCompatibilityConfig(logger)

	registry, err := application.NewServiceRegistry(cfg, logger)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := registry.Initialize(ctx); err != nil {
		return nil, err
	}

	return registry, nil
}

func loadFutureParityCompatibilityConfig(logger *slog.Logger) *appconfig.Config {
	cfg, err := appconfig.LoadConfig(resolveRuntimeConfigPath())
	if err != nil {
		logger.Warn("futureparity config load failed, using compatibility defaults", "error", err)
		cfg = futureParityDefaultConfig()
	}

	cfg.Profile = appconfig.ProfileLite
	cfg.Storage.Backend = appconfig.StorageBackendFilesystem
	cfg.Storage.FilesystemPath = futureParityStoragePath()
	cfg.Redis.Addr = ""
	cfg.Publishing.Enabled = false
	if strings.TrimSpace(cfg.Server.Host) == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 9093
	}
	if strings.TrimSpace(cfg.App.Environment) == "" {
		cfg.App.Environment = "development"
	}

	return cfg
}

func futureParityDefaultConfig() *appconfig.Config {
	return &appconfig.Config{
		Profile: appconfig.ProfileLite,
		Storage: appconfig.StorageConfig{
			Backend:        appconfig.StorageBackendFilesystem,
			FilesystemPath: futureParityStoragePath(),
		},
		Server: appconfig.ServerConfig{
			Port:                    9093,
			Host:                    "127.0.0.1",
			ReadTimeout:             30 * time.Second,
			WriteTimeout:            30 * time.Second,
			IdleTimeout:             120 * time.Second,
			GracefulShutdownTimeout: 30 * time.Second,
		},
		App: appconfig.AppConfig{
			Name:        "futureparity-compat",
			Environment: "development",
		},
		Publishing: appconfig.PublishingConfig{
			Enabled: false,
		},
	}
}

func futureParityStoragePath() string {
	statePath := strings.TrimSpace(os.Getenv(runtimeStateFileEnv))
	if statePath == "" {
		return filepath.Join(os.TempDir(), "amp-futureparity-compat.sqlite")
	}

	statePath = filepath.Clean(statePath)
	dir := filepath.Dir(statePath)
	base := filepath.Base(statePath)
	ext := filepath.Ext(base)
	base = strings.TrimSuffix(base, ext)
	if base == "" || base == "." {
		base = "runtime-state"
	}

	return filepath.Join(dir, base+".sqlite")
}

func registerFutureParityBootstrapFailure(mux *http.ServeMux, err error) {
	message := "futureparity compatibility bootstrap failed: " + err.Error()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, message, http.StatusInternalServerError)
	})
}
