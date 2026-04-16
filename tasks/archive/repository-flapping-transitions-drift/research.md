# REPOSITORY-FLAPPING-TRANSITIONS-DRIFT — Research

## Ключевые файлы

| Файл | Роль |
|------|------|
| `go-app/internal/infrastructure/repository/postgres_history.go` | Реализация `GetFlappingAlerts` (строки 517–624) |
| `go-app/internal/infrastructure/repository/postgres_history_test.go` | Тесты, включая падающий `TestGetFlappingAlerts_MultipleTransitions` (строки 147–185) |
| `go-app/internal/core/interfaces.go` | Определение `FlappingAlert`, `AlertHistoryRepository` |

---

## Анализ SQL-запроса `GetFlappingAlerts`

### Текущий запрос (строки 550–583)

```sql
WITH state_changes AS (
    SELECT
        fingerprint,
        alert_name,
        labels->>'namespace' as namespace,
        status,
        starts_at,
        LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at) as prev_status
    FROM alerts
    <WHERE clause>
),
transition_counts AS (
    SELECT
        fingerprint,
        alert_name,
        namespace,
        COUNT(*) FILTER (WHERE status != prev_status) as transition_count,
        MAX(starts_at) as last_transition_at
    FROM state_changes
    WHERE prev_status IS NOT NULL
    GROUP BY fingerprint, alert_name, namespace
)
SELECT
    fingerprint,
    alert_name,
    namespace,
    transition_count,
    CAST(transition_count AS FLOAT) / EXTRACT(EPOCH FROM (NOW() - last_transition_at)) * 3600 as flapping_score,
    last_transition_at
FROM transition_counts
WHERE transition_count >= $N
ORDER BY flapping_score DESC
LIMIT 50
```

### Семантика подсчёта переходов

`LAG(status) OVER (... ORDER BY starts_at)` возвращает статус предыдущей
строки для каждого `fingerprint`, упорядоченной по `starts_at`. Для строки с
`prev_status IS NULL` (первая строка группы) переход не считается.

Для **N строк** с чередующимися статусами максимальное число переходов = **N − 1**.

Пример (4 строки, различный `starts_at`):

| `starts_at` | `status`   | `prev_status` | `status != prev_status` |
|-------------|-----------|---------------|------------------------|
| T+0         | firing    | NULL          | (исключено)            |
| T+10        | resolved  | firing        | **true** → +1          |
| T+20        | firing    | resolved      | **true** → +1          |
| T+30        | resolved  | firing        | **true** → +1          |

Итог: `transition_count = 3`.

---

## Root Cause анализа падающего теста

### Баг 1 — одинаковый `starts_at` (недетерминизм)

Тест-фикстура (строки 156–163):

```sql
INSERT INTO alerts (fingerprint, alert_name, status, starts_at, created_at, labels)
VALUES
('fp_flap', 'FlappingAlert', 'firing',   $1, $1,                          '...'),
('fp_flap', 'FlappingAlert', 'resolved', $1, $1 + INTERVAL '10 minutes',  '...'),
('fp_flap', 'FlappingAlert', 'firing',   $1, $1 + INTERVAL '20 minutes',  '...'),
('fp_flap', 'FlappingAlert', 'resolved', $1, $1 + INTERVAL '30 minutes',  '...')
```

Обратите внимание: `starts_at = $1` для **всех 4 строк** (одинаковый),
только `created_at` различается. SQL сортирует по `starts_at`, поэтому
порядок строк внутри оконной функции **неопределён**. PostgreSQL может
вернуть строки в произвольном порядке → `transition_count` = 0, 1, 2 или 3.

### Баг 2 — неверное ожидание

```go
if alerts[0].TransitionCount < 4 {                    // строка 182
    t.Errorf("Expected at least 4 transitions, got %d", alerts[0].TransitionCount)
}
```

Даже при корректном `starts_at` из 4 строк можно получить только 3 перехода.
Ожидание `>= 4` всегда будет ложно.

### Как эти два бага взаимодействуют

- Если `starts_at` недетерминирован → тест падает на `len(alerts) != 1`
  (0 записей возвращается, т.к. transition_count < threshold=3) **или**
  на `TransitionCount < 4`.
- Даже если исправить `starts_at`, тест падает на `TransitionCount < 4`
  (получаем 3, ожидаем >= 4).

---

## Схема таблицы `alerts`

```sql
CREATE TABLE IF NOT EXISTS alerts (
    id          SERIAL PRIMARY KEY,
    fingerprint VARCHAR(255) NOT NULL,
    alert_name  VARCHAR(255) NOT NULL,
    status      VARCHAR(50)  NOT NULL,
    starts_at   TIMESTAMP WITH TIME ZONE NOT NULL,
    ends_at     TIMESTAMP WITH TIME ZONE,
    generator_url TEXT,
    labels      JSONB,
    annotations JSONB,
    timestamp   TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

Поле `id` — автоинкремент, строго монотонный в рамках одной сессии вставки.
Может использоваться как надёжный тайбрейкер в ORDER BY.

---

## Другие тесты flapping — статус

| Тест | Баги | Статус |
|------|------|--------|
| `TestGetFlappingAlerts_NoStateTransitions` (строки 119–145) | Нет (вставляется 1 строка, transitions=0 < threshold=2) | Green |
| `TestGetFlappingAlerts_MultipleTransitions` (строки 147–185) | Баг 1 + Баг 2 | **Red** |
| `TestGetFlappingAlerts_ThresholdFiltering` (строки 297–334) | Нет аномалий `starts_at` — строки имеют разные timestamps | Вероятно green |

---

## Точки интеграции

- **SQL ORDER BY** в `GetFlappingAlerts` (`postgres_history.go:558`):
  добавить `id` как тайбрейкер → `ORDER BY starts_at, id`
- **Тест-фикстура** `TestGetFlappingAlerts_MultipleTransitions` (`postgres_history_test.go:156–163`):
  использовать `starts_at + INTERVAL` вместо одного значения `$1`
- **Ожидание транзишн-каунта** (`postgres_history_test.go:182`):
  скорректировать с `>= 4` на `>= 3` **или** добавить 5-ю строку для получения 4 переходов

---

## Выбор решения

### Вариант A — минимальный (рекомендуется)
- Исправить `starts_at` в INSERT, использовав базовое время + интервалы
- Изменить ожидание с `>= 4` на `>= 3`
- Добавить `id` в ORDER BY как тайбрейкер
- Обновить комментарий в тесте: "4 rows, 3 transitions"

### Вариант B — расширенный
- Вставить 5 строк (firing→resolved→firing→resolved→firing = 4 transitions)
- Ожидание `>= 4` остаётся корректным
- Порог threshold=3 по-прежнему улавливает алерт

Вариант A проще и точнее документирует семантику (N−1 переходов).
Принят вариант A.
