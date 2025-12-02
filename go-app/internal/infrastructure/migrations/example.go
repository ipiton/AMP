package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Example показывает пример использования системы миграций
func Example() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("🚀 Starting Migration System Example")

	// 1. Загружаем конфигурацию
	migrationConfig, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load migration config: %v", err)
	}

	backupConfig, err := LoadBackupConfig()
	if err != nil {
		log.Fatalf("Failed to load backup config: %v", err)
	}

	healthConfig, err := LoadHealthConfig()
	if err != nil {
		log.Fatalf("Failed to load health config: %v", err)
	}

	// Выводим конфигурацию
	migrationConfig.PrintConfig(logger)

	// 2. Создаем соединение с базой данных
	db, err := sql.Open(migrationConfig.Driver, migrationConfig.DSN)
	if err != nil {
		log.Fatalf("Failed to create database connection: %v", err)
	}
	defer db.Close()

	// 3. Создаем менеджер миграций
	manager, err := NewMigrationManager(migrationConfig)
	if err != nil {
		log.Fatalf("Failed to create migration manager: %v", err)
	}

	// 4. Создаем менеджер backup'ов
	backupManager := NewBackupManager(backupConfig, db, logger)

	// 5. Создаем health checker
	healthChecker := NewHealthChecker(db, healthConfig, logger)

	// 6. Создаем CLI интерфейс
	cli := NewCLI(manager, backupManager, healthChecker, logger)

	// 7. Пример использования CLI команд
	logger.Info("📋 Available CLI Commands:")
	fmt.Println("  migrate up           - Apply all pending migrations")
	fmt.Println("  migrate down         - Rollback all migrations")
	fmt.Println("  migrate status       - Show migration status")
	fmt.Println("  migrate create <name> - Create new migration file")
	fmt.Println("  migrate backup create - Create database backup")
	fmt.Println("  migrate health       - Run health checks")

	// 8. Демонстрация работы с миграциями
	logger.Info("🔍 Checking migration status...")

	statuses, err := manager.Status(ctx)
	if err != nil {
		log.Fatalf("Failed to get migration status: %v", err)
	}

	fmt.Printf("\n📊 Migration Status:\n")
	fmt.Printf("%-10s %-15s %-12s %s\n", "VERSION", "APPLIED", "TIMESTAMP", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 80))

	for _, status := range statuses {
		applied := "NO"
		if status.IsApplied {
			applied = "YES"
		}

		timestamp := "N/A"
		if !status.Timestamp.IsZero() {
			timestamp = status.Timestamp.Format("2006-01-02 15:04")
		}

		fmt.Printf("%-10d %-15s %-12s %s\n",
			status.VersionID,
			applied,
			timestamp,
			status.Description)
	}

	// 9. Демонстрация health checks
	logger.Info("🏥 Running health checks...")

	if err := healthChecker.PreMigrationCheck(ctx); err != nil {
		logger.Error("Health check failed", "error", err)
	} else {
		fmt.Println("✅ All health checks passed")
	}

	// 10. Демонстрация backup функциональности
	logger.Info("💾 Checking backup status...")

	stats, err := backupManager.GetBackupStats(ctx)
	if err != nil {
		logger.Error("Failed to get backup stats", "error", err)
	} else {
		fmt.Printf("\n📈 Backup Statistics:\n")
		fmt.Printf("Total backups: %v\n", stats["total_backups"])
		fmt.Printf("Total size: %v bytes\n", stats["total_size"])
		if oldest := stats["oldest_backup"]; oldest != nil {
			fmt.Printf("Oldest backup: %v\n", oldest)
		}
		if newest := stats["newest_backup"]; newest != nil {
			fmt.Printf("Newest backup: %v\n", newest)
		}
	}

	logger.Info("🎉 Migration system example completed successfully")

	// Для интерактивного использования
	if len(os.Args) > 1 {
		logger.Info("🚀 Running CLI command...")
		if err := cli.Execute(); err != nil {
			log.Fatalf("CLI command failed: %v", err)
		}
	}
}

// ExampleWithPostgres показывает пример работы с PostgreSQL
func ExampleWithPostgres() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("🐘 PostgreSQL Migration Example")

	// Устанавливаем переменные окружения для PostgreSQL
	os.Setenv("MIGRATION_DRIVER", "postgres")
	os.Setenv("MIGRATION_DSN", "postgres://user:password@localhost:5432/alert_history?sslmode=disable")
	os.Setenv("MIGRATION_DIALECT", "postgres")
	os.Setenv("MIGRATION_DIR", "migrations")
	os.Setenv("MIGRATION_VERBOSE", "true")

	// Запускаем пример
	Example()
}

// ExampleWithSQLite показывает пример работы с SQLite
func ExampleWithSQLite() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("📱 SQLite Migration Example")

	// Устанавливаем переменные окружения для SQLite
	os.Setenv("MIGRATION_DRIVER", "sqlite")
	os.Setenv("MIGRATION_DSN", "file:./alert_history.db?cache=shared&mode=rwc")
	os.Setenv("MIGRATION_DIALECT", "sqlite")
	os.Setenv("MIGRATION_DIR", "migrations")
	os.Setenv("MIGRATION_VERBOSE", "true")

	// Запускаем пример
	Example()
}

