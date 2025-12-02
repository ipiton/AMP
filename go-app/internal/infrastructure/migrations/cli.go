package migrations

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// CLI представляет командную строку для управления миграциями
type CLI struct {
	manager       *MigrationManager
	backupManager *BackupManager
	healthChecker *HealthChecker
	logger        *slog.Logger
}

// NewCLI создает новый CLI интерфейс
func NewCLI(manager *MigrationManager, backupManager *BackupManager, healthChecker *HealthChecker, logger *slog.Logger) *CLI {
	if logger == nil {
		logger = slog.Default()
	}

	return &CLI{
		manager:       manager,
		backupManager: backupManager,
		healthChecker: healthChecker,
		logger:        logger,
	}
}

// GetRootCommand возвращает корневую команду CLI
func (cli *CLI) GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Database migration management tool",
		Long:  "A powerful tool for managing database schema migrations with backup, health checks, and monitoring.",
	}

	// Добавляем подкоманды
	rootCmd.AddCommand(
		cli.upCommand(),
		cli.downCommand(),
		cli.statusCommand(),
		cli.versionCommand(),
		cli.createCommand(),
		cli.redoCommand(),
		cli.resetCommand(),
		cli.validateCommand(),
		cli.fixCommand(),
		cli.backupCommand(),
		cli.restoreCommand(),
		cli.healthCommand(),
	)

	return rootCmd
}

// upCommand команда для применения миграций
func (cli *CLI) upCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up [version]",
		Short: "Apply migrations",
		Long:  "Apply all pending migrations or up to a specific version",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Предварительные проверки здоровья
			if err := cli.healthChecker.PreMigrationCheck(ctx); err != nil {
				return fmt.Errorf("pre-migration health check failed: %w", err)
			}

			// Создаем backup перед миграцией
			if _, err := cli.backupManager.CreatePreMigrationBackup(ctx); err != nil {
				cli.logger.Warn("Failed to create pre-migration backup", "error", err)
			}

			// Применяем миграции
			var err error
			if len(args) == 0 {
				err = cli.manager.Up(ctx)
			} else {
				version, parseErr := strconv.ParseInt(args[0], 10, 64)
				if parseErr != nil {
					return fmt.Errorf("invalid version number: %w", parseErr)
				}
				err = cli.manager.UpTo(ctx, version)
			}

			if err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}

			// Создаем backup после миграции
			if _, err := cli.backupManager.CreatePostMigrationBackup(ctx); err != nil {
				cli.logger.Warn("Failed to create post-migration backup", "error", err)
			}

			// Проверки здоровья после миграции
			if err := cli.healthChecker.PostMigrationCheck(ctx); err != nil {
				return fmt.Errorf("post-migration health check failed: %w", err)
			}

			fmt.Println("Migrations applied successfully")
			return nil
		},
	}

	cmd.Flags().BoolP("dry-run", "d", false, "Show what would be migrated without applying")
	cmd.Flags().Bool("no-backup", false, "Skip backup creation")
	cmd.Flags().Bool("no-health-check", false, "Skip health checks")

	return cmd
}

// downCommand команда для отката миграций
func (cli *CLI) downCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down [steps]",
		Short: "Rollback migrations",
		Long:  "Rollback all migrations or a specific number of steps",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Создаем backup перед откатом
			if _, err := cli.backupManager.CreatePreMigrationBackup(ctx); err != nil {
				cli.logger.Warn("Failed to create pre-rollback backup", "error", err)
			}

			// Откатываем миграции
			var err error
			if len(args) == 0 {
				err = cli.manager.Down(ctx)
			} else {
				steps, parseErr := strconv.Atoi(args[0])
				if parseErr != nil {
					return fmt.Errorf("invalid number of steps: %w", parseErr)
				}

				for i := 0; i < steps; i++ {
					if downErr := cli.manager.DownByOne(ctx); downErr != nil {
						err = downErr
						break
					}
				}
			}

			if err != nil {
				return fmt.Errorf("rollback failed: %w", err)
			}

			// Создаем backup после отката
			if _, err := cli.backupManager.CreatePostMigrationBackup(ctx); err != nil {
				cli.logger.Warn("Failed to create post-rollback backup", "error", err)
			}

			fmt.Println("Migrations rolled back successfully")
			return nil
		},
	}

	cmd.Flags().Bool("no-backup", false, "Skip backup creation")

	return cmd
}

// statusCommand команда для проверки статуса миграций
func (cli *CLI) statusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration status",
		Long:  "Show the current status of all migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			statuses, err := cli.manager.Status(ctx)
			if err != nil {
				return fmt.Errorf("failed to get migration status: %w", err)
			}

			version, err := cli.manager.Version(ctx)
			if err != nil {
				return fmt.Errorf("failed to get current version: %w", err)
			}

			fmt.Printf("Current migration version: %d\n\n", version)
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

			return nil
		},
	}

	return cmd
}

// versionCommand команда для получения текущей версии
func (cli *CLI) versionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show current migration version",
		Long:  "Show the current migration version",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			version, err := cli.manager.Version(ctx)
			if err != nil {
				return fmt.Errorf("failed to get migration version: %w", err)
			}

			fmt.Printf("Current migration version: %d\n", version)
			return nil
		},
	}

	return cmd
}

