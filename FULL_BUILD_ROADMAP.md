# 🗺️ Полная Сборка - Детальный План

**Цель**: Собрать полный `cmd/server` без ошибок
**Текущий статус**: pkg/core ✅ собирается
**Осталось**: 4 шага (3-4 часа работы)

---

## 📋 Шаг 1: Исправить Circular Import в pkg/configvalidator

### Проблема
```
pkg/configvalidator imports pkg/configvalidator/parser
pkg/configvalidator/parser imports pkg/configvalidator
→ Circular dependency!
```

### Причина
`parser/json_parser.go` использует типы из `pkg/configvalidator`:
- `validatorpkg.Error`
- `validatorpkg.Location`

А `validator.go` импортирует `parser`.

### Решение (3 варианта)

#### Вариант A: Вынести общие типы (РЕКОМЕНДУЕТСЯ) ⭐
**Время**: 15-20 минут
**Сложность**: Низкая

```bash
# 1. Создать pkg/configvalidator/types/types.go
mkdir -p go-app/pkg/configvalidator/types

# 2. Переместить Error, Location, Result в types/
cat > go-app/pkg/configvalidator/types/types.go << 'EOF'
package types

// Error represents validation error
type Error struct {
    Type       string
    Code       string
    Message    string
    Location   Location
    Suggestion string
}

// Location in config file
type Location struct {
    Line   int
    Column int
    File   string
}

// Result of validation
type Result struct {
    Valid  bool
    Errors []Error
}
EOF

# 3. Обновить импорты:
# - validator.go: import "types"
# - parser/*.go: import "types"
# - Заменить validatorpkg.Error → types.Error
```

**Файлы для изменения**:
- `pkg/configvalidator/validator.go` (~10 замен)
- `pkg/configvalidator/result.go` (перенести в types/)
- `pkg/configvalidator/parser/json_parser.go` (~15 замен)
- `pkg/configvalidator/parser/yaml_parser.go` (~10 замен)

#### Вариант B: Использовать интерфейсы
**Время**: 30 минут
**Сложность**: Средняя

Создать `pkg/configvalidator/interfaces.go` с интерфейсами, которые реализуют parser'ы.

#### Вариант C: Объединить пакеты
**Время**: 10 минут
**Сложность**: Низкая (но хуже архитектурно)

