package lock

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ExampleDistributedLock демонстрирует использование distributed lock
func ExampleDistributedLock() {
	// Создаем Redis клиент
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Создаем конфигурацию
	config := &LockConfig{
		TTL:            30 * time.Second,
		MaxRetries:     3,
		RetryInterval:  100 * time.Millisecond,
		AcquireTimeout: 5 * time.Second,
		ReleaseTimeout: 2 * time.Second,
		ValuePrefix:    "example",
	}

	// Создаем логгер
	logger := slog.Default()

	// Создаем блокировку
	lock := NewDistributedLock(client, "example_lock", config, logger)

	ctx := context.Background()

	// Пытаемся получить блокировку
	acquired, err := lock.Acquire(ctx)
	if err != nil {
		logger.Error("Failed to acquire lock", "error", err)
		return
	}

	if !acquired {
		logger.Info("Lock already held by another process")
		return
	}

	// Выполняем критическую секцию
	logger.Info("Entering critical section")
	time.Sleep(2 * time.Second)

	// Продлеваем блокировку если нужно
	err = lock.Extend(ctx, 60*time.Second)
	if err != nil {
		logger.Error("Failed to extend lock", "error", err)
	}

	// Завершаем критическую секцию
	logger.Info("Exiting critical section")

	// Освобождаем блокировку
	err = lock.Release(ctx)
	if err != nil {
		logger.Error("Failed to release lock", "error", err)
	}
}

// ExampleLockManager демонстрирует использование LockManager
func ExampleLockManager() {
	// Создаем Redis клиент
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Создаем менеджер блокировок
	manager := NewLockManager(client, nil, nil)

	ctx := context.Background()

	// Получаем несколько блокировок
	_, err := manager.AcquireLock(ctx, "resource_1")
	if err != nil {
		fmt.Printf("Failed to acquire lock1: %v\n", err)
		return
	}

	_, err = manager.AcquireLock(ctx, "resource_2")
	if err != nil {
		fmt.Printf("Failed to acquire lock2: %v\n", err)
		manager.ReleaseLock(ctx, "resource_1")
		return
	}

	// Выполняем операции с заблокированными ресурсами
	fmt.Printf("Working with resources: %v\n", manager.ListLocks())

	// Освобождаем все блокировки
	err = manager.ReleaseAll(ctx)
	if err != nil {
		fmt.Printf("Failed to release locks: %v\n", err)
	}
}

// ExampleConcurrentProcessing демонстрирует обработку задач с блокировками
func ExampleConcurrentProcessing() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	manager := NewLockManager(client, nil, nil)
	ctx := context.Background()

	// Список задач для обработки
	tasks := []string{"task_1", "task_2", "task_3", "task_1", "task_2"} // task_1 и task_2 дублируются

	for _, taskID := range tasks {
		lockKey := fmt.Sprintf("process_task_%s", taskID)

		// Пытаемся получить блокировку для задачи
		_, err := manager.AcquireLock(ctx, lockKey)
		if err != nil {
			fmt.Printf("Task %s is already being processed by another instance\n", taskID)
			continue
		}

		// Обрабатываем задачу
		fmt.Printf("Processing task: %s\n", taskID)
		time.Sleep(1 * time.Second)

		// Освобождаем блокировку
		err = manager.ReleaseLock(ctx, lockKey)
		if err != nil {
			fmt.Printf("Failed to release lock for task %s: %v\n", taskID, err)
		}
	}
}

// ExampleAlertProcessing демонстрирует обработку алертов с блокировками
func ExampleAlertProcessing() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	config := &LockConfig{
		TTL:            60 * time.Second, // 1 минута на обработку алерта
		MaxRetries:     5,
		RetryInterval:  200 * time.Millisecond,
		AcquireTimeout: 10 * time.Second,
		ReleaseTimeout: 5 * time.Second,
		ValuePrefix:    "alert_processing",
	}

	manager := NewLockManager(client, config, nil)
	ctx := context.Background()

	// Обрабатываем алерт по его fingerprint
	alertFingerprint := "alert_fingerprint_12345"
	lockKey := fmt.Sprintf("alert_processing:%s", alertFingerprint)

	lock, err := manager.AcquireLock(ctx, lockKey)
	if err != nil {
		fmt.Printf("Alert %s is already being processed\n", alertFingerprint)
		return
	}

	// Обрабатываем алерт
	fmt.Printf("Processing alert: %s\n", alertFingerprint)

	// Имитируем обработку
	time.Sleep(2 * time.Second)

	// Продлеваем блокировку если обработка затягивается
	err = lock.Extend(ctx, 120*time.Second)
	if err != nil {
		fmt.Printf("Failed to extend lock: %v\n", err)
	}

	// Завершаем обработку
	fmt.Printf("Alert %s processed successfully\n", alertFingerprint)

	// Освобождаем блокировку
	err = manager.ReleaseLock(ctx, lockKey)
	if err != nil {
		fmt.Printf("Failed to release lock: %v\n", err)
	}
}

// ExampleBatchProcessing демонстрирует пакетную обработку с блокировками
func ExampleBatchProcessing() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	manager := NewLockManager(client, nil, nil)
	ctx := context.Background()

	// Пытаемся получить блокировку для пакетной обработки
	_, err := manager.AcquireLock(ctx, "batch_processing")
	if err != nil {
		fmt.Println("Batch processing is already running")
		return
	}

	// Обрабатываем пакет
	fmt.Println("Starting batch processing...")

	// Имитируем обработку пакета
	time.Sleep(5 * time.Second)

	fmt.Println("Batch processing completed")

	// Освобождаем блокировку
	err = manager.ReleaseLock(ctx, "batch_processing")
	if err != nil {
		fmt.Printf("Failed to release batch lock: %v\n", err)
	}
}

// ExampleHealthCheck демонстрирует проверку здоровья блокировок
func ExampleHealthCheck() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	manager := NewLockManager(client, nil, nil)
	ctx := context.Background()

	// Получаем несколько блокировок
	_, err1 := manager.AcquireLock(ctx, "health_check_1")
	_, err2 := manager.AcquireLock(ctx, "health_check_2")

	if err1 != nil || err2 != nil {
		fmt.Println("Failed to acquire locks for health check")
		return
	}

	// Проверяем состояние блокировок
	fmt.Printf("Active locks: %v\n", manager.ListLocks())

	for _, lockKey := range manager.ListLocks() {
		lock, exists := manager.GetLock(lockKey)
		if exists {
			fmt.Printf("Lock %s: acquired=%v, ttl=%v\n",
				lockKey, lock.IsAcquired(), lock.GetTTL())
		}
	}

	// Освобождаем все блокировки
	err := manager.ReleaseAll(ctx)
	if err != nil {
		fmt.Printf("Failed to release all locks: %v\n", err)
	}
}
