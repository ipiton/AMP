package application

import (
	"context"
	"os"
	"strings"
	"time"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/prometheus/client_golang/prometheus"
)

const serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

func (r *ServiceRegistry) initializePublishing(ctx context.Context) {
	if !r.config.Publishing.Enabled {
		r.publisher = NewMetricsOnlyPublisher("publishing_disabled", r.logger)
		r.logger.Info("Publishing disabled by config")
		return
	}

	if r.config.Profile != appconfig.ProfileStandard {
		r.publisher = NewMetricsOnlyPublisher("lite_profile", r.logger)
		r.logger.Info("Publishing running in metrics-only mode for non-standard profile",
			"profile", r.config.Profile,
		)
		return
	}

	if err := r.initializePublishingRuntime(ctx); err != nil {
		r.logger.Warn("Publishing runtime unavailable, falling back to metrics-only mode", "error", err)
		r.shutdownPublishing()
		r.publisher = NewMetricsOnlyPublisher("publishing_stack_unavailable", r.logger)
		return
	}
}

func (r *ServiceRegistry) initializePublishingRuntime(ctx context.Context) error {
	k8sConfig := k8s.DefaultK8sClientConfig()
	k8sConfig.Logger = r.logger

	k8sClient, err := k8s.NewK8sClient(k8sConfig)
	if err != nil {
		return err
	}
	r.k8sClient = k8sClient

	discovery, err := businesspublishing.NewTargetDiscoveryManager(
		k8sClient,
		resolvePublishingNamespace(r.config),
		r.config.Publishing.Discovery.LabelSelector,
		r.logger,
		nil,
	)
	if err != nil {
		return err
	}
	r.publishingDiscovery = discovery

	if err := discovery.DiscoverTargets(ctx); err != nil {
		r.logger.Warn("Initial publishing target discovery failed, starting with empty cache", "error", err)
	}

	discoveryAdapter, err := NewDiscoveryAdapter(discovery)
	if err != nil {
		return err
	}
	r.publishingDiscoveryAdapter = discoveryAdapter

	r.publishingMode = infrapublishing.NewModeManager(discoveryAdapter, r.logger, nil)
	if _, _, err := r.publishingMode.CheckModeTransition(); err != nil {
		return err
	}
	if err := r.publishingMode.Start(ctx); err != nil {
		return err
	}

	publishingMetrics := v2.Global().Publishing
	externalURL := r.config.Server.ExternalURL
	r.publisherFactory = infrapublishing.NewPublisherFactory(
		infrapublishing.NewAlertFormatter(externalURL),
		r.logger,
		publishingMetrics,
		externalURL,
	)

	queueConfig := infrapublishing.DefaultPublishingQueueConfig()
	queueConfig.WorkerCount = r.config.Publishing.Queue.WorkerCount
	queueConfig.HighPriorityQueueSize = r.config.Publishing.Queue.HighPriorityQueueSize
	queueConfig.MediumPriorityQueueSize = r.config.Publishing.Queue.MediumPriorityQueueSize
	queueConfig.LowPriorityQueueSize = r.config.Publishing.Queue.LowPriorityQueueSize
	queueConfig.MaxRetries = r.config.Publishing.Queue.MaxRetries
	queueConfig.RetryInterval = r.config.Publishing.Queue.RetryInterval
	queueConfig.Metrics = publishingMetrics

	r.publishingQueue = infrapublishing.NewPublishingQueue(
		r.publisherFactory,
		nil,
		infrapublishing.NewLRUJobTrackingStore(r.config.Publishing.Queue.JobTrackingCapacity),
		queueConfig,
		r.publishingMode,
		r.logger,
	)
	r.publishingQueue.Start()

	coordinatorConfig := infrapublishing.DefaultCoordinatorConfig()
	coordinatorConfig.MaxConcurrent = r.config.Publishing.Queue.MaxConcurrent
	r.publishingCoordinator = infrapublishing.NewPublishingCoordinator(
		r.publishingQueue,
		discoveryAdapter,
		r.publishingMode,
		coordinatorConfig,
		r.logger,
	)

	if r.config.Publishing.Refresh.Enabled {
		refreshConfig := businesspublishing.DefaultRefreshConfig()
		refreshConfig.Interval = r.config.Publishing.Refresh.Interval
		refreshConfig.MaxRetries = r.config.Publishing.Refresh.MaxRetries
		refreshConfig.BaseBackoff = r.config.Publishing.Refresh.BaseBackoff
		refreshConfig.MaxBackoff = r.config.Publishing.Refresh.MaxBackoff
		refreshConfig.RateLimitPer = r.config.Publishing.Refresh.RateLimitPer
		refreshConfig.RefreshTimeout = r.config.Publishing.Refresh.Timeout
		refreshConfig.WarmupPeriod = r.config.Publishing.Refresh.WarmupPeriod

		refreshManager, err := businesspublishing.NewRefreshManager(
			discovery,
			refreshConfig,
			r.logger,
			prometheus.NewRegistry(),
		)
		if err != nil {
			return err
		}
		if err := refreshManager.Start(); err != nil {
			return err
		}
		r.publishingRefresh = refreshManager
	}

	if r.config.Publishing.Health.Enabled {
		healthConfig := businesspublishing.DefaultHealthConfig()
		healthConfig.CheckInterval = r.config.Publishing.Health.CheckInterval
		healthConfig.HTTPTimeout = r.config.Publishing.Health.HTTPTimeout
		healthConfig.WarmupDelay = r.config.Publishing.Health.WarmupDelay
		healthConfig.FailureThreshold = r.config.Publishing.Health.FailureThreshold
		healthConfig.DegradedThreshold = r.config.Publishing.Health.DegradedThreshold
		healthConfig.MaxConcurrentChecks = r.config.Publishing.Health.MaxConcurrentChecks
		healthConfig.MaxIdleConns = r.config.Publishing.Health.MaxIdleConns
		healthConfig.TLSSkipVerify = r.config.Publishing.Health.TLSSkipVerify
		healthConfig.FollowRedirects = r.config.Publishing.Health.FollowRedirects
		healthConfig.MaxRedirects = r.config.Publishing.Health.MaxRedirects

		healthMonitor, err := businesspublishing.NewHealthMonitor(
			discovery,
			healthConfig,
			r.logger,
			publishingMetrics,
		)
		if err != nil {
			return err
		}
		if err := healthMonitor.Start(); err != nil {
			return err
		}
		r.publishingHealth = healthMonitor
	}

	r.publishingMetricsCollector = businesspublishing.NewPublishingMetricsCollector()
	r.publishingMetricsCollector.RegisterCollector(businesspublishing.NewDiscoveryMetricsCollector(discovery))
	r.publishingMetricsCollector.RegisterCollector(businesspublishing.NewQueueMetricsCollector(r.publishingQueue))
	r.publishingMetricsCollector.RegisterCollector(businesspublishing.NewModeMetricsCollector(r.publishingMode))
	if r.publishingRefresh != nil {
		r.publishingMetricsCollector.RegisterCollector(businesspublishing.NewRefreshMetricsCollector(r.publishingRefresh))
	}
	if r.publishingHealth != nil {
		r.publishingMetricsCollector.RegisterCollector(businesspublishing.NewHealthMetricsCollector(r.publishingHealth))
	}

	publisher, err := NewApplicationPublishingAdapter(r.publishingCoordinator, r.logger)
	if err != nil {
		return err
	}
	r.publisher = publisher

	r.logger.Info("Publishing runtime initialized",
		"namespace", resolvePublishingNamespace(r.config),
		"targets", len(discovery.ListTargets()),
		"mode", r.publishingMode.GetCurrentMode().String(),
	)

	return nil
}

