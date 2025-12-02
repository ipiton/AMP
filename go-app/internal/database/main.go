// Package database provides PostgreSQL connection management for Alert History Service
package database

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ipiton/AMP/internal/database/postgres"
)

func main() {
	// Настраиваем structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	slog.SetDefault(logger)

	// Загружаем конфигурацию из переменных окружения
	config := postgres.LoadFromEnv()

	// Создаем connection pool
	pool := postgres.NewPostgresPool(config, logger)

	// Настраиваем graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Обработчик сигналов для graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down gracefully", "signal", sig)
		cancel()
	}()

	// Подключаемся к базе данных
	logger.Info("Starting database connection pool...")
	if err := pool.Connect(ctx); err != nil {
		logger.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	logger.Info("✅ Successfully connected to PostgreSQL!")

	// Запускаем health monitoring
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := pool.Health(ctx); err != nil {
					logger.Warn("Health check failed", "error", err)
				} else {
					stats := pool.Stats()
					logger.Info("Database health check passed",
						"active_conns", stats.ActiveConnections,
						"idle_conns", stats.IdleConnections,
						"total_conns", stats.TotalConnections,
						"success_rate", fmt.Sprintf("%.2f%%", pool.GetMetrics().GetSuccessRate()))
				}
			}
		}
	}()

	// Выполняем тестовые запросы
	logger.Info("Running test queries...")

	// Проверяем версию PostgreSQL
	rows, err := pool.Query(ctx, "SELECT version()")
	if err != nil {
		logger.Error("Failed to query PostgreSQL version", "error", err)
	} else {
		defer rows.Close()
		if rows.Next() {
			var version string
			if err := rows.Scan(&version); err != nil {
				logger.Error("Failed to scan version", "error", err)
			} else {
				logger.Info("PostgreSQL version", "version", version)
			}
		}
	}

	// Проверяем активные соединения
	row := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pg_stat_activity WHERE datname = $1", config.Database)
	var activeConnections int
	if err := row.Scan(&activeConnections); err != nil {
		logger.Error("Failed to query active connections", "error", err)
	} else {
		logger.Info("Active database connections", "count", activeConnections)
	}

	// Демонстрируем транзакцию
	tx, err := pool.Begin(ctx)
	if err != nil {
		logger.Error("Failed to begin transaction", "error", err)
	} else {
		// Выполняем запрос в транзакции
		var dbSize string
		err := tx.QueryRow(ctx, "SELECT pg_size_pretty(pg_database_size($1))", config.Database).Scan(&dbSize)
		if err != nil {
			logger.Error("Failed to query database size", "error", err)
			tx.Rollback(ctx)
		} else {
			logger.Info("Database size", "size", dbSize)
			if err := tx.Commit(ctx); err != nil {
				logger.Error("Failed to commit transaction", "error", err)
			} else {
				logger.Info("Transaction committed successfully")
			}
		}
	}

	// Ждем сигнала завершения
	logger.Info("Database connection pool is running. Press Ctrl+C to stop.")
	<-ctx.Done()

	// Graceful shutdown
	logger.Info("Shutting down database connection pool...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := pool.Disconnect(shutdownCtx); err != nil {
		logger.Error("Error during database disconnect", "error", err)
		os.Exit(1)
	}

	logger.Info("✅ Database connection pool shut down gracefully")
}
