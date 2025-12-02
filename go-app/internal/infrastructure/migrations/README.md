# 🐦 Migration System

Production-ready система управления миграциями базы данных с использованием **Goose** для Alert History Service.

## 🎯 **Обзор**

Система миграций обеспечивает:
- ✅ **Версионированное управление схемой БД**
- ✅ **Безопасное развертывание** с backup и rollback
- ✅ **Много-платформенную поддержку** (PostgreSQL, SQLite)
- ✅ **Production-grade надежность** с health checks
- ✅ **Полный аудит и мониторинг**

## 📋 **Архитектура**

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   CLI Interface │    │ MigrationManager │    │  Error Handler  │
│   cobra/cmd     │────│   goose wrapper  │────│  retry logic    │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
                    ┌─────────────────┐
                    │   Backup Mgr    │
                    │   auto backup   │
                    └─────────────────┘
                                 │
                    ┌─────────────────┐
                    │ Health Checker │
                    │ pre/post checks│
                    └─────────────────┘
```

## 🚀 **Быстрый старт**

### 1. Установка зависимостей

```bash
# Установка goose
go install github.com/pressly/goose/v3/cmd/goose@latest

# Или через make
make -f Makefile.migrations install-goose
```

### 2. Настройка переменных окружения

```bash
# PostgreSQL
export MIGRATION_DRIVER=postgres
export MIGRATION_DSN="postgres://user:pass@localhost:5432/alert_history?sslmode=disable"

# SQLite (для разработки)
export MIGRATION_DRIVER=sqlite
export MIGRATION_DSN="file:./alert_history.db?cache=shared&mode=rwc"

# Общие настройки
export MIGRATION_DIR=./migrations
export MIGRATION_VERBOSE=true
export BACKUP_ENABLED=true
```

### 3. Использование CLI

```bash
# Просмотр статуса миграций
make -f Makefile.migrations migrate-status

# Применение всех миграций
make -f Makefile.migrations migrate-up

# Создание новой миграции
make -f Makefile.migrations migrate-create name=add_user_table

# Откат миграций
make -f Makefile.migrations migrate-down
```

## 📁 **Структура файлов**

```
internal/infrastructure/migrations/
├── manager.go           # Основной менеджер миграций
├── errors.go            # Обработка ошибок и recovery
├── backup.go            # Система backup/restore
├── health.go            # Health checks
├── cli.go              # CLI интерфейс
├── config.go           # Конфигурация
├── example.go          # Примеры использования
├── manager_test.go     # Тесты
└── README.md           # Эта документация

migrations/
└── 20240101120000_initial_schema.sql  # Файлы миграций
```

## 🔧 **Конфигурация**

### Переменные окружения

| Переменная | По умолчанию | Описание |
|------------|-------------|----------|
| `MIGRATION_DRIVER` | `postgres` | Тип базы данных |
| `MIGRATION_DSN` | - | Строка подключения |
| `MIGRATION_DIR` | `./migrations` | Директория миграций |
| `MIGRATION_TABLE` | `goose_db_version` | Таблица версий |
| `MIGRATION_TIMEOUT` | `5m` | Таймаут операций |
| `MIGRATION_VERBOSE` | `false` | Подробный вывод |
| `BACKUP_ENABLED` | `true` | Включить backup |
| `HEALTH_ENABLED` | `true` | Включить health checks |

### Примеры конфигурации

#### Production PostgreSQL
```bash
export MIGRATION_DRIVER=postgres
export MIGRATION_DSN="postgres://prod_user:prod_pass@prod_host:5432/prod_db?sslmode=require"
export BACKUP_ENABLED=true
export MIGRATION_VERBOSE=false
```

#### Development SQLite
```bash
export MIGRATION_DRIVER=sqlite
export MIGRATION_DSN="file:./dev.db?cache=shared&mode=rwc"
export BACKUP_ENABLED=false
export MIGRATION_VERBOSE=true
```

## 🛠️ **CLI Команды**

### Основные команды миграций

```bash
# Применить все миграции
goose -dir ./migrations postgres "dsn" up

# Откатить все миграции
goose -dir ./migrations postgres "dsn" down

# Показать статус
goose -dir ./migrations postgres "dsn" status

# Создать новую миграцию
goose -dir ./migrations postgres "dsn" create add_users_table sql

# Показать версию
goose -dir ./migrations postgres "dsn" version
```

### Make команды

```bash
# Полная справка
make -f Makefile.migrations help

# Применить миграции
make -f Makefile.migrations migrate-up

# Статус миграций
make -f Makefile.migrations migrate-status

# Создать миграцию
make -f Makefile.migrations migrate-create name=add_indexes

# Health check
make -f Makefile.migrations health-check

# Backup
make -f Makefile.migrations backup-create
```

## 📝 **Создание миграций**

### Формат файла миграции

```sql
-- +goose Up
-- SQL команды для применения миграции

CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);

-- +goose Down
-- SQL команды для отката миграции

