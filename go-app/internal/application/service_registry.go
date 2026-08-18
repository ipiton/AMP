package application

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	handlers "github.com/ipiton/AMP/internal/application/handlers"
	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	businessrouting "github.com/ipiton/AMP/internal/business/routing"
	businesssilencing "github.com/ipiton/AMP/internal/business/silencing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	coreinv "github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/core/services"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
	dbmigrations "github.com/ipiton/AMP/internal/database"
	"github.com/ipiton/AMP/internal/database/postgres"
	infrastructure "github.com/ipiton/AMP/internal/infrastructure"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	inhibitionpkg "github.com/ipiton/AMP/internal/infrastructure/inhibition"
	investigationinfra "github.com/ipiton/AMP/internal/infrastructure/investigation"
	invtools "github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	investigationrepo "github.com/ipiton/AMP/internal/infrastructure/repository"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // BusinessMetrics has no pkg/metrics/v2 equivalent yet; migration tracked separately
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

	// State
	startTime         time.Time
	reloadCoordinator *appconfig.ReloadCoordinator
	initialized       bool
	degradedReasons   []string
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

	// Step 2.7: Initialize grouping subsystem (non-fatal — graceful
	// degradation). Task 2.2, alertmanager-parity. Disabled by default
	// (grouping.enabled=false) and a clean skip without a route: tree.
	if err := r.initializeGrouping(ctx); err != nil {
		r.logger.Warn("Grouping subsystem initialization failed, continuing without alert grouping",
			"error", err)
		r.addDegradedReason("grouping subsystem unavailable: %v", err)
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
		r.logger.Warn("silences are memory-only in lite profile and will be lost on restart")
		return nil
	}

	if r.database == nil || r.database.Pool() == nil {
		return fmt.Errorf("postgres pool not available for silence repository")
	}

	r.silenceRepo = infrasilencing.NewPostgresSilenceRepository(r.database.Pool(), r.logger)
	r.logger.Info("Silence repository initialized (DB-first silence writes enabled)")

	if err := r.rehydrateSilenceStore(ctx); err != nil {
		// Non-fatal: writes still reach the database; the read cache starts
		// empty until silences are re-created or the service restarts.
		r.logger.Error("Silence store rehydration failed; silences API starts empty",
			"error", err)
	}
	return nil
}

// rehydrateSilenceStore loads active and pending silences from the persistent
// repository into the in-memory SilenceStore after a restart. The database is
// the source of truth; memory is a read cache for the API. Expired silences
// are intentionally skipped — the memory store recomputes state from
// timestamps on every read, so they would be dead weight.
func (r *ServiceRegistry) rehydrateSilenceStore(ctx context.Context) error {
	if r.silenceRepo == nil || r.silenceStore == nil {
		return nil
	}

	now := time.Now().UTC()
	restored := 0

	const pageSize = 1000
	for offset := 0; ; offset += pageSize {
		list, err := r.silenceRepo.ListSilences(ctx, infrasilencing.SilenceFilter{
			Statuses: []coresilencing.SilenceStatus{
				coresilencing.SilenceStatusActive,
				coresilencing.SilenceStatusPending,
			},
			Limit:   pageSize,
			Offset:  offset,
			OrderBy: "created_at",
		})
		if err != nil {
			return fmt.Errorf("list silences for rehydration: %w", err)
		}
		if len(list) == 0 {
			break
		}

		apiSilences := make([]core.APISilence, 0, len(list))
		for _, silence := range list {
			apiSilences = append(apiSilences, handlers.DomainSilenceToAPI(silence, now))
		}
		if err := r.silenceStore.RestoreFromPersistence(apiSilences, now); err != nil {
			return fmt.Errorf("restore silences into memory store: %w", err)
		}
		restored += len(apiSilences)

		if len(list) < pageSize {
			break
		}
	}

	if restored > 0 {
		r.logger.Info("Silence store rehydrated from persistent storage", "silences", restored)
	}
	return nil
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

	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("silence manager start failed: %w", err)
	}

	r.silenceManager = manager
	r.logger.Info("Silence manager started (GC/stats worker running)")
	return nil
}