Перенести parser/*.go в pkg/configvalidator/ напрямую.

### Рекомендация
✅ **Вариант A** - правильная архитектура, легко расширять

---

## 📋 Шаг 2: Реализовать BusinessMetrics Методы

### Проблема
```go
// Нужны методы в BusinessMetrics:
m.metrics.IncActiveGroups()          // +1
m.metrics.DecActiveGroups()          // +2
m.metrics.RecordGroupOperation()     // +3
m.metrics.RecordGroupOperationDuration() // +4
m.metrics.RecordGroupsCleanedUp()    // +5
m.metrics.RecordGroupsRestored()     // +6
// ... ещё 4-5 методов
```

### Решение
**Время**: 45-60 минут
**Сложность**: Средняя

```go
// go-app/pkg/metrics/metrics.go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// BusinessMetrics holds all business-level metrics
type BusinessMetrics struct {
    // Grouping metrics
    activeGroups      prometheus.Gauge
    groupOperations   *prometheus.CounterVec
    groupDuration     *prometheus.HistogramVec
    groupsCleanedUp   prometheus.Counter
    groupsRestored    prometheus.Counter

    // Silence metrics
    SilenceOperationsTotal  *prometheus.CounterVec
    SilenceValidationErrors *prometheus.CounterVec
    SilenceCacheHitsTotal   *prometheus.CounterVec
    SilenceCacheMissesTotal *prometheus.CounterVec

    // Publishing metrics
    publishOperations *prometheus.CounterVec
    publishDuration   *prometheus.HistogramVec
}

// NewBusinessMetrics creates and registers all metrics
func NewBusinessMetrics() *BusinessMetrics {
    m := &BusinessMetrics{
        // Grouping
        activeGroups: promauto.NewGauge(prometheus.GaugeOpts{
            Name: "amp_grouping_active_groups",
            Help: "Number of active alert groups",
        }),
        groupOperations: promauto.NewCounterVec(
            prometheus.CounterOpts{
                Name: "amp_grouping_operations_total",
                Help: "Total grouping operations",
            },
            []string{"operation", "status"},
        ),
        groupDuration: promauto.NewHistogramVec(
            prometheus.HistogramOpts{
                Name: "amp_grouping_operation_duration_seconds",
                Help: "Grouping operation duration",
            },
            []string{"operation"},
        ),
        groupsCleanedUp: promauto.NewCounter(prometheus.CounterOpts{
            Name: "amp_grouping_cleaned_up_total",
            Help: "Total groups cleaned up",
        }),
        groupsRestored: promauto.NewCounter(prometheus.CounterOpts{
            Name: "amp_grouping_restored_total",
            Help: "Total groups restored",
        }),

        // Silence
        SilenceOperationsTotal: promauto.NewCounterVec(
            prometheus.CounterOpts{
                Name: "amp_silence_operations_total",
                Help: "Total silence operations",
            },
            []string{"operation", "status"},
        ),
        // ... остальные
    }
    return m
}

// Grouping methods
func (m *BusinessMetrics) IncActiveGroups() {
    m.activeGroups.Inc()
}

func (m *BusinessMetrics) DecActiveGroups() {
    m.activeGroups.Dec()
}

func (m *BusinessMetrics) RecordGroupOperation(op string, status string) {
    m.groupOperations.WithLabelValues(op, status).Inc()
}

func (m *BusinessMetrics) RecordGroupOperationDuration(op string, duration float64) {
    m.groupDuration.WithLabelValues(op).Observe(duration)
}

func (m *BusinessMetrics) RecordGroupsCleanedUp(count int) {
    m.groupsCleanedUp.Add(float64(count))
}

func (m *BusinessMetrics) RecordGroupsRestored(count int) {
    m.groupsRestored.Add(float64(count))
}
```

### Методы для реализации (полный список)

#### Grouping (6 методов)
1. `IncActiveGroups()` - Увеличить счётчик активных групп
2. `DecActiveGroups()` - Уменьшить счётчик
3. `RecordGroupOperation(op, status)` - Записать операцию
4. `RecordGroupOperationDuration(op, duration)` - Записать время
5. `RecordGroupsCleanedUp(count)` - Групп очищено
6. `RecordGroupsRestored(count)` - Групп восстановлено

#### Publishing (4 метода)
7. `DefaultRegistry` (переменная) - Registry по умолчанию
8. `MetricsRegistry` (интерфейс) - Интерфейс registry
9. `RegisterPublishMetrics()` - Регистрация метрик
10. `RecordPublishOperation(target, status)` - Операция публикации

### Файлы для создания/изменения
- `pkg/metrics/metrics.go` - Основной файл (~300 LOC)
- `pkg/metrics/registry.go` - Registry (~100 LOC)
- `pkg/metrics/doc.go` - Документация

---

## 📋 Шаг 3: Добавить Resilience Patterns

### Проблема
```go
// internal/infrastructure/llm/client.go
undefined: resilience.RetryPolicy
undefined: resilience.WithRetryFunc
```

### Решение
**Время**: 30-40 минут
**Сложность**: Средняя

```go
// go-app/internal/core/resilience/retry.go
package resilience

import (
    "context"
    "time"
)

// RetryPolicy defines retry behavior
type RetryPolicy struct {
    MaxAttempts int
    InitialDelay time.Duration
    MaxDelay time.Duration
    Multiplier float64
    ShouldRetry func(error) bool
}

// DefaultRetryPolicy returns sensible defaults
func DefaultRetryPolicy() *RetryPolicy {
    return &RetryPolicy{
        MaxAttempts:  3,
        InitialDelay: 100 * time.Millisecond,
        MaxDelay:     5 * time.Second,
        Multiplier:   2.0,
        ShouldRetry:  IsRetryableError,
    }
}

// WithRetryFunc executes fn with retry logic
func WithRetryFunc(ctx context.Context, policy *RetryPolicy, fn func() error) error {
    var lastErr error
    delay := policy.InitialDelay

    for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
        // Try operation
        err := fn()
        if err == nil {
            return nil // Success!
        }

        lastErr = err

        // Check if should retry
        if !policy.ShouldRetry(err) {
            return err // Not retryable
        }

        // Check context
        if ctx.Err() != nil {
            return ctx.Err()
        }

        // Last attempt?
        if attempt == policy.MaxAttempts-1 {
            break
        }

        // Wait with exponential backoff
        select {
        case <-time.After(delay):
            // Continue
        case <-ctx.Done():
            return ctx.Err()
        }

        // Increase delay
        delay = time.Duration(float64(delay) * policy.Multiplier)
        if delay > policy.MaxDelay {
            delay = policy.MaxDelay
        }
    }

    return lastErr
}

// IsRetryableError checks if error is transient
func IsRetryableError(err error) bool {
    if err == nil {
        return false
    }

    // Network errors - retry
    // Timeout errors - retry
    // 5xx HTTP errors - retry
    // Other errors - don't retry

    errStr := err.Error()
    retryable := []string{
        "timeout",
        "connection refused",
        "connection reset",
        "temporary failure",
        "503",
        "504",
        "502",
    }

    for _, pattern := range retryable {
        if strings.Contains(errStr, pattern) {
            return true
        }
    }

    return false
}

// CircuitBreaker для advanced resilience (опционально)
type CircuitBreaker struct {
    maxFailures int
    timeout time.Duration
    // ... state
}
```

### Дополнительные паттерны (опционально)
- `Timeout` - Timeout wrapper
- `Bulkhead` - Resource isolation
- `CircuitBreaker` - Prevent cascading failures
- `RateLimiter` - Request limiting

### Файлы для создания
- `internal/core/resilience/retry.go` (~200 LOC)
- `internal/core/resilience/errors.go` (~50 LOC)
- `internal/core/resilience/doc.go` (~20 LOC)

---

## 📋 Шаг 4: Собрать Полный cmd/server

### После Шагов 1-3

**Время**: 10-15 минут (если всё правильно)
**Сложность**: Низкая (тестирование)

```bash
# 1. Проверить что всё на месте
cd go-app

# 2. Clean build
go clean -cache

# 3. Update dependencies
go mod tidy

# 4. Try build
go build -v -o ../bin/alertmanager-plus-plus ./cmd/server

# 5. Check binary
ls -lh ../bin/
file ../bin/alertmanager-plus-plus

# 6. Quick test (если запускается)
../bin/alertmanager-plus-plus --help
```

### Возможные Проблемы

#### Проблема 1: Ещё остались ошибки
**Решение**:
- Читаем ошибку компиляции
- Ищем недостающий метод/тип
- Добавляем stub или реализацию

#### Проблема 2: Infrastructure зависимости
**Решение**:
```bash
# Временно закомментировать проблемные части:
# - LLM client (если resilience не хватает)
# - Publishing discovery (если metrics не хватает)
# - Grouping manager (если metrics не хватает)
```

#### Проблема 3: Слишком много ошибок
**Решение**: Упростить main.go - создать minimal версию:
```go
// cmd/server/main_minimal.go
// Только базовые HTTP endpoints без всех фич
```

---

## 📊 Сводная Таблица

| Шаг | Время | Сложность | Приоритет | Файлов |
|-----|-------|-----------|-----------|--------|
| 1. Circular import | 15-20 мин | Низкая | Средний | 4-5 |
| 2. BusinessMetrics | 45-60 мин | Средняя | Высокий | 2-3 |
| 3. Resilience | 30-40 мин | Средняя | Высокий | 2-3 |
| 4. Full build | 10-15 мин | Низкая | Высокий | 0 |
| **ИТОГО** | **~2-2.5 часа** | **Средняя** | - | **8-11** |

---

## 🎯 Рекомендуемая Последовательность

### Вариант А: Быстрый путь (1.5 часа)
Пропустить configvalidator, сосредоточиться на main build:
1. ✅ Resilience patterns (30 мин)
2. ✅ BusinessMetrics core methods (45 мин)
3. ✅ Try build (15 мин)
4. 📋 Configvalidator позже (опционально)

### Вариант Б: Полный путь (2.5 часа)
Всё по порядку:
1. ✅ Circular import fix (20 мин)
2. ✅ BusinessMetrics full (60 мин)
3. ✅ Resilience patterns (40 мин)
4. ✅ Full build + testing (30 мин)

### Вариант В: Минимальный (30 минут)
Создать stubs для всего:
1. ✅ Stub BusinessMetrics (~5 методов пустых)
2. ✅ Stub Resilience (простой retry)
3. ✅ Try build
4. ✅ Если не работает - закомментировать проблемные части

---

## 🚀 Готовы Начать?

Выберите вариант:
- **А** - Хочу быстро собрать (1.5 часа, без configvalidator)
- **Б** - Хочу полную реализацию (2.5 часа, всё правильно)
- **В** - Хочу минимум для запуска (30 минут, stubs)

Или скажите "стоп" и оставим только pkg/core готовым! 😊

---

**Текущий статус**: pkg/core ✅ готов, остальное опционально
**Рекомендация**: Вариант А (быстро собрать main.go без configvalidator)
