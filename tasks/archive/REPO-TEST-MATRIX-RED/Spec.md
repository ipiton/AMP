# Spec: REPO-TEST-MATRIX-RED

**Status**: Closed as stabilization slice  
**Last Verified**: 2026-03-09
**Current Outcome**: duplicate-metrics / Redis-config / SQLite-test-driver / nil-logger / retryable-error groundwork landed; remaining logic-level failures explicitly split into narrower follow-up bugs instead of keeping this slice open indefinitely.

## Strategies

### 1. Duplicate Metrics (Panic)
**Problem**: `prometheus.MustRegister` вызывается повторно для тех же метрик в тестах.
**Solution**:
- В пакетах `publishing`, `repository`, `webhook` и `pkg/metrics` изменить инициализацию метрик так, чтобы можно было передавать кастомный реестр.
- В тестах использовать `prometheus.NewRegistry()`, чтобы каждый тест имел изолированные метрики.
- Или использовать `prometheus.Unregister` в `Cleanup` (менее надежно).
- **Preferred**: Внедрить зависимость от `prometheus.Registerer` в конструкторы.

### 2. Redis Configuration
**Problem**: "Failed to create Redis client: invalid cache configuration".
**Solution**:
- Проверить `go-app/internal/infrastructure/inhibition/integration_test.go`.
- Убедиться, что для тестов создается валидный `appconfig.Config` с заполненными параметрами Redis (даже если он не используется, парсер конфигурации может требовать валидные поля).

### 3. K8s Context Errors
**Problem**: Ожидается `TimeoutError`, но приходит `K8sError` с `context canceled`.
**Solution**:
- Обновить `go-app/internal/infrastructure/k8s/client_test.go`, чтобы он проверял наличие `context.Canceled` в цепочке ошибок или принимал `K8sError` как корректную обертку.

### 4. SQLite Driver
**Problem**: `sql: unknown driver "sqlite"`.
**Solution**:
- Добавить `_ "modernc.org/sqlite"` или аналогичный драйвер в `manager_test.go` или `common_test.go` в пакете `migrations`.

### 5. Publishing Assertion/Panic
**Problem**: `nil pointer dereference` в логгере и mismatch в текстах ошибок.
**Solution**:
- В `webhook_client.go` добавить проверку на `nil` логгер или инициализировать его дефолтным `slog.Default()`.
- Обновить тексты ожидаемых ошибок в `pagerduty_errors_test.go` и `rootly_errors_test.go`.

### 6. Telemetry Drift
**Problem**: `TestResponseWriter` ожидает 4, получает 0.
**Solution**:
- Изучить `pkg/telemetry/tracer_test.go`. Возможно, `ResponseWriter` не перехватывает вызовы `Write` или `WriteHeader` из-за изменений в интерфейсе или логике трассировки.

## Acceptance Criteria
- targeted package list этой задачи либо возвращает `PASS`, либо оставшийся red честно сужен до отдельного follow-up с явной ownership.
- Код тестов остается читаемым и следует паттернам проекта.
