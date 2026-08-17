# PostgreSQL Connection Pool

PostgreSQL connection pool для AMP, построенный на базе [pgx v5](https://github.com/jackc/pgx).

## Особенности

- **Обработка ошибок**: Типизированные ошибки, retry и circuit breaker
- **Наблюдаемость**: Метрики pool и health checks
- **Безопасность**: Поддержка SSL/TLS, parameterized queries
- **Конфигурируемость**: Параметры connection pool через env-переменные

## 📋 Быстрый старт

### 1. Импорт пакета

```go
import "github.com/ipiton/AMP/internal/database/postgres"
```

### 2. Создание connection pool

```go
// Загрузка конфигурации из переменных окружения
config := postgres.LoadFromEnv()

// Создание logger
logger := slog.Default()

// Создание connection pool
pool := postgres.NewPostgresPool(config, logger)
```

### 3. Подключение к базе данных

```go
ctx := context.Background()
if err := pool.Connect(ctx); err != nil {
    log.Fatal("Failed to connect to database:", err)
}
defer pool.Disconnect(ctx)
```

### 4. Выполнение запросов

```go
// Простой запрос
rows, err := pool.Query(ctx, "SELECT id, title FROM alerts")
if err != nil {
    log.Fatal("Query failed:", err)
}
defer rows.Close()

// Обработка результатов
for rows.Next() {
    var id int
    var title string
    if err := rows.Scan(&id, &title); err != nil {
        log.Fatal("Scan failed:", err)
    }
    fmt.Printf("Alert: %d - %s\n", id, title)
}

// Единичный запрос
var count int
err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM alerts").Scan(&count)

// Команда (INSERT, UPDATE, DELETE)
tag, err := pool.Exec(ctx, "INSERT INTO alerts (title) VALUES ($1)", "New Alert")
if err != nil {
    log.Fatal("Exec failed:", err)
}
fmt.Printf("Inserted %d rows\n", tag.RowsAffected())
```

## ⚙️ Конфигурация

### Переменные окружения

| Переменная | Описание | Значение по умолчанию |
|------------|----------|----------------------|
| `DB_HOST` | Хост PostgreSQL | `localhost` |
| `DB_PORT` | Порт PostgreSQL | `5432` |
| `DB_NAME` | Имя базы данных | `alerthistory` |
| `DB_USER` | Пользователь базы данных | `alerthistory` |
| `DB_PASSWORD` | Пароль пользователя | `""` |
| `DB_SSL_MODE` | Режим SSL | `disable` |
| `DB_MAX_CONNS` | Максимум соединений | `20` |
| `DB_MIN_CONNS` | Минимум соединений | `2` |
| `DB_MAX_CONN_LIFETIME` | Максимальное время жизни соединения | `1h` |
| `DB_MAX_CONN_IDLE_TIME` | Максимальное время простоя | `5m` |
| `DB_HEALTH_CHECK_PERIOD` | Период health check | `30s` |

### Пример docker-compose.yml

```yaml
version: '3.8'
services:
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_DB: alerthistory
      POSTGRES_USER: alerthistory
      POSTGRES_PASSWORD: mypassword
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data

  app:
    image: alerthistory:latest
    environment:
      DB_HOST: postgres
      DB_PORT: 5432
      DB_NAME: alerthistory
      DB_USER: alerthistory
      DB_PASSWORD: mypassword
      DB_MAX_CONNS: 20
      DB_MIN_CONNS: 2
    depends_on:
      postgres:
        condition: service_healthy
```

## 🏗️ Архитектура

### Основные компоненты

#### 1. PostgresPool
Основной компонент, управляющий connection pool:
- Управление жизненным циклом соединений
- Метрики производительности
- Health monitoring
- Graceful shutdown

#### 2. PostgresConfig
Конфигурация подключения:
- Параметры подключения к PostgreSQL
- Настройки SSL/TLS
- Параметры connection pool
- Таймауты и лимиты

#### 3. PoolMetrics
Метрики производительности:
- Статистика соединений (активные/неактивные)
- Время выполнения запросов
- Количество ошибок
- Уровень успешности операций

#### 4. HealthChecker
Мониторинг здоровья:
- Периодические проверки соединения
- Автоматическое восстановление
- Circuit breaker паттерн
- Детальная диагностика

## 🛡️ Обработка ошибок

### Типы ошибок

#### DatabaseError
```go
err := postgres.NewDatabaseError("08006", "connection_failure")
err.WithOperation("connect")
err.WithQuery("SELECT * FROM users", userID)

// Проверка типа ошибки
if postgres.IsRetryable(err) {
    // Повторить операцию
}
```

#### ConnectionError
```go
err := postgres.NewConnectionError("connect", "timeout")
err.WithDuration("30s")
```

#### TimeoutError
```go
err := postgres.NewTimeoutError("query", "30s")
err.WithQuery("SELECT * FROM alerts WHERE created_at > $1", since)
```

### Retry механизм

```go
retryConfig := postgres.DefaultRetryConfig()
retryExecutor := postgres.NewRetryExecutor(retryConfig, logger)

err := retryExecutor.Execute(ctx, func() error {
    return pool.Connect(ctx)
})
```

### Circuit Breaker

```go
cb := postgres.NewCircuitBreaker(3, 10*time.Second)

err := cb.Call(func() error {
    return pool.Health(ctx)
})
```

## 📊 Мониторинг и метрики

### Доступные метрики

```go
stats := pool.Stats()
fmt.Printf("Active connections: %d\n", stats.ActiveConnections)
fmt.Printf("Idle connections: %d\n", stats.IdleConnections)
fmt.Printf("Total connections: %d\n", stats.TotalConnections)
fmt.Printf("Success rate: %.2f%%\n", pool.GetMetrics().GetSuccessRate())
```

### Health check endpoint

```go
if err := pool.Health(ctx); err != nil {
    log.Printf("Database unhealthy: %v", err)
} else {
    log.Println("Database healthy")
}
```

## 🔧 Расширенное использование

### Prepared statements

```go
// Подготовка statement
err := pool.PrepareStatement(ctx, "get_alert",
    "SELECT id, title FROM alerts WHERE id = $1")
if err != nil {
    log.Fatal("Failed to prepare statement:", err)
}

// Использование prepared statement
row := pool.QueryRow(ctx, "get_alert", alertID)
```

### Транзакции

```go
tx, err := pool.Begin(ctx)
if err != nil {
    log.Fatal("Failed to begin transaction:", err)
}
defer tx.Rollback(ctx)

// Выполнение операций в транзакции
_, err = tx.Exec(ctx, "INSERT INTO alerts (title) VALUES ($1)", "New Alert")
if err != nil {
    log.Fatal("Transaction failed:", err)
}

if err := tx.Commit(ctx); err != nil {
    log.Fatal("Failed to commit:", err)
}
```

### Context с таймаутами

```go
// Таймаут для конкретного запроса
queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

rows, err := pool.Query(queryCtx, "SELECT * FROM alerts")
```

## 🧪 Тестирование

### Unit тесты

```go
func TestPostgresPool_Connect(t *testing.T) {
    config := postgres.DefaultConfig()
    pool := postgres.NewPostgresPool(config, slog.Default())

    ctx := context.Background()
    err := pool.Connect(ctx)
    require.NoError(t, err)
    assert.True(t, pool.IsConnected())

    err = pool.Disconnect(ctx)
    assert.NoError(t, err)
}
```

### Integration тесты

```go
func TestPostgresPool_Query(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    config := loadTestConfig()
    pool := postgres.NewPostgresPool(config, slog.Default())

    ctx := context.Background()
    err := pool.Connect(ctx)
    require.NoError(t, err)
    defer pool.Disconnect(ctx)

    rows, err := pool.Query(ctx, "SELECT 1 as test")
    require.NoError(t, err)
    defer rows.Close()

    assert.True(t, rows.Next())
    var result int
    err = rows.Scan(&result)
    assert.NoError(t, err)
    assert.Equal(t, 1, result)
}
```

## 🚀 Производительность

### Рекомендуемые настройки

#### Для высоконагруженных приложений
```go
config := &postgres.PostgresConfig{
    MaxConns:            50,
    MinConns:            10,
    MaxConnLifetime:     30 * time.Minute,
    MaxConnIdleTime:     10 * time.Minute,
    HealthCheckPeriod:   15 * time.Second,
}
```

#### Для приложений с переменной нагрузкой
```go
config := &postgres.PostgresConfig{
    MaxConns:            20,
    MinConns:            2,
    MaxConnLifetime:     1 * time.Hour,
    MaxConnIdleTime:     5 * time.Minute,
    HealthCheckPeriod:   30 * time.Second,
}
```

## 🔒 Безопасность

### SQL Injection защита
Все методы автоматически используют prepared statements и parameterized queries:

```go
// ✅ Безопасно
pool.Query(ctx, "SELECT * FROM users WHERE id = $1", userID)

// ❌ Уязвимо
pool.Query(ctx, fmt.Sprintf("SELECT * FROM users WHERE id = %d", userID))
```

### SSL/TLS конфигурация
```go
config := &postgres.PostgresConfig{
    SSLMode: "verify-full", // Максимальная безопасность
    // SSLCert, SSLKey, SSLRootCert для клиентских сертификатов
}
```

## 📚 Дополнительные ресурсы

- [pgx Documentation](https://pkg.go.dev/github.com/jackc/pgx/v5)
- [PostgreSQL Connection Pooling](https://www.postgresql.org/docs/current/libpq-connect.html)

## 📄 Лицензия

См. `LICENSE` в корне репозитория (AGPL-3.0).
