package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ipiton/AMP/cmd/server/handlers"
	"github.com/ipiton/AMP/internal/application"
	"github.com/ipiton/AMP/internal/business/grouping"
	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/business/routing"
	"github.com/ipiton/AMP/internal/business/silencing"
	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	groupinginfra "github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	publishinginfra "github.com/ipiton/AMP/internal/infrastructure/publishing"
	"github.com/ipiton/AMP/internal/infrastructure/repository"
	silencinginfra "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

const (
	appName    = "Alertmanager++"
	appVersion = "1.0.0-preview"
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("🚀 Starting Alertmanager++",
		"version", appVersion,
		"profile", "OSS Core",
	)

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Initialize database
	var pool postgres.Pool
	if cfg.Profile == "lite" || cfg.Storage.Backend == "filesystem" {
		slog.Info("Using SQLite backend (Lite profile)")
		// SQLite initialization would go here
		// For now, skip if not implemented
	} else {
		slog.Info("Initializing PostgreSQL connection pool")
		pool, err = postgres.NewPool(ctx, postgres.Config{
			Host:     cfg.Database.Host,
			Port:     cfg.Database.Port,
			User:     cfg.Database.User,
			Password: cfg.Database.Password,
			Database: cfg.Database.Database,
		})
		if err != nil {
			slog.Error("Failed to initialize database", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		slog.Info("✅ Database connection established")
	}

	// Initialize Redis cache (optional)
	var redisCache cache.Cache
	if cfg.Redis.Addr != "" {
		slog.Info("Initializing Redis cache", "addr", cfg.Redis.Addr)
		redisCache, err = cache.NewRedisCache(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
		if err != nil {
			slog.Warn("Failed to initialize Redis, continuing without cache", "error", err)
		} else {
			slog.Info("✅ Redis cache initialized")
		}
	}

	// Initialize repositories
	alertRepo := repository.NewPostgresHistoryRepository(pool)
	silenceRepo := silencinginfra.NewPostgresSilenceRepository(pool)

	// Initialize core services
	dedupService := services.NewDeduplicationService()
	filterEngine := services.NewSimpleFilterEngine()

	// Initialize business services
	groupManager := grouping.NewAlertGroupManager(
		groupinginfra.NewInMemoryGroupRepository(),
		cfg.Grouping,
	)

	silenceManager := silencing.NewSilenceManager(silenceRepo)
	inhibitionManager := inhibition.NewInhibitionManager(pool)

	routeEvaluator := routing.NewRouteEvaluator()
	
	publisherFactory := publishing.NewPublisherFactory()
	targetDiscovery := publishinginfra.NewTargetDiscoveryManager()

	// Initialize alert processor
	alertProcessor := services.NewAlertProcessor(
		alertRepo,
		dedupService,
		filterEngine,
		groupManager,
		silenceManager,
		inhibitionManager,
		routeEvaluator,
		publisherFactory,
		targetDiscovery,
	)

	// Setup HTTP router
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// Health check
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "healthy",
			"version": appVersion,
			"profile": cfg.Profile,
		})
	})

	// Prometheus metrics
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API v2 (Alertmanager compatible)
	v2 := router.Group("/api/v2")
	{
		// Alert ingestion
		alertHandler := handlers.NewPrometheusAlertsHandler(alertProcessor)
		v2.POST("/alerts", alertHandler.HandlePostAlerts)
		v2.GET("/alerts", handlers.HandleGetAlerts(alertRepo))

		// Silences
		silenceHandler := handlers.NewSilenceHandler(silenceManager)
		v2.POST("/silences", silenceHandler.CreateSilence)
		v2.GET("/silences", silenceHandler.ListSilences)
		v2.GET("/silences/:id", silenceHandler.GetSilence)
		v2.PUT("/silences/:id", silenceHandler.UpdateSilence)
		v2.DELETE("/silences/:id", silenceHandler.DeleteSilence)

		// Status
		v2.GET("/status", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"cluster": gin.H{
					"status": "ready",
				},
				"versionInfo": gin.H{
					"version": appVersion,
				},
				"config": gin.H{
					"original": "",
				},
				"uptime": time.Since(time.Now()).String(),
			})
		})
	}

	// Universal webhook endpoint
	router.POST("/webhook", handlers.HandleWebhook(alertProcessor))

	// Start HTTP server
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		slog.Info("🌐 HTTP server listening", "port", cfg.Server.Port)
		slog.Info("📍 Alertmanager API: http://localhost:%d/api/v2", cfg.Server.Port)
		slog.Info("📊 Metrics: http://localhost:%d/metrics", cfg.Server.Port)
		slog.Info("💚 Health: http://localhost:%d/healthz", cfg.Server.Port)
		
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("🛑 Shutting down gracefully...")

	// Shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("✅ Server stopped gracefully")
}