// createCommand команда для создания новой миграции
func (cli *CLI) createCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new migration file",
		Long:  "Create a new migration file with the given name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			name := args[0]
			filename, err := cli.manager.Create(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to create migration: %w", err)
			}

			fmt.Printf("Created migration file: %s\n", filename)
			return nil
		},
	}

	return cmd
}

// redoCommand команда для переприменения последней миграции
func (cli *CLI) redoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "redo",
		Short: "Redo the last migration",
		Long:  "Rollback and reapply the last migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := cli.manager.Redo(ctx); err != nil {
				return fmt.Errorf("failed to redo migration: %w", err)
			}

			fmt.Println("Last migration redone successfully")
			return nil
		},
	}

	return cmd
}

// resetCommand команда для сброса всех миграций
func (cli *CLI) resetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset all migrations",
		Long:  "Rollback all migrations and reset the database to initial state",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Требуем подтверждения для опасной операции
			fmt.Print("WARNING: This will reset ALL migrations and potentially lose data. Continue? (yes/no): ")
			var response string
			fmt.Scanln(&response)

			if strings.ToLower(response) != "yes" {
				fmt.Println("Operation cancelled")
				return nil
			}

			ctx := context.Background()

			if err := cli.manager.Reset(ctx); err != nil {
				return fmt.Errorf("failed to reset migrations: %w", err)
			}

			fmt.Println("All migrations reset successfully")
			return nil
		},
	}

	return cmd
}

// validateCommand команда для валидации миграций
func (cli *CLI) validateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate migrations",
		Long:  "Validate the integrity and consistency of migration files",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := cli.manager.Validate(ctx); err != nil {
				return fmt.Errorf("migration validation failed: %w", err)
			}

			fmt.Println("Migration validation successful")
			return nil
		},
	}

	return cmd
}

// fixCommand команда для исправления проблем с миграциями
func (cli *CLI) fixCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fix",
		Short: "Fix migration issues",
		Long:  "Attempt to fix common migration problems automatically",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := cli.manager.Fix(ctx); err != nil {
				return fmt.Errorf("failed to fix migrations: %w", err)
			}

			fmt.Println("Migration fix completed successfully")
			return nil
		},
	}

	return cmd
}

// backupCommand команда для управления backup'ами
func (cli *CLI) backupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
		Long:  "Create, list, and manage database backups for migrations",
	}

	cmd.AddCommand(
		cli.backupCreateCommand(),
		cli.backupListCommand(),
		cli.backupCleanupCommand(),
	)

	return cmd
}

// backupCreateCommand команда для создания backup
func (cli *CLI) backupCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a database backup",
		Long:  "Create a backup of the current database state",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			backupFile, err := cli.backupManager.CreatePreMigrationBackup(ctx)
			if err != nil {
				return fmt.Errorf("failed to create backup: %w", err)
			}

			fmt.Printf("Backup created: %s\n", backupFile)
			return nil
		},
	}

	return cmd
}

// backupListCommand команда для просмотра backup файлов
func (cli *CLI) backupListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backup files",
		Long:  "Show all available backup files with their statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			stats, err := cli.backupManager.GetBackupStats(ctx)
			if err != nil {
				return fmt.Errorf("failed to get backup stats: %w", err)
			}

			fmt.Printf("Total backups: %v\n", stats["total_backups"])
			fmt.Printf("Total size: %v bytes\n", stats["total_size"])

			if oldest := stats["oldest_backup"]; oldest != nil {
				fmt.Printf("Oldest backup: %v\n", oldest)
			}

			if newest := stats["newest_backup"]; newest != nil {
				fmt.Printf("Newest backup: %v\n", newest)
			}

			return nil
		},
	}

	return cmd
}

// backupCleanupCommand команда для очистки старых backup файлов
func (cli *CLI) backupCleanupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up old backup files",
		Long:  "Remove backup files older than the retention period",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := cli.backupManager.CleanupOldBackups(ctx); err != nil {
				return fmt.Errorf("failed to cleanup backups: %w", err)
			}

			fmt.Println("Backup cleanup completed")
			return nil
		},
	}

	return cmd
}

// restoreCommand команда для восстановления из backup
func (cli *CLI) restoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <backup-file>",
		Short: "Restore from backup",
		Long:  "Restore the database from a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backupFile := args[0]
			ctx := context.Background()

			// Проверяем существование файла
			if _, err := os.Stat(backupFile); os.IsNotExist(err) {
				return fmt.Errorf("backup file does not exist: %s", backupFile)
			}

			// Проверяем backup
			if err := cli.backupManager.VerifyBackup(ctx, backupFile); err != nil {
				return fmt.Errorf("backup verification failed: %w", err)
			}

			// Восстанавливаем
			if err := cli.backupManager.RestoreFromBackup(ctx, backupFile); err != nil {
				return fmt.Errorf("failed to restore from backup: %w", err)
			}

			fmt.Printf("Database restored from backup: %s\n", backupFile)
			return nil
		},
	}

	return cmd
}

// healthCommand команда для проверок здоровья
func (cli *CLI) healthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Run health checks",
		Long:  "Run health checks on the database and migration system",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := cli.healthChecker.PreMigrationCheck(ctx); err != nil {
				return fmt.Errorf("health check failed: %w", err)
			}

			fmt.Println("All health checks passed")
			return nil
		},
	}

	return cmd
}

// Execute запускает CLI
func (cli *CLI) Execute() error {
	return cli.GetRootCommand().Execute()
}
