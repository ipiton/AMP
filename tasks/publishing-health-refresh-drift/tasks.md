# PUBLISHING-HEALTH-REFRESH-DRIFT — Tasks

## Подход

Каждый slice — один независимый, верифицируемый fix. Порядок: от самого изолированного
к наиболее связанному. Запускать тесты после каждого slice.

Команда для проверки всего пакета:
```bash
cd go-app && go test ./internal/business/publishing/... -count=1 -v 2>&1 | grep -E "(FAIL|PASS|---)"
```

---

## Slice 1: Верификация — запустить тесты и зафиксировать baseline

- [ ] Запустить `cd go-app && go test ./internal/business/publishing/... -count=1 -v` и записать все FAIL
- [ ] Зафиксировать точные имена падающих тестов и сообщения об ошибках
- [ ] Проверить: падают ли тесты по таймингу (flaky) или стабильно

---

## Slice 2: Fix — sanitizeErrorMessage (health_errors.go)

Файл: `go-app/internal/business/publishing/health_errors.go`

- [ ] Изменить replacement строку: `" [REDACTED]"` → `"[REDACTED]"` (убрать leading space)
  - Строки ~187 и ~191 в `sanitizeErrorMessage()`
- [ ] Проверить тест `TestSanitizeErrorMessage`:
  - [ ] Case "Bearer token": input `"Bearer token123 is invalid"`, want `"Bearer [REDACTED] is invalid"`
  - [ ] Case "Authorization header": убедиться что want остаётся корректным
- [ ] Исправить want-строки в `health_errors_test.go` если нужно:
  - `"token= [REDACTED]&"` → `"token=[REDACTED]&"` (если это артефакт лишнего пробела)
  - `"api_key= [REDACTED]&"` → `"api_key=[REDACTED]&"` (аналогично)
- [ ] Запустить: `cd go-app && go test ./internal/business/publishing/... -run TestSanitizeErrorMessage -count=1 -v`
- [ ] Убедиться: все subtests в `TestSanitizeErrorMessage` — PASS

---

## Slice 3: Fix — GetStats() drift (health_impl.go)

Файл: `go-app/internal/business/publishing/health_impl.go`

- [ ] Изменить метод `GetStats()` (строки ~282–289):
  ```go
  // Было:
  allStatuses := m.statusCache.GetAll()
  stats := calculateAggregateStats(allStatuses)
  return stats, nil

  // Стало:
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
  ```
- [ ] Запустить: `cd go-app && go test ./internal/business/publishing/... -run TestHealthMonitor_GetStats -count=1 -v`
- [ ] Убедиться: `TotalTargets` совпадает с числом targets в discovery

---

## Slice 4: Fix — WarmupDelay в timing тесте (health_test.go)

Файл: `go-app/internal/business/publishing/health_test.go`

- [ ] Найти `TestHealthMonitor_DegradedState` (~строка 467)
- [ ] Добавить `config.WarmupDelay = 10 * time.Millisecond` после `config.FailureThreshold = 3`
- [ ] Проверить что `time.Sleep(300ms)` достаточно для 3 cycles при `CheckInterval=100ms`:
  - 10ms warmup + initial check + 2 ticks = ~310ms — достаточно
- [ ] Запустить: `cd go-app && go test ./internal/business/publishing/... -run TestHealthMonitor_DegradedState -count=1 -v`
- [ ] Запустить несколько раз для проверки stablility (тест timing-sensitive):
  ```bash
  cd go-app && for i in {1..5}; do go test ./internal/business/publishing/... -run TestHealthMonitor_DegradedState -count=1 2>&1 | tail -1; done
  ```
- [ ] Если flaky — увеличить `time.Sleep` до 500ms или использовать poll loop

---

## Slice 5: Fix — Metric isolation в тестах (если нужно)

Файл: `go-app/internal/business/publishing/health_test.go`

Выполнить только если Slice 1 показал падения связанные с metric counts.

- [ ] Определить какие тесты проверяют конкретные метрики (Prometheus counter values)
- [ ] Для этих тестов заменить `getTestMetrics(t)` на изолированный registry:
  ```go
  promReg := prometheus.NewRegistry()
  metrics := v2.NewPublishingMetrics(promReg)
  monitor, err := NewHealthMonitor(discoveryMgr, config, slog.Default(), metrics)
  ```
- [ ] Убедиться что изменение не ломает тесты которым registry не важен

---

## Slice 6: Верификация — полный прогон пакета

- [ ] `cd go-app && go test ./internal/business/publishing/... -count=1`
- [ ] Все тесты — PASS (или явно задокументированы как known-flaky с причиной)
- [ ] `cd go-app && go test ./internal/business/publishing/... -count=1 -race` — нет race conditions
- [ ] Обновить `BUGS.md`: закрыть `PUBLISHING-HEALTH-REFRESH-DRIFT`
- [ ] Обновить `DONE.md`: добавить запись о закрытии

---

## Slice 7 (опционально): refresh_* тесты

Если после Slice 6 остаются падения в refresh тестах:

- [ ] Запустить: `cd go-app && go test ./internal/business/publishing/... -run "^TestRefresh" -count=1 -v`
- [ ] Зафиксировать конкретные сообщения об ошибках
- [ ] Применить аналогичный подход: WarmupDelay override, isolated metrics

---

## Definition of Done

- [ ] `go test ./internal/business/publishing/... -count=1` — green (exit 0)
- [ ] `go test ./internal/business/publishing/... -count=1 -race` — no races
- [ ] `BUGS.md` — `PUBLISHING-HEALTH-REFRESH-DRIFT` перемещён в Resolved
- [ ] `DONE.md` — запись добавлена
- [ ] `git diff --check` — нет whitespace errors
- [ ] Коммит в feature branch (не main)
