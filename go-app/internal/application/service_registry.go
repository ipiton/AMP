package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	handlers "github.com/ipiton/AMP/internal/application/handlers"
	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	businessrouting "github.com/ipiton/AMP/internal/business/routing"
	businesssilencing "github.com/ipiton/AMP/internal/business/silencing"
	"github.com/ipiton/AMP/internal/business/templating"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	coreinv "github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/core/services"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
	dbmigrations "github.com/ipiton/AMP/internal/database"
	"github.com/ipiton/AMP/internal/database/postgres"
	infrastructure "github.com/ipiton/AMP/internal/infrastructure"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/cluster"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	inhibitionpkg "github.com/ipiton/AMP/internal/infrastructure/inhibition"
	investigationinfra "github.com/ipiton/AMP/internal/infrastructure/investigation"
	invtools "github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	"github.com/ipiton/AMP/internal/infrastructure/lock"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	investigationrepo "github.com/ipiton/AMP/internal/infrastructure/repository"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/snapshot"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	pkglogger "github.com/ipiton/AMP/pkg/logger"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // BusinessMetrics has no pkg/metrics/v2 equivalent yet; migration tracked separately
	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/jackc/pgx/v5/stdlib"
)

// alertCacheWithLifecycle extends ActiveAlertCache with lifecycle management (Stop).
// Using a concrete interface here avoids type assertions in Shutdown and ensures that
// Stop() is always called if the field is non-nil.
type alertCacheWithLifecycle interface {
	inhibitionpkg.ActiveAlertCache
	Stop()
}

// ServiceRegistry manages all application services.
//
// This registry follows the Registry pattern to centralize service
// initialization and lifecycle management. It prevents the God Object
// anti-pattern by encapsulating service dependencies.
//
// Responsibilities:
//   - Initialize all services with proper dependencies
//   - Provide access to services for handlers
//   - Manage service lifecycle (start, stop, health checks)
//   - Handle graceful degradation (fallback to memory storage, etc.)
//
// Services are initialized in dependency order:
//  1. Infrastructure (database, cache, metrics)
//  2. Core services (alert processor, classification)
//  3. Business services (publishing, silencing, grouping)
type ServiceRegistry struct {
	config *appconfig.Config
	logger *slog.Logger

	// Infrastructure Services
	database       *postgres.PostgresPool
	storageRuntime storageRuntime
	storage        core.AlertStorage
	cache          infrastructurecache.Cache
	metrics        *metrics.BusinessMetrics

	// Memory Stores (for Alertmanager compatibility mode)
	alertStore   *memory.AlertStore
	silenceStore *memory.SilenceStore

	// Silence persistence (SPLIT-BRAIN-RISK slice 2): DB-first write path for
	// silences in the standard profile. Nil in the lite profile — silences
	// then stay memory-only and are lost on restart.
	silenceRepo infrasilencing.SilenceRepository

	// Silence manager (task 2.1, alertmanager-parity): background GC/stats
	// worker over silenceRepo — periodic expiry of active->expired silences,
	// deletion of old expired rows past retention, and periodic cache
	// resync + stats. This is NOT the read-path API for alert filtering;
	// that stays memory.SilenceStore (silenceStore above), rehydrated at
	// boot and kept current by the HTTP handlers' DB-first writes. Nil when
	// silenceRepo is nil (lite profile or persistence init failure).
	silenceManager businesssilencing.SilenceManager

	// Silence event bus (task 6.3, alertmanager-parity): Redis pub/sub used
	// to invalidate OTHER replicas' memory.SilenceStore when this replica
	// commits a silence write (see internal/infrastructure/silencing/
	// redis_event_bus.go for the "why" — memory.SilenceStore has no other
	// cross-replica sync mechanism at all). Nil in the lite profile or when
	// the standard profile has no live Redis cache backend; in that case
	// silences converge only on restart, same posture as nflog/grouping's
	// Redis-optional fallbacks (newNotifyLog/newGroupingStorage).
	silenceEventBus *infrasilencing.RedisSilenceEventBus

	// Cancels/awaits the background subscribe+periodic-resync goroutines
	// started by initializeSilenceEventSync. Both nil if that step never
	// started them (lite profile, no Redis, or init failure).
	silenceSyncCancel context.CancelFunc
	silenceSyncDone   chan struct{}

	// Silence GC leader election (task 6.4, alertmanager-parity): gates
	// silenceManager's GC worker (expires/deletes silence rows in shared
	// PostgreSQL) to a single replica at a time instead of running it
	// redundantly on every replica. Nil when: lite profile (initializeSilenceManager
	// returns before this is ever wired — coordination is meaningless with
	// one replica), or standard profile without a live Redis cache backend
	// (falls back to the pre-6.4 posture: GC just runs on every replica,
	// same as before this task — see initializeSilenceGCElection's doc
	// comment). LeaderElector() below is the read-only hook task 6.5's
	// status endpoint uses; it treats nil the same as "always leader."
	//
	// Write-once invariant: set at most once, during Initialize (before any
	// concurrent reader — e.g. 6.5's status endpoint — exists), and never
	// reset back to nil afterward, including by Shutdown. This is
	// deliberate, not an oversight: 6.5's status handler calls
	// LeaderElector()/IsLeader() concurrently with the request-serving
	// goroutines, so a Shutdown that nil'd this field out from under a
	// concurrent read would be a data race. Elector.Stop() already leaves
	// IsLeader() reporting false (a real, correct answer — this replica no
	// longer holds leadership), so there's nothing Shutdown needs the field
	// cleared for; it just keeps pointing at a stopped, inert Elector.
	leaderElector lock.Elector

	// Cluster heartbeat (task 6.5, alertmanager-parity): Redis peer
	// registration backing the `cluster` field of /api/v2/status. Nil when:
	// lite profile (no clustering concept — see initializeClusterHeartbeat),
	// or standard profile without a live Redis cache backend. Same
	// write-once posture as leaderElector above and for the identical
	// reason — ClusterStatus() below is read concurrently with Shutdown by
	// request-serving goroutines, so Shutdown must not nil this out from
	// under a concurrent read. HeartbeatRegistry.Stop() already flips
	// IsRegistered() to false, which ClusterStatus() reports as "disabled"
	// — a correct post-shutdown answer — so there is nothing nil-ing the
	// field would buy beyond reintroducing the race task 6.4 already fixed
	// once for leaderElector.
	clusterHeartbeat *cluster.HeartbeatRegistry

	// Core Services
	alertProcessor    *services.AlertProcessor
	classificationSvc services.ClassificationService
	deduplicationSvc  services.DeduplicationService
	filterEngine      services.FilterEngine
	publisher         services.Publisher

	// Inhibition subsystem (TN-130, PARITY-A2)
	inhibitionCache   alertCacheWithLifecycle              // two-tier cache of firing alerts (includes Stop)
	inhibitionMatcher inhibitionpkg.InhibitionMatcher      // rule engine
	inhibitionState   inhibitionpkg.InhibitionStateManager // active inhibition tracking

	// Routing engine (task 1.4, alertmanager-parity). Both nil when
	// cfg.Routing is nil (no `route:` section — lite/legacy mode).
	routeTreeManager *businessrouting.RouteTreeManager // hot-reloadable route tree
	routeEvaluator   services.RouteEvaluator           // wired into AlertProcessorConfig.RouteEvaluator

	// Notification templates (notification-templates epic, slice 1): the
	// upstream-compatible template library — embedded defaults plus every file
	// matched by cfg.Routing.Templates (`templates:`) — behind an atomic swap
	// so a config reload never re-parses a template a sender is executing.
	//
	// NEVER nil after Initialize: initializeTemplating falls back to the
	// embedded defaults if the operator's globs fail, because a receiver that
	// references `slack.default.title` must still render. Nil only before
	// Initialize (and in tests that construct a bare registry), which is why
	// TemplateRegistry() is the accessor rather than the field.
	//
	// Slice 1 wires the lifecycle only — no formatter reads it yet. Slice 2
	// adds the per-integration template fields that turn this into rendered
	// output on the wire.
	templateRegistry *templating.Registry

	// Grouping subsystem (task 2.2, alertmanager-parity): GroupManager (group
	// lifecycle) + TimerManager (group_wait/group_interval/repeat_interval
	// timers). All three nil unless cfg.Grouping.Enabled is true AND a
	// route: tree is configured (grouping.enabled defaults to false).
	// groupKeyGenerator is the SAME instance passed to both
	// DefaultGroupManagerConfig.KeyGenerator (below) and
	// AlertProcessorConfig.GroupKeyGenerator (task 2.3) — a single source of
	// truth for key-generation options/behavior between the two.
	groupManager      *grouping.DefaultGroupManager
	groupTimerManager *grouping.DefaultTimerManager
	groupKeyGenerator *grouping.GroupKeyGenerator

	// groupStorageManager is non-nil only when newGroupingStorage chose the
	// standard profile's Redis-backed GroupStorage (task fu5-cfg item 2,
	// FU-STORAGEMANAGER-FAILBACK): it wraps that RedisGroupStorage with a
	// periodic health probe + automatic runtime failback to an in-memory
	// GroupStorage on Redis loss, and failforward back on recovery — see
	// grouping.StorageManager's package doc for what recovery deliberately
	// does NOT do (no state merge). nil in the lite profile, when Redis
	// init failed (already memory-only, nothing to probe), or when grouping
	// itself is disabled/skipped. Owns a background goroutine; Stop() it in
	// Shutdown before groupTimerManager is torn down below.
	groupStorageManager *grouping.StorageManager

	// memoryNotifyLog holds the in-memory GroupNotifyLog instance selected by
	// newNotifyLog (lite profile, or the standard-profile fallback when
	// Redis is unavailable), type-asserted against grouping.NflogSnapshotter
	// so file-snapshot persistence (wave 6, FU-LITE-FILE-SNAPSHOT) can
	// save/restore its state without reaching into groupManager's private
	// notifyLog field. nil when grouping is disabled (cfg.Grouping.Enabled=
	// false, the default) or the standard profile selected the Redis-backed
	// RedisNotifyLog, which owns its own durability and does not implement
	// NflogSnapshotter.
	memoryNotifyLog grouping.NflogSnapshotter

	// File-snapshot persistence (wave 6, FU-LITE-FILE-SNAPSHOT):
	// snapshotPath is r.config.Storage.SnapshotPath, cached here so Shutdown
	// can do the final write without re-reading config; both nil/empty
	// unless initializeSnapshotting actually engaged (lite profile AND
	// storage.path set). snapshotCancel/snapshotDone are the periodic
	// writer's stop signal, same cancel+wait pattern as
	// silenceSyncCancel/silenceSyncDone above.
	snapshotPath   string
	snapshotCancel context.CancelFunc
	snapshotDone   chan struct{}

	// Business Services
	k8sClient                  k8s.K8sClient
	publishingDiscovery        businesspublishing.TargetDiscoveryManager
	publishingDiscoveryAdapter *DiscoveryAdapter
	publishingRefresh          businesspublishing.RefreshManager
	publishingHealth           businesspublishing.HealthMonitor
	publishingMode             infrapublishing.ModeManager
	publishingQueue            *infrapublishing.PublishingQueue
	publishingCoordinator      *infrapublishing.PublishingCoordinator
	publishingMetricsCollector *businesspublishing.PublishingMetricsCollector
	publisherFactory           *infrapublishing.PublisherFactory

	// Investigation pipeline (PHASE-5A)
	investigationRepo  core.InvestigationRepository
	investigationQueue *investigationinfra.InvestigationQueue
	// investigationToolsDB is a *sql.DB wrapper around the pgx pool used by
	// the database investigation tool. We retain the handle so Shutdown can
	// close it cleanly and avoid a leak if the pool is ever reinitialised.
	investigationToolsDB *sql.DB

	// llmClient is the process's single LLM client, built on demand by
	// sharedLLMClient() for whichever consumer asks first (classification, or
	// the investigation pipeline) and reused by the other — see that method
	// for why there must be exactly one. It is the object LLMReloadable swaps
	// on a config reload. Nil when no consumer was built at all
	// (llm.enabled=false).
	llmClient *llm.HTTPLLMClient

	// State
	startTime         time.Time
	reloadCoordinator *appconfig.ReloadCoordinator
	initialized       bool
	degradedReasons   []string

	// Hot-reload plumbing (INF-A slice 1).
	//
	// restartWarnings collects the W6xx "this change needs a restart"
	// findings raised by the registered Reloadable components; metricsGate is
	// the runtime on/off switch the router wraps /metrics with; logHandler is
	// the process's swappable slog handler, supplied by cmd/server/main.go
	// (which owns logger construction) via SetLogHandler before Initialize.
	restartWarnings *appconfig.RestartWarnings
	metricsGate     *metricsv2.ExpositionGate
	logHandler      *pkglogger.SwappableHandler
}

// NewServiceRegistry creates a new service registry.
func NewServiceRegistry(config *appconfig.Config, logger *slog.Logger) (*ServiceRegistry, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &ServiceRegistry{
		config:          config,
		logger:          logger,
		startTime:       time.Now().UTC(),
		degradedReasons: make([]string, 0, 4),
	}, nil
}