// ExampleCreateMigration показывает как создать новую миграцию
func ExampleCreateMigration() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("✨ Creating New Migration Example")

	// Загружаем конфигурацию
	config, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Создаем менеджер миграций
	manager, err := NewMigrationManager(config)
	if err != nil {
		log.Fatalf("Failed to create migration manager: %v", err)
	}

	// Создаем новую миграцию
	migrationName := fmt.Sprintf("add_user_preferences_%d", time.Now().Unix())
	filename, err := manager.Create(ctx, migrationName)
	if err != nil {
		log.Fatalf("Failed to create migration: %v", err)
	}

	logger.Info("✅ Migration created successfully",
		"name", migrationName,
		"filename", filename)

	fmt.Printf("\n📝 Created migration: %s\n", filename)
	fmt.Printf("Edit the file to add your schema changes\n")
}

// ExampleDryRun показывает dry-run режим
func ExampleDryRun() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("🔍 Migration Dry Run Example")

	// Устанавливаем dry-run режим
	os.Setenv("MIGRATION_DRY_RUN", "true")

	// Загружаем конфигурацию
	config, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Создаем менеджер миграций
	manager, err := NewMigrationManager(config)
	if err != nil {
		log.Fatalf("Failed to create migration manager: %v", err)
	}

	logger.Info("🚀 Starting dry run...")

	// Показываем, что будет применено
	statuses, err := manager.Status(ctx)
	if err != nil {
		log.Fatalf("Failed to get status: %v", err)
	}

	fmt.Printf("\n📋 Pending migrations (DRY RUN):\n")
	pendingCount := 0
	for _, status := range statuses {
		if !status.IsApplied {
			fmt.Printf("  - %s (version %d)\n", status.Description, status.VersionID)
			pendingCount++
		}
	}

	if pendingCount == 0 {
		fmt.Println("  No pending migrations")
	} else {
		fmt.Printf("\nWould apply %d migrations\n", pendingCount)
	}

	logger.Info("✅ Dry run completed")
}

// ExampleBackupWorkflow показывает полный workflow с backup
func ExampleBackupWorkflow() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("🔄 Migration + Backup Workflow Example")

	// 1. Загружаем все конфигурации
	migrationConfig, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load migration config: %v", err)
	}

	backupConfig, err := LoadBackupConfig()
	if err != nil {
		log.Fatalf("Failed to load backup config: %v", err)
	}

	healthConfig, err := LoadHealthConfig()
	if err != nil {
		log.Fatalf("Failed to load health config: %v", err)
	}

	// 2. Создаем соединение с БД
	db, err := sql.Open(migrationConfig.Driver, migrationConfig.DSN)
	if err != nil {
		log.Fatalf("Failed to create database connection: %v", err)
	}
	defer db.Close()

	// 3. Создаем все компоненты
	manager, err := NewMigrationManager(migrationConfig)
	if err != nil {
		log.Fatalf("Failed to create migration manager: %v", err)
	}

	backupManager := NewBackupManager(backupConfig, db, logger)

	healthChecker := NewHealthChecker(db, healthConfig, logger)

	logger.Info("🔒 Step 1: Pre-migration health check")
	if err := healthChecker.PreMigrationCheck(ctx); err != nil {
		log.Fatalf("Pre-migration health check failed: %v", err)
	}
	fmt.Println("✅ Health check passed")

	logger.Info("💾 Step 2: Creating pre-migration backup")
	backupFile, err := backupManager.CreatePreMigrationBackup(ctx)
	if err != nil {
		log.Fatalf("Failed to create backup: %v", err)
	}
	fmt.Printf("✅ Backup created: %s\n", backupFile)

	logger.Info("🚀 Step 3: Applying migrations")
	if err := manager.Up(ctx); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	fmt.Println("✅ Migrations applied successfully")

	logger.Info("💾 Step 4: Creating post-migration backup")
	postBackupFile, err := backupManager.CreatePostMigrationBackup(ctx)
	if err != nil {
		log.Fatalf("Failed to create post-migration backup: %v", err)
	}
	fmt.Printf("✅ Post-migration backup created: %s\n", postBackupFile)

	logger.Info("🔍 Step 5: Post-migration health check")
	if err := healthChecker.PostMigrationCheck(ctx); err != nil {
		log.Fatalf("Post-migration health check failed: %v", err)
	}
	fmt.Println("✅ Post-migration health check passed")

	logger.Info("🧹 Step 6: Cleanup old backups")
	if err := backupManager.CleanupOldBackups(ctx); err != nil {
		logger.Warn("Failed to cleanup old backups", "error", err)
	} else {
		fmt.Println("✅ Old backups cleaned up")
	}

	logger.Info("🎉 Migration workflow completed successfully!")
	fmt.Println("\n📊 Summary:")
	fmt.Printf("  - Pre-migration backup: %s\n", backupFile)
	fmt.Printf("  - Post-migration backup: %s\n", postBackupFile)
	fmt.Println("  - Health checks: PASSED")
	fmt.Println("  - Migrations: APPLIED")
	fmt.Println("  - Cleanup: COMPLETED")
}
