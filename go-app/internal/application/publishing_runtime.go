package application

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/business/templating"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
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

	// Non-standard (lite) profile has no Kubernetes access, so it used to be
	// metrics-only unconditionally. Since FU-RECEIVERS-INTEGRATION the
	// config's own `receivers:` integrations provision publishing targets
	// without any Secrets, so a lite deployment with receiver integrations CAN
	// deliver — it just runs the publishing stack in config-only mode (no K8s
	// client, no Secret discovery, no periodic refresh).
	configOnly := r.config.Profile != appconfig.ProfileStandard
	if configOnly && !hasConfigProvisionedTargets(r.config) {
		r.publisher = NewMetricsOnlyPublisher("lite_profile", r.logger)
		r.logger.Info("Publishing running in metrics-only mode for non-standard profile",
			"profile", r.config.Profile,
			"reason", "no receivers: integrations configured",
		)
		return
	}

	if err := r.initializePublishingRuntime(ctx, configOnly); err != nil {
		r.logger.Warn("Publishing runtime unavailable, falling back to metrics-only mode", "error", err)
		r.shutdownPublishing()
		r.publisher = NewMetricsOnlyPublisher("publishing_stack_unavailable", r.logger)
		return
	}
}

func (r *ServiceRegistry) initializePublishingRuntime(ctx context.Context, configOnly bool) error {
	var discovery businesspublishing.TargetDiscoveryManager

	if configOnly {
		// No cluster access at all: every target comes from `receivers:`.
		discovery = businesspublishing.NewConfigOnlyTargetDiscoveryManager(r.logger, nil)
	} else {
		k8sConfig := k8s.DefaultK8sClientConfig()
		k8sConfig.Logger = r.logger

		k8sClient, err := k8s.NewK8sClient(k8sConfig)
		if err != nil {
			return err
		}
		r.k8sClient = k8sClient

		discovery, err = businesspublishing.NewTargetDiscoveryManager(
			k8sClient,
			resolvePublishingNamespace(r.config),
			r.config.Publishing.Discovery.LabelSelector,
			r.logger,
			nil,
		)
		if err != nil {
			return err
		}

		if err := discovery.DiscoverTargets(ctx); err != nil {
			r.logger.Warn("Initial publishing target discovery failed, starting with empty cache", "error", err)
		}
	}

	r.publishingDiscovery = discovery

	// FU-RECEIVERS-INTEGRATION: provision targets from the config's
	// `receivers:` integrations and merge them into the same discovery view
	// (R3). MUST happen before the mode manager's first transition check
	// below — otherwise a config-only deployment looks target-less and starts
	// in metrics-only mode.
	r.applyConfigTargets()

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

	// TEMPLATES-EPIC slice 2: hand the notification template library to the
	// publisher factory, which wraps each target's formatter so the receiver's
	// own `title`/`text`/`description`/`message`/`subject` templates render onto
	// the wire. Without this call the factory keeps the fixed formatters, i.e.
	// exactly the pre-epic behaviour — so a failed template load (registry nil,
	// see initializeTemplating) degrades to "no templating", never to "no
	// notifications".
	//
	// This is UNCONDITIONAL for every config-provisioned
	// slack/pagerduty/telegram/email target, and it is meant to be: those targets
	// carry upstream's own defaults (`{{ template "slack.default.title" . }}`
	// etc.), so an untouched upstream config renders UPSTREAM's output — the
	// drop-in promise — which also means a `receivers:` deployment that names no
	// template at all sees its Slack payload change shape (Block Kit → upstream
	// attachment) and its email body switch to `email.default.html`. That is the
	// epic's headline breaking change; publishing.templates.enabled=false is the
	// documented revert (registry stays nil above, so this whole block is
	// skipped).
	//
	// The registry is a stable pointer that swaps its own contents on reload, so
	// this is a one-time wiring: reloadTemplates does not need to re-enter here.
	//
	// externalURL is the SAME value the fixed formatters use
	// (server.external_url) — it is what `{{ .ExternalURL }}` and every default
	// template's alertmanager link render.
	if registry := r.TemplateRegistry(); registry != nil {
		r.publisherFactory.SetTemplateRegistry(registry)

		// Give the abandoned-execution counters a metric consumer (slice-1
		// review I2 left them observable only from a debugger). Pull-based: the
		// funcs are read at scrape time, so no ticker and no staleness.
		if publishingMetrics != nil {
			publishingMetrics.SetTemplateExecutionSource(&v2.TemplateExecutionSource{
				Abandoned: func() float64 { return float64(templating.AbandonedExecutions()) },
				InFlight:  func() float64 { return float64(templating.InFlightAbandonedExecutions()) },
			})
		}

		r.logger.Info("Notification templates wired into publishing",
			"external_url_configured", externalURL != "")
	}

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
	// Task rec fix round 1 (review finding I3): operator-tunable, and the
	// single source for the derived grouping-side budgets — see
	// initializeGrouping and grouping/notify_budget.go.
	coordinatorConfig.DeliveryConfirmationTimeout = r.config.Publishing.Queue.DeliveryConfirmationTimeout
	coordinatorConfig.Metrics = publishingMetrics
	r.publishingCoordinator = infrapublishing.NewPublishingCoordinator(
		r.publishingQueue,
		discoveryAdapter,
		r.publishingMode,
		coordinatorConfig,
		r.logger,
	)

	// Refresh polls the K8s API for Secret changes; in config-only mode there
	// is nothing to poll (config targets are rebuilt on config reload, not on
	// a timer), so the worker would just burn a goroutine.
	if r.config.Publishing.Refresh.Enabled && !configOnly {
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

	// Blackhole support (re-review finding R2): the coordinator must know which
	// receivers the config DECLARES, so a declared receiver with no targets is
	// an intentional drop rather than a "no targets found" error. Must run after
	// the coordinator exists, hence not folded into applyConfigTargets.
	r.applyKnownReceivers(r.config)

	stats := discovery.GetStats()
	r.logger.Info("Publishing runtime initialized",
		"namespace", resolvePublishingNamespace(r.config),
		"targets", len(discovery.ListTargets()),
		"k8s_targets", stats.ValidTargets,
		"config_targets", stats.ConfigTargets,
		"config_only", configOnly,
		"mode", r.publishingMode.GetCurrentMode().String(),
	)

	return nil
}

// applyConfigTargets rebuilds the config-provisioned publishing targets from
// the LIVE config and swaps them into the discovery view
// (FU-RECEIVERS-INTEGRATION slice 1, item 3).
//
// Called at startup and again from ReloadConfig — the reload pipeline already
// fires on receivers-only edits (the routing fingerprint covers
// route:/receivers:/global:), so this is the hook that makes such an edit
// actually change delivery.
//
// The swap inside SetConfigTargets is atomic, so a reload never leaves the
// receiver with zero targets; a rebuild that produces nothing (all
// integrations removed) correctly clears the set.
func (r *ServiceRegistry) applyConfigTargets() {
	if r.publishingDiscovery == nil {
		return
	}

	sink, ok := r.publishingDiscovery.(businesspublishing.ConfigTargetSink)
	if !ok {
		// Only reachable if the discovery manager is swapped for an
		// implementation that cannot accept config targets; say so rather than
		// silently delivering nothing.
		r.logger.Warn("Discovery manager does not accept config-provisioned targets; receivers: integrations will not deliver",
			"manager", fmt.Sprintf("%T", r.publishingDiscovery))
		return
	}

	sink.SetConfigTargets(businesspublishing.BuildConfigTargets(r.config.Routing, r.logger))
}

// applyKnownReceivers pushes the receiver names declared in the LIVE config into
// the publishing coordinator (re-review finding R2).
//
// Called at startup and again from ReloadConfig. On reload it runs FIRST — before
// the config pointer swap, the route-tree reload and applyConfigTargets
// (fix-round-2 re-review minor 1): otherwise an alert routed to a NEWLY declared
// blackhole receiver in that sub-millisecond window still took the loud path
// (207 on ingest, or an error log plus a retry on the next group fire). Pushing
// the new set early is safe in both directions — a receiver that HAS targets
// never reaches the blackhole branch, and a receiver dropped from the config
// simply goes back to being a loud unknown receiver.
//
// Takes the config explicitly rather than reading r.config, so the reload can
// publish the NEW receiver set before r.config itself is swapped.
func (r *ServiceRegistry) applyKnownReceivers(cfg *appconfig.Config) {
	if r.publishingCoordinator == nil {
		return
	}

	var names []string
	if cfg != nil && cfg.Routing != nil {
		names = make([]string, 0, len(cfg.Routing.Receivers))
		for _, receiver := range cfg.Routing.Receivers {
			if receiver != nil && receiver.Name != "" {
				names = append(names, receiver.Name)
			}
		}
	}

	r.publishingCoordinator.SetKnownReceivers(names)
}

// hasConfigProvisionedTargets reports whether the config carries at least one
// receiver integration block, i.e. whether config provisioning can produce any
// publishing target at all. Used to decide whether a lite-profile deployment
// gets a real publishing stack instead of the metrics-only publisher.
func hasConfigProvisionedTargets(cfg *appconfig.Config) bool {
	if cfg == nil || cfg.Routing == nil {
		return false
	}
	for _, receiver := range cfg.Routing.Receivers {
		if receiver != nil && receiver.GetConfigCount() > 0 {
			return true
		}
	}
	return false
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

// validateNotifyTimingBudget asserts, at startup, that the notify-fire time
// budget is internally consistent (task rec fix round 1, review findings C1
// and I3).
//
// Since task rec, DefaultGroupManager.publishGroupAlerts waits for CONFIRMED
// delivery inside a timer callback while holding the cross-replica publish
// claim, which makes four durations from two packages mutually dependent:
//
//	delivery-confirmation wait  <  timer-callback deadline  <  publish-claim TTL
//	                                                        <  reconciliation grace
//
// Each violation has its own silent failure mode:
//
//   - callback deadline <= wait: every delivery is truncated at the callback
//     deadline and the wait's own timeout is unreachable (round-1 finding C1).
//   - claim TTL <= callback deadline: the claim can expire while the fire is
//     still publishing (or doing its post-delivery bookkeeping), reopening the
//     double-publish window HA correctness depends on.
//   - reconciliation grace <= claim TTL: a LIVE fire looks orphaned to
//     reconcileOrphanedTimers, and the adopting replica — correctly blocked
//     from notifying by the claim — still deletes the shared timer record,
//     racing the publisher's continuation SaveTimer and potentially leaving the
//     group with no persisted timer at all (round-2 finding R4).
//
// None of these show up in logs, so this refuses to start instead.
//
// NOT an error, but reported here because it is the same budget: the
// distributed per-group timer lock (grouping.TimerLockTTL, 30s, never renewed)
// is SHORTER than the callback deadline, so a long fire runs on with that lock
// already expired. The publish claim is what prevents a second replica from
// notifying the same group in that window — it went from backstop to
// load-bearing when publishing became blocking.
//
// ServiceRegistry derives all three from publishing.queue.
// delivery_confirmation_timeout, so a violation means either a code change to
// notify_budget.go's helpers or hand-wiring that bypassed them — a bug, not a
// misconfiguration an operator can fix, hence the explicit "internal
// inconsistency" wording. Returns nil when either side is absent (publishing
// disabled, grouping disabled): there is no blocking publish to budget for.
func (r *ServiceRegistry) validateNotifyTimingBudget() error {
	if r.publishingCoordinator == nil || r.groupManager == nil {
		return nil
	}

	wait := r.publishingCoordinator.DeliveryConfirmationTimeout()
	claimTTL := r.groupManager.NotifyLogClaimTTL()

	if claimTTL <= wait {
		return fmt.Errorf(
			"notify timing budget internal inconsistency: nflog publish-claim TTL (%s) must exceed publishing delivery-confirmation timeout (%s), "+
				"otherwise the claim can expire mid-publish and two replicas can notify the same group (see grouping/notify_budget.go)",
			claimTTL, wait)
	}

	if r.groupTimerManager != nil {
		callbackTimeout := r.groupTimerManager.CallbackTimeout()
		if callbackTimeout <= wait {
			return fmt.Errorf(
				"notify timing budget internal inconsistency: timer callback timeout (%s) must exceed publishing delivery-confirmation timeout (%s), "+
					"otherwise every group notification is truncated at the callback deadline and confirmed deliveries go unrecorded (see grouping/notify_budget.go)",
				callbackTimeout, wait)
		}
		if claimTTL <= callbackTimeout {
			// Strict since fix round 2 (review finding R8): the claim must
			// still be held while the post-delivery bookkeeping runs, and that
			// work happens at the very end of the callback budget.
			return fmt.Errorf(
				"notify timing budget internal inconsistency: nflog publish-claim TTL (%s) must exceed the timer callback timeout (%s), "+
					"otherwise a long fire outlives its own claim before its bookkeeping completes (see grouping/notify_budget.go)",
				claimTTL, callbackTimeout)
		}

		// Orphan-adoption grace (review finding R4). Zero means the
		// reconciliation loop is disabled — nothing can be adopted, so there is
		// no window to protect (lite profile, or a non-Redis timer storage).
		if grace := r.groupTimerManager.ReconciliationGrace(); grace > 0 && grace <= claimTTL {
			return fmt.Errorf(
				"notify timing budget inconsistency: grouping.reconciliation_grace (%s) must exceed the nflog publish-claim TTL (%s, derived from "+
					"publishing.queue.delivery_confirmation_timeout=%s), otherwise a notification still being delivered looks orphaned and the adopting "+
					"replica deletes the group's shared timer record while the publisher is still using it — raise grouping.reconciliation_grace to at "+
					"least %s or lower publishing.queue.delivery_confirmation_timeout (see grouping/notify_budget.go, ReconciliationGraceFor)",
				grace, claimTTL, wait, grouping.ReconciliationGraceFor(wait))
		}
	}

	r.logger.Info("Notify timing budget validated",
		"delivery_confirmation_timeout", wait,
		"timer_callback_timeout", callbackTimeoutOf(r.groupTimerManager),
		"nflog_claim_ttl", claimTTL,
		"reconciliation_grace", reconciliationGraceOf(r.groupTimerManager),
		// Deliberately surfaced: shorter than the callback deadline by design,
		// which is why the claim above is load-bearing rather than a backstop.
		"timer_lock_ttl", grouping.TimerLockTTL())

	return nil
}

// callbackTimeoutOf / reconciliationGraceOf keep the log line above readable
// when grouping is wired without a timer manager (timers disabled).
func callbackTimeoutOf(tm *grouping.DefaultTimerManager) time.Duration {
	if tm == nil {
		return 0
	}
	return tm.CallbackTimeout()
}

func reconciliationGraceOf(tm *grouping.DefaultTimerManager) time.Duration {
	if tm == nil {
		return 0
	}
	return tm.ReconciliationGrace()
}

// rootRouteReceiver returns the root route's receiver — upstream Alertmanager's
// catch-all — or "" when there is no route tree (or the tree carries no root
// receiver, which config validation rejects, so only a hand-built config can
// reach that).
//
// Used as the fallback receiver when a configured route tree produces no
// decision for an alert (re-review finding R1); "" makes that case a hard error
// for the alert rather than an unscoped fan-out.
func rootRouteReceiver(cfg *appconfig.Config) string {
	if cfg == nil || cfg.Routing == nil || cfg.Routing.Route == nil {
		return ""
	}
	return cfg.Routing.Route.Receiver
}