// Initialize initializes all services.
//
// Services are initialized in dependency order to ensure proper setup.
// If a service fails to initialize, graceful degradation is attempted
// (e.g. fallback to memory storage if database unavailable).
func (r *ServiceRegistry) Initialize(ctx context.Context) error {
	if r.initialized {
		return fmt.Errorf("services already initialized")
	}

	r.logger.Info("Initializing service registry...")

	// Initialize Reload Coordinator (TN-152)
	// Constructors are required: bare struct literals leave the underlying
	// go-playground validator nil and /-/reload panics on first use.
	validator := appconfig.NewConfigValidator()
	comparator := appconfig.NewConfigComparator()
	reloader := appconfig.NewConfigReloader(r.logger)
	// storage and lockManager can be nil for basic reload
	configPath := os.Getenv("AMP_CONFIG_FILE")
	if configPath == "" {
		configPath = "config.yaml"
	}
	r.reloadCoordinator = appconfig.NewReloadCoordinator(
		r.config,
		configPath,
		validator,
		comparator,
		reloader,
		nil,
		nil,
		r.logger,
	)

	// Hot-reload plumbing (INF-A slice 1). Both must exist before the router
	// is built (MetricsGate) and before components register (restartWarnings),
	// so they are created here rather than in one of the initialize* steps.
	r.restartWarnings = appconfig.NewRestartWarnings()
	r.metricsGate = metricsv2.NewExpositionGate(r.config.Metrics.Enabled)

	// Step 1: Initialize Infrastructure
	if err := r.initializeInfrastructure(ctx); err != nil {
		return fmt.Errorf("infrastructure initialization failed: %w", err)
	}

	// Step 1.5: Initialize silence manager (GC/stats worker, task 2.1;
	// non-fatal — mirrors initializeInhibition/initializeRouting below).
	// The read path for alert filtering keeps using memory.SilenceStore
	// regardless of whether this succeeds.
	if err := r.initializeSilenceManager(ctx); err != nil {
		r.logger.Warn("Silence manager initialization failed, continuing without background GC/stats worker",
			"error", err)
		r.addDegradedReason("silence manager unavailable: %v", err)
	}

	// Step 1.6: Initialize cross-replica silence cache invalidation (task
	// 6.3; non-fatal — mirrors Step 1.5 above). Its own degraded reason (if
	// any) is added inside initializeSilenceEventSync.
	if err := r.initializeSilenceEventSync(ctx); err != nil {
		r.logger.Warn("Silence event sync initialization failed, continuing without cross-replica invalidation",
			"error", err)
	}

	// Step 1.7: Initialize cluster heartbeat (task 6.5; non-fatal — mirrors
	// Step 1.5/1.6 above). Populates the /api/v2/status `cluster` field;
	// nothing else in AMP depends on it.
	if err := r.initializeClusterHeartbeat(ctx); err != nil {
		r.logger.Warn("Cluster heartbeat initialization failed, /api/v2/status will report cluster.status=disabled",
			"error", err)
		r.addDegradedReason("cluster heartbeat unavailable: %v", err)
	}

	// Step 2: Initialize Core Services
	if err := r.initializeCoreServices(ctx); err != nil {
		return fmt.Errorf("core services initialization failed: %w", err)
	}

	// Step 2.5: Initialize Inhibition subsystem (non-fatal — graceful degradation)
	if err := r.initializeInhibition(ctx); err != nil {
		r.logger.Warn("Inhibition subsystem initialization failed, continuing without inhibition",
			"error", err)
		r.addDegradedReason("inhibition unavailable: %v", err)
	}

	// Step 2.6: Initialize routing engine (non-fatal — graceful degradation)
	if err := r.initializeRouting(ctx); err != nil {
		r.logger.Warn("Routing engine initialization failed, continuing without route-tree evaluation",
			"error", err)
		r.addDegradedReason("routing engine unavailable: %v", err)
	}

	// Step 2.6.1: Load the notification template library (never fatal — falls
	// back to the embedded upstream defaults; see initializeTemplating).
	r.initializeTemplating()

	// Step 2.7: Initialize grouping subsystem (non-fatal — graceful
	// degradation). Task 2.2, alertmanager-parity. Disabled by default
	// (grouping.enabled=false) and a clean skip without a route: tree.
	if err := r.initializeGrouping(ctx); err != nil {
		r.logger.Warn("Grouping subsystem initialization failed, continuing without alert grouping",
			"error", err)
		r.addDegradedReason("grouping subsystem unavailable: %v", err)
	}

	// Step 2.8: Initialize lite-profile file-snapshot persistence (non-fatal
	// — mirrors Step 2.7 above). Task FU-LITE-FILE-SNAPSHOT, alertmanager-
	// parity wave 6. Runs after Step 2.7 so r.memoryNotifyLog (set inside
	// initializeGrouping, when grouping is enabled) is already available to
	// restore into; r.silenceStore has existed since Step 1 regardless.
	if err := r.initializeSnapshotting(ctx); err != nil {
		r.logger.Warn("File-snapshot persistence initialization failed, continuing without restart durability",
			"error", err)
		r.addDegradedReason("file-snapshot persistence unavailable: %v", err)
	}

	// Step 3: Initialize Business Services
	if err := r.initializeBusinessServices(ctx); err != nil {
		return fmt.Errorf("business services initialization failed: %w", err)
	}

	// Step 3.5: Initialize Investigation pipeline (non-fatal — graceful degradation)
	if err := r.initializeInvestigation(); err != nil {
		r.logger.Warn("Investigation pipeline initialization failed, continuing without investigations",
			"error", err)
		r.addDegradedReason("investigation pipeline unavailable: %v", err)
	}

	// Step 4: Initialize Alert Processor after publisher wiring is ready
	if err := r.initializeAlertProcessor(ctx); err != nil {
		return fmt.Errorf("alert processor initialization failed: %w", err)
	}

	// Step 5: register the config.Reloadable components (INF-A slice 1). Last,
	// so every component it wraps (pool, cache, LLM client) already exists —
	// a component wired with a nil dependency reports restart-required
	// forever, which would be a silent downgrade of hot reload.
	r.registerReloadables(reloader)

	r.initialized = true
	r.logger.Info("Service registry initialized successfully")
	return nil
}

// initializeInfrastructure initializes infrastructure services.
//
// Infrastructure services include:
//   - Database (PostgreSQL or SQLite based on profile)
//   - Cache (Redis or Memory based on profile)
//   - Metrics Registry (Prometheus)
func (r *ServiceRegistry) initializeInfrastructure(ctx context.Context) error {
	r.logger.Info("Initializing infrastructure services...")

	// Initialize Metrics first (needed by other services)
	r.metrics = metrics.NewBusinessMetrics()
	r.logger.Info("Business Metrics initialized")

	// Initialize Memory Stores (compatibility mode)
	r.alertStore = memory.NewAlertStore()
	r.silenceStore = memory.NewSilenceStore()
	r.logger.Info("Memory stores initialized (compatibility mode)")

	// Initialize Database based on profile
	if err := r.initializeDatabase(ctx); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	// Initialize Storage (required for active bootstrap path)
	if err := r.initializeStorage(ctx); err != nil {
		return fmt.Errorf("storage initialization failed: %w", err)
	}

	// SPLIT-BRAIN-RISK: repopulate the in-memory alert store from the database
	// so a restart doesn't serve an empty API while dedup silently drops
	// re-fired alerts as duplicates. Non-fatal: boot continues on failure.
	if err := r.rehydrateAlertStore(ctx); err != nil {
		r.logger.Error("Alert store rehydration failed; API starts empty until alerts re-fire",
			"error", err)
	}

	// SPLIT-BRAIN-RISK slice 2: DB-first silence persistence + rehydration of
	// the in-memory silence store. Non-fatal: boot continues on failure, but
	// silences stay memory-only until the next restart.
	if err := r.initializeSilencePersistence(ctx); err != nil {
		r.logger.Error("Silence persistence initialization failed; silences are memory-only and will be lost on restart",
			"error", err)
	}

	// Initialize Cache (Redis or Memory based on profile)
	if err := r.initializeCache(ctx); err != nil {
		r.logger.Error("Cache initialization failed", "error", err)
		// Continue without cache (graceful degradation)
	}

	r.logger.Info("Infrastructure services initialized")
	return nil
}

// rehydrateAlertStore loads firing alerts from persistent storage into the
// in-memory AlertStore after a restart (SPLIT-BRAIN-RISK). The database is the
// source of truth; memory is a read cache for the API.
func (r *ServiceRegistry) rehydrateAlertStore(ctx context.Context) error {
	if r.storage == nil || r.alertStore == nil {
		return nil
	}

	now := time.Now().UTC()
	firing := core.StatusFiring
	restored := 0

	const pageSize = 1000
	for offset := 0; ; offset += pageSize {
		list, err := r.storage.ListAlerts(ctx, &core.AlertFilters{
			Status: &firing,
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("list alerts for rehydration: %w", err)
		}
		if list == nil || len(list.Alerts) == 0 {
			break
		}

		// DTO-FRAGMENTATION item 3: restore takes []*core.Alert directly —
		// no core.Alert → APIAlert → AlertIngestInput string round-trip.
		if err := r.alertStore.RestoreFromPersistence(list.Alerts, now); err != nil {
			return fmt.Errorf("restore alerts into memory store: %w", err)
		}
		restored += len(list.Alerts)

		if len(list.Alerts) < pageSize {
			break
		}
	}

	if restored > 0 {
		r.logger.Info("Alert store rehydrated from persistent storage", "alerts", restored)
	}
	return nil
}

// initializeSilencePersistence wires the persistent silence repository and
// rehydrates the in-memory silence store from it (SPLIT-BRAIN-RISK slice 2).
//
// Standard profile: silences become DB-first — the HTTP handlers commit to
// PostgreSQL before touching memory, and a restart restores active + pending
// silences from the database.
//
// Lite profile: there is no SQLite silence repository yet (the postgres
// implementation requires pgx), so silences stay memory-only. This is logged
// explicitly so operators know restarts drop all silences.
func (r *ServiceRegistry) initializeSilencePersistence(ctx context.Context) error {
	if r.config.Profile == appconfig.ProfileLite {
		// Softened (wave 6, FU-LITE-FILE-SNAPSHOT): silences are still
		// memory-only in the sense that there is no SQLite/Postgres silence
		// repository in the lite profile, but they no longer have to be LOST
		// on restart once storage.path is set — initializeSnapshotting
		// (Step 2.8, runs later in Initialize) reloads them from the file
		// snapshot before the HTTP server starts serving. Only warn when
		// snapshotting is actually off.
		if r.config.Storage.SnapshotPath != "" {
			r.logger.Info("silences are memory-only in lite profile; persisted via file snapshot",
				"storage.path", r.config.Storage.SnapshotPath)
		} else {
			r.logger.Warn("silences are memory-only in lite profile and will be lost on restart (set storage.path to enable file-snapshot persistence)")
		}
		return nil
	}

	if r.database == nil || r.database.Pool() == nil {
		return fmt.Errorf("postgres pool not available for silence repository")
	}

	r.silenceRepo = infrasilencing.NewPostgresSilenceRepository(r.database.SharePool(), r.logger)
	r.logger.Info("Silence repository initialized (DB-first silence writes enabled)")

	if err := r.rehydrateSilenceStore(ctx); err != nil {
		// Non-fatal: writes still reach the database; the read cache starts
		// empty until silences are re-created or the service restarts.
		r.logger.Error("Silence store rehydration failed; silences API starts empty",
			"error", err)
	}
	return nil
}

// rehydrateSilenceStore loads active, pending, and (per the F3 fix below)
// expired silences from the persistent repository into the in-memory
// SilenceStore after a restart. The database is the source of truth; memory
// is a read cache for the API — including expired silences keeps
// GET /api/v2/silences (status.state == "expired") correct immediately after
// a restart, not just until the next resync evicts them again. Row removal
// stays owned exclusively by the GC retention worker, which naturally bounds
// how many expired rows exist to fetch.
func (r *ServiceRegistry) rehydrateSilenceStore(ctx context.Context) error {
	if r.silenceRepo == nil || r.silenceStore == nil {
		return nil
	}

	apiSilences, err := r.fetchSilencesForReadCache(ctx)
	if err != nil {
		return fmt.Errorf("list silences for rehydration: %w", err)
	}

	now := time.Now().UTC()
	if err := r.silenceStore.RestoreFromPersistence(apiSilences, now); err != nil {
		return fmt.Errorf("restore silences into memory store: %w", err)
	}

	if len(apiSilences) > 0 {
		r.logger.Info("Silence store rehydrated from persistent storage", "silences", len(apiSilences))
	}
	return nil
}

// fetchSilencesForReadCache pages through the persistent repository for
// every active, pending, AND expired silence, converted to the API DTO
// shape memory.SilenceStore consumes. Shared by rehydrateSilenceStore
// (boot-time, additive into an empty store) and resyncSilenceStore (task
// 6.3, full resync into a possibly-non-empty store via SilenceStore.Rebuild).
//
// Expired silences are included (alertmanager-parity amtool audit F3):
// memory.SilenceStore's own read path (List/Get) already recomputes state
// from StartsAt/EndsAt live on every call rather than trusting a cached
// state label, so holding expired rows here is correct, not "dead weight" —
// excluding them would make GET /api/v2/silences flip an already-fixed
// expired silence back to invisible on the next periodic resync (every 5m,
// see runSilencePeriodicResync) or on any other replica's next reconnect
// resync, undoing handleSilenceDelete's expire-in-place fix. Row removal
// remains exclusively the GC retention worker's job (ExpireSilences with
// deleteExpired=true), which bounds how many expired rows this ever fetches.
func (r *ServiceRegistry) fetchSilencesForReadCache(ctx context.Context) ([]core.APISilence, error) {
	now := time.Now().UTC()
	var apiSilences []core.APISilence

	const pageSize = 1000
	for offset := 0; ; offset += pageSize {
		list, err := r.silenceRepo.ListSilences(ctx, infrasilencing.SilenceFilter{
			Statuses: []coresilencing.SilenceStatus{
				coresilencing.SilenceStatusActive,
				coresilencing.SilenceStatusPending,
				coresilencing.SilenceStatusExpired,
			},
			Limit:   pageSize,
			Offset:  offset,
			OrderBy: "created_at",
		})
		if err != nil {
			return nil, fmt.Errorf("list silences: %w", err)
		}
		if len(list) == 0 {
			break
		}

		for _, silence := range list {
			apiSilences = append(apiSilences, handlers.DomainSilenceToAPI(silence, now))
		}

		if len(list) < pageSize {
			break
		}
	}

	return apiSilences, nil
}

// initializeSilenceManager wires the READY DefaultSilenceManager as a
// background GC/stats worker over the persistent silence repository (task
// 2.1, alertmanager-parity). It does NOT become the read-path API for alert
// filtering — that stays memory.SilenceStore (rehydrateSilenceStore above),
// which the alert processor and HTTP handlers already read/write today.
// This manager only owns:
//   - GC worker: expires active->expired silences past EndsAt, then deletes
//     expired rows past the retention window.
//   - Sync worker: periodically rebuilds its own internal cache from the
//     repository (used by GetStats/IsAlertSilenced if this manager's own
//     API is used elsewhere later — not by the current read path).
//
// Skip conditions (clean skip, no degradation of the existing read path):
//   - Lite profile: no persistent silence repository exists yet, matching
//     initializeSilencePersistence's lite-profile skip above.
//   - Nil silenceRepo: persistence init failed or was skipped for another
//     reason. NewDefaultSilenceManager panics on a nil repository, so this
//     guard is required, not just an optimization.
func (r *ServiceRegistry) initializeSilenceManager(ctx context.Context) error {
	if r.config.Profile == appconfig.ProfileLite {
		r.logger.Info("Silence manager disabled in lite profile (no persistent silence repository)")
		return nil
	}
	if r.silenceRepo == nil {
		r.logger.Warn("Silence manager disabled: no silence repository available")
		return nil
	}

	r.logger.Info("Initializing silence manager (GC/stats worker)...")

	matcher := coresilencing.NewSilenceMatcher()
	manager := businesssilencing.NewDefaultSilenceManager(r.silenceRepo, matcher, r.logger, nil)

	// Task 6.4: must run before manager.Start() below — EnableLeaderGatedGC
	// (called inside here, only when a real elector can be wired) has to
	// take effect before Start() decides whether to auto-start GC.
	r.initializeSilenceGCElection(manager)

	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("silence manager start failed: %w", err)
	}

	if r.leaderElector != nil {
		if err := r.leaderElector.Start(ctx); err != nil {
			r.logger.Warn("Silence GC leader election failed to start, GC worker will not run on this replica until the next restart",
				"error", err)
			r.addDegradedReason("silence GC leader election unavailable: %v", err)
		}
	}

	r.silenceManager = manager
	r.logger.Info("Silence manager started (GC/stats worker running)")
	return nil
}

