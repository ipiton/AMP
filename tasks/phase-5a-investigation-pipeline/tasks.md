# Tasks: PHASE-5A — Двухфазный Async Investigation Pipeline

## Вертикальные слайсы

Каждый слайс — рабочий, тестируемый инкремент. Реализация строго в этом порядке.

---

## Слайс 1: DB + Repository (день 1, утро)

**Цель**: персистентный слой готов, тесты проходят.

- [ ] **1.1** Написать миграцию `go-app/migrations/20260422000000_create_investigation_table.sql`
  - Таблица `alert_investigations` по схеме из Spec.md §1
  - Проверить: `goose up` проходит на dev БД

- [ ] **1.2** Добавить core-типы в `go-app/internal/core/`
  - `InvestigationStatus` constants
  - `InvestigationJob`, `InvestigationResult`, `Investigation` structs
  - Новый файл: `go-app/internal/core/investigation.go`

- [ ] **1.3** Определить `InvestigationRepository` интерфейс
  - Файл: `go-app/internal/core/investigation_repository.go`
  - Методы: Create, UpdateStatus, SaveResult, SaveError, GetLatestByFingerprint, MoveToDLQ

- [ ] **1.4** Реализовать PostgreSQL repository
  - Файл: `go-app/internal/infrastructure/repository/investigation_repository.go`
  - По образцу соседних файлов в том же пакете
  - Тест: `investigation_repository_test.go` (integration, реальная БД)
    - Create → GetLatestByFingerprint
    - SaveResult → status=completed
    - SaveError + retry → status=failed
    - MoveToDLQ → status=dlq

---

## Слайс 2: LLM investigation call (день 1, день)

**Цель**: LLM умеет расследовать алерт, есть mock для тестов.

- [ ] **2.1** Добавить `InvestigateAlert()` в интерфейс `LLMClient`
  - Файл: `go-app/internal/core/services/alert_processor.go` (интерфейс)
  - Сигнатура: `InvestigateAlert(ctx, alert, classification) (*InvestigationResult, error)`

- [ ] **2.2** Реализовать `InvestigateAlert()` в `HTTPLLMClient`
  - Файл: `go-app/internal/infrastructure/llm/client.go`
  - Режим `openai-compatible`: POST `/chat/completions` с investigation-промптом (Spec §4)
  - Structured JSON output: системный промпт + JSON schema в `response_format`
  - Маппинг ответа в `InvestigationResult`

- [ ] **2.3** Добавить mock/dry-run реализацию
  - Файл: `go-app/internal/infrastructure/llm/` (dry-run client)
  - `DryRunLLMClient.InvestigateAlert()` → фиксированный ответ для тестов

- [ ] **2.4** Unit-тест `InvestigateAlert()`
  - Mock HTTP server, проверить формирование промпта
  - Проверить маппинг JSON-ответа в `InvestigationResult`
  - Проверить обработку ошибок (timeout, invalid JSON, 429)

---

## Слайс 3: InvestigationQueue + Worker (день 1 вечер — день 2 утро)

**Цель**: очередь принимает задания и обрабатывает через LLM.

- [ ] **3.1** Создать пакет `go-app/internal/infrastructure/investigation/`
  - `queue.go` — `InvestigationQueue` struct, `Submit()`, `Start()`, `Stop()`
  - `QueueConfig` с defaults
  - Submit: если канал полон → drop + increment `amp_investigations_dropped_total`

- [ ] **3.2** Реализовать worker loop
  - `runWorker()` — select на jobs channel и ctx.Done()
  - `processJob()` — UpdateStatus → InvestigateAlert → SaveResult/SaveError
  - Error classification: transient (timeout, 429, 5xx) → retry с backoff; permanent → failed
  - Exponential backoff: `min(retryInterval * 2^retryCount, 60s)`

- [ ] **3.3** Метрики
  - Файл: `go-app/internal/infrastructure/investigation/metrics.go`
  - `amp_investigation_queue_depth` (Gauge)
  - `amp_investigations_submitted_total` (Counter)
  - `amp_investigations_total{status}` (CounterVec)
  - `amp_investigations_dropped_total` (Counter)
  - `amp_investigation_duration_seconds` (Histogram, buckets: 1s, 5s, 15s, 30s, 60s)

- [ ] **3.4** Graceful shutdown
  - `Stop()`: выставить `stopped=true`, close done channel, `wg.Wait()` с timeout 30s

- [ ] **3.5** Unit-тесты queue
  - Submit happy path: задание попадает в очередь
  - Submit при full queue: дропается без паники
  - processJob success: SaveResult вызван, status=completed
  - processJob transient error: retry after backoff
  - processJob permanent error: SaveError, status=failed, no retry
  - processJob 3 retries exhausted: MoveToDLQ
  - Stop(): graceful, воркеры завершаются

---

## Слайс 4: Интеграция в AlertProcessor (день 2, утро)

**Цель**: Phase 2 стартует автоматически после Phase 1 Classification.

