# PUBLISHING-HEALTH-REFRESH-DRIFT — Spec

## Архитектурное решение

Минимальные точечные исправления внутри пакета `internal/business/publishing`.
Никаких изменений публичных интерфейсов. Никакой новой архитектуры.

Четыре независимых fix-группы:

1. **GetStats drift** — изменить `GetStats()` чтобы использовать discovery-filtered данные
2. **WarmupDelay в timing-тестах** — добавить override в проблемный тест
3. **sanitizeErrorMessage** — верифицировать и исправить если нужно
4. **Error classification** — добавить case-insensitive или unwrap для wrapped errors

---

## Fix 1: GetStats() — синхронизация с discovery state

### Проблема

`GetStats()` в `health_impl.go` использует `statusCache.GetAll()` напрямую:

```go
func (m *DefaultHealthMonitor) GetStats(ctx context.Context) (*HealthStats, error) {
    allStatuses := m.statusCache.GetAll()           // ← orphaned entries included
    stats := calculateAggregateStats(allStatuses)
    return stats, nil
}
```

`statusCache.GetAll()` возвращает все non-stale entries, включая записи для targets,
которые уже удалены из discovery (orphaned entries).

`GetHealth()` при этом корректно фильтрует по `discoveryMgr.ListTargets()`.

### Исправление

В `health_impl.go`, метод `GetStats()` — использовать тот же подход что и `GetHealth()`:
получать актуальный список из discovery, затем брать статусы только для них.

```go
// БЫЛО:
func (m *DefaultHealthMonitor) GetStats(ctx context.Context) (*HealthStats, error) {
    allStatuses := m.statusCache.GetAll()
    stats := calculateAggregateStats(allStatuses)
    return stats, nil
}

// СТАЛО:
func (m *DefaultHealthMonitor) GetStats(ctx context.Context) (*HealthStats, error) {
    // Фильтруем по актуальному discovery state (то же что GetHealth)
    targets := m.discoveryMgr.ListTargets()
    statuses := make([]TargetHealthStatus, 0, len(targets))
    for _, target := range targets {
        if status, ok := m.statusCache.Get(target.Name); ok {
            statuses = append(statuses, *status)
        } else {
            status := initializeHealthStatus(target.Name, target.Type, target.Enabled)
            statuses = append(statuses, *status)
        }
    }
    stats := calculateAggregateStats(statuses)
    return stats, nil
}
```

**Эффект**: `GetStats().TotalTargets` теперь совпадает с `len(GetHealth())`.
Orphaned entries не влияют на агрегированную статистику.

**Трейдофф**: `GetStats()` теперь чуть медленнее (два вызова вместо одного).
При 100 targets: +~1µs — приемлемо.

---

## Fix 2: WarmupDelay в timing-sensitive тестах

### Проблема

`TestHealthMonitor_DegradedState` в `health_test.go` создаёт monitor с
`DefaultHealthConfig()` и override только `CheckInterval` и `FailureThreshold`.
`WarmupDelay` остаётся дефолтным — может быть 10s, тогда за `time.Sleep(300ms)` = 0 checks.

```go
// health_test.go ~484
config := DefaultHealthConfig()
config.CheckInterval = 100 * time.Millisecond
config.FailureThreshold = 3

monitor, err := NewHealthMonitor(discoveryMgr, config, slog.Default(), nil)
// ...
time.Sleep(300 * time.Millisecond)
```

### Исправление

Добавить override `WarmupDelay`:

```go
config := DefaultHealthConfig()
config.CheckInterval = 100 * time.Millisecond
config.WarmupDelay = 10 * time.Millisecond   // ← добавить
config.FailureThreshold = 3
```

Аналогично проверить `TestHealthMonitor_ConcurrentChecks` — там `DefaultHealthConfig()`
без WarmupDelay override, но тест не делает `time.Sleep`, поэтому не ломается.

---

## Fix 3: sanitizeErrorMessage — верификация и исправление

### Текущая реализация

```go
// health_errors.go:177-195
for _, p := range patterns {
    if idx := strings.Index(sanitized, p.prefix); idx != -1 {
        start := idx + len(p.prefix)
        end := strings.Index(sanitized[start:], p.suffix)
        if end == -1 {
            sanitized = sanitized[:start] + " [REDACTED]"
        } else {
            sanitized = sanitized[:start] + " [REDACTED]" + sanitized[start+end:]
        }
    }
}
```

### Анализ тест-случаев