// initializeSilenceGCElection wires leader election (task 6.4,
// alertmanager-parity) so the silence manager's GC worker — which mutates
// shared PostgreSQL rows (expires active->expired silences past EndsAt,
// then deletes expired rows past the retention window) — runs on exactly
// one replica at a time instead of redundantly on all of them. This is a
// throughput/DB-load optimization, not a correctness fix: ExpireSilences is
// idempotent (a WHERE ... LIMIT N update/delete that N replicas racing on
// would just repeat with shrinking effect, same as today pre-6.4), so
// skipping election entirely is always a safe fallback, never a
// correctness risk — only a wasted-work one.
//
// Sets manager.EnableLeaderGatedGC() + r.leaderElector only when a real
// Redis backend is available; otherwise leaves both untouched, which means
// manager.Start() (called by the caller right after this) auto-starts GC
// unconditionally — i.e. exactly the pre-6.4 behavior on every replica.
// Mirrors newSilenceEventBus's own posture: no live *cache.RedisCache is
// not a NEW degraded reason (Step 1's initializeCache already recorded one
// for the same underlying issue if that's why there's no Redis backend at
// all).
func (r *ServiceRegistry) initializeSilenceGCElection(manager *businesssilencing.DefaultSilenceManager) {
	redisCache, ok := r.cache.(*infrastructurecache.RedisCache)
	if !ok {
		r.logger.Warn("No Redis cache backend, silence GC leader election disabled (GC worker runs on every replica, as before task 6.4)")
		return
	}

	manager.EnableLeaderGatedGC()
	r.leaderElector = lock.NewLeaderElector(
		redisCache.ShareClient(),
		"amp:leader:silence-gc",
		nil, // defaults: 20s TTL / ~7s renew / 2s retry — see lock.Default* constants
		r.logger,
		manager.StartGC,
		manager.StopGC,
	)
}

// LeaderElector returns the leader-election hook wired for the silence GC
// worker (task 6.4, alertmanager-parity), or nil when it was never wired
// (lite profile, or standard profile without a live Redis backend — both
// leave GC running unconditionally on every replica, so there is no
// leadership concept to report). Exposed for task 6.5's status endpoint
// (peers/leader info). Use IsLeader() below for the common "am I the
// leader, treating unwired as trivially yes" case.
func (r *ServiceRegistry) LeaderElector() lock.Elector {
	return r.leaderElector
}

// IsLeader reports whether this replica currently owns the silence GC
// worker's leadership slot. Nil leaderElector (GC unconditionally runs on
// every replica — see initializeSilenceGCElection) reports true: there is
// no coordination, so every replica is trivially "the leader" for this
// worker's purposes.
func (r *ServiceRegistry) IsLeader() bool {
	if r.leaderElector == nil {
		return true
	}
	return r.leaderElector.IsLeader()
}

// initializeClusterHeartbeat wires the Redis peer heartbeat (task 6.5,
// alertmanager-parity Phase 6) that backs /api/v2/status's `cluster`
// field. Lite profile has no clustering concept at all — a single replica
// with no coordination backend, matching every other Phase 6 feature's
// lite-profile posture (leader election's AlwaysLeader, silence event
// sync's nil bus). Standard profile without a live *cache.RedisCache backend
// (Step 1's initializeCache already recorded its own degraded reason for
// that) also leaves r.clusterHeartbeat nil — ClusterStatus() then reports
// "disabled", the safe fallback, not a new failure mode.
//
// Registration happens synchronously inside HeartbeatRegistry.Start, so a
// non-nil error here means the FIRST heartbeat SET itself failed (e.g.
// Redis briefly unreachable at boot) — logged and degraded by the caller,
// same as every other non-fatal Initialize step.
func (r *ServiceRegistry) initializeClusterHeartbeat(ctx context.Context) error {
	if r.config.Profile != appconfig.ProfileStandard {
		return nil
	}

	redisCache, ok := r.cache.(*infrastructurecache.RedisCache)
	if !ok {
		r.logger.Warn("No Redis cache backend, cluster heartbeat disabled (status endpoint reports cluster.status=disabled)")
		return nil
	}

	address := r.config.Server.ExternalURL
	registry := cluster.NewHeartbeatRegistry(redisCache.ShareClient(), "", address, 0, 0, r.logger)
	if err := registry.Start(ctx); err != nil {
		return fmt.Errorf("cluster heartbeat start failed: %w", err)
	}

	r.clusterHeartbeat = registry
	r.logger.Info("Cluster heartbeat registered", "self_id", registry.SelfID())
	return nil
}

// ClusterStatus returns the `cluster` field for /api/v2/status (task 6.5).
// "disabled" (Name/Peers omitted, matching the pre-6.5 stub's wire shape)
// when clusterHeartbeat was never wired or this replica's own registration
// isn't currently active — lite profile, standard profile without a live
// Redis backend, or after Shutdown. "ready" with this replica's self ID
// and the full live peer list otherwise. A Peers() lookup failure (Redis
// hiccup after a successful registration) degrades to "ready" with just
// this replica's own name and an empty peer list rather than failing the
// whole /api/v2/status response — the same fail-open posture Phase 6's
// other Redis-backed features (nflog, silence sync) already use.
func (r *ServiceRegistry) ClusterStatus(ctx context.Context) handlers.ClusterStatus {
	if r.clusterHeartbeat == nil || !r.clusterHeartbeat.IsRegistered() {
		return handlers.ClusterStatus{Status: "disabled"}
	}

	selfID := r.clusterHeartbeat.SelfID()
	peers, err := r.clusterHeartbeat.Peers(ctx)
	if err != nil {
		r.logger.Warn("cluster status: peers listing failed, reporting self only", "error", err)
		return handlers.ClusterStatus{Status: "ready", Name: selfID}
	}

	clusterPeers := make([]handlers.ClusterPeer, 0, len(peers))
	for _, p := range peers {
		clusterPeers = append(clusterPeers, handlers.ClusterPeer{Name: p.Name, Address: p.Address})
	}

	return handlers.ClusterStatus{Status: "ready", Name: selfID, Peers: clusterPeers}
}

// newSilenceEventBus selects the cross-replica silence cache invalidation
// backend (task 6.3) by deployment profile: Redis (reusing the
// already-initialized cache client, same pattern as newGroupingStorage/
// newNotifyLog) for standard, nothing for lite.
//
// Return contract mirrors newNotifyLog exactly, for the same reasons:
//   - (nil, nil): use no cross-replica sync at all — silence writes stay
//     local until the next restart. Either lite profile, or standard
//     profile without a live *cache.RedisCache — the latter is NOT a new
//     degraded reason, since Step 1's initializeCache already recorded one.
//   - (nil, err): standard profile, cache backend IS a live *cache.RedisCache
//     (so Step 1 saw no failure), but the event bus's own Redis check failed
//     anyway. This is silence-sync-specific and would otherwise be invisible
//     in /health//readiness, so initializeSilenceEventSync adds its own
//     degraded reason for it.
func (r *ServiceRegistry) newSilenceEventBus(ctx context.Context) (*infrasilencing.RedisSilenceEventBus, error) {
	if r.config.Profile == appconfig.ProfileStandard {
		if redisCache, ok := r.cache.(*infrastructurecache.RedisCache); ok {
			bus, err := infrasilencing.NewRedisSilenceEventBus(ctx, &infrasilencing.SilenceEventBusConfig{
				Client: redisCache.ShareClient(),
				Logger: r.logger,
			})
			if err != nil {
				return nil, fmt.Errorf("redis silence event bus init failed: %w", err)
			}

			r.logger.Info("Silence sync using Redis pub/sub (cross-replica cache invalidation)")
			return bus, nil
		}

		r.logger.Warn("Standard profile without a Redis cache backend, cross-replica silence sync disabled")
	}

	return nil, nil
}

// initializeSilenceEventSync wires the cross-replica silence cache
// invalidation subscriber (task 6.3): a background goroutine that applies
// SilenceEvents published by ANY replica (including this one) to this
// replica's memory.SilenceStore, plus a periodic full-resync fallback. See
// internal/infrastructure/silencing/redis_event_bus.go for why this exists —
// memory.SilenceStore otherwise has no cross-replica sync mechanism.
//
// Skip conditions (clean skip, no degradation of the existing single-replica
// behavior):
//   - No silence repository/store (lite profile, or persistence init
//     failed): nothing to sync against.
//   - No live Redis cache backend: logged by newSilenceEventBus, not
//     repeated here as a degraded reason (mirrors newNotifyLog's own
//     posture in initializeGrouping).
func (r *ServiceRegistry) initializeSilenceEventSync(ctx context.Context) error {
	if r.silenceRepo == nil || r.silenceStore == nil {
		return nil
	}

	bus, err := r.newSilenceEventBus(ctx)
	if err != nil {
		r.logger.Warn("Redis silence event bus init failed, cross-replica silence sync disabled (converges only on restart)", "error", err)
		r.addDegradedReason("silence sync degraded: Redis init failed, cross-replica silence invalidation disabled (converges only on restart): %v", err)
		return nil
	}
	if bus == nil {
		return nil
	}

	r.silenceEventBus = bus

	syncCtx, cancel := context.WithCancel(context.Background())
	r.silenceSyncCancel = cancel
	r.silenceSyncDone = make(chan struct{})

	go r.runSilenceEventSync(syncCtx)

	r.logger.Info("Silence event subscriber started")
	return nil
}

// runSilenceEventSync runs the subscribe loop and the periodic fallback
// resync concurrently until ctx is cancelled, then signals silenceSyncDone
// once both have actually stopped touching silenceStore/silenceRepo — this
// is what Shutdown waits on before those fields are torn down.
func (r *ServiceRegistry) runSilenceEventSync(ctx context.Context) {
	defer close(r.silenceSyncDone)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		r.runSilenceSubscribeLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		r.runSilencePeriodicResync(ctx)
	}()

	wg.Wait()
}

