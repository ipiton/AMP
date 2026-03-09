package application

import (
	"context"
	"fmt"
	"log/slog"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	dbmigrations "github.com/ipiton/AMP/internal/database"
	"github.com/ipiton/AMP/internal/database/postgres"
	infrastructure "github.com/ipiton/AMP/internal/infrastructure"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/ipiton/AMP/pkg/metrics"
)

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

	// Core Services
	alertProcessor    *services.AlertProcessor
	classificationSvc services.ClassificationService
	deduplicationSvc  services.DeduplicationService
	filterEngine      services.FilterEngine
	publisher         services.Publisher

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

	// State
	initialized     bool
	degradedReasons []string
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

	// Step 1: Initialize Infrastructure
	if err := r.initializeInfrastructure(ctx); err != nil {
		return fmt.Errorf("infrastructure initialization failed: %w", err)
	}

	// Step 2: Initialize Core Services
	if err := r.initializeCoreServices(ctx); err != nil {
		return fmt.Errorf("core services initialization failed: %w", err)
	}

	// Step 3: Initialize Business Services
	if err := r.initializeBusinessServices(ctx); err != nil {
		return fmt.Errorf("business services initialization failed: %w", err)
	}

	// Step 4: Initialize Alert Processor after publisher wiring is ready
	if err := r.initializeAlertProcessor(ctx); err != nil {
		return fmt.Errorf("alert processor initialization failed: %w", err)
	}

	r.initialized = true
	r.logger.Info("✅ Service registry initialized successfully")
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
	r.logger.Info("✅ Business Metrics initialized")

	// Initialize Memory Stores (compatibility mode)
	r.alertStore = memory.NewAlertStore()
	r.silenceStore = memory.NewSilenceStore()
	r.logger.Info("✅ Memory stores initialized (compatibility mode)")

	// Initialize Database based on profile
	if err := r.initializeDatabase(ctx); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	// Initialize Storage (required for active bootstrap path)
	if err := r.initializeStorage(ctx); err != nil {
		return fmt.Errorf("storage initialization failed: %w", err)
	}

	// Initialize Cache (Redis or Memory based on profile)
	if err := r.initializeCache(ctx); err != nil {
		r.logger.Error("Cache initialization failed", "error", err)
		// Continue without cache (graceful degradation)
	}

	r.logger.Info("✅ Infrastructure services initialized")
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
	r.logger.Info("✅ PostgreSQL connected successfully")

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

	r.logger.Info("✅ Storage backend initialized",
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
	r.logger.Info("✅ Redis cache initialized", "addr", cacheConfig.Addr, "db", cacheConfig.DB)
	_ = ctx
	return nil
}

// initializeCoreServices initializes core business logic services.
func (r *ServiceRegistry) initializeCoreServices(ctx context.Context) error {
	r.logger.Info("Initializing core services...")

	// Initialize Filter Engine
	r.filterEngine = services.NewSimpleFilterEngine(r.logger)
	r.logger.Info("✅ Filter Engine initialized")

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

	r.logger.Info("✅ Core services initialized")
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
	r.logger.Info("✅ Deduplication Service initialized")
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
	r.logger.Info("✅ Classification Service initialized",
		"provider", llmConfig.Provider,
		"model", llmConfig.Model,
	)
	_ = ctx
	return nil
}

// initializeAlertProcessor initializes the alert processor.
func (r *ServiceRegistry) initializeAlertProcessor(ctx context.Context) error {
	r.logger.Info("Initializing Alert Processor...")

	config := services.AlertProcessorConfig{
		FilterEngine:    r.filterEngine,
		LLMClient:       r.classificationSvc,
		Publisher:       r.publisher,
		Deduplication:   r.deduplicationSvc,
		BusinessMetrics: r.metrics,
		Logger:          r.logger,
		Metrics:         nil, // TODO: MetricsManager
	}

	processor, err := services.NewAlertProcessor(config)
	if err != nil {
		return fmt.Errorf("failed to create alert processor: %w", err)
	}

	r.alertProcessor = processor
	r.logger.Info("✅ Alert Processor initialized")
	return nil
}

// initializeBusinessServices initializes business logic services.
func (r *ServiceRegistry) initializeBusinessServices(ctx context.Context) error {
	r.logger.Info("Initializing business services...")

	r.initializePublishing(ctx)

	r.logger.Info("✅ Business services initialized")
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
			r.logger.Info("✅ Database disconnected")
		}
	}

	r.initialized = false
	r.logger.Info("✅ All services shut down")
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
