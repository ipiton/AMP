package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BackupManager управляет backup'ами базы данных
type BackupManager struct {
	config *BackupConfig
	db     *sql.DB
	logger *slog.Logger
}

// BackupConfig определяет конфигурацию backup
type BackupConfig struct {
	Enabled       bool          `env:"BACKUP_ENABLED" default:"true"`
	Type          string        `env:"BACKUP_TYPE" default:"schema"`
	Path          string        `env:"BACKUP_PATH" default:"./backups"`
	RetentionDays int           `env:"BACKUP_RETENTION_DAYS" default:"30"`
	Compress      bool          `env:"BACKUP_COMPRESS" default:"true"`
	Timeout       time.Duration `env:"BACKUP_TIMEOUT" default:"10m"`
}

// NewBackupManager создает новый менеджер backup
func NewBackupManager(config *BackupConfig, db *sql.DB, logger *slog.Logger) *BackupManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &BackupManager{
		config: config,
		db:     db,
		logger: logger,
	}
}

// CreatePreMigrationBackup создает backup перед миграцией
func (bm *BackupManager) CreatePreMigrationBackup(ctx context.Context) (string, error) {
	if !bm.config.Enabled {
		bm.logger.Info("Backup disabled, skipping pre-migration backup")
		return "", nil
	}

	bm.logger.Info("Creating pre-migration backup")

	// Создаем timestamp для backup
	timestamp := time.Now().Format("20060102_150405")
	backupFile := fmt.Sprintf("pre_migration_%s.sql", timestamp)

	// Определяем полный путь
	fullPath := filepath.Join(bm.config.Path, backupFile)

	// Создаем директорию если не существует
	if err := os.MkdirAll(bm.config.Path, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Определяем тип базы данных и создаем соответствующий backup
	dbType, err := bm.detectDatabaseType(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to detect database type: %w", err)
	}

	switch dbType {
	case "postgres":
		return bm.createPostgreSQLBackup(ctx, fullPath)
	case "sqlite":
		return bm.createSQLiteBackup(ctx, fullPath)
	default:
		return "", fmt.Errorf("unsupported database type for backup: %s", dbType)
	}
}

// CreatePostMigrationBackup создает backup после миграции
func (bm *BackupManager) CreatePostMigrationBackup(ctx context.Context) (string, error) {
	if !bm.config.Enabled {
		bm.logger.Info("Backup disabled, skipping post-migration backup")
		return "", nil
	}

	bm.logger.Info("Creating post-migration backup")

	// Создаем timestamp для backup
	timestamp := time.Now().Format("20060102_150405")
	backupFile := fmt.Sprintf("post_migration_%s.sql", timestamp)

	// Определяем полный путь
	fullPath := filepath.Join(bm.config.Path, backupFile)

	// Создаем директорию если не существует
	if err := os.MkdirAll(bm.config.Path, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Определяем тип базы данных и создаем соответствующий backup
	dbType, err := bm.detectDatabaseType(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to detect database type: %w", err)
	}

	switch dbType {
	case "postgres":
		return bm.createPostgreSQLBackup(ctx, fullPath)
	case "sqlite":
		return bm.createSQLiteBackup(ctx, fullPath)
	default:
		return "", fmt.Errorf("unsupported database type for backup: %s", dbType)
	}
}

// createPostgreSQLBackup создает backup PostgreSQL
func (bm *BackupManager) createPostgreSQLBackup(ctx context.Context, backupFile string) (string, error) {
	bm.logger.Info("Creating PostgreSQL backup", "file", backupFile)

	// Получаем параметры подключения из DSN
	// В реальном приложении это нужно сделать более надежно
	dsn := os.Getenv("MIGRATION_DSN")
	if dsn == "" {
		return "", fmt.Errorf("MIGRATION_DSN environment variable not set")
	}

	// Используем pg_dump для создания schema-only backup
	args := []string{
		"--schema-only",
		"--no-owner",
		"--no-privileges",
		"--file", backupFile,
		dsn,
	}

	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", bm.extractPassword(dsn)))

	output, err := cmd.CombinedOutput()
	if err != nil {
		bm.logger.Error("PostgreSQL backup failed",
			"error", err,
			"output", string(output))
		return "", fmt.Errorf("failed to create PostgreSQL backup: %w", err)
	}

	// Проверяем размер файла
	if fileStat, err := os.Stat(backupFile); err != nil {
		return "", fmt.Errorf("failed to stat backup file: %w", err)
	} else if fileStat.Size() == 0 {
		return "", fmt.Errorf("backup file is empty")
	}

	fileStat, err := os.Stat(backupFile)
	if err != nil {
		return "", fmt.Errorf("failed to stat backup file: %w", err)
	}

	bm.logger.Info("PostgreSQL backup created successfully",
		"file", backupFile,
		"size", fileStat.Size())

	return backupFile, nil
}

// createSQLiteBackup создает backup SQLite
func (bm *BackupManager) createSQLiteBackup(ctx context.Context, backupFile string) (string, error) {
	bm.logger.Info("Creating SQLite backup", "file", backupFile)

	// Для SQLite используем .dump команду
	dumpQuery := fmt.Sprintf(".dump > %s", backupFile)

	if _, err := bm.db.ExecContext(ctx, dumpQuery); err != nil {
		bm.logger.Error("SQLite backup failed", "error", err)
		return "", fmt.Errorf("failed to create SQLite backup: %w", err)
	}

	// Проверяем размер файла
	if fileStat, err := os.Stat(backupFile); err != nil {
		return "", fmt.Errorf("failed to stat backup file: %w", err)
	} else if fileStat.Size() == 0 {
		return "", fmt.Errorf("backup file is empty")
	}

	fileStat, err := os.Stat(backupFile)
	if err != nil {
		return "", fmt.Errorf("failed to stat backup file: %w", err)
	}

	bm.logger.Info("SQLite backup created successfully",
		"file", backupFile,
		"size", fileStat.Size())

	return backupFile, nil
}

// VerifyBackup проверяет целостность backup файла
func (bm *BackupManager) VerifyBackup(ctx context.Context, backupFile string) error {
	bm.logger.Info("Verifying backup file", "file", backupFile)

	// Проверяем существование файла
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %s", backupFile)
	}

	// Проверяем размер файла
	stat, err := os.Stat(backupFile)
	if err != nil {
		return fmt.Errorf("failed to stat backup file: %w", err)
	}

	if stat.Size() == 0 {
		return fmt.Errorf("backup file is empty: %s", backupFile)
	}

	// Проверяем, что файл читаемый
	file, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf("backup file is not readable: %w", err)
	}
	defer file.Close()

	// Читаем первые несколько байт для проверки
	buffer := make([]byte, 1024)
	_, err = file.Read(buffer)
	if err != nil && err.Error() != "EOF" {
		return fmt.Errorf("backup file is corrupted: %w", err)
	}

	// Для SQL файлов проверяем наличие SQL команд
	content := string(buffer)
	if !strings.Contains(content, "--") && !strings.Contains(content, "CREATE") {
		bm.logger.Warn("Backup file may not contain valid SQL",
			"file", backupFile)
	}

	bm.logger.Info("Backup verification successful",
		"file", backupFile,
		"size", stat.Size())

	return nil
}