// runSilenceSubscribeLoop owns the RedisSilenceEventBus.Subscribe session:
// it resubscribes with a fixed backoff whenever Subscribe returns a non-nil
// error (a dropped/failed Redis connection), which naturally triggers
// resyncSilenceStore again via onResync on every successful (re)subscribe —
// see RedisSilenceEventBus.Subscribe's doc comment for why a full resync,
// not just catching up on the missed messages, is the correct recovery.
func (r *ServiceRegistry) runSilenceSubscribeLoop(ctx context.Context) {
	retryDelay := r.silenceSubscribeRetryBackoff()

	for {
		err := r.silenceEventBus.Subscribe(ctx, r.resyncSilenceStore, r.applySilenceEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.logger.Warn("Silence event subscription lost, retrying", "error", err, "retry_in", retryDelay)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

// runSilencePeriodicResync is a backstop independent of pub/sub health: even
// with a fully healthy subscription, a Publish call on the WRITING replica
// can fail without surfacing as an HTTP error (publishSilenceEvent in
// internal/application/handlers/silences.go is deliberately best-effort, to
// match persistSilenceDBFirst's existing "cache failure must not fail the
// request" posture). Without this backstop, that single silence would never
// converge on other replicas until they happen to restart. The interval is
// deliberately the same order of magnitude as the silence GC worker's
// default (5m, see DefaultSilenceManagerConfig) — frequent enough that a
// silently-dropped publish is a minor, bounded staleness window rather than
// a permanent one, without adding a steady background load comparable to
// the pub/sub path itself.
func (r *ServiceRegistry) runSilencePeriodicResync(ctx context.Context) {
	fallbackInterval := r.silencePeriodicResyncInterval()

	ticker := time.NewTicker(fallbackInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.resyncSilenceStore(ctx)
		}
	}
}

// resyncSilenceStore performs a full resync of memory.SilenceStore from the
// persistent repository (task 6.3): the pub/sub (re)connect handler and the
// periodic fallback backstop both call this directly. Unlike
// rehydrateSilenceStore's boot-time RestoreFromPersistence (additive into a
// store known to be empty), this uses SilenceStore.Rebuild so entries
// deleted elsewhere while this replica's view was stale — subscription
// down, or simply between fallback ticks — are actually evicted, not just
// left behind. fetchSilencesForReadCache includes expired silences (F3), so
// a resync converges an expired-in-place silence the same way it converges
// everything else, instead of wiping it back out of the cache.
func (r *ServiceRegistry) resyncSilenceStore(ctx context.Context) {
	apiSilences, err := r.fetchSilencesForReadCache(ctx)
	if err != nil {
		r.logger.Warn("Silence store resync failed; cache may be stale until the next resync or event", "error", err)
		return
	}

	now := time.Now().UTC()
	if err := r.silenceStore.Rebuild(apiSilences, now); err != nil {
		r.logger.Warn("Silence store rebuild failed during resync", "error", err)
		return
	}

	r.logger.Info("Silence store resynced (full)", "silences", len(apiSilences))
}

// applySilenceEvent mirrors a single SilenceEvent into memory.SilenceStore
// (task 6.3). The event carries only {id, op} — it always re-fetches the
// current row from silenceRepo rather than trusting a payload that may have
// raced with a subsequent write, so the database stays the single source of
// truth (see redis_event_bus.go's package doc for the full rationale).
//
// F3 fix: an Upsert event whose fetched row is already "expired" (either
// because it naturally elapsed, or because it was forced there by
// handleSilenceDelete's ExpireSilence call) is mirrored in AS EXPIRED, not
// evicted. memory.SilenceStore's read path (List/Get) recomputes state from
// StartsAt/EndsAt live on every call, so caching an expired row is correct,
// not "dead weight" — evicting it here would silently undo the expire-in-
// place fix on every replica except the one that issued the DELETE.
func (r *ServiceRegistry) applySilenceEvent(ctx context.Context, event infrasilencing.SilenceEvent) {
	if event.ID == "" {
		return
	}

	if event.Op == infrasilencing.SilenceEventDelete {
		r.silenceStore.Delete(event.ID)
		return
	}

	silence, err := r.silenceRepo.GetSilenceByID(ctx, event.ID)
	if err != nil {
		if errors.Is(err, infrasilencing.ErrSilenceNotFound) || errors.Is(err, infrasilencing.ErrInvalidUUID) {
			// Deleted (or never valid) by the time we got around to fetching
			// it — converge by evicting any local copy.
			r.silenceStore.Delete(event.ID)
			return
		}
		r.logger.Warn("Silence event apply: fetch by ID failed, cache entry may be stale until next resync",
			"silence_id", event.ID, "error", err)
		return
	}

	now := time.Now().UTC()
	api := handlers.DomainSilenceToAPI(silence, now)
	if _, err := r.silenceStore.UpsertFromAPI(api, now); err != nil {
		r.logger.Warn("Silence event apply: memory upsert failed", "silence_id", event.ID, "error", err)
	}
}

// initializeDatabase initializes the database connection.
func (r *ServiceRegistry) initializeDatabase(ctx context.Context) error {
	// Skip database for lite profile (uses SQLite embedded in storage)
	if r.config.Profile == appconfig.ProfileLite {
		r.logger.Info("Skipping PostgreSQL initialization (lite profile uses SQLite)")
		return nil
	}

	r.logger.Info("Initializing PostgreSQL...")

	// Build PostgreSQL config. Shared with DatabaseReloadable so a hot reload
	// produces the exact same pool config from the same YAML.
	dbCfg := appconfig.PostgresConfigFrom(r.config.Database)

	// Create and connect
	pool := postgres.NewPostgresPool(dbCfg, r.logger)
	if err := pool.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	r.database = pool
	r.logger.Info("PostgreSQL connected successfully")

	// Run migrations
	if err := dbmigrations.RunMigrations(ctx, pool, r.logger); err != nil {
		return fmt.Errorf("migrations failed: %w", err)
	}

	return nil
}

// initializeStorage initializes the storage backend.
func (r *ServiceRegistry) initializeStorage(ctx context.Context) error {
	r.logger.Info("Initializing storage backend...")

	switch r.config.Profile {
	case appconfig.ProfileLite:
		sqliteConfig := &infrastructure.Config{
			Driver:          "sqlite",
			Logger:          r.logger,
			SQLiteFile:      r.config.Storage.FilesystemPath,
			MaxOpenConns:    r.config.Database.MaxConnections,
			MaxIdleConns:    r.config.Database.MinConnections,
			ConnMaxLifetime: r.config.Database.MaxConnLifetime,
			ConnMaxIdleTime: r.config.Database.MaxConnIdleTime,
		}

		sqliteDB, err := infrastructure.NewSQLiteDatabase(sqliteConfig)
		if err != nil {
			return fmt.Errorf("failed to create sqlite storage: %w", err)
		}
		if err := sqliteDB.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect sqlite storage: %w", err)
		}
		if err := sqliteDB.MigrateUp(ctx); err != nil {
			return fmt.Errorf("failed to migrate sqlite storage: %w", err)
		}

		r.storageRuntime = sqliteDB
		r.storage = sqliteDB

	case appconfig.ProfileStandard:
		if r.database == nil || r.database.Pool() == nil {
			return fmt.Errorf("postgres database is not initialized")
		}

		storageAdapter, err := infrastructure.NewPostgresStorageAdapter(r.database.SharePool(), r.logger)
		if err != nil {
			return fmt.Errorf("failed to create postgres storage adapter: %w", err)
		}

		r.storageRuntime = storageAdapter
		r.storage = storageAdapter

	default:
		return fmt.Errorf("unsupported deployment profile: %q", r.config.Profile)
	}

	r.logger.Info("Storage backend initialized",
		"type", r.config.Profile,
		"backend", getStorageType(r.config.Profile),
	)

	return nil
}

// initializeCache initializes the cache backend.
func (r *ServiceRegistry) initializeCache(ctx context.Context) error {
	r.logger.Info("Initializing cache backend...")

	// Shared with RedisReloadable so a hot reload produces the exact same
	// client config from the same YAML.
	cacheConfig := appconfig.CacheConfigFrom(r.config.Redis)

	redisCache, err := infrastructurecache.NewRedisCache(cacheConfig, r.logger)
	if err != nil {
		r.logger.Warn("Redis cache unavailable, falling back to in-memory cache",
			"error", err,
			"addr", cacheConfig.Addr,
		)
		r.addDegradedReason("cache backend unavailable: %v", err)
		r.cache = infrastructurecache.NewMemoryCache(r.logger)
		return nil
	}

	r.cache = redisCache
	r.logger.Info("Redis cache initialized", "addr", cacheConfig.Addr, "db", cacheConfig.DB)
	_ = ctx
	return nil
}

// initializeCoreServices initializes core business logic services.
func (r *ServiceRegistry) initializeCoreServices(ctx context.Context) error {
	r.logger.Info("Initializing core services...")

	// Initialize Filter Engine
	r.filterEngine = services.NewSimpleFilterEngine(r.logger)
	r.logger.Info("Filter Engine initialized")

	// Initialize Deduplication Service
	if err := r.initializeDeduplication(ctx); err != nil {
		r.logger.Warn("Deduplication service initialization failed", "error", err)
		r.addDegradedReason("deduplication unavailable: %v", err)
		// Continue without deduplication (graceful degradation)
	}

	// Initialize Classification Service
	if err := r.initializeClassification(ctx); err != nil {
		r.logger.Warn("Classification service initialization failed", "error", err)
		r.addDegradedReason("classification unavailable: %v", err)
		// Continue without classification (graceful degradation)
	}

	r.logger.Info("Core services initialized")
	return nil
}

// initializeDeduplication initializes the deduplication service.
func (r *ServiceRegistry) initializeDeduplication(ctx context.Context) error {
	if r.storage == nil {
		return fmt.Errorf("storage not available")
	}

	r.logger.Info("Initializing Deduplication Service...")

	fingerprintGen := services.NewFingerprintGenerator(&services.FingerprintConfig{
		Algorithm: services.AlgorithmFNV1a,
	})

	dedupConfig := &services.DeduplicationConfig{
		Storage:         r.storage,
		Fingerprint:     fingerprintGen,
		Logger:          r.logger,
		BusinessMetrics: r.metrics,
	}

	svc, err := services.NewDeduplicationService(dedupConfig)
	if err != nil {
		return fmt.Errorf("failed to create deduplication service: %w", err)
	}

	r.deduplicationSvc = svc
	r.logger.Info("Deduplication Service initialized")
	return nil
}

// initializeClassification initializes the classification service.
func (r *ServiceRegistry) initializeClassification(ctx context.Context) error {
	if !r.config.LLM.Enabled {
		r.logger.Info("Classification service disabled (LLM not enabled)")
		return nil
	}

	r.logger.Info("Initializing Classification Service...")

	if r.cache == nil {
		r.logger.Warn("Cache backend unavailable for classification, using in-memory cache fallback")
		r.cache = infrastructurecache.NewMemoryCache(r.logger)
	}

	// ONE client for the whole process (fix-round C1). Classification used to
	// build its own, identically-configured *HTTPLLMClient while only the
	// investigation pipeline's copy was registered for hot reload — so an
	// llm.model/llm.api_key reload reported success while every alert
	// classification, the higher-traffic path, kept the old model and the old
	// credential.
	llmClient := r.sharedLLMClient()
	llmConfig := llmClient.GetConfig()

	classificationConfig := services.DefaultClassificationConfig()
	classificationConfig.EnableLLM = true
	if r.config.LLM.Timeout > 0 {
		classificationConfig.LLMTimeout = r.config.LLM.Timeout
	}

	svc, err := services.NewClassificationService(services.ClassificationServiceConfig{
		LLMClient:       llmClient,
		Cache:           r.cache,
		Storage:         r.storage,
		Config:          classificationConfig,
		Logger:          r.logger,
		BusinessMetrics: r.metrics,
	})
	if err != nil {
		return fmt.Errorf("failed to create classification service: %w", err)
	}

	r.classificationSvc = svc
	r.logger.Info("Classification Service initialized",
		"provider", llmConfig.Provider,
		"model", llmConfig.Model,
	)
	_ = ctx
	return nil
}

// initializeInhibition initializes the inhibition subsystem (TN-130, PARITY-A2).
// Non-fatal: if no rules are configured, the subsystem is skipped (graceful degradation).
func (r *ServiceRegistry) initializeInhibition(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before inhibition init: %w", err)
	}

	rules, err := r.config.Inhibition.ToInhibitionRules()
	if err != nil {
		// Task 5.4 (carried fix): a broken inhibition.config_file is
		// normally already caught at LoadConfig time (fail-fast); this is
		// a defense-in-depth path (e.g. the file changed on disk between
		// LoadConfig and here) reported as an error rather than silently
		// dropping the file-based rules.
		return fmt.Errorf("failed to load inhibition rules: %w", err)
	}
	if len(rules) == 0 {
		r.logger.Warn("No inhibition rules configured, inhibition engine disabled")
		return nil
	}

	r.logger.Info("Initializing inhibition subsystem...", "rules", len(rules))

	alertCache := inhibitionpkg.NewTwoTierAlertCache(r.cache, r.logger)
	stateManager := inhibitionpkg.NewDefaultStateManager(r.cache, r.logger, r.metrics)
	matcher := inhibitionpkg.NewMatcher(alertCache, rules, r.logger)

	r.inhibitionCache = alertCache
	r.inhibitionState = stateManager
	r.inhibitionMatcher = matcher

	r.logger.Info("Inhibition subsystem initialized", "rules", len(rules))
	return nil
}

// initializeRouting builds the Alertmanager-compatible route tree from
// cfg.Routing (task 1.3: `route:`/`receivers:` parsing via routing.Parse())
// and wires a hot-reload-capable RouteEvaluator into the alert processor
// (task 1.4).
//
// Non-fatal — mirrors initializeInhibition/initializeInvestigation above:
//   - Absent route tree (cfg.Routing == nil) is the expected lite/legacy
//     state: routeTreeManager/routeEvaluator stay nil and AlertProcessor
//     skips route evaluation entirely, no error.
//   - A present-but-malformed tree degrades gracefully instead of blocking
//     startup. routing.Parse() (task 1.3) already validated field-level/
//     structural constraints; a TreeBuilder failure here (cycle, dangling
//     receiver reference, duplicate matcher, ...) is a deeper config
//     authoring bug best surfaced via the degraded-reasons/health path
//     rather than a crash loop.
func (r *ServiceRegistry) initializeRouting(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before routing init: %w", err)
	}

	if !r.config.HasRouteTree() {
		r.logger.Info("No route tree configured (route:/receivers: absent), routing engine disabled")
		return nil
	}

	r.logger.Info("Initializing routing engine...", "receivers", len(r.config.Routing.Receivers))

	tree, err := businessrouting.NewTreeBuilder(r.config.Routing, businessrouting.DefaultBuildOptions()).Build()
	if err != nil {
		return fmt.Errorf("route tree build failed: %w", err)
	}

	manager, err := businessrouting.NewRouteTreeManager(tree)
	if err != nil {
		return fmt.Errorf("route tree manager init failed: %w", err)
	}

	// No pre-compiled regex patterns to preload; the matcher compiles and
	// caches them lazily on first use. The matcher is tree-independent
	// (regex cache + options only) so it is safe to keep across reloads —
	// only the manager's tree is swapped by Reload.
	//
	// Metrics are injected rather than left to MatcherOptions'/
	// EvaluatorOptions' own promauto construction: routingMatcherMetricsOnce/
	// routingEvaluatorMetricsOnce (route_evaluator.go) build each metrics
	// instance exactly once per process via sync.OnceValue, so
	// initializeRouting running more than once per process (tests construct
	// a fresh ServiceRegistry per case; a future re-init path could too)
	// reuses the same instances instead of double-registering against the
	// default Prometheus registry.
	matcherOpts := businessrouting.DefaultMatcherOptions()
	matcherOpts.EnableMetrics = true
	matcherOpts.Metrics = routingMatcherMetricsOnce()
	matcher := businessrouting.NewRouteMatcher(nil, matcherOpts)

	r.routeTreeManager = manager
	r.routeEvaluator = newRouteTreeEvaluator(manager, matcher, routingEvaluatorMetricsOnce())

	stats := tree.GetStats()
	r.logger.Info("Routing engine initialized",
		"nodes", stats.NodeCount,
		"depth", stats.MaxDepth,
		"receivers", stats.ReceiverCount)
	return nil
}

