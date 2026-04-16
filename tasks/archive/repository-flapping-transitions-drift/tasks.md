# REPOSITORY-FLAPPING-TRANSITIONS-DRIFT — Tasks

## Вертикальный слайс

Один атомарный фикс: исправить test fixture + SQL tiebreaker → green test matrix.

---

## Чеклист реализации

### Слайс 1 — SQL fix (детерминированная сортировка)

- [ ] Открыть `go-app/internal/infrastructure/repository/postgres_history.go`
- [ ] Найти строку ~558: `LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at)`
- [ ] Изменить на: `LAG(status) OVER (PARTITION BY fingerprint ORDER BY starts_at, id)`
- [ ] Проверить, что `id` присутствует в SELECT подзапроса `state_changes`
      (если нет — добавить `id` в SELECT state_changes)
- [ ] `go vet ./internal/infrastructure/repository/...` — чисто

### Слайс 2 — Test fixture fix (корректные `starts_at`)

- [ ] Открыть `go-app/internal/infrastructure/repository/postgres_history_test.go`
- [ ] Найти функцию `TestGetFlappingAlerts_MultipleTransitions` (строка ~147)
- [ ] Исправить INSERT: заменить `starts_at = $1` для всех строк на
      `starts_at = $1 + INTERVAL 'N minutes'` (уникальный для каждой строки)
      - Row 1: `starts_at = $1 + INTERVAL '0 minutes'`  (или просто `$1`)
      - Row 2: `starts_at = $1 + INTERVAL '10 minutes'`
      - Row 3: `starts_at = $1 + INTERVAL '20 minutes'`
      - Row 4: `starts_at = $1 + INTERVAL '30 minutes'`
- [ ] Синхронизировать `created_at` с `starts_at` для каждой строки
- [ ] Обновить комментарий: "4 rows, 3 transitions (N rows => N-1 transitions)"
- [ ] Исправить assertion: `TransitionCount < 4` → `TransitionCount < 3`
- [ ] `go vet ./internal/infrastructure/repository/...` — чисто

### Слайс 3 — Верификация

- [ ] Запустить тест изолированно:
      ```
      cd go-app && GOCACHE=$(pwd)/.cache/go-build \
        go test ./internal/infrastructure/repository/... \
        -run TestGetFlappingAlerts -v -count=3
      ```
      Ожидаем PASS для всех 3 flapping тестов, 3 прогона
- [ ] Запустить весь пакет:
      ```
      cd go-app && GOCACHE=$(pwd)/.cache/go-build \
        go test ./internal/infrastructure/repository/... -count=1
      ```
      Ожидаем все тесты green (или задокументировать pre-existing failures)
- [ ] `git diff --check` — нет trailing whitespace / mixed endings

### Слайс 4 — Обновление планирования

- [ ] Отметить баг как закрытый в `docs/06-planning/BUGS.md`:
      `- [x] **REPOSITORY-FLAPPING-TRANSITIONS-DRIFT**`
      Дописать краткое closure-описание (аналогично другим закрытым записям)
- [ ] Добавить запись в `docs/06-planning/DONE.md` (или `CHANGELOG`)
- [ ] Обновить `docs/06-planning/NEXT.md` если задача была в WIP

---

## Критерии закрытия задачи

| Критерий | Проверка |
|----------|---------|
| `TestGetFlappingAlerts_MultipleTransitions` green | `-run TestGetFlappingAlerts_MultipleTransitions -count=3` |
| Весь пакет `repository` без новых red | `go test ./internal/infrastructure/repository/...` |
| `go vet` чист | `go vet ./internal/infrastructure/repository/...` |
| BUGS.md обновлён | `grep -A3 "REPOSITORY-FLAPPING" docs/06-planning/BUGS.md` |
| branch != main | `git branch --show-current` |

---

## Зависимости

Нет внешних зависимостей. Задача полностью изолирована в:
- `go-app/internal/infrastructure/repository/postgres_history.go`
- `go-app/internal/infrastructure/repository/postgres_history_test.go`

Тесты используют `testcontainers-go` (PostgreSQL 15-alpine) — требуется
Docker daemon при локальном запуске.