// initializeDatabase initializes the database connection.
func (r *ServiceRegistry) initializeDatabase(ctx context.Context) error {
	// Skip database for lite profile (uses SQLite embedded in storage)
	if r.config.Profile == appconfig.ProfileLite {
		r.logger.Info("Skipping PostgreSQL initialization (lite profile uses SQLite)")
		return nil
	}

	r.logger.Info("Initializing PostgreSQL...")

	// Build PostgreSQL config
	dbCfg := postgres.DefaultConfig()
	dbCfg.Host = r.config.Database.Host
	dbCfg.Port = r.config.Database.Port
	dbCfg.Database = r.config.Database.Database
	dbCfg.User = r.config.Database.Username
	dbCfg.Password = r.config.Database.Password
	dbCfg.SSLMode = r.config.Database.SSLMode
	if r.config.Database.MaxConnections > 0 {
		dbCfg.MaxConns = int32(r.config.Database.MaxConnections)
	}
	if r.config.Database.MinConnections > 0 {
		dbCfg.MinConns = int32(r.config.Database.MinConnections)
	}
	if r.config.Database.MaxConnLifetime > 0 {
		dbCfg.MaxConnLifetime = r.config.Database.MaxConnLifetime
	}
	if r.config.Database.MaxConnIdleTime > 0 {
		dbCfg.MaxConnIdleTime = r.config.Database.MaxConnIdleTime
	}
	if r.config.Database.ConnectTimeout > 0 {
		dbCfg.ConnectTimeout = r.config.Database.ConnectTimeout
	}

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

		storageAdapter, err := infrastructure.NewPostgresStorageAdapter(r.database.Pool(), r.logger)
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

	cacheConfig := &infrastructurecache.CacheConfig{
		Addr:            r.config.Redis.Addr,
		Password:        r.config.Redis.Password,
		DB:              r.config.Redis.DB,
		PoolSize:        r.config.Redis.PoolSize,
		MinIdleConns:    r.config.Redis.MinIdleConns,
		DialTimeout:     r.config.Redis.DialTimeout,
		ReadTimeout:     r.config.Redis.ReadTimeout,
		WriteTimeout:    r.config.Redis.WriteTimeout,
		MaxRetries:      r.config.Redis.MaxRetries,
		MinRetryBackoff: r.config.Redis.MinRetryBackoff,
		MaxRetryBackoff: r.config.Redis.MaxRetryBackoff,
	}

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

	llmConfig := llm.DefaultConfig()
	llmConfig.Provider = r.config.LLM.Provider
	llmConfig.BaseURL = r.config.LLM.BaseURL
	llmConfig.APIKey = r.config.LLM.APIKey
	llmConfig.Model = r.config.LLM.Model
	llmConfig.MaxTokens = r.config.LLM.MaxTokens
	llmConfig.Temperature = r.config.LLM.Temperature
	llmConfig.Timeout = r.config.LLM.Timeout
	llmConfig.MaxRetries = r.config.LLM.MaxRetries

	llmClient := llm.NewHTTPLLMClient(llmConfig, r.logger)

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
	// EnableMetrics is forced off (unlike DefaultMatcherOptions()):
	// MatcherMetrics registers via promauto against the default Prometheus
	// registry, which panics on double-registration. initializeRouting can
	// run more than once per process (tests construct a fresh
	// ServiceRegistry per case; a future re-init path could too), so a
	// metrics-enabled matcher is not safe to construct here. Same reasoning
	// as routeTreeEvaluator's EnableMetrics:false — see route_evaluator.go.
	matcherOpts := businessrouting.DefaultMatcherOptions()
	matcherOpts.EnableMetrics = false
	matcher := businessrouting.NewRouteMatcher(nil, matcherOpts)

	r.routeTreeManager = manager
	r.routeEvaluator = newRouteTreeEvaluator(manager, matcher)

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

	// Build the TimerManager first, without a GroupManager reference — see
	// SetGroupManager below for why.
	timerManagerCfg := grouping.TimerManagerConfig{
		Storage: timerStorage,
		Logger:  r.logger,
		Metrics: r.metrics,
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
		timerManagerCfg.ReconciliationGrace = r.config.Grouping.ReconciliationGrace
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
				Client:  redisCache.GetClient(),
				Logger:  r.logger,
				Metrics: r.metrics,
			})
			if err != nil {
				r.logger.Warn("Redis group storage init failed, grouping falls back to in-memory storage", "error", err)
				groupStorage, timerStorage := r.memoryGroupingStorage()
				return groupStorage, timerStorage, fmt.Errorf("redis group storage init failed: %w", err)
			}

			timerStorage, err := grouping.NewRedisTimerStorage(redisCache, r.logger)
			if err != nil {
				r.logger.Warn("Redis timer storage init failed, grouping falls back to in-memory storage", "error", err)
				memGroupStorage, memTimerStorage := r.memoryGroupingStorage()
				return memGroupStorage, memTimerStorage, fmt.Errorf("redis timer storage init failed: %w", err)
			}

			r.logger.Info("Grouping subsystem using Redis storage")
			return groupStorage, timerStorage, nil
		}

		r.logger.Warn("Standard profile without a Redis cache backend, grouping falls back to in-memory storage")
	}

	groupStorage, timerStorage := r.memoryGroupingStorage()
	return groupStorage, timerStorage, nil
}