// reloadRoutingTree hot-reloads the route tree manager from r.config.Routing
// (task 1.4). Extracted from ReloadConfig so it can be unit-tested directly
// without going through the full file-based reload pipeline.
//
// Unlike the inhibition-matcher hot-reload branch in ReloadConfig (which
// fails open — logs a warning and continues), a route tree reload failure
// is returned to the caller: routing.Parse() already validated the config
// structurally at load time, so a Reload() failure here means something is
// wrong that the operator must see immediately (per task 1.4: "Reload
// errors must propagate").
func (r *ServiceRegistry) reloadRoutingTree() error {
	if r.routeTreeManager == nil {
		if r.config.HasRouteTree() {
			r.logger.Warn("route: section added but routing engine was disabled at startup; restart required to enable routing")
		}
		return nil
	}

	if r.config.Routing == nil {
		r.logger.Warn("route: section removed from config; routing engine keeps the last-known tree until restart")
		return nil
	}

	if err := r.routeTreeManager.Reload(r.config.Routing); err != nil {
		return fmt.Errorf("route tree reload failed: %w", err)
	}
	return nil
}

// initializeTemplating loads the notification template library: the embedded
// upstream defaults plus every file matched by cfg.Routing.Templates
// (`templates:`).
//
// Never returns an error, and never leaves r.templateRegistry nil UNLESS the
// operator turned templating off (publishing.templates.enabled=false) — that is a
// deliberate difference from the other initialize* steps:
//
//   - internal/config.loadRouteConfig ALREADY validated the same globs through
//     the same engine and failed the config load if they were malformed, so by
//     the time this runs a failure means the files changed on disk in between.
//   - Every receiver in the config may reference a default definition
//     (`slack.default.title`, `email.default.subject`, ...). Leaving the
//     registry nil because the operator's OWN file broke would take the
//     shipped defaults down with it, which is strictly worse than ignoring the
//     broken overrides — so a failure degrades to defaults-only and records a
//     degraded reason.
//
// No route: section (lite/legacy mode) is not degradation at all: there are no
// `templates:` globs to load, and the defaults are exactly what an unconfigured
// deployment should render.
func (r *ServiceRegistry) initializeTemplating() {
	// Kill switch (slice-2 review C1). Leaving the registry nil is what makes
	// this a WHOLESALE revert: initializePublishingRuntime only calls
	// SetTemplateRegistry for a non-nil registry, so every publisher keeps the
	// fixed formatter and no `templates:` file or presentation field is rendered
	// anywhere. Read once at startup — flipping it takes a restart, and
	// reloadTemplates deliberately stays a no-op while the registry is nil.
	if !r.config.Publishing.Templates.IsEnabled() {
		r.logger.Info("Notification templating disabled by publishing.templates.enabled=false; notifications render through AMP's fixed formatters (pre-TEMPLATES-EPIC behaviour)")
		return
	}

	var globs []string
	if r.config.Routing != nil {
		globs = r.config.Routing.Templates
	}

	registry, err := templating.NewRegistry(globs, r.templateOptions())
	if err != nil {
		r.logger.Error("Notification template files failed to load; falling back to the built-in defaults",
			"templates", globs, "error", err)
		r.addDegradedReason("notification templates unavailable: %v", err)

		// Defaults-only registry. This cannot fail: the templates are embedded
		// in the binary and parsed by a unit test on every build.
		registry, err = templating.NewRegistry(nil, r.templateOptions())
		if err != nil {
			r.logger.Error("Built-in notification templates failed to load", "error", err)
			return
		}
	}

	r.templateRegistry = registry
	if len(globs) > 0 {
		r.logger.Info("Notification templates loaded", "globs", globs,
			"files", loadedTemplateFiles(registry),
			"definitions", len(registry.Current().DefinitionNames()))
		r.warnUnmatchedTemplateGlobs(registry)
	}
}

// loadedTemplateFiles lists the template files actually parsed, so the
// "templates loaded" log line differs between "your files loaded" and "your glob
// matched nothing" — before this it was identical either way (review I3).
func loadedTemplateFiles(registry *templating.Registry) []string {
	var files []string
	for _, match := range registry.Current().GlobMatches() {
		files = append(files, match.Files...)
	}
	return files
}

// warnUnmatchedTemplateGlobs WARNs for every configured glob that matched no
// files. An empty match stays legal (a ConfigMap may mount seconds later), but
// it is also the ONLY symptom of a wrong path, so it must be visible.
func (r *ServiceRegistry) warnUnmatchedTemplateGlobs(registry *templating.Registry) {
	if unmatched := registry.Current().UnmatchedGlobs(); len(unmatched) > 0 {
		r.logger.Warn("Notification template glob matched no files; the built-in defaults will be used for anything it was meant to override",
			"globs", unmatched,
			"hint", "paths are resolved relative to the config file's directory; a ConfigMap mounted after startup is the benign case")
	}
}

// templateOptions builds the template execution guards from config
// (publishing.templates.*), falling back to templating's own defaults for any
// value the operator did not set (TEMPLATES-EPIC slice 2 / slice-1 review
// Minor 5: the guards used to be compile-time constants).
func (r *ServiceRegistry) templateOptions() templating.Options {
	return templating.Options{
		Timeout:        r.config.Publishing.Templates.RenderTimeout,
		MaxOutputBytes: r.config.Publishing.Templates.MaxOutputBytes,
	}
}

// TemplateRegistry exposes the live notification template library. Returns nil
// before Initialize; callers must handle that (slice 2's formatters treat a nil
// registry as "no templating configured" and use the fixed formatter).
func (r *ServiceRegistry) TemplateRegistry() *templating.Registry {
	return r.templateRegistry
}

// reloadTemplates rebuilds the template library from the new config's
// `templates:` globs and swaps it in atomically.
//
// Failure is reported but NOT fatal, and the previous library stays live
// (Registry.Reload only publishes a template that parsed cleanly) — same
// posture as the inhibition-matcher branch in ReloadConfig, and for the same
// reason: continuing to render with the last-known-good templates beats
// reverting an operator's customisations because of one bad edit.
func (r *ServiceRegistry) reloadTemplates() {
	if r.templateRegistry == nil {
		// Initialize never ran (or ran before this field existed): nothing live
		// to swap, and building it here would skip the degraded-reason
		// bookkeeping initializeTemplating owns.
		return
	}

	var globs []string
	if r.config.Routing != nil {
		globs = r.config.Routing.Templates
	}

	if err := r.templateRegistry.Reload(globs); err != nil {
		r.logger.Error("Notification template reload failed; keeping the previously loaded templates",
			"templates", globs, "error", err)
		return
	}
	r.logger.Info("Notification templates reloaded", "globs", globs,
		"files", loadedTemplateFiles(r.templateRegistry),
		"definitions", len(r.templateRegistry.Current().DefinitionNames()))
	if len(globs) > 0 {
		r.warnUnmatchedTemplateGlobs(r.templateRegistry)
	}
}

// initializeGrouping wires the alert grouping subsystem (task 2.2,
// alertmanager-parity): GroupManager (group lifecycle, storage-backed) +
// TimerManager (group_wait/group_interval/repeat_interval timers), with
// storage selected by deployment profile (Redis for standard, in-memory for
// lite — newGroupingStorage below).
//
// Construction order breaks the GroupManager<->TimerManager cycle via
// SetGroupManager (Task 2.2): the TimerManager is built first without a
// GroupManager reference, then the GroupManager is built with the
// TimerManager injected, then SetGroupManager wires the TimerManager's
// reference back to the GroupManager.
//
// Non-fatal — mirrors initializeInhibition/initializeRouting: any failure
// here degrades to "no grouping" rather than blocking startup. This method
// does NOT touch the alert ingest pipeline — task 2.3 wires that. Today it
// only starts background lifecycle: storage selection, timer restore
// (RestoreTimers, HA recovery after a restart), and — via Shutdown in
// ServiceRegistry.Shutdown — graceful teardown.
//
// Skip conditions (clean skip, no degradation):
//   - cfg.Grouping.Enabled == false (default): subsystem fully disabled.
//   - No route: tree configured (cfg.Routing == nil): BuildGroupingConfig
//     returns ErrGroupingRequiresRouteTree — the grouping package has no
//     config of its own for group_by/group_wait/group_interval/
//     repeat_interval, so there is nothing to build it from.
func (r *ServiceRegistry) initializeGrouping(ctx context.Context) error {
	if !r.config.Grouping.Enabled {
		r.logger.Info("Grouping subsystem disabled (grouping.enabled=false)")
		return nil
	}

	groupingCfg, err := r.config.BuildGroupingConfig()
	if err != nil {
		r.logger.Info("Grouping subsystem disabled: no route tree configured", "error", err)
		return nil
	}

	r.logger.Info("Initializing grouping subsystem...")

	groupStorage, timerStorage, storageErr := r.newGroupingStorage(ctx)
	if storageErr != nil {
		// Redis cache itself is healthy (otherwise Step 1's initializeCache
		// already recorded a degraded reason and newGroupingStorage returns
		// nil here) — this is a grouping-specific storage failure that would
		// otherwise be invisible in /health//readiness: timers won't survive
		// a restart until Redis-backed storage comes back.
		r.addDegradedReason("grouping storage degraded: Redis init failed, using in-memory (no timer persistence across restart): %v", storageErr)
	}

	notifyLog, notifyLogErr := r.newNotifyLog(ctx)
	if notifyLogErr != nil {
		// Same posture as newGroupingStorage's storageErr above: the cache
		// backend is healthy (otherwise this would duplicate Step 1's own
		// degraded reason — see newNotifyLog), but the nflog-specific Redis
		// client failed anyway. Falling back to in-memory means notification
		// dedup no longer works across replicas until Redis recovers.
		r.addDegradedReason("nflog degraded: Redis init failed, using in-memory (no cross-replica notification dedup): %v", notifyLogErr)
	}
	// Wave 6 (FU-LITE-FILE-SNAPSHOT): keep a typed handle on the in-memory
	// nflog instance (when that's what newNotifyLog returned) so
	// initializeSnapshotting/writeSnapshot below can save/restore its state.
	// Deliberately a type assertion, not a profile check: RedisNotifyLog
	// never implements NflogSnapshotter, so this is naturally nil whenever
	// the standard profile picked the Redis-backed implementation.
	if snapshotter, ok := notifyLog.(grouping.NflogSnapshotter); ok {
		r.memoryNotifyLog = snapshotter
	}

	// Notify-fire time budget (task rec fix round 1, review finding C1): the
	// timer-callback deadline and the cross-replica publish-claim TTL are
	// DERIVED from the publishing stack's delivery-confirmation wait, because
	// publishGroupAlerts now blocks on that wait inside a timer callback while
	// holding the claim. Three independent literals silently truncated every
	// fire to the shortest of them; see grouping/notify_budget.go.
	//
	// Read from config even though initializePublishing (which configures the
	// coordinator with the same value) runs LATER — both sides read the same
	// knob, and validateNotifyTimingBudget re-checks the built objects once
	// both exist.
	deliveryConfirmationTimeout := r.config.Publishing.Queue.DeliveryConfirmationTimeout

	// Build the TimerManager first, without a GroupManager reference — see
	// SetGroupManager below for why.
	timerManagerCfg := grouping.TimerManagerConfig{
		Storage:         timerStorage,
		Logger:          r.logger,
		Metrics:         r.metrics,
		CallbackTimeout: grouping.TimerCallbackTimeoutFor(deliveryConfirmationTimeout),
	}

	// Distributed timer reconciliation (task 6.2) only makes sense when
	// timerStorage is genuinely shared across replicas — i.e. actually
	// backed by Redis, not just "standard profile was requested". This
	// type assertion is what makes the standard-profile Redis-init-failure
	// fallback (newGroupingStorage above, returns InMemoryTimerStorage on
	// error) and the lite profile behave identically here: neither shares
	// timer state with any other replica, so scanning for "orphans" left by
	// another replica is meaningless and the loop stays off regardless of
	// cfg.Grouping.ReconciliationInterval.
	if _, redisBacked := timerStorage.(*grouping.RedisTimerStorage); redisBacked {
		timerManagerCfg.ReconciliationInterval = r.config.Grouping.ReconciliationInterval
		timerManagerCfg.ReconciliationGrace = reconciliationGraceFor(r.config.Grouping.ReconciliationGrace, deliveryConfirmationTimeout)
	}

	timerManager, err := grouping.NewDefaultTimerManager(timerManagerCfg)
	if err != nil {
		return fmt.Errorf("timer manager init failed: %w", err)
	}

	// Stored on the registry (r.groupKeyGenerator) so task 2.3's
	// AlertProcessor wiring uses the exact same generator instance/options
	// as the GroupManager below, instead of a second independently-configured
	// one.
	keyGenerator := grouping.NewGroupKeyGenerator()

	// Publisher is left nil here: initializeGrouping runs BEFORE
	// initializePublishing (step 2.7 vs step 3 in Initialize — the publishing
	// stack, and therefore anything implementing GroupNotificationPublisher,
	// doesn't exist yet at this point), so it cannot be supplied at
	// construction time. initializeAlertProcessor (step 4, after
	// initializePublishing) wires it in afterward via SetPublisher once
	// r.publisher exists — see that method's doc comment (task 2.4). Until
	// then, nil is explicitly backwards-compatible: timer callbacks only log,
	// no alerts are sent.
	//
	// InhibitionChecker/SilenceChecker (task 2.4, notify-chain Inhibit/
	// Silence steps) ARE available here: r.inhibitionMatcher (step 2.5) and
	// r.silenceStore (step 1, initializeInfrastructure) both run before
	// grouping init (step 2.7), so — unlike Publisher — no SetX workaround
	// is needed for either.
	var silenceChecker grouping.GroupSilenceChecker
	if r.silenceStore != nil {
		silenceChecker = r.silenceStore
	}

	// TimeIntervalLookup (task 3.2, notify-chain TimeMute step) — available
	// here for the same reason InhibitionChecker/SilenceChecker are:
	// r.routeTreeManager (step 2.6, initializeRouting) runs before grouping
	// init (step 2.7). nil (no route tree configured, or routing engine
	// init failed/degraded) means the chain skips TimeMute entirely — same
	// backwards-compatible posture as every other optional checker here.
	var timeIntervalLookup grouping.GroupTimeIntervalLookup
	if r.routeTreeManager != nil {
		timeIntervalLookup = newRouteTreeTimeIntervalLookup(r.routeTreeManager)
	}

	groupManager, err := grouping.NewDefaultGroupManager(ctx, grouping.DefaultGroupManagerConfig{
		KeyGenerator:       keyGenerator,
		Config:             groupingCfg,
		Storage:            groupStorage,
		TimerManager:       timerManager,
		InhibitionChecker:  r.inhibitionMatcher,
		SilenceChecker:     silenceChecker,
		TimeIntervalLookup: timeIntervalLookup,
		NotifyLog:          notifyLog,
		NotifyLogClaimTTL:  grouping.NotifyLogClaimTTLFor(deliveryConfirmationTimeout),
		Logger:             r.logger,
		Metrics:            r.metrics,
	})
	if err != nil {
		return fmt.Errorf("group manager init failed: %w", err)
	}

	// Break the GroupManager<->TimerManager construction cycle (Task 2.2):
	// inject the now-built GroupManager into the already-built TimerManager.
	if err := timerManager.SetGroupManager(groupManager); err != nil {
		return fmt.Errorf("timer manager group manager injection failed: %w", err)
	}

	// Restore persisted timers after a restart (HA recovery). Harmless no-op
	// for in-memory timer storage (nothing survives process restart there);
	// meaningful for Redis-backed storage in the standard profile.
	restoreCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	restored, missed, err := timerManager.RestoreTimers(restoreCtx)
	cancel()
	if err != nil {
		r.logger.Warn("Timer restoration failed, continuing with no restored timers", "error", err)
	} else {
		r.logger.Info("Timer restoration completed", "restored", restored, "missed", missed)
	}

	r.groupManager = groupManager
	r.groupTimerManager = timerManager
	r.groupKeyGenerator = keyGenerator

	r.logger.Info("Grouping subsystem initialized")
	return nil
}