**Case "Bearer token"** (строка 237):
```
Input: "Auth error: Bearer token123 is invalid"
Want:  "Auth error: Bearer [REDACTED] is invalid"
Pattern: prefix="Bearer ", suffix=" "
```
- `idx` = позиция "Bearer "
- `start` = после "Bearer " (указывает на "token123...")
- `sanitized[start:]` = "token123 is invalid"
- `end` = index of " " = 8 (перед " is")
- Результат: `"Auth error: Bearer " + " [REDACTED]" + " is invalid"`
         = `"Auth error: Bearer  [REDACTED] is invalid"` ← **двойной пробел!**
- Want: `"Auth error: Bearer [REDACTED] is invalid"` ← одинарный пробел

**Проблема**: prefix `"Bearer "` включает trailing space, но вставка начинается с `" [REDACTED]"` → двойной пробел.

**Исправление**: убрать leading space из `" [REDACTED]"` → `"[REDACTED]"`:

```go
// БЫЛО:
sanitized = sanitized[:start] + " [REDACTED]"
// ...
sanitized = sanitized[:start] + " [REDACTED]" + sanitized[start+end:]

// СТАЛО:
sanitized = sanitized[:start] + "[REDACTED]"
// ...
sanitized = sanitized[:start] + "[REDACTED]" + sanitized[start+end:]
```

Пересчёт всех cases после исправления:

| Case | Input | Want (тест) | Результат СТАЛО |
|------|-------|-------------|-----------------|
| Authorization | `Authorization: Bearer secret123\n` | `Authorization: [REDACTED]\nother` | `Authorization: [REDACTED]\nother` ✓ |
| Bearer token | `Bearer token123 is` | `Bearer [REDACTED] is` | `Bearer [REDACTED] is` ✓ |
| token= | `token=secret123&` | `token= [REDACTED]&` | — |

**Проблема с `token=`**:
- prefix = `"token="`, start указывает после `=`
- Результат СТАЛО: `token=[REDACTED]&`
- Want в тесте: `token= [REDACTED]&` ← пробел между `=` и `[REDACTED]`

Wait, перечитаем тест:
```go
want: "URL: https://api.example.com?token= [REDACTED]&other=value",
```
Пробел ПЕРЕД `[REDACTED]` в want — это пробел внутри значения? Или это артефакт?

На самом деле в оригинале prefix = `"token="`, input = `"token=secret123&"`.
После исправления: start после `=`, вставляем `[REDACTED]` без пробела → `token=[REDACTED]&`.
Тест хочет `token= [REDACTED]&` — с пробелом.

**Вывод**: Нужно изменить тестовые want-строки, чтобы они отражали корректное поведение
(без лишних пробелов). ИЛИ: Оставить `" [REDACTED]"` и исправить only Bearer case.

**Предпочтительный вариант**: Исправить паттерн для `Bearer` — убрать trailing space из prefix,
добавить его в replacement:

```go
// Паттерны с явным разделителем
{"Bearer ", " "},  // prefix включает space
// Замена: start указывает ПОСЛЕ space, вставляем [REDACTED]
// "Bearer " + start → "Bearer [REDACTED] ..."
```

Если `start` указывает ПОСЛЕ `"Bearer "` (т.е. прямо на "token123"), то:
```
sanitized[:start] = "Auth error: Bearer "
+ "[REDACTED]" + sanitized[start+end:] = " is invalid"
→ "Auth error: Bearer [REDACTED] is invalid" ✓
```

Нужно только убрать leading space из replacement. Для `token=` и `api_key=`:
```
sanitized[:start] = "...?token="
+ "[REDACTED]" + "&other=value"
→ "...?token=[REDACTED]&other=value"
```

Тест ожидает `token= [REDACTED]` — это **неверное** ожидание в тесте (лишний пробел).
Нужно исправить want в тесте.

### Итоговое решение Fix 3

1. В `health_errors.go` убрать leading space: `"[REDACTED]"` вместо `" [REDACTED]"`
2. В `health_errors_test.go` исправить want для `token=` и `api_key=` cases:
   - `"token= [REDACTED]&"` → `"token=[REDACTED]&"`
   - `"api_key= [REDACTED]&"` → `"api_key=[REDACTED]&"`

---

## Fix 4: Error classification — улучшение надёжности

### Проблема с wrapped errors в classifyHTTPError

Go HTTP клиент часто возвращает wrapped errors через `fmt.Errorf("...: %w", err)`.
`strings.Contains(err.Error())` работает только на строковом представлении, не на
wrapped chain. Для classifyHTTPError это нормально (работает со string matching),
но нужно проверить edge cases.

