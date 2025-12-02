package postgres

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"
)

// ExampleUsage демонстрирует использование PostgreSQL connection pool
func ExampleUsage() {
	// Создаем конфигурацию из переменных окружения
	config := LoadFromEnv()

	// Создаем logger
	logger := slog.New(slog.NewTextHandler(log.Writer(), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Создаем connection pool
	pool := NewPostgresPool(config, logger)

	// Подключаемся к базе данных
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println("Connecting to database...")
	if err := pool.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	fmt.Println("✅ Connected successfully!")

	// Выполняем health check
	fmt.Println("Performing health check...")
	if err := pool.Health(ctx); err != nil {
		log.Printf("Health check failed: %v", err)
	} else {
		fmt.Println("✅ Health check passed!")
	}

	// Получаем статистику
	stats := pool.Stats()
	fmt.Printf("📊 Pool Statistics:\n")
	fmt.Printf("  - Active connections: %d\n", stats.ActiveConnections)
	fmt.Printf("  - Idle connections: %d\n", stats.IdleConnections)
	fmt.Printf("  - Total connections: %d\n", stats.TotalConnections)
	fmt.Printf("  - Success rate: %.2f%%\n", pool.GetMetrics().GetSuccessRate())

	// Выполняем простой запрос
	fmt.Println("Executing test query...")
	rows, err := pool.Query(ctx, "SELECT version()")
	if err != nil {
		log.Printf("Query failed: %v", err)
	} else {
		defer rows.Close()

		for rows.Next() {
			var version string
			if err := rows.Scan(&version); err != nil {
				log.Printf("Scan failed: %v", err)
				continue
			}
			fmt.Printf("📋 PostgreSQL Version: %s\n", version)
		}
	}

	// Демонстрируем транзакцию
	fmt.Println("Testing transaction...")
	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Printf("Failed to begin transaction: %v", err)
	} else {
		// Выполняем запрос в транзакции
		var count int
		err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM pg_stat_activity").Scan(&count)
		if err != nil {
			log.Printf("Transaction query failed: %v", err)
			tx.Rollback(ctx)
		} else {
			fmt.Printf("📊 Active connections in database: %d\n", count)
			tx.Commit(ctx)
		}
	}

	// Отображаем финальную статистику
	fmt.Println("\n📈 Final Statistics:")
	finalStats := pool.Stats()
	fmt.Printf("  - Total queries: %d\n", finalStats.TotalQueries)
	fmt.Printf("  - Average query time: %v\n", pool.GetMetrics().GetAverageQueryTime())
	fmt.Printf("  - Connection wait time: %v\n", pool.GetMetrics().GetAverageConnectionWait())

	// Закрываем соединение
	fmt.Println("Disconnecting...")
	if err := pool.Disconnect(ctx); err != nil {
		log.Printf("Disconnect failed: %v", err)
	} else {
		fmt.Println("✅ Disconnected successfully!")
	}
}

// ExampleWithRetry демонстрирует использование retry механизма
func ExampleWithRetry() {
	config := LoadFromEnv()
	logger := slog.New(slog.NewTextHandler(log.Writer(), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	pool := NewPostgresPool(config, logger)

	// Создаем retry executor
	retryConfig := DefaultRetryConfig()
	retryExecutor := NewRetryExecutor(retryConfig, logger)

	ctx := context.Background()

	// Подключаемся с retry
	fmt.Println("Connecting with retry...")
	err := retryExecutor.Execute(ctx, func() error {
		return pool.Connect(ctx)
	})

	if err != nil {
		log.Fatalf("Failed to connect even with retry: %v", err)
	}
	fmt.Println("✅ Connected with retry!")

	// Выполняем несколько запросов с retry
	for i := 0; i < 5; i++ {
		fmt.Printf("Executing query %d with retry...\n", i+1)
		_, err := retryExecutor.ExecuteWithResult(ctx, func() (interface{}, error) {
			return pool.Query(ctx, "SELECT pg_sleep(0.1)") // Имитация медленного запроса
		})

		if err != nil {
			log.Printf("Query %d failed: %v", i+1, err)
		} else {
			fmt.Printf("✅ Query %d succeeded!\n", i+1)
		}
	}

	pool.Disconnect(ctx)
}

// ExampleWithCircuitBreaker демонстрирует использование circuit breaker
func ExampleWithCircuitBreaker() {
	config := LoadFromEnv()
	logger := slog.Default()

	pool := NewPostgresPool(config, logger)

	// Создаем circuit breaker
	cb := NewCircuitBreaker(3, 10*time.Second)

	ctx := context.Background()

	// Подключаемся через circuit breaker
	fmt.Println("Connecting through circuit breaker...")
	err := cb.Call(func() error {
		return pool.Connect(ctx)
	})

	if err != nil {
		if err == ErrCircuitBreakerOpen {
			fmt.Println("❌ Circuit breaker is open!")
		} else {
			log.Fatalf("Failed to connect: %v", err)
		}
	} else {
		fmt.Println("✅ Connected through circuit breaker!")
	}

	// Имитируем несколько неудачных операций
	fmt.Println("Testing circuit breaker with failures...")
	for i := 0; i < 5; i++ {
		err := cb.Call(func() error {
			return fmt.Errorf("simulated failure %d", i+1)
		})

		fmt.Printf("Attempt %d: ", i+1)
		if err != nil {
			if err == ErrCircuitBreakerOpen {
				fmt.Println("❌ Circuit breaker opened!")
				break
			} else {
				fmt.Printf("⚠️  Operation failed: %v\n", err)
			}
		} else {
			fmt.Println("✅ Operation succeeded!")
		}
	}

	fmt.Printf("Circuit breaker state: %v\n", cb.GetState())
	fmt.Printf("Failure count: %d\n", cb.GetFailureCount())

	if pool.IsConnected() {
		pool.Disconnect(ctx)
	}
}