// reconciliationGraceFor returns the effective grouping.reconciliation_grace
// to wire into the timer manager (wave-4 hygiene item 3, review finding M-a).
//
// configured is whatever config.go decoded grouping.reconciliation_grace to
// — zero when the operator left the key unset (config.go's setDefaults
// deliberately registers no viper default for it, see that call site).
// deliveryConfirmationTimeout is the ACTUAL configured
// publishing.queue.delivery_confirmation_timeout, not a hardcoded default —
// using the real value is what makes this a fix rather than a relocation of
// the same drift: service_registry.go used to read a static "90s" literal
// completely independent of this timeout, so raising the timeout alone (all
// PublishGroupToTargets and the timer callback keep tracking it correctly)
// left reconciliation_grace pointing at a now-too-small default and failed
// startup with a message that named a knob the operator never touched.
//
// An explicit operator value always wins over the derivation —
// validateNotifyTimingBudget still rejects the end result if the combination
// violates the budget invariant, exactly as before.
func reconciliationGraceFor(configured, deliveryConfirmationTimeout time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return grouping.ReconciliationGraceFor(deliveryConfirmationTimeout)
}

// Pre-config-knob defaults (task fu5-cfg item 1) for the silence sync
// intervals below — unchanged from the literals runSilenceSubscribeLoop and
// runSilencePeriodicResync hardcoded before this task.
const (
	defaultSilenceSubscribeRetryBackoff  = 2 * time.Second
	defaultSilencePeriodicResyncInterval = 5 * time.Minute
)

// silenceSubscribeRetryBackoff returns the configured resubscribe backoff,
// falling back to the pre-config-knob default when r.config is nil (unit
// tests that construct *ServiceRegistry directly without going through
// LoadConfig — same posture as reconciliationGraceFor above) or the value is
// left at its zero default.
//
// Restart-only (fix-round Minor #3): this is read once by
// runSilenceSubscribeLoop before its loop starts, so a POST /-/reload that
// changes silencing.subscribe_retry_backoff has no effect until the process
// restarts — validateSilencing does still re-run on reload, so the value is
// re-validated, just not re-applied live.
func (r *ServiceRegistry) silenceSubscribeRetryBackoff() time.Duration {
	if r.config != nil && r.config.Silencing.SubscribeRetryBackoff > 0 {
		return r.config.Silencing.SubscribeRetryBackoff
	}
	return defaultSilenceSubscribeRetryBackoff
}

// silencePeriodicResyncInterval returns the configured periodic fallback
// resync interval, with the same nil/zero fallback posture as
// silenceSubscribeRetryBackoff above.
//
// Restart-only (fix-round Minor #3): read once to build the
// runSilencePeriodicResync ticker, same posture as
// silenceSubscribeRetryBackoff above — a reload changes the validated value
// but not the running ticker's interval until restart.
func (r *ServiceRegistry) silencePeriodicResyncInterval() time.Duration {
	if r.config != nil && r.config.Silencing.PeriodicResyncInterval > 0 {
		return r.config.Silencing.PeriodicResyncInterval
	}
	return defaultSilencePeriodicResyncInterval
}

// newGroupingStorage selects group + timer storage backends for the grouping
// subsystem by deployment profile (task 2.2): Redis (reusing the
// already-initialized cache client) for standard, in-memory for lite.
//
// The standard profile degrades to in-memory storage — rather than failing
// grouping init outright — for two distinct reasons, only one of which is
// already visible elsewhere:
//
//   - Cache backend isn't a live *cache.RedisCache: initializeCache (Step 1)
//     already fell back to infrastructurecache.NewMemoryCache on Redis
//     failure and recorded its own degraded reason there. Returning a nil
//     error here avoids a duplicate report for the same underlying failure.
//   - Redis cache IS healthy, but the grouping-specific RedisGroupStorage or
//     RedisTimerStorage failed to initialize anyway (e.g. ACL/permission
//     issue scoped to those keys). Step 1 has no visibility into this, so it
//     would otherwise vanish silently — the non-nil error return lets
//     initializeGrouping add a degraded reason for /health//readiness.
func (r *ServiceRegistry) newGroupingStorage(ctx context.Context) (grouping.GroupStorage, grouping.TimerStorage, error) {
	if r.config.Profile == appconfig.ProfileStandard {
		if redisCache, ok := r.cache.(*infrastructurecache.RedisCache); ok {
			groupStorage, err := grouping.NewRedisGroupStorage(ctx, &grouping.RedisGroupStorageConfig{
				Client:  redisCache.ShareClient(),
				Logger:  r.logger,
				Metrics: r.metrics,
			})
			if err != nil {
				r.logger.Warn("Redis group storage init failed, grouping falls back to in-memory storage", "error", err)
				groupStorage, timerStorage := r.memoryGroupingStorage()
				// fix-round Minor #1: this return handed back memory storage
				// without ever setting the backend-active gauge, so it read
				// 0 for BOTH labels in exactly the case an operator would
				// dashboard on.
				if r.metrics != nil {
					r.metrics.SetActiveGroupStorageBackend("memory")
				}
				return groupStorage, timerStorage, fmt.Errorf("redis group storage init failed: %w", err)
			}

			timerStorage, err := grouping.NewRedisTimerStorage(redisCache, r.logger)
			if err != nil {
				r.logger.Warn("Redis timer storage init failed, grouping falls back to in-memory storage", "error", err)
				memGroupStorage, memTimerStorage := r.memoryGroupingStorage()
				// fix-round Minor #1: same gauge gap as the Redis
				// group-storage-init-failure branch above.
				if r.metrics != nil {
					r.metrics.SetActiveGroupStorageBackend("memory")
				}
				return memGroupStorage, memTimerStorage, fmt.Errorf("redis timer storage init failed: %w", err)
			}

			// Wrap the Redis group storage with a runtime health probe +
			// automatic failback/failforward (task fu5-cfg item 2,
			// FU-STORAGEMANAGER-FAILBACK): before this, a Redis outage AFTER
			// startup had no detection at all — groupStorage above would be
			// returned as-is and every Store/Load call would just surface the
			// raw Redis error until the process restarted. StorageManager
			// polls groupStorage.Ping and switches to an in-memory fallback
			// on loss, back to Redis on recovery (clean cutover, not a state
			// merge — see grouping.StorageManager's package doc). TimerStorage
			// is NOT wrapped: no equivalent exists yet, and timer liveness
			// already has its own, separate reconciliation mechanism (task
			// 6.2, grouping.TimerManagerConfig.ReconciliationInterval) — that
			// gap is out of this task's minimum-viable scope.
			// Built directly (not via memoryGroupingStorage(), whose "using
			// in-memory storage" log line would be misleading here — Redis
			// IS the primary; this is only the backstop StorageManager
			// falls back to).
			memGroupStorage := grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{
				Logger:  r.logger,
				Metrics: r.metrics,
			})
			r.groupStorageManager = grouping.NewStorageManager(grouping.StorageManagerConfig{
				Primary:  groupStorage,
				Fallback: memGroupStorage,
				Logger:   r.logger,
				Metrics:  r.metrics,
			})

			r.logger.Info("Grouping subsystem using Redis storage with runtime health-probe failback")
			return r.groupStorageManager, timerStorage, nil
		}

		r.logger.Warn("Standard profile without a Redis cache backend, grouping falls back to in-memory storage")
	}

	groupStorage, timerStorage := r.memoryGroupingStorage()
	if r.metrics != nil {
		r.metrics.SetActiveGroupStorageBackend("memory")
	}
	return groupStorage, timerStorage, nil
}

// newNotifyLog selects the notify-chain's Dedup + cross-replica publish
// claim backend (task 6.1) by deployment profile: Redis (reusing the
// already-initialized cache client, same pattern as newGroupingStorage
// above) for standard, in-memory for lite.
//
// Return contract mirrors newGroupingStorage, with one change from wave 6
// (FU-LITE-FILE-SNAPSHOT): the "use the in-memory default" case now
// constructs grouping.NewMemoryNotifyLog() explicitly instead of returning
// (nil, nil) and letting NewDefaultGroupManager default it internally —
// functionally identical (NewDefaultGroupManager's own nil-check would have
// built the exact same underlying type), but this way initializeGrouping's
// caller gets a handle it can type-assert against grouping.NflogSnapshotter
// and hold onto for file-snapshot persistence. Cases:
//
//   - (memory instance, nil): lite profile, or standard profile without a
//     live *cache.RedisCache — the latter is NOT a new degraded reason,
//     since Step 1's initializeCache already recorded one for the same
//     underlying "no Redis cache at all" situation.
//   - (nil, err): standard profile, cache backend IS a live *cache.RedisCache
//     (so Step 1 saw no failure), but RedisNotifyLog's own Redis check
//     failed anyway. This is nflog-specific and would otherwise be
//     invisible in /health//readiness, so initializeGrouping adds its own
//     degraded reason for it. Deliberately still nil (not the memory
//     fallback instance) here: this path is standard-profile-only, which
//     file snapshotting never engages (gated on profile==lite), so there is
//     no snapshot benefit to constructing one, and NewDefaultGroupManager's
//     own nil-check already provides the working fallback.
func (r *ServiceRegistry) newNotifyLog(ctx context.Context) (grouping.GroupNotifyLog, error) {
	if r.config.Profile == appconfig.ProfileStandard {
		if redisCache, ok := r.cache.(*infrastructurecache.RedisCache); ok {
			notifyLog, err := grouping.NewRedisNotifyLog(ctx, &grouping.RedisNotifyLogConfig{
				Client: redisCache.ShareClient(),
				Logger: r.logger,
			})
			if err != nil {
				r.logger.Warn("Redis nflog init failed, notification dedup falls back to in-memory (no cross-replica dedup)", "error", err)
				return nil, fmt.Errorf("redis nflog init failed: %w", err)
			}

			r.logger.Info("Notification log (nflog) using Redis storage (cross-replica dedup)")
			return notifyLog, nil
		}

		r.logger.Warn("Standard profile without a Redis cache backend, nflog falls back to in-memory (no cross-replica dedup)")
	}

	return grouping.NewMemoryNotifyLog(), nil
}

// memoryGroupingStorage builds the in-memory group + timer storage pair used
// by the lite profile and by every standard-profile fallback path above.
func (r *ServiceRegistry) memoryGroupingStorage() (grouping.GroupStorage, grouping.TimerStorage) {
	r.logger.Info("Grouping subsystem using in-memory storage")
	return grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{
		Logger:  r.logger,
		Metrics: r.metrics,
	}), grouping.NewInMemoryTimerStorage(r.logger)
}

// defaultSnapshotInterval is the pre-config-knob fallback used when
// r.config.Storage.SnapshotInterval is left at zero — same posture as
// defaultSilencePeriodicResyncInterval above (a hand-built *ServiceRegistry
// in unit tests that skips LoadConfig). Matches the viper default in
// internal/config/config.go's setDefaults.
const defaultSnapshotInterval = 5 * time.Minute

