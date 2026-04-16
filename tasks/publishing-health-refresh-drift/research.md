# PUBLISHING-HEALTH-REFRESH-DRIFT — Research

## Затронутые файлы

```
go-app/internal/business/publishing/
  health.go                      # интерфейс HealthMonitor + HealthConfig
  health_impl.go                 # DefaultHealthMonitor (Start/Stop/GetHealth/GetStats)
  health_cache.go                # healthStatusCache (Get/Set/GetAll/Delete/Clear)
  health_checker.go              # httpConnectivityTest, checkSingleTarget, checkTargetWithRetry
  health_errors.go               # ErrorType, classifyNetworkError, classifyHTTPError, sanitizeErrorMessage
  health_worker.go               # checkAllTargets, recheckUnhealthyTargets
  health_status.go               # HealthStatus, processHealthCheckResult, calculateAggregateStats
  health_test.go                 # тесты HealthMonitor
  health_errors_test.go          # тесты error classification и sanitize
  health_checker_test.go         # тесты httpConnectivityTest
  refresh_manager.go             # интерфейс RefreshManager + RefreshConfig
  refresh_manager_impl.go        # DefaultRefreshManager
  refresh_worker.go              # runBackgroundWorker, executeRefresh, refreshWithRetry
  refresh_retry.go               # retry logic с exponential backoff
  refresh_errors.go              # ErrAlreadyStarted, ErrNotStarted, ErrShutdownTimeout
  refresh_worker_test.go         # тесты refresh worker
  refresh_manager_impl_test.go   # тесты DefaultRefreshManager
  stats_collector.go             # PublishingMetricsCollector, MetricsSnapshot
  stats_collector_health.go      # HealthMetricsCollector
  discovery.go                   # интерфейс TargetDiscoveryManager
  discovery_cache.go             # targetCache (atomic Set, Get, List)
```

## Ключевые структуры

### healthStatusCache (health_cache.go:48)
```go
type healthStatusCache struct {
    mu     sync.RWMutex
    data   map[string]*TargetHealthStatus
    maxAge time.Duration  // 10 * time.Minute
}
```

Поведение `Get()` (строки 102–117):
- Если `time.Since(status.LastCheck) > maxAge` → возвращает `nil, false`
- Иначе возвращает статус, даже если target уже удалён из discovery

Поведение `GetAll()` (строки 235–254):
- Итерирует все записи, пропуская stale (`> maxAge`)
- **Не проверяет**, существует ли target в discovery — orphaned entries включаются

### DefaultHealthMonitor (health_impl.go:53)
```go
type DefaultHealthMonitor struct {
    discoveryMgr TargetDiscoveryManager
    httpClient   *http.Client
    config       HealthConfig
    statusCache  *healthStatusCache
    running      atomic.Bool
    cancel       context.CancelFunc
    wg           sync.WaitGroup
    logger       *slog.Logger
    metrics      *v2.PublishingMetrics
}
```

`GetHealth()` (строки 217–235):
```go
targets := m.discoveryMgr.ListTargets()  // актуальный список
for _, target := range targets {
    if status, ok := m.statusCache.Get(target.Name); ok {
        statuses = append(statuses, *status)
    } else {
        status := initializeHealthStatus(...)  // HealthStatusUnknown
        statuses = append(statuses, *status)
    }
}
// Orphaned entries в statusCache ИГНОРИРУЮТСЯ здесь (не в targets)
// НО GetStats() → statusCache.GetAll() включает orphaned entries!
```

`GetStats()` (строки 282–289):
```go
allStatuses := m.statusCache.GetAll()       // ← включает orphaned!
stats := calculateAggregateStats(allStatuses)
return stats, nil
```

**Несоответствие**: `GetHealth()` фильтрует по discovery targets, `GetStats()` — нет.

### DefaultRefreshManager (refresh_manager_impl.go:59)

`updateState()` (строки 334–359) — вызывается после каждого refresh:
```go
func (m *DefaultRefreshManager) updateState(
    state RefreshState, lastRefresh time.Time,
    lastError error, targetStats targetStats, duration time.Duration,
) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.state = state
    // ... обновляет внутренние поля
    // НЕТ: уведомления HealthMonitor о смене списка targets
}
```

`executeRefresh()` в refresh_worker.go вызывает `m.discovery.DiscoverTargets(ctx)` →
результат идёт в `targetCache.Set(newTargets)`. HealthMonitor не знает об этом.

### checkAllTargets (health_worker.go:49)

```go
func (m *DefaultHealthMonitor) checkAllTargets(ctx context.Context, checkType CheckType) error {
    targets := m.discoveryMgr.ListTargets()  // T=0: snapshot

    // ... фильтрация, создание goroutine pool

    for _, target := range enabledTargets {
        wg.Add(1)
        go func(t *core.PublishingTarget) {
            // T=0+N: checks выполняются async
            // В это время refresh мог обновить targetCache
            result := checkTargetWithRetry(ctx, t, checkType, m.httpClient, m.config)
            results <- result
        }(target)
    }
    // результаты пишутся в statusCache для возможно-удалённых targets
}
```