// RestoreFromBackup восстанавливает базу данных из backup
func (bm *BackupManager) RestoreFromBackup(ctx context.Context, backupFile string) error {
	bm.logger.Warn("Starting database restore from backup", "file", backupFile)

	// Проверяем существование backup файла
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %s", backupFile)
	}

	// Определяем тип базы данных
	dbType, err := bm.detectDatabaseType(ctx)
	if err != nil {
		return fmt.Errorf("failed to detect database type: %w", err)
	}

	switch dbType {
	case "postgres":
		return bm.restorePostgreSQLBackup(ctx, backupFile)
	case "sqlite":
		return bm.restoreSQLiteBackup(ctx, backupFile)
	default:
		return fmt.Errorf("unsupported database type for restore: %s", dbType)
	}
}

// restorePostgreSQLBackup восстанавливает PostgreSQL из backup
func (bm *BackupManager) restorePostgreSQLBackup(ctx context.Context, backupFile string) error {
	bm.logger.Info("Restoring PostgreSQL from backup", "file", backupFile)

	// Получаем DSN
	dsn := os.Getenv("MIGRATION_DSN")
	if dsn == "" {
		return fmt.Errorf("MIGRATION_DSN environment variable not set")
	}

	// Используем psql для восстановления
	args := []string{
		"--file", backupFile,
		dsn,
	}

	cmd := exec.CommandContext(ctx, "psql", args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", bm.extractPassword(dsn)))

	output, err := cmd.CombinedOutput()
	if err != nil {
		bm.logger.Error("PostgreSQL restore failed",
			"error", err,
			"output", string(output))
		return fmt.Errorf("failed to restore PostgreSQL backup: %w", err)
	}

	bm.logger.Info("PostgreSQL restore completed successfully")
	return nil
}

// restoreSQLiteBackup восстанавливает SQLite из backup
func (bm *BackupManager) restoreSQLiteBackup(ctx context.Context, backupFile string) error {
	bm.logger.Info("Restoring SQLite from backup", "file", backupFile)

	// Читаем backup файл
	content, err := os.ReadFile(backupFile)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	// Выполняем SQL команды из backup
	if _, err := bm.db.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("failed to execute backup SQL: %w", err)
	}

	bm.logger.Info("SQLite restore completed successfully")
	return nil
}

