package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	"github.com/ipiton/AMP/internal/upstreamcore"
	"github.com/ipiton/AMP/internal/upstreamtargets"
)

const (
	// defaultTargetRefreshInterval is publishing.refresh.interval's default.
	defaultTargetRefreshInterval = 5 * time.Minute
	initialTargetRefreshTimeout  = 30 * time.Second
	serviceAccountNamespaceFile  = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// newTargetSource adds the K8s-Secret publishing targets to the core's
// configuration (upstreamcore.Options.ExtendConfig) in the standard profile
// with publishing.enabled. Without a cluster to talk to it logs a warning and
// the core runs on the file alone. It returns a nil source when the targets
// are off.
func newTargetSource(logger *slog.Logger, cfg *appconfig.Config, opts *upstreamcore.Options) (*upstreamtargets.Source, time.Duration, error) {
	if cfg.Profile != appconfig.ProfileStandard || !cfg.Publishing.Enabled {
		logger.Info("K8s-Secret publishing targets disabled",
			"profile", cfg.Profile, "publishing_enabled", cfg.Publishing.Enabled)
		return nil, 0, nil
	}

	k8sConfig := k8s.DefaultK8sClientConfig()
	k8sConfig.Logger = logger
	client, err := k8s.NewK8sClient(k8sConfig)
	if err != nil {
		logger.Warn("K8s-Secret publishing targets unavailable, using the configuration file's receivers only", "err", err)
		return nil, 0, nil
	}
	namespace := publishingNamespace(cfg)
	discovery, err := publishing.NewTargetDiscoveryManager(client, namespace, cfg.Publishing.Discovery.LabelSelector, logger, nil)
	if err != nil {
		return nil, 0, err
	}
	source, err := upstreamtargets.New(discovery, logger, opts.Registerer)
	if err != nil {
		return nil, 0, err
	}

	// The first snapshot goes into the initial configuration. A failure
	// leaves it empty; the poll retries.
	ctx, cancel := context.WithTimeout(context.Background(), initialTargetRefreshTimeout)
	defer cancel()
	if _, err := source.Refresh(ctx); err != nil {
		logger.Warn("initial K8s target discovery failed, starting without K8s targets", "err", err)
	}
	opts.ExtendConfig = source.ExtendConfig

	interval := cfg.Publishing.Refresh.Interval
	if interval <= 0 {
		interval = defaultTargetRefreshInterval
	}
	logger.Info("K8s-Secret publishing targets enabled", "namespace", namespace, "refresh_interval", interval)
	return source, interval, nil
}

// publishingNamespace is the namespace target discovery watches:
// publishing.discovery.namespace, else the pod's own namespace from the env.
func publishingNamespace(cfg *appconfig.Config) string {
	if ns := strings.TrimSpace(cfg.Publishing.Discovery.Namespace); ns != "" {
		return ns
	}
	for _, key := range []string{"POD_NAMESPACE", "K8S_NAMESPACE", "NAMESPACE"} {
		if ns := strings.TrimSpace(os.Getenv(key)); ns != "" {
			return ns
		}
	}
	if data, err := os.ReadFile(serviceAccountNamespaceFile); err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}
	return "default"
}