DROP INDEX IF EXISTS idx_users_email;
DROP TABLE IF EXISTS users;
```

### Правила именования

```bash
# Хорошие примеры
20240101120000_add_users_table.sql
20240101120100_create_user_indexes.sql
20240101120200_add_user_validation.sql

# Плохие примеры
migration_1.sql
users.sql
add_stuff.sql
```

## 🔒 **Безопасность и надежность**

### Backup стратегия

- **Pre-migration**: Backup создается автоматически перед каждой миграцией
- **Post-migration**: Backup создается после успешной миграции
- **Retention**: Старые backup'ы удаляются автоматически (30 дней)
- **Verification**: Каждый backup проверяется на целостность

### Health Checks

#### Pre-migration checks:
- ✅ Подключение к БД
- ✅ Права доступа
- ✅ Целостность существующих данных
- ✅ Свободное место на диске

#### Post-migration checks:
- ✅ Подключение к БД
- ✅ Целостность схемы
- ✅ Согласованность данных
- ✅ Состояние индексов

### Error Recovery

- **Retry logic**: Автоматические повторы при временных ошибках
- **Circuit breaker**: Предотвращение каскадных сбоев
- **Graceful rollback**: Безопасный откат при ошибках

## 🧪 **Тестирование**

### Запуск тестов

```bash
# Все тесты
go test ./internal/infrastructure/migrations/...

# С бенчмарками
go test -bench=. ./internal/infrastructure/migrations/...

# С покрытием
go test -cover ./internal/infrastructure/migrations/...
```

### Интеграционные тесты

```bash
# Тесты с PostgreSQL
MIGRATION_DRIVER=postgres MIGRATION_DSN="test_dsn" go test ./...

# Тесты с SQLite
MIGRATION_DRIVER=sqlite MIGRATION_DSN=":memory:" go test ./...
```

## 📊 **Мониторинг и метрики**

### Метрики

- Количество примененных миграций
- Время выполнения миграций
- Количество ошибок и recovery
- Размер backup файлов
- Статус health checks

### Логи

```json
{
  "level": "info",
  "timestamp": "2024-01-01T12:00:00Z",
  "message": "Migration applied successfully",
  "migration_version": 20240101120000,
  "execution_time": "1.23s",
  "database": "postgres"
}
```

## 🔧 **Расширение системы**

### Добавление нового драйвера БД

```go
// В manager.go добавить поддержку нового драйвера
func (mm *MigrationManager) NewMigrationManager(config *MigrationConfig) (*MigrationManager, error) {
    var dialect goose.Dialect
    switch config.Driver {
    case "postgres":
        dialect = goose.DialectPostgres
    case "mysql":
        dialect = goose.DialectMySQL // Новый драйвер
    case "sqlite":
        dialect = goose.DialectSQLite3
    default:
        return nil, fmt.Errorf("unsupported database driver: %s", config.Driver)
    }
    // ...
}
```

### Кастомные health checks

```go
// Добавить кастомную проверку
func (hc *HealthChecker) checkCustomLogic(ctx context.Context) error {
    // Ваша логика проверки
    return hc.db.PingContext(ctx)
}
```

## 🐛 **Устранение неполадок**

### Распространенные проблемы

#### "Migration table does not exist"

```bash
# Решение: применить хотя бы одну миграцию
make migrate-up
```

#### "Permission denied"

```bash
# Проверить права пользователя БД
make health-check
```

#### "Connection timeout"

```bash
# Проверить настройки подключения
export MIGRATION_TIMEOUT=10m
```

### Debug режим

```bash
export MIGRATION_VERBOSE=true
export MIGRATION_DRY_RUN=true
make migrate-status
```

## 📚 **Примеры использования**

### В коде приложения

```go
// Инициализация системы миграций
config, err := migrations.LoadConfig()
if err != nil {
    log.Fatal(err)
}

manager, err := migrations.NewMigrationManager(config)
if err != nil {
    log.Fatal(err)
}

// Автоматическое применение миграций при старте
if err := manager.Up(context.Background()); err != nil {
    log.Fatal("Failed to apply migrations:", err)
}
```

### В CI/CD pipeline

```yaml
# .github/workflows/deploy.yml
- name: Run Migrations
  run: |
    make -f Makefile.migrations migrate-up
    make -f Makefile.migrations health-check

- name: Create Backup
  run: |
    make -f Makefile.migrations backup-create
```

## 🤝 **Contributing**

### Добавление новой функциональности

1. Создайте issue с описанием требования
2. Напишите тесты для новой функциональности
3. Реализуйте функциональность
4. Обновите документацию
5. Создайте PR

### Соглашения по коду

- Использовать `gofmt` для форматирования
- Добавлять тесты для всех публичных функций
- Следовать принципам SOLID
- Использовать structured logging

---

## 📞 **Поддержка**

- 📧 **Email**: dev@company.com
- 💬 **Slack**: #database-migrations
- 📖 **Docs**: [Internal Wiki](https://wiki.company.com/database/migrations)

---

**🎉 Happy migrating!** 🐦