// CleanupOldBackups удаляет старые backup файлы
func (bm *BackupManager) CleanupOldBackups(ctx context.Context) error {
	if bm.config.RetentionDays <= 0 {
		bm.logger.Info("Backup cleanup disabled (retention days <= 0)")
		return nil
	}

	bm.logger.Info("Starting backup cleanup",
		"retention_days", bm.config.RetentionDays)

	cutoffDate := time.Now().AddDate(0, 0, -bm.config.RetentionDays)

	// Читаем содержимое директории backup
	entries, err := os.ReadDir(bm.config.Path)
	if err != nil {
		return fmt.Errorf("failed to read backup directory: %w", err)
	}

	deletedCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Проверяем, является ли файл backup файлом
		if !bm.isBackupFile(entry.Name()) {
			continue
		}

		// Парсим timestamp из имени файла
		timestamp, err := bm.parseBackupTimestamp(entry.Name())
		if err != nil {
			bm.logger.Warn("Failed to parse timestamp from backup file",
				"file", entry.Name(),
				"error", err)
			continue
		}

		// Проверяем, нужно ли удалить файл
		if timestamp.Before(cutoffDate) {
			filePath := filepath.Join(bm.config.Path, entry.Name())

			if err := os.Remove(filePath); err != nil {
				bm.logger.Error("Failed to remove old backup file",
					"file", filePath,
					"error", err)
			} else {
				bm.logger.Info("Removed old backup file",
					"file", entry.Name(),
					"age_days", int(time.Since(timestamp).Hours()/24))
				deletedCount++
			}
		}
	}

	bm.logger.Info("Backup cleanup completed",
		"deleted_files", deletedCount)

	return nil
}

// isBackupFile проверяет, является ли файл backup файлом
func (bm *BackupManager) isBackupFile(filename string) bool {
	return strings.HasPrefix(filename, "pre_migration_") ||
		strings.HasPrefix(filename, "post_migration_")
}

// parseBackupTimestamp парсит timestamp из имени backup файла
func (bm *BackupManager) parseBackupTimestamp(filename string) (time.Time, error) {
	// Формат: pre_migration_20250102_150405.sql
	// Извлекаем timestamp: 20250102_150405
	var timestampStr string

	if strings.HasPrefix(filename, "pre_migration_") {
		timestampStr = strings.TrimPrefix(filename, "pre_migration_")
	} else if strings.HasPrefix(filename, "post_migration_") {
		timestampStr = strings.TrimPrefix(filename, "post_migration_")
	} else {
		return time.Time{}, fmt.Errorf("invalid backup filename format")
	}

	// Убираем расширение .sql если есть
	timestampStr = strings.TrimSuffix(timestampStr, ".sql")

	// Парсим timestamp
	return time.Parse("20060102_150405", timestampStr)
}

// detectDatabaseType определяет тип базы данных
func (bm *BackupManager) detectDatabaseType(ctx context.Context) (string, error) {
	// Пробуем PostgreSQL запрос
	var pgExists bool
	pgQuery := "SELECT EXISTS (SELECT 1 FROM information_schema.tables LIMIT 1)"
	err := bm.db.QueryRowContext(ctx, pgQuery).Scan(&pgExists)

	if err == nil {
		return "postgres", nil
	}

	// Пробуем SQLite запрос
	var sqliteVersion string
	sqliteQuery := "SELECT sqlite_version()"
	err = bm.db.QueryRowContext(ctx, sqliteQuery).Scan(&sqliteVersion)

	if err == nil {
		return "sqlite", nil
	}

	return "", fmt.Errorf("unable to determine database type")
}

// extractPassword извлекает пароль из DSN
func (bm *BackupManager) extractPassword(dsn string) string {
	// Простая реализация - в реальном приложении нужно более надежное решение
	// Рекомендуется использовать внешнее управление секретами
	if strings.Contains(dsn, "password=") {
		parts := strings.Split(dsn, "password=")
		if len(parts) > 1 {
			password := parts[1]
			if idx := strings.Index(password, " "); idx > 0 {
				password = password[:idx]
			}
			return password
		}
	}
	return ""
}

// GetBackupStats возвращает статистику по backup файлам
func (bm *BackupManager) GetBackupStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Проверяем существование директории
	if _, err := os.Stat(bm.config.Path); os.IsNotExist(err) {
		stats["total_backups"] = 0
		stats["oldest_backup"] = nil
		stats["newest_backup"] = nil
		stats["total_size"] = 0
		return stats, nil
	}

	entries, err := os.ReadDir(bm.config.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	totalSize := int64(0)
	totalBackups := 0
	var oldestTime, newestTime *time.Time

	for _, entry := range entries {
		if entry.IsDir() || !bm.isBackupFile(entry.Name()) {
			continue
		}

		totalBackups++

		filePath := filepath.Join(bm.config.Path, entry.Name())
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		totalSize += fileInfo.Size()

		timestamp, err := bm.parseBackupTimestamp(entry.Name())
		if err != nil {
			continue
		}

		if oldestTime == nil || timestamp.Before(*oldestTime) {
			oldestTime = &timestamp
		}

		if newestTime == nil || timestamp.After(*newestTime) {
			newestTime = &timestamp
		}
	}

	stats["total_backups"] = totalBackups
	stats["total_size"] = totalSize
	stats["oldest_backup"] = oldestTime
	stats["newest_backup"] = newestTime

	return stats, nil
}