- [ ] **4.1** Добавить `InvestigationQueue` interface в `services` пакет
  - В `go-app/internal/core/services/alert_processor.go`
  - `type InvestigationQueue interface { Submit(*core.Alert, *core.ClassificationResult) error }`

- [ ] **4.2** Добавить `InvestigationQueue` в `AlertProcessorConfig`
  - Опциональное поле, nil = investigation disabled

- [ ] **4.3** Вставить submit в `processEnrichedMode()` или inline
  - После `ClassifyAlert()`, только если `alert.Status == StatusFiring`
  - Fire-and-forget: ошибка submit → Warn-лог, НЕ ошибка обработки
  - Добавить метрику: если submit fails → increment dropped

- [ ] **4.4** Тест интеграции в AlertProcessor
  - Mock `InvestigationQueue`: проверить что Submit вызван после ClassifyAlert
  - Submit error не должна прерывать ProcessAlert
  - Для resolved-алерта: Submit НЕ вызывается

---

## Слайс 5: Config + Wiring (день 2, день)

**Цель**: investigation включается через конфиг.

- [ ] **5.1** Добавить `InvestigationConfig` в `go-app/internal/config/config.go`
  - Поля: Enabled, WorkerCount, QueueSize, MaxRetries, RetryInterval, OnlyFiring
  - Дефолты: false, 3, 200, 3, 5s, true

- [ ] **5.2** Wiring в `ServiceRegistry.initializeAlertProcessor()`
  - Файл: `go-app/internal/application/service_registry.go`
  - Если `config.Investigation.Enabled`: создать `InvestigationQueue`, вызвать `Start()`
  - Передать в `AlertProcessorConfig.InvestigationQueue`
  - Добавить в shutdown sequence: `invQueue.Stop()`

- [ ] **5.3** Обновить config-файлы примеров (если есть в `examples/` или `helm/`)
  - Добавить секцию `investigation:` с комментариями

---

## Слайс 6: HTTP endpoint (день 2, вечер)

**Цель**: результаты расследования доступны по API.

- [ ] **6.1** Handler `GET /api/v1/alerts/{fingerprint}/investigation`
  - Файл: `go-app/internal/application/handlers/investigation_handler.go`
  - `InvestigationRepository.GetLatestByFingerprint()`
  - 404 если не найдено
  - 200 с partial response если status=queued/processing (без findings)
  - 200 с full response если status=completed

- [ ] **6.2** Регистрация маршрута в router
  - Файл: `go-app/internal/application/router.go`
  - `GET /api/v1/alerts/{fingerprint}/investigation`

- [ ] **6.3** Unit-тест handler
  - 404 для несуществующего fingerprint
  - 200 queued: нет findings в ответе
  - 200 completed: полный ответ с findings

---

## Слайс 7: Smoke test + документация (день 3)

**Цель**: end-to-end проверка, качество-гейт пройден.

- [ ] **7.1** Integration test (опционально, если есть test DB)
  - Послать алерт через webhook
  - Дождаться investigation completed (polling или sleep)
  - GET /api/v1/alerts/{fingerprint}/investigation → status=completed

- [ ] **7.2** `go vet ./...` + `go test ./...` проходят без новых ошибок

- [ ] **7.3** Обновить `docs/06-planning/NEXT.md`
  - Перевести PHASE-5A из Queue в Done (или WIP)

- [ ] **7.4** Обновить `docs/06-planning/DONE.md`
  - Добавить PHASE-5A запись

---

## Файловый манифест (новые файлы)

```
go-app/migrations/20260422000000_create_investigation_table.sql
go-app/internal/core/investigation.go
go-app/internal/core/investigation_repository.go
go-app/internal/infrastructure/repository/investigation_repository.go
go-app/internal/infrastructure/repository/investigation_repository_test.go
go-app/internal/infrastructure/investigation/queue.go
go-app/internal/infrastructure/investigation/metrics.go
go-app/internal/infrastructure/investigation/queue_test.go
go-app/internal/application/handlers/investigation_handler.go
go-app/internal/application/handlers/investigation_handler_test.go
```

## Изменяемые файлы

```
go-app/internal/core/services/alert_processor.go     -- InvestigationQueue interface + Submit
go-app/internal/infrastructure/llm/client.go          -- InvestigateAlert() impl
go-app/internal/config/config.go                      -- InvestigationConfig
go-app/internal/application/service_registry.go       -- wiring
go-app/internal/application/router.go                 -- маршрут
```

## Блокеры и риски

| Риск | Митигация |
|------|-----------|
| LLMClient интерфейс используется в тестах с моками | Добавить `InvestigateAlert` в mock-реализации (DryRunClient и test mocks) |
| Медленные LLM-вызовы блокируют воркеры | WorkerCount=3 достаточно для начала; circuit breaker уже есть в HTTPLLMClient |
| Queue overflow при всплеске алертов | Drop с метрикой `dropped_total` + алерт на эту метрику |
| InvestigateAlert в режиме `proxy` (не openai-compatible) | Добавить отдельный endpoint или использовать chat/completions для обоих режимов |
