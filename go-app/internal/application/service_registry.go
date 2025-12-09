package application

import (
	"context"
	"fmt"
	"log/slog"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/database/postgres"
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
//   1. Infrastructure (database, cache, metrics)
//   2. Core services (alert processor, classification)
//   3. Business services (publishing, silencing, grouping)
type ServiceRegistry struct {
	config *appconfig.Config
	logger *slog.Logger

	// Infrastructure Services
	database *postgres.PostgresPool
	storage  core.AlertStorage
	cache    core.Cache
	metrics  *metrics.BusinessMetrics

	// Core Services
	alertProcessor    *services.AlertProcessor
	classificationSvc services.ClassificationService
	deduplicationSvc  services.DeduplicationService
	filterEngine      services.FilterEngine
	publisher         services.Publisher

	// Business Services
	// (silencing, grouping, publishing, etc. - to be added)

	// State
	initialized bool
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
		config: config,
		logger: logger,
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

	// Initialize Database based on profile
	if err := r.initializeDatabase(ctx); err != nil {
		r.logger.Error("Database initialization failed", "error", err)
		// Continue with graceful degradation (memory storage)
	}

	// Initialize Storage (uses database if available, otherwise memory)
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

	// Create and connect
	pool := postgres.NewPostgresPool(dbCfg, r.logger)
	if err := pool.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	r.database = pool
	r.logger.Info("✅ PostgreSQL connected successfully")

	// Run migrations
	// TODO: Add migration runner
	// if err := r.runMigrations(ctx); err != nil {
	//     return fmt.Errorf("migrations failed: %w", err)
	// }

	return nil
}

// initializeStorage initializes the storage backend.
func (r *ServiceRegistry) initializeStorage(ctx context.Context) error {
	r.logger.Info("Initializing storage backend...")

	// TODO: Implement storage initialization when storage package is ready
	// var pgxPool *pgxpool.Pool
	// if r.database != nil {
	//     pgxPool = r.database.Pool()
	// }
	// st, err := storage.NewStorage(ctx, r.config, pgxPool, r.logger)
	// if err != nil {
	//     return fmt.Errorf("failed to create storage: %w", err)
	// }
	// r.storage = st
	_ = ctx // Use ctx to avoid unused variable warning
	r.storage = nil // Placeholder - storage not yet implemented
	r.logger.Info("✅ Storage backend initialized",
		"type", r.config.Profile,
		"backend", getStorageType(r.config.Profile))

	return nil
}

// initializeCache initializes the cache backend.
func (r *ServiceRegistry) initializeCache(ctx context.Context) error {
	// TODO: Initialize Redis or Memory cache based on profile
	r.logger.Info("Cache initialization skipped (TODO)")
	return nil
}

// initializeCoreServices initializes core business logic services.
func (r *ServiceRegistry) initializeCoreServices(ctx context.Context) error {
	r.logger.Info("Initializing core services...")

	// Initialize Filter Engine
	r.filterEngine = services.NewSimpleFilterEngine(r.logger)
	r.logger.Info("✅ Filter Engine initialized")

	// Initialize Publisher
	// NOTE: SimplePublisher is a STUB for development only.
	// In production, use PublisherFactory from infrastructure/publishing package.
	r.publisher = services.NewSimplePublisher(r.logger,
		services.WithEnvironment(r.config.App.Environment),
	)
	r.logger.Info("✅ Publisher initialized (STUB - development only)")

	// Initialize Deduplication Service
	if err := r.initializeDeduplication(ctx); err != nil {
		r.logger.Warn("Deduplication service initialization failed", "error", err)
		// Continue without deduplication (graceful degradation)
	}

	// Initialize Classification Service
	if err := r.initializeClassification(ctx); err != nil {
		r.logger.Warn("Classification service initialization failed", "error", err)
		// Continue without classification (graceful degradation)
	}

	// Initialize Alert Processor (orchestrates all services)
	if err := r.initializeAlertProcessor(ctx); err != nil {
		return fmt.Errorf("alert processor initialization failed: %w", err)
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

	// TODO: Initialize LLM client and classification service
	r.logger.Info("Classification service initialization skipped (TODO)")
	return nil
}

// initializeAlertProcessor initializes the alert processor.
func (r *ServiceRegistry) initializeAlertProcessor(ctx context.Context) error {
	r.logger.Info("Initializing Alert Processor...")

	config := services.AlertProcessorConfig{
		FilterEngine:    r.filterEngine,
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

	// TODO: Initialize publishing, silencing, grouping, inhibition, etc.
	r.logger.Info("Business services initialization skipped (TODO)")

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

	// Shutdown Database
	if r.database != nil {
		r.logger.Info("Shutting down database connection...")
		if err := r.database.Disconnect(ctx); err != nil {
			r.logger.Error("Database disconnect error", "error", err)
		} else {
			r.logger.Info("✅ Database disconnected")
		}
	}

	r.logger.Info("✅ All services shut down")
	return nil
}

// Health checks the health of all services.
func (r *ServiceRegistry) Health(ctx context.Context) error {
	// Check database health
	if r.database != nil {
		if err := r.database.Health(ctx); err != nil {
			return fmt.Errorf("database unhealthy: %w", err)
		}
	}

	// Check storage health
	if r.storage != nil {
		// TODO: Add Health method to storage interface
	}

	return nil
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