// initializeSnapshotting wires the lite profile's file-snapshot persistence
// for silences + the notification log (wave 6, FU-LITE-FILE-SNAPSHOT),
// giving a single-binary AMP the restart durability upstream Alertmanager's
// --storage.path already provides. Disabled by default
// (cfg.Storage.SnapshotPath == "") — see that field's doc comment
// (internal/config/config.go) for why upstream's own non-empty default is
// deliberately NOT copied here.
//
// Standard profile: Postgres/Redis already own durability (silences via
// PostgresSilenceRepository, nflog via RedisNotifyLog when Redis is live),
// so file snapshotting must never engage there even if an operator sets
// storage.path anyway — logged and skipped, not an error, the same
// non-fatal posture as every other Step 1.x/2.x initializer.
//
// Runs after initializeGrouping (Step 2.7) so r.memoryNotifyLog — set there,
// only when grouping is enabled — is already populated; r.silenceStore has
// existed since Step 1 (initializeInfrastructure) regardless of grouping.
// Both loading and starting the periodic writer happen here, well before
// cmd/server/main.go calls server.ListenAndServe — the brief's "load at
// startup BEFORE the API starts serving" requirement.
func (r *ServiceRegistry) initializeSnapshotting(ctx context.Context) error {
	path := r.config.Storage.SnapshotPath
	if path == "" {
		return nil
	}
	if r.config.Profile != appconfig.ProfileLite {
		r.logger.Info("storage.path is set but profile is not lite; file snapshotting is not engaged (Postgres/Redis already own durability)",
			"profile", r.config.Profile, "storage.path", path)
		return nil
	}

	r.logger.Info("Initializing lite-profile file-snapshot persistence...", "storage.path", path)
	r.loadSnapshot(path)

	interval := r.config.Storage.SnapshotInterval
	if interval <= 0 {
		interval = defaultSnapshotInterval
	}

	writerCtx, cancel := context.WithCancel(context.Background())
	r.snapshotPath = path
	r.snapshotCancel = cancel
	r.snapshotDone = make(chan struct{})
	go r.runSnapshotWriter(writerCtx, path, interval)

	return nil
}

// loadSnapshot loads path and restores silences/nflog state from it.
// Tolerates a missing file (routine — first boot with snapshotting freshly
// enabled, logged at Info) and a corrupt/unreadable/version-mismatched file
// (logged at Warn) identically: start empty. NEVER crashes on a bad
// snapshot — the brief's explicit requirement, since a botched upgrade or a
// hand-edited file must not turn into a boot failure for what is, at worst,
// a restart-durability regression back to pre-wave-6 behavior.
func (r *ServiceRegistry) loadSnapshot(path string) {
	now := time.Now().UTC()

	data, err := snapshot.Load(path)
	switch {
	case err == nil:
		if r.silenceStore != nil {
			if restoreErr := r.silenceStore.RestoreFromPersistence(data.Silences, now); restoreErr != nil {
				r.logger.Error("File snapshot silences restore failed; silences API starts empty", "error", restoreErr)
			} else if len(data.Silences) > 0 {
				r.logger.Info("Silences restored from file snapshot", "silences", len(data.Silences))
			}
		}
		if r.memoryNotifyLog != nil {
			if loadErr := r.memoryNotifyLog.LoadNflogSnapshot(data.Nflog, now); loadErr != nil {
				r.logger.Warn("File snapshot nflog restore failed; notification dedup starts empty", "error", loadErr)
			} else {
				r.logger.Info("Notification log restored from file snapshot",
					"entries", len(data.Nflog.Entries), "delivered", len(data.Nflog.Delivered))
			}
		}
		r.logger.Info("File snapshot loaded", "storage.path", path, "written_at", data.WrittenAt)
	case errors.Is(err, snapshot.ErrNotExist):
		r.logger.Info("No file snapshot found, starting with empty silences/nflog", "storage.path", path)
	default:
		r.logger.Warn("File snapshot unreadable, starting with empty silences/nflog", "storage.path", path, "error", err)
	}
}

// runSnapshotWriter periodically flushes silences + nflog state to path
// every interval until ctx is cancelled, then signals r.snapshotDone. The
// FINAL write on graceful shutdown is a separate, synchronous call from
// Shutdown (writeSnapshot below) made after this goroutine has already
// stopped — see Shutdown's ordering comment — so there is never a
// concurrent writer racing the shutdown write for the same path.
func (r *ServiceRegistry) runSnapshotWriter(ctx context.Context, path string, interval time.Duration) {
	defer close(r.snapshotDone)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.writeSnapshot(path); err != nil {
				r.logger.Warn("Periodic file snapshot write failed", "storage.path", path, "error", err)
			}
		}
	}
}

// writeSnapshot captures the current silences + nflog state and persists it
// atomically to path (snapshot.Write). Called by both the periodic writer
// and Shutdown's final write.
func (r *ServiceRegistry) writeSnapshot(path string) error {
	now := time.Now().UTC()
	data := snapshot.Data{
		Version:   snapshot.CurrentVersion,
		WrittenAt: now,
	}
	if r.silenceStore != nil {
		data.Silences = r.silenceStore.ExportForPersistence(now)
	}
	if r.memoryNotifyLog != nil {
		data.Nflog = r.memoryNotifyLog.SnapshotNflog()
	}
	return snapshot.Write(path, data)
}

// initializeInvestigation sets up the async investigation pipeline (PHASE-5B).
// Only available in standard profile with a live PostgreSQL pool.
func (r *ServiceRegistry) initializeInvestigation() error {
	if r.config.Profile != appconfig.ProfileStandard {
		r.logger.Info("Skipping investigation pipeline (non-standard profile)")
		return nil
	}
	if r.database == nil || r.database.Pool() == nil {
		return fmt.Errorf("postgres pool not available for investigation pipeline")
	}
	if !r.config.Investigation.Enabled {
		r.logger.Info("Skipping investigation pipeline (investigation.enabled=false)")
		return nil
	}
	if !r.config.LLM.Enabled {
		r.logger.Info("Skipping investigation pipeline (LLM disabled)")
		return nil
	}

	r.logger.Info("Initializing investigation pipeline...")

	r.investigationRepo = investigationrepo.NewPostgresInvestigationRepository(r.database.SharePool(), r.logger)

	// Same shared client as the classification path (fix-round C1): it is the
	// object LLMReloadable swaps, so both consumers must be looking at it.
	llmClient := r.sharedLLMClient()

	qCfg := investigationinfra.DefaultQueueConfig()
	if r.config.Investigation.WorkerCount > 0 {
		qCfg.WorkerCount = r.config.Investigation.WorkerCount
	}
	if r.config.Investigation.QueueSize > 0 {
		qCfg.QueueSize = r.config.Investigation.QueueSize
	}
	if r.config.Investigation.MaxRetries > 0 {
		qCfg.MaxRetries = r.config.Investigation.MaxRetries
	}
	if r.config.Investigation.RetryInterval > 0 {
		qCfg.RetryInterval = r.config.Investigation.RetryInterval
	}
	if r.config.Investigation.LLMTimeout > 0 {
		qCfg.LLMTimeout = r.config.Investigation.LLMTimeout
	}
	qCfg.OnlyFiring = r.config.Investigation.OnlyFiring
	r.investigationQueue = investigationinfra.NewInvestigationQueue(
		r.investigationRepo,
		llmClient,
		qCfg,
		r.logger,
		nil, // use default prometheus registerer
	)

	// Phase 5B: wire agentic loop if AgentMode is enabled.
	if r.config.LLM.AgentMode {
		registry := coreinv.NewToolRegistry()
		toolsCfg := r.config.Investigation.Tools

		if toolsCfg.Prometheus != nil && toolsCfg.Prometheus.Endpoint != "" {
			registry.Register(invtools.NewPrometheusTool(toolsCfg.Prometheus))
			r.logger.Info("Prometheus investigation tool registered", "endpoint", toolsCfg.Prometheus.Endpoint)
		}

		if toolsCfg.Loki != nil && toolsCfg.Loki.Endpoint != "" {
			registry.Register(invtools.NewLokiTool(toolsCfg.Loki))
			r.logger.Info("Loki investigation tool registered", "endpoint", toolsCfg.Loki.Endpoint)
		}

		if toolsCfg.Kubernetes != nil && toolsCfg.Kubernetes.Enabled {
			k8sTool, err := invtools.NewKubernetesToolFromConfig(toolsCfg.Kubernetes.Kubeconfig)
			if err != nil {
				r.logger.Warn("Kubernetes investigation tool init failed, skipping", "error", err)
			} else {
				registry.Register(k8sTool)
				r.logger.Info("Kubernetes investigation tool registered")
			}
		}

		if toolsCfg.Database != nil && toolsCfg.Database.Enabled && r.database != nil && r.database.Pool() != nil {
			// stdlib.OpenDBFromPool returns a fresh *sql.DB that wraps the
			// underlying pgx pool. We keep the handle on the registry so
			// Shutdown can close it deterministically; without this it
			// would leak if the pool is replaced (e.g. on hot-reload).
			r.investigationToolsDB = stdlib.OpenDBFromPool(r.database.SharePool())
			registry.Register(invtools.NewDatabaseTool(r.investigationToolsDB))
			r.logger.Info("Database investigation tool registered")
		}

		agentLoop := coreinv.NewAgentLoop(llmClient, registry, coreinv.DefaultAgentLoopConfig())
		r.investigationQueue.SetAgentLoop(agentLoop)
		r.logger.Info("Agentic investigation loop enabled")
	}

	r.investigationQueue.Start()

	r.logger.Info("Investigation pipeline initialized",
		"workers", qCfg.WorkerCount,
		"queue_size", qCfg.QueueSize,
		"agent_mode", r.config.LLM.AgentMode,
	)
	return nil
}

// initializeAlertProcessor initializes the alert processor.
func (r *ServiceRegistry) initializeAlertProcessor(ctx context.Context) error {
	r.logger.Info("Initializing Alert Processor...")

	// Task 2.4: wire the notify-stage publisher into GroupManager now that
	// r.publisher exists (initializePublishing, step 3, runs AFTER
	// initializeGrouping, step 2.7 — see initializeGrouping's Publisher
	// comment for why it couldn't be supplied at GroupManager construction
	// time). r.publisher is always non-nil by this point (initializePublishing
	// falls back to MetricsOnlyPublisher rather than leaving it nil), and
	// both concrete publisher types implement GroupNotificationPublisher —
	// but this is checked via type assertion, not assumed, so a future
	// publisher type that DOESN'T implement it degrades to "group
	// notifications no-op" (logged) instead of a panic or compile break.
	// Task rec fix round 1 (review findings C1/I3): the publisher wired in
	// just below makes group notifications BLOCK on confirmed delivery, so the
	// wait/callback/claim-TTL triple must be consistent before that goes live.
	// Refuse to start rather than truncate deliveries invisibly.
	if err := r.validateNotifyTimingBudget(); err != nil {
		return err
	}

	if r.groupManager != nil {
		if groupPublisher, ok := r.publisher.(grouping.GroupNotificationPublisher); ok {
			r.groupManager.SetPublisher(groupPublisher)
		} else {
			r.logger.Warn("Publisher does not implement GroupNotificationPublisher; grouped notifications will no-op",
				"publisher_type", fmt.Sprintf("%T", r.publisher))
		}
	}

	// Task 2.3: only wire a non-nil GroupManager when r.groupManager is
	// actually set. Assigning a nil *grouping.DefaultGroupManager straight
	// into the services.GroupManager interface field would produce a
	// non-nil interface wrapping a nil pointer (classic Go typed-nil
	// gotcha) — AlertProcessor's shouldGroup() nil-check on the interface
	// would then see "non-nil" and later panic calling AddAlertToGroup on a
	// nil receiver. r.groupManager/r.groupKeyGenerator are always set or
	// cleared together (initializeGrouping / Shutdown), matching
	// NewAlertProcessor's "both or neither" validation.
	var groupManager services.GroupManager
	if r.groupManager != nil {
		groupManager = r.groupManager
	}

	config := services.AlertProcessorConfig{
		FilterEngine:       r.filterEngine,
		LLMClient:          r.classificationSvc,
		Publisher:          r.publisher,
		Deduplication:      r.deduplicationSvc,
		InvestigationQueue: r.investigationQueue, // PHASE-5A: may be nil (graceful degradation)
		InhibitionMatcher:  r.inhibitionMatcher,
		InhibitionState:    r.inhibitionState,
		InhibitionCache:    r.inhibitionCache,
		BusinessMetrics:    r.metrics,
		RouteEvaluator:     r.routeEvaluator,          // task 1.4: may be nil (lite/legacy mode, no route: section)
		GroupingEnabled:    r.config.Grouping.Enabled, // task 2.3
		// Re-review finding R1: a CONFIGURED route tree must never degrade into
		// an unscoped publish, even when the tree failed to build
		// (initializeRouting is non-fatal) or Evaluate fails for an alert.
		// RouteTreeConfigured therefore comes from the config, NOT from
		// r.routeEvaluator != nil, and DefaultReceiver carries the root route's
		// catch-all receiver as the fallback.
		RouteTreeConfigured:    r.config.HasRouteTree(),
		DefaultReceiver:        rootRouteReceiver(r.config),
		RoutingFallbackMetrics: routingFallbackMetricsOnce(),
		GroupManager:           groupManager,        // task 2.3: nil unless grouping subsystem initialized (route tree required)
		GroupKeyGenerator:      r.groupKeyGenerator, // task 2.3: nil unless grouping subsystem initialized
		Logger:                 r.logger,
		Metrics:                nil, // TODO: MetricsManager
	}

	processor, err := services.NewAlertProcessor(config)
	if err != nil {
		return fmt.Errorf("failed to create alert processor: %w", err)
	}

	r.alertProcessor = processor
	r.logger.Info("Alert Processor initialized")
	return nil
}

// initializeBusinessServices initializes business logic services.
func (r *ServiceRegistry) initializeBusinessServices(ctx context.Context) error {
	r.logger.Info("Initializing business services...")

	r.initializePublishing(ctx)

	r.logger.Info("Business services initialized")
	return nil
}