// newNotifyLog selects the notify-chain's Dedup + cross-replica publish
// claim backend (task 6.1) by deployment profile: Redis (reusing the
// already-initialized cache client, same pattern as newGroupingStorage
// above) for standard, in-memory for lite.
//
// Return contract mirrors newGroupingStorage exactly, for the same reasons:
//
//   - (nil, nil): use the in-memory default (DefaultGroupManagerConfig.
//     NotifyLog left nil — NewDefaultGroupManager fills in notifyDedupLog).
//     Either lite profile, or standard profile without a live
//     *cache.RedisCache — the latter is NOT a new degraded reason, since
//     Step 1's initializeCache already recorded one for the same underlying
//     "no Redis cache at all" situation.
//   - (nil, err): standard profile, cache backend IS a live *cache.RedisCache
//     (so Step 1 saw no failure), but RedisNotifyLog's own Redis check
//     failed anyway. This is nflog-specific and would otherwise be
//     invisible in /health//readiness, so initializeGrouping adds its own
//     degraded reason for it.
func (r *ServiceRegistry) newNotifyLog(ctx context.Context) (grouping.GroupNotifyLog, error) {
	if r.config.Profile == appconfig.ProfileStandard {
		if redisCache, ok := r.cache.(*infrastructurecache.RedisCache); ok {
			notifyLog, err := grouping.NewRedisNotifyLog(ctx, &grouping.RedisNotifyLogConfig{
				Client: redisCache.GetClient(),
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

	return nil, nil
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

	r.investigationRepo = investigationrepo.NewPostgresInvestigationRepository(r.database.Pool(), r.logger)

	llmCfg := llm.DefaultConfig()
	llmCfg.Provider = r.config.LLM.Provider
	llmCfg.BaseURL = r.config.LLM.BaseURL
	llmCfg.APIKey = r.config.LLM.APIKey
	llmCfg.Model = r.config.LLM.Model
	llmCfg.MaxTokens = r.config.LLM.MaxTokens
	llmCfg.Temperature = r.config.LLM.Temperature
	llmCfg.Timeout = r.config.LLM.Timeout
	llmCfg.MaxRetries = r.config.LLM.MaxRetries

	llmClient := llm.NewHTTPLLMClient(llmCfg, r.logger)

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
			r.investigationToolsDB = stdlib.OpenDBFromPool(r.database.Pool())
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
		GroupManager:       groupManager,              // task 2.3: nil unless grouping subsystem initialized (route tree required)
		GroupKeyGenerator:  r.groupKeyGenerator,       // task 2.3: nil unless grouping subsystem initialized
		Logger:             r.logger,
		Metrics:            nil, // TODO: MetricsManager
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

	// Shutdown Grouping subsystem timer manager (task 2.2). Placed alongside
	// the silence manager above: both are background workers independent of
	// the request path, safe to stop before Publishing/Storage/Database
	// teardown below. GroupManager itself owns no goroutines/connections of
	// its own to close — only the TimerManager's timer goroutines need
	// Shutdown.
	if r.groupTimerManager != nil {
		r.logger.Info("Shutting down grouping timer manager...")
		if err := r.groupTimerManager.Shutdown(ctx); err != nil {
			r.logger.Warn("Grouping timer manager stop warning", "error", err)
		}
		r.groupTimerManager = nil
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

func (r *ServiceRegistry) StartTime() time.Time {
	return r.startTime
}

func (r *ServiceRegistry) ReloadCoordinator() *appconfig.ReloadCoordinator {
	return r.reloadCoordinator
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

	result, err := r.reloadCoordinator.ReloadFromFile(ctx, configPath)
	if err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("reload failed: %v", result.Error)
	}

	// Update local config pointer
	r.config = r.reloadCoordinator.GetCurrentConfig()

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
	// route:/receivers: fields change; unlike that (currently-unused)
	// Reloadable-registry path, ServiceRegistry applies live-component
	// updates directly here — same pattern as the inhibition matcher above.
	if err := r.reloadRoutingTree(); err != nil {
		return err
	}

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