### Проверка текущих тестов

`TestClassifyHTTPError_DNS`:
```go
err := errors.New("Get https://invalid-host: no such host")
```
`classifyHTTPError` → `strings.Contains(errStr, "no such host")` → `ErrorTypeDNS` ✓

`TestClassifyNetworkError_DNS` с `"dns resolution failed"`:
```go
err: errors.New("dns resolution failed"),
want: ErrorTypeDNS,
```
`classifyNetworkError` → `strings.Contains(errStr, "dns")` → `ErrorTypeDNS` ✓
(lowercase "dns" матчит)

**Вывод**: String-based matching для тестовых cases работает. Реальная проблема может
быть только при production errors с разным регистром или wrapped chains.

Если тесты зелёные без изменений этих функций — не трогаем.

---

## Fix 5: Shared testMetrics — изоляция

### Проблема

```go
// health_test.go:559-572
var (
    testMetrics     *v2.PublishingMetrics
    testMetricsOnce sync.Once
)

func getTestMetrics(t *testing.T) *v2.PublishingMetrics {
    testMetricsOnce.Do(func() {
        testMetrics = v2.NewPublishingMetrics(prometheus.NewRegistry())
    })
    return testMetrics
}
```

Prometheus counters/gauges в `testMetrics` аккумулируются через тесты.
Если тест проверяет конкретное число вызовов метрик — получает завышенный count.

### Исправление

Каждый тест, проверяющий metric counts, должен создавать свой registry:

```go
// В тестах где важен metric count:
promReg := prometheus.NewRegistry()
metrics := v2.NewPublishingMetrics(promReg)
// Создавать monitor с этим metrics, не с getTestMetrics(t)
```

Для тестов, которым metric count не важен — `getTestMetrics(t)` остаётся.

---

## Контракты (не изменяются)

### HealthMonitor interface (health.go)
```go
type HealthMonitor interface {
    Start() error
    Stop(timeout time.Duration) error
    GetHealth(ctx context.Context) ([]TargetHealthStatus, error)
    GetHealthByName(ctx context.Context, targetName string) (*TargetHealthStatus, error)
    CheckNow(ctx context.Context, targetName string) (*TargetHealthStatus, error)
    GetStats(ctx context.Context) (*HealthStats, error)
}
```
Контракт `GetStats` не меняется — возвращаемый тип `*HealthStats` тот же.
Меняется только семантика: данные теперь filtered by discovery state.

### RefreshManager interface (refresh_manager.go)
Не изменяется.

### TargetDiscoveryManager interface (discovery.go)
Не изменяется.

### healthStatusCache
Не изменяется (private type, только меняется вызывающий код в health_impl.go).

---

## Архитектурные решения

**Решение: не добавлять callback/notification между Refresh и Health.**

Альтернатива — добавить `OnTargetsChanged(func([]*core.PublishingTarget))` в
`TargetDiscoveryManager` или callback в `RefreshManager`. Это требует изменения
интерфейсов и увеличивает coupling.

Выбранный подход: `GetStats()` читает discovery state при каждом вызове (O(n) lookup).
Это идемпотентно, thread-safe, и не требует синхронизации между компонентами.

**Почему GetStats читает discovery, а не инвалидирует cache:**
- Инвалидация требует события (callback от refresh)
- Cache инвалидация добавляет сложность (что если callback не дошёл?)
- Lazy filtering при чтении проще и надёжнее для данного случая

**Orphaned entries в statusCache не удаляются:**
- Запись в statusCache не вредна пока `GetHealth()` и `GetStats()` её не видят
- Удаление потребовало бы event от refresh → добавляет coupling
- Через 10m запись станет stale и GetAll() её пропустит
- Trade-off принят: небольшая memory утечка (bounded by 10m TTL) vs простота

---

## Файлы для изменения

| Файл | Изменение |
|------|-----------|
| `health_impl.go` | Fix `GetStats()`: фильтровать по discovery state |
| `health_test.go` | Fix WarmupDelay в `TestHealthMonitor_DegradedState` |
| `health_errors.go` | Fix `sanitizeErrorMessage`: убрать leading space из replacement |
| `health_errors_test.go` | Fix want strings для `token=` и `api_key=` cases |

Дополнительно (если metric count assertions падают):
| `health_test.go` | Изолировать prometheus registry в тестах с metric count проверками |
