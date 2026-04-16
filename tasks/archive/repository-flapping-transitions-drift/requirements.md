# REPOSITORY-FLAPPING-TRANSITIONS-DRIFT — Requirements

## Контекст

Задача выделена из `REPO-TEST-MATRIX-RED` после stabilization pass. Пакет
`internal/infrastructure/repository` почти green, но один тест стабильно
падает на несоответствии ожидаемого числа переходов.

Исходная запись в `BUGS.md`:
> **REPOSITORY-FLAPPING-TRANSITIONS-DRIFT** — после cleanup SQL/fixture проблем
> пакет `internal/infrastructure/repository` почти green, но
> `TestGetFlappingAlerts_MultipleTransitions` все ещё падает на mismatch по
> ожидаемому числу transitions.

## Проблема

`TestGetFlappingAlerts_MultipleTransitions` (файл
`go-app/internal/infrastructure/repository/postgres_history_test.go:147`)
вставляет 4 строки с одинаковым значением `starts_at` и ожидает
`TransitionCount >= 4`. Тест нестабилен и систематически не проходит из-за:

1. **Недетерминированная сортировка** — все 4 строки имеют одинаковый `starts_at`,
   поэтому `LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at)`
   возвращает произвольный порядок; число посчитанных переходов непредсказуемо.
2. **Неверное ожидание** — из 4 строк с чередующимися статусами
   (firing → resolved → firing → resolved) можно получить максимум **3**
   смены состояния (n − 1 переходов); тест ожидает >= 4.

## Цели

| # | Критерий успеха |
|---|----------------|
| 1 | `TestGetFlappingAlerts_MultipleTransitions` стабильно проходит при повторных запусках |
| 2 | Тест корректно документирует семантику: N строк → N−1 переходов |
| 3 | SQL `GetFlappingAlerts` устойчив к строкам с одинаковым `starts_at` |
| 4 | Все остальные тесты пакета `internal/infrastructure/repository` остаются green |
| 5 | `go vet ./internal/infrastructure/repository/...` чист |

## Scope

**В scope:**
- Исправление тест-фикстуры `TestGetFlappingAlerts_MultipleTransitions`
- Исправление SQL ORDER BY в `GetFlappingAlerts` (добавить тайбрейкер)
- Проверка корректности других тестов flapping (`_NoStateTransitions`, `_ThresholdFiltering`)

**Вне scope:**
- Изменение публичного контракта `GetFlappingAlerts` (сигнатура, возвращаемые типы)
- Изменение схемы БД
- Доработка логики `FlappingScore`
- Другие тесты пакета (`GetTopAlerts`, `GetAggregatedStats`, `GetHistory`)