### sanitizeErrorMessage (health_errors.go:166)

Текущая реализация:
```go
patterns := []struct {
    prefix string
    suffix string
}{
    {"Authorization:", "\n"},
    {"X-API-Key:", "\n"},
    {"Bearer ", " "},
    {"token=", "&"},
    {"api_key=", "&"},
}
// ...
sanitized = sanitized[:start] + " [REDACTED]" + sanitized[start+end:]
```

Тест ожидает (health_errors_test.go:244):
```
Input:  "URL: https://api.example.com?token=secret123&other=value"
Want:   "URL: https://api.example.com?token= [REDACTED]&other=value"
```

Проблема: `prefix = "token="`, `start` указывает ПОСЛЕ `=`, вставка `" [REDACTED]"` даёт
`token= [REDACTED]`. Это **совпадает** с want-строкой — но только при наличии suffix `&`.
При отсутствии suffix (`end == -1`) вставляется `" [REDACTED]"` без suffix — нужно
верифицировать с реальным запуском теста.

### classifyNetworkError — gap (health_errors.go:64)

```go
// DNS
if strings.Contains(errStr, "no such host") ||
    strings.Contains(errStr, "dns") {
    return ErrorTypeDNS
}
```

Проблема: `strings.Contains(errStr, "dns")` — case sensitive. Некоторые ОС дают
`DNS` или `DNS resolution failed` с заглавной. Тест `TestClassifyNetworkError_DNS`
с `errors.New("dns resolution failed")` — в нижнем регистре, должно матчиться.

Но `classifyHTTPError` аналогично:
```go
if strings.Contains(errStr, "no such host") ||
    strings.Contains(errStr, "dns") {
    return ErrorTypeDNS
}
```

Тест `TestClassifyHTTPError_DNS`:
```go
err := errors.New("Get https://invalid-host: no such host")
// "no such host" присутствует — должно матчиться как ErrorTypeDNS
```

### TestHealthMonitor_DegradedState — timing sensitivity

```go
config.CheckInterval = 100 * time.Millisecond
config.FailureThreshold = 3
// Start monitor
time.Sleep(300 * time.Millisecond)  // ← 3 * CheckInterval
// Verify degraded or unhealthy
```

При WarmupDelay (по умолчанию 10s в DefaultHealthConfig) + CheckInterval 100ms:
- 10s warmup → 1 check → 100ms ticker → 2 check → ...
- За 300ms без учёта WarmupDelay: только ~3 checks
- Но `DefaultHealthConfig().WarmupDelay` может быть 10s, тогда за 300ms = 0 checks

Тест создаёт monitor с `DefaultHealthConfig()` (не overrides WarmupDelay):
```go
monitor, err := NewHealthMonitor(discoveryMgr, config, slog.Default(), nil)
```
Где `config.CheckInterval = 100ms, config.FailureThreshold = 3`, но `config.WarmupDelay`
берётся из `DefaultHealthConfig()` и может быть 10s.

### TestHealthMonitor в createTestHealthMonitor

Helper `createTestHealthMonitor` правильно override оба:
```go
config.CheckInterval = 100 * time.Millisecond
config.WarmupDelay = 10 * time.Millisecond
```

Но `TestHealthMonitor_DegradedState` создаёт monitor напрямую, не через helper,
и override делает только `CheckInterval`. WarmupDelay не задан → Default.

## Metric count assertions

`GetStats()` вызывает `statusCache.GetAll()` — возвращает все non-stale entries.
После теста `TestHealthMonitor_GetStats`, cache может содержать entries из
предыдущих subtests (shared testMetrics via `testMetricsOnce sync.Once`).

Проблема: `testMetrics` — singleton через `testMetricsOnce.Do`. Если предыдущий тест
записал статусы в statusCache или зарегистрировал Prometheus метрики — они
accumulate. Это объясняет "metric-count expectations" failures.

## Существующие test helpers

`health_test_utils.go` содержит:
- `TestHealthDiscoveryManager` — mock discovery manager с `SetTargets()`
- `NewTestHealthDiscoveryManager()` — конструктор
- Реализует `TargetDiscoveryManager` interface

`refresh_test_utils.go` содержит аналогичные helpers для refresh.

## Что работает правильно

- `healthStatusCache` — thread-safe, корректная RWMutex логика
- `targetCache` в discovery — atomic Set (copy-on-write)
- `DefaultRefreshManager` — single-flight pattern через `m.inProgress bool`
- `checkTargetWithRetry` — retry только на transient errors

## Что нужно исправить

| Проблема | Файл | Строки |
|----------|------|--------|
| `GetStats()` использует `statusCache.GetAll()` вместо discovery-filtered | `health_impl.go` | 282–289 |
| Нет invalidation при refresh | `refresh_manager_impl.go` + `health_impl.go` | — |
| WarmupDelay не override в timing test | `health_test.go` | ~484 |
| Возможный gap в sanitize — нужна верификация | `health_errors.go` | 166+ |
| Wrapped errors в classifyHTTPError | `health_errors.go` | 119+ |
| Shared testMetrics между тестами | `health_test.go` | 559+ |