func (r *ServiceRegistry) shutdownPublishing() {
	if r.publishingRefresh != nil {
		timeout := r.config.Publishing.Queue.StopTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		if err := r.publishingRefresh.Stop(timeout); err != nil {
			r.logger.Warn("Publishing refresh shutdown failed", "error", err)
		}
		r.publishingRefresh = nil
	}

	if r.publishingHealth != nil {
		timeout := r.config.Publishing.Queue.StopTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		if err := r.publishingHealth.Stop(timeout); err != nil {
			r.logger.Warn("Publishing health monitor shutdown failed", "error", err)
		}
		r.publishingHealth = nil
	}

	if r.publishingMode != nil {
		if err := r.publishingMode.Stop(); err != nil {
			r.logger.Warn("Publishing mode manager shutdown failed", "error", err)
		}
		r.publishingMode = nil
	}

	if r.publishingQueue != nil {
		timeout := r.config.Publishing.Queue.StopTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		if err := r.publishingQueue.Stop(timeout); err != nil {
			r.logger.Warn("Publishing queue shutdown failed", "error", err)
		}
		r.publishingQueue = nil
	}

	if r.publisherFactory != nil {
		r.publisherFactory.Shutdown()
		r.publisherFactory = nil
	}

	if r.k8sClient != nil {
		if err := r.k8sClient.Close(); err != nil {
			r.logger.Warn("Publishing k8s client shutdown failed", "error", err)
		}
		r.k8sClient = nil
	}

	r.publishingCoordinator = nil
	r.publishingDiscoveryAdapter = nil
	r.publishingDiscovery = nil
	r.publishingMetricsCollector = nil
}

func resolvePublishingNamespace(cfg *appconfig.Config) string {
	if cfg != nil {
		if namespace := strings.TrimSpace(cfg.Publishing.Discovery.Namespace); namespace != "" {
			return namespace
		}
	}

	for _, key := range []string{"POD_NAMESPACE", "K8S_NAMESPACE", "NAMESPACE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}

	if data, err := os.ReadFile(serviceAccountNamespacePath); err == nil {
		if namespace := strings.TrimSpace(string(data)); namespace != "" {
			return namespace
		}
	}

	return "default"
}