// Shutdown shuts down all services gracefully.
func (r *ServiceRegistry) Shutdown(ctx context.Context) error {
	r.logger.Info("Shutting down services...")

	// Step 0: file-snapshot final write (wave 6, FU-LITE-FILE-SNAPSHOT) —
	// deliberately FIRST, before any other teardown below touches
	// silenceStore/memoryNotifyLog, so this write captures the freshest
	// possible state. Stop the periodic writer and wait for it to actually
	// exit BEFORE doing the final write, so the two never race for the same
	// path (Write's atomic tmp+rename is safe under concurrent writers
	// regardless — see snapshot_test.go's TestWrite_ConcurrentWritesToSamePath
	// — but serializing them means the LAST write is unambiguously this
	// one).
	if r.snapshotCancel != nil {
		r.snapshotCancel()
		<-r.snapshotDone
		r.snapshotCancel = nil
		r.snapshotDone = nil
	}
	if r.snapshotPath != "" {
		r.logger.Info("Writing final file snapshot before shutdown...", "storage.path", r.snapshotPath)
		if err := r.writeSnapshot(r.snapshotPath); err != nil {
			r.logger.Warn("Final file snapshot write failed", "storage.path", r.snapshotPath, "error", err)
		}
		r.snapshotPath = ""
	}

	// Shutdown in reverse order of initialization

	// Shutdown Alert Processor
	if r.alertProcessor != nil {
		r.logger.Info("Shutting down Alert Processor...")
		// TODO: Add shutdown method
	}

	// Shutdown Investigation Queue (PHASE-5A)
	if r.investigationQueue != nil {
		r.logger.Info("Shutting down investigation queue...")
		if err := r.investigationQueue.Stop(5 * time.Second); err != nil {
			r.logger.Warn("Investigation queue stop warning", "error", err)
		}
	}

	// Close the *sql.DB handle owned by the database investigation tool.
	// This is a stdlib wrapper around the pgx pool; the pool itself is
	// closed below when the postgres pool is disconnected.
	if r.investigationToolsDB != nil {
		if err := r.investigationToolsDB.Close(); err != nil {
			r.logger.Warn("Investigation tools DB close warning", "error", err)
		}
		r.investigationToolsDB = nil
	}

	// Shutdown silence GC leader election (task 6.4) before the silence
	// manager itself: Stop cancels the election loop and, if this replica
	// currently holds leadership, runs its OnLost callback (manager.StopGC)
	// synchronously — that must happen while the manager is still alive,
	// and releases the Redis lock so another replica can take over
	// immediately instead of waiting out the full TTL.
	//
	// Deliberately NOT setting r.leaderElector = nil afterward (see the
	// field's own doc comment): 6.5's status endpoint reads it
	// concurrently with Shutdown, and Elector.Stop() already leaves
	// IsLeader() reporting false, which is the correct answer post-shutdown
	// anyway — nil-ing the field here would only add a data race, not a
	// more correct one.
	if r.leaderElector != nil {
		r.logger.Info("Shutting down silence GC leader election...")
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := r.leaderElector.Stop(stopCtx); err != nil {
			r.logger.Warn("Silence GC leader election stop warning", "error", err)
		}
		cancel()
	}

	// Shutdown cluster heartbeat (task 6.5). Deliberately NOT setting
	// r.clusterHeartbeat = nil afterward — same write-once rationale as
	// leaderElector just above: ClusterStatus() reads it concurrently with
	// Shutdown, and Stop() already leaves IsRegistered() reporting false,
	// which ClusterStatus() correctly reports as "disabled" post-shutdown.
	if r.clusterHeartbeat != nil {
		r.logger.Info("Shutting down cluster heartbeat...")
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := r.clusterHeartbeat.Stop(stopCtx); err != nil {
			r.logger.Warn("Cluster heartbeat stop warning", "error", err)
		}
		cancel()
	}

	// Shutdown Silence manager background workers (task 2.1). Stop before
	// the database connection is torn down below — the manager's GC/sync
	// workers still read/write through silenceRepo during graceful stop.
	if r.silenceManager != nil {
		r.logger.Info("Shutting down silence manager...")
		if err := r.silenceManager.Stop(ctx); err != nil {
			r.logger.Warn("Silence manager stop warning", "error", err)
		}
		r.silenceManager = nil
	}

	// Shutdown silence event subscriber (task 6.3). Cancel and wait for the
	// subscribe+resync goroutines to actually stop before nil-ing out
	// silenceEventBus/silenceRepo/silenceStore below — they read/write all
	// three.
	if r.silenceSyncCancel != nil {
		r.logger.Info("Shutting down silence event subscriber...")
		r.silenceSyncCancel()
		<-r.silenceSyncDone
		r.silenceSyncCancel = nil
		r.silenceSyncDone = nil
	}
	r.silenceEventBus = nil

	// Shutdown Grouping subsystem timer manager (task 2.2). Placed alongside
	// the silence manager above: both are background workers independent of
	// the request path, safe to stop before Publishing/Storage/Database
	// teardown below. GroupManager itself owns no goroutines/connections of
	// its own to close — only the TimerManager's timer goroutines (and, since
	// task fu5-cfg item 2, groupStorageManager's health-probe goroutine
	// below) need Shutdown.
	if r.groupTimerManager != nil {
		r.logger.Info("Shutting down grouping timer manager...")
		if err := r.groupTimerManager.Shutdown(ctx); err != nil {
			r.logger.Warn("Grouping timer manager stop warning", "error", err)
		}
		r.groupTimerManager = nil
	}
	// Stop the group-storage health probe (task fu5-cfg item 2,
	// FU-STORAGEMANAGER-FAILBACK) — non-nil only when newGroupingStorage
	// wrapped a Redis-backed GroupStorage. Stop() is synchronous (closes
	// stopChan, no wait needed) and safe to call even if the probe never
	// switched away from primary.
	if r.groupStorageManager != nil {
		r.logger.Info("Shutting down grouping storage manager health probe...")
		r.groupStorageManager.Stop()
		r.groupStorageManager = nil
	}
	r.groupManager = nil
	r.groupKeyGenerator = nil

	// Shutdown Inhibition cache background worker
	if r.inhibitionCache != nil {
		r.logger.Info("Shutting down inhibition cache...")
		r.inhibitionCache.Stop()
		r.logger.Info("Inhibition cache stopped")
	}

	r.shutdownPublishing()

	// Shutdown Storage runtime before database ownership is torn down
	if r.storageRuntime != nil {
		r.logger.Info("Shutting down storage runtime...")
		if err := r.storageRuntime.Disconnect(ctx); err != nil {
			r.logger.Error("Storage runtime disconnect error", "error", err)
		}
		r.storageRuntime = nil
	}
	r.storage = nil

	// Shutdown Database
	if r.database != nil {
		r.logger.Info("Shutting down database connection...")
		if err := r.database.Disconnect(ctx); err != nil {
			r.logger.Error("Database disconnect error", "error", err)
		} else {
			r.logger.Info("Database disconnected")
		}
	}

	r.initialized = false
	r.logger.Info("All services shut down")
	return nil
}

// Health checks the health of all services.
func (r *ServiceRegistry) Health(ctx context.Context) error {
	return r.Readiness(ctx)
}

// Getters for services (used by handlers)

func (r *ServiceRegistry) AlertProcessor() *services.AlertProcessor {
	return r.alertProcessor
}

func (r *ServiceRegistry) Storage() core.AlertStorage {
	return r.storage
}

func (r *ServiceRegistry) Metrics() *metrics.BusinessMetrics {
	return r.metrics
}

func (r *ServiceRegistry) FilterEngine() services.FilterEngine {
	return r.filterEngine
}

func (r *ServiceRegistry) Publisher() services.Publisher {
	return r.publisher
}

func (r *ServiceRegistry) PublishingMetricsCollector() *businesspublishing.PublishingMetricsCollector {
	return r.publishingMetricsCollector
}

func (r *ServiceRegistry) Config() *appconfig.Config {
	return r.config
}

func (r *ServiceRegistry) Logger() *slog.Logger {
	return r.logger
}

func (r *ServiceRegistry) AlertStore() *memory.AlertStore {
	return r.alertStore
}

func (r *ServiceRegistry) SilenceStore() *memory.SilenceStore {
	return r.silenceStore
}

// SilenceRepository returns the persistent silence repository, or nil when
// running memory-only (lite profile or persistence init failure).
func (r *ServiceRegistry) SilenceRepository() infrasilencing.SilenceRepository {
	return r.silenceRepo
}

// SilenceManager returns the background GC/stats worker manager, or nil when
// running memory-only (lite profile or persistence init failure). It is not
// the read-path API for alert filtering — see memory.SilenceStore / SilenceStore().
func (r *ServiceRegistry) SilenceManager() businesssilencing.SilenceManager {
	return r.silenceManager
}

// SilenceEventPublisher returns the cross-replica silence cache invalidation
// publisher (task 6.3), or nil when running without one (lite profile, or a
// standard-profile deployment without a live Redis cache backend). Explicit
// nil check below: r.silenceEventBus is a concrete *RedisSilenceEventBus
// field, and returning it directly through the interface-typed field/return
// would otherwise produce a non-nil interface wrapping a nil pointer —
// callers' "if publisher != nil" checks (see publishSilenceEvent) depend on
// this being a genuine nil interface.
func (r *ServiceRegistry) SilenceEventPublisher() infrasilencing.SilenceEventPublisher {
	if r.silenceEventBus == nil {
		return nil
	}
	return r.silenceEventBus
}

func (r *ServiceRegistry) StartTime() time.Time {
	return r.startTime
}

func (r *ServiceRegistry) ReloadCoordinator() *appconfig.ReloadCoordinator {
	return r.reloadCoordinator
}

// RouteEvaluator returns the live route-tree evaluator, or nil when running
// without a `route:` section (lite/legacy single-receiver mode). It follows
// config reloads, because initializeRouting wires a hot-reload-capable
// evaluator over routeTreeManager.
//
// Exposed for /api/v2/alerts/groups (final review finding 17), which needs the
// per-alert receiver and group_by that only the route tree can answer.
func (r *ServiceRegistry) RouteEvaluator() services.RouteEvaluator {
	if r.routeEvaluator == nil {
		return nil
	}
	return r.routeEvaluator
}

// InhibitionState returns the inhibition state manager (may be nil if not configured).
func (r *ServiceRegistry) InhibitionState() inhibitionpkg.InhibitionStateManager {
	return r.inhibitionState
}

// InvestigationRepository returns the investigation repository (may be nil if not initialized).
func (r *ServiceRegistry) InvestigationRepository() core.InvestigationRepository {
	return r.investigationRepo
}

func (r *ServiceRegistry) ReloadConfig(ctx context.Context) error {
	if r.reloadCoordinator == nil {
		return fmt.Errorf("reload coordinator not initialized")
	}

	configPath := os.Getenv("AMP_CONFIG_FILE")
	if configPath == "" {
		configPath = "config.yaml"
	}

	// Restart-required findings are NOT cleared here (fix-round I2). Each
	// component re-raises its own warning on every attempt while its live
	// state still differs from the config, and resolves it the moment it does
	// not — so an unrelated reload can no longer erase the fact that an
	// operator's earlier edit is still waiting for a restart.
	previousConfig := r.config

	result, err := r.reloadCoordinator.ReloadFromFile(ctx, configPath)
	if err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("reload failed: %v", result.Error)
	}

	newConfig := r.reloadCoordinator.GetCurrentConfig()

	// Blackhole receiver set FIRST, before the config pointer swap below
	// (fix-round-2 re-review minor 1): a receiver newly declared without
	// integrations must be recognised as a blackhole by the time the new routing
	// can route to it, otherwise alerts landing in that sub-millisecond window
	// take the loud "no targets found" path. Safe in both directions — a
	// receiver with targets never consults this set.
	r.applyKnownReceivers(newConfig)

	// Update local config pointer
	r.config = newConfig

	// PARITY-A2: hot-reload inhibition rules into the live matcher. The alert
	// processor holds the same matcher instance, so an in-place update is the
	// only way to propagate new rules without a restart.
	// Task 5.4 (carried fix): ToInhibitionRules() now returns an error
	// instead of swallowing a broken inhibition.config_file. By this point
	// LoadConfig has already fail-fast validated the same file (Phase 1 of
	// ReloadFromFile, before the atomic config swap above), so an error
	// here would only come from the file changing on disk in that narrow
	// window; report it rather than silently keeping stale rules.
	newRules, rulesErr := r.config.Inhibition.ToInhibitionRules()
	if rulesErr != nil {
		r.logger.Error("Failed to load inhibition rules after config reload; live matcher rules unchanged", "error", rulesErr)
	} else if r.inhibitionMatcher != nil {
		if updater, ok := r.inhibitionMatcher.(interface {
			UpdateRules([]inhibitionpkg.InhibitionRule)
		}); ok {
			updater.UpdateRules(newRules)
		} else {
			r.logger.Warn("Inhibition matcher does not support hot-reload; new inhibit_rules require restart")
		}
	} else if len(newRules) > 0 {
		// Matcher was never initialized (no rules at startup); wiring it into the
		// already-running alert processor needs a restart.
		r.logger.Warn("Inhibition rules added but engine was disabled at startup; restart required to enable inhibition")
	}

	// Task 1.4: hot-reload the route tree. reload_coordinator's
	// identifyAffectedComponents already flags affected["routing"] when
	// route:/receivers: fields change; ServiceRegistry applies these
	// live-component updates directly here rather than as Reloadables — same
	// pattern as the inhibition matcher above.
	//
	// This runs AFTER the coordinator has committed the config and after all
	// five registered components adopted it, so a failure here must undo both
	// (fix-round I4) instead of reporting "reload failed" over a fully-live new
	// config. Folding these appliers into the component registry, so one
	// failure policy covers everything, is FU-RELOAD-UNIFY-APPLIERS.
	if err := r.reloadRoutingTree(); err != nil {
		return r.rollbackPostCommit(ctx, previousConfig, "routing", err)
	}

	// Notification templates: `templates:` globs are part of the same
	// Alertmanager-shaped config section, so an edit to them must take effect on
	// reload rather than needing a restart. Non-fatal by design — see
	// reloadTemplates.
	r.reloadTemplates()

	// FU-RECEIVERS-INTEGRATION: rebuild the config-provisioned publishing
	// targets from the new config and swap them into the discovery view. The
	// routing fingerprint already makes a receivers-only edit reach this
	// point; without this call such an edit would change routing and change
	// nothing about delivery. Unconditional (not gated on a diff) because the
	// rebuild is pure and the swap is atomic — cheaper than tracking which
	// integration fields moved.
	r.applyConfigTargets()

	return nil
}

// Helper functions

func getStorageType(profile appconfig.DeploymentProfile) string {
	switch profile {
	case appconfig.ProfileLite:
		return "SQLite (embedded)"
	case appconfig.ProfileStandard:
		return "PostgreSQL"
	default:
		return "Memory (fallback)"
	}
}
