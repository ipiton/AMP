# REPOSITORY-FLAPPING-TRANSITIONS-DRIFT — Spec

## Контракты (без изменений публичного API)

### `GetFlappingAlerts` — сигнатура остаётся прежней

```go
// go-app/internal/core/interfaces.go
GetFlappingAlerts(ctx context.Context, timeRange *core.TimeRange, threshold int) ([]*core.FlappingAlert, error)
```

```go
// go-app/internal/core/interfaces.go
type FlappingAlert struct {
    Fingerprint      string
    AlertName        string
    Namespace        *string
    TransitionCount  int
    FlappingScore    float64
    LastTransitionAt time.Time
}
```

Никаких изменений в публичных типах нет.

---

## Изменение 1 — SQL ORDER BY (тайбрейкер)

**Файл:** `go-app/internal/infrastructure/repository/postgres_history.go`  
**Строка:** ~558

### До

```sql
LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at) as prev_status
```

### После

```sql
LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at, id) as prev_status
```

**Обоснование:**  
Поле `id SERIAL PRIMARY KEY` автоинкрементируется в порядке вставки.
Добавление `id` как тайбрейкера даёт детерминированный порядок строк
с одинаковым `starts_at`, не меняя логику для строк с разными `starts_at`.

**Побочные эффекты:** нет. `id` доступен в подзапросе `state_changes`,
т.к. он входит в базовую таблицу `alerts`.

---

## Изменение 2 — Тест-фикстура

**Файл:** `go-app/internal/infrastructure/repository/postgres_history_test.go`  
**Функция:** `TestGetFlappingAlerts_MultipleTransitions` (строка 147)

### До

```go
// Insert a flapping alert (firing -> resolved -> firing -> resolved)
// 4 transitions
baseTime := time.Now().Add(-24 * time.Hour)
_, err := pool.Exec(context.Background(), `
    INSERT INTO alerts (fingerprint, alert_name, status, starts_at, created_at, labels)
    VALUES
    ('fp_flap', 'FlappingAlert', 'firing',   $1, $1,                         '{"namespace": "prod"}'),
    ('fp_flap', 'FlappingAlert', 'resolved', $1, $1 + INTERVAL '10 minutes', '{"namespace": "prod"}'),
    ('fp_flap', 'FlappingAlert', 'firing',   $1, $1 + INTERVAL '20 minutes', '{"namespace": "prod"}'),
    ('fp_flap', 'FlappingAlert', 'resolved', $1, $1 + INTERVAL '30 minutes', '{"namespace": "prod"}')
`, baseTime)
```

```go
if alerts[0].TransitionCount < 4 {
    t.Errorf("Expected at least 4 transitions, got %d", alerts[0].TransitionCount)
}
```

### После

```go
// Insert a flapping alert: 4 rows with distinct starts_at
// firing -> resolved -> firing -> resolved = 3 state transitions (N rows => N-1 transitions)
baseTime := time.Now().Add(-24 * time.Hour)
_, err := pool.Exec(context.Background(), `
    INSERT INTO alerts (fingerprint, alert_name, status, starts_at, created_at, labels)
    VALUES
    ('fp_flap', 'FlappingAlert', 'firing',   $1,                          $1,                         '{"namespace": "prod"}'),
    ('fp_flap', 'FlappingAlert', 'resolved', $1 + INTERVAL '10 minutes',  $1 + INTERVAL '10 minutes', '{"namespace": "prod"}'),
    ('fp_flap', 'FlappingAlert', 'firing',   $1 + INTERVAL '20 minutes',  $1 + INTERVAL '20 minutes', '{"namespace": "prod"}'),
    ('fp_flap', 'FlappingAlert', 'resolved', $1 + INTERVAL '30 minutes',  $1 + INTERVAL '30 minutes', '{"namespace": "prod"}')
`, baseTime)
```

```go
if alerts[0].TransitionCount < 3 {
    t.Errorf("Expected at least 3 transitions, got %d", alerts[0].TransitionCount)
}
```

**Обоснование изменений:**
1. `starts_at` теперь уникален для каждой строки — ORDER BY детерминирован.
2. Ожидание исправлено с `>= 4` на `>= 3`: 4 строки → 3 смены состояния.
3. Комментарий отражает реальную семантику: "N rows => N-1 transitions".
4. `created_at` теперь совпадает с `starts_at` — фикстура не вводит несогласованных данных.

---

## Архитектурные решения

### Решение 1: `id` как тайбрейкер, не `created_at`

`created_at` имеет default `CURRENT_TIMESTAMP` и может совпасть у строк,
вставленных в одном batch insert. `id` гарантированно уникален и строго
монотонен в рамках INSERT. Поэтому `ORDER BY starts_at, id` надёжнее
`ORDER BY starts_at, created_at`.

### Решение 2: не менять threshold в вызове

Тест вызывает `GetFlappingAlerts(..., threshold=3)`. После исправления
`starts_at` фикстура даёт 3 перехода, threshold=3 → алерт проходит фильтр.
Менять threshold не требуется.

### Решение 3: не менять другие тесты flapping

- `TestGetFlappingAlerts_NoStateTransitions` — корректен, изменений не требует.
- `TestGetFlappingAlerts_ThresholdFiltering` — использует `time.Now()` без
  offset для строк с разными `created_at` тоже имеет риск, но тест
  намеренно вставляет всего 3 строки с одним fingerprint с `status = firing`
  (нет смены состояния вовсе), поэтому ORDER BY стабилен по смыслу.

---

## Ожидаемый результат после патча

```
go test ./internal/infrastructure/repository/... -run TestGetFlappingAlerts -v -count=3
--- PASS: TestGetFlappingAlerts_NoStateTransitions (N.NNs)
--- PASS: TestGetFlappingAlerts_MultipleTransitions (N.NNs)
--- PASS: TestGetFlappingAlerts_ThresholdFiltering (N.NNs)
PASS
```

Запуск с `-count=3` верифицирует стабильность (отсутствие flakiness).
