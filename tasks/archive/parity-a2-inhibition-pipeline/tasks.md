# Implementation Checklist: PARITY-A2-INHIBITION-PIPELINE

## Research & Spec

- [x] Research завершён: `research.md`
- [x] Spec согласован: `Spec.md`

---

## Slice 1 — Config extension (0.5d)

> Цель: чтобы inhibition rules можно было задать в `config.yaml`

- [ ] **1.1** Добавить `InhibitionRuleConfig` struct в `go-app/internal/config/config.go`
- [ ] **1.2** Добавить `InhibitionConfig` struct в `go-app/internal/config/config.go`
- [ ] **1.3** Добавить поле `Inhibition InhibitionConfig` в `Config` struct
- [ ] **1.4** Добавить метод `ToInhibitionRules() []inhibition.InhibitionRule` на `InhibitionConfig`
  - конвертирует `InhibitionRuleConfig` → `inhibition.InhibitionRule`
  - если задан `ConfigFile` — парсить через `inhibition.NewParser().ParseFile()`
- [ ] **1.5** Обновить `config.yaml.example` — добавить секцию `inhibition.inhibit_rules` с примером

**Проверка Slice 1**:
```bash
go build ./go-app/internal/config/...
```

---

## Slice 2 — ServiceRegistry wiring (0.5d)

> Цель: inhibition компоненты создаются при старте сервиса

- [ ] **2.1** Добавить поля в `ServiceRegistry` struct:
  ```go
  inhibitionCache   inhibition.ActiveAlertCache
  inhibitionMatcher inhibition.InhibitionMatcher
  inhibitionState   inhibition.InhibitionStateManager
  ```
- [ ] **2.2** Реализовать `initializeInhibition(ctx context.Context) error`:
  - Вызвать `config.Inhibition.ToInhibitionRules()`
  - При `len(rules) == 0` → логировать warning, выйти (graceful degradation)
  - Создать `inhibitionpkg.NewTwoTierAlertCache(r.cache, r.logger)`
  - Создать `inhibitionpkg.NewDefaultStateManager(r.cache, r.logger, r.metrics)`
  - Создать `inhibitionpkg.NewMatcher(cache, rules, r.logger)`
- [ ] **2.3** Вставить `initializeInhibition` в цепочку `Initialize()` — после init cache, до init alert processor
  - Ошибка — non-fatal, добавить в `r.degradedReasons`
- [ ] **2.4** Обновить `initializeAlertProcessor()` — передать `InhibitionMatcher`, `InhibitionState`
- [ ] **2.5** Обновить `Shutdown()` — остановить inhibition cache (вызвать `Stop()` если доступен)
- [ ] **2.6** Добавить accessor `InhibitionState() inhibition.InhibitionStateManager`

**Проверка Slice 2**:
```bash
go build ./go-app/...
# Запустить сервис с пустым config (без inhibit_rules) — должен запуститься без ошибок
```

---

## Slice 3 — Cache population (0.5d)

> Цель: кэш активных алертов наполняется и очищается при ingest алертов

- [ ] **3.1** Добавить поле `inhibitionCache inhibition.ActiveAlertCache` в `AlertProcessor` struct
- [ ] **3.2** Добавить поле `InhibitionCache inhibition.ActiveAlertCache` в `AlertProcessorConfig`
- [ ] **3.3** Передать `InhibitionCache` в `NewAlertProcessor()` → сохранить в struct
- [ ] **3.4** Обновить `service_registry.go:initializeAlertProcessor()` — передать `InhibitionCache: r.inhibitionCache`
- [ ] **3.5** В `ProcessAlert()`, после dedup (Step 0), добавить cache update:
  - `StatusFiring` → `p.inhibitionCache.AddFiringAlert(ctx, alert)` (non-critical error)
  - `StatusResolved` → `p.inhibitionCache.RemoveAlert(ctx, fingerprint)` (non-critical error)
- [ ] **3.6** Реализовать `cleanupInhibitionsForSource(ctx, sourceFingerprint)` в `AlertProcessor`:
  - Получить все active inhibitions
  - Удалить те, где `SourceFingerprint == sourceFingerprint`
- [ ] **3.7** Вызвать `cleanupInhibitionsForSource` при `StatusResolved`

**Проверка Slice 3**:
```bash
go vet ./go-app/...
# Запустить: отправить firing alert → проверить что он в cache
# Отправить resolved → проверить что cache очищен
```

---

## Slice 4 — API endpoint (0.5d)

> Цель: можно запросить список активных ингибиций

- [ ] **4.1** Создать `go-app/internal/application/handlers/inhibitions.go`:
  - Struct `InhibitionsHandler`
  - Метод `List(w, r)` → GET /api/v2/inhibitions
  - Response type `GettableInhibition` (targetFP, sourceFP, ruleName, inhibitedAt, expiresAt)
  - При `stateManager == nil` → вернуть пустой массив `[]`
- [ ] **4.2** Зарегистрировать route в `go-app/internal/application/router.go`:
  - `GET /api/v2/inhibitions` → `inhibitionsHandler.List`
  - Создавать handler только если `registry.InhibitionState() != nil`

**Проверка Slice 4**:
```bash
curl http://localhost:9093/api/v2/inhibitions
# Должен вернуть [] или список активных ингибиций
```

---

## Testing

- [ ] **T1** Unit test: `ProcessAlert` с firing alert + matching source в кэше → возвращает nil (не публикует)
  - Файл: `go-app/internal/core/services/alert_processor_inhibition_test.go`
- [ ] **T2** Unit test: `ProcessAlert` с firing alert + нет source в кэше → публикует нормально
- [ ] **T3** Unit test: `ProcessAlert` с resolved alert → `RemoveAlert` вызывается
- [ ] **T4** Unit test: `initializeInhibition` с пустым config → graceful degradation (нет ошибки)
- [ ] **T5** Unit test: `initializeInhibition` с валидными rules → matcher не nil
- [ ] **T6** Unit test: `GET /api/v2/inhibitions` → 200 с пустым массивом когда нет ингибиций
- [ ] **T7** Integration test: end-to-end — POST firing source → POST firing target → target ингибируется
- [ ] `go vet ./go-app/...` — без ошибок
- [ ] `go test ./go-app/...` — все тесты проходят, нет регрессий

---

## Documentation & Cleanup

- [ ] Обновить `docs/06-planning/BACKLOG.md` — PARITY-A2 → `[x]`
- [ ] Обновить `NEXT.md` — убрать из WIP/Queue, добавить в Done
- [ ] Обновить `go-app/internal/application/handlers/` README если есть
- [ ] Проверить `config.yaml.example` содержит пример inhibition rules
- [ ] Commit message: `feat(inhibition): wire inhibition pipeline into alert processor`

---

## Pre-merge checklist

- [ ] Branch не `main`
- [ ] `requirements.md`, `research.md`, `Spec.md`, `tasks.md` — все файлы созданы
- [ ] `go vet ./go-app/...` — чист
- [ ] `go test ./go-app/...` — зелёный
- [ ] `git diff --check` — нет trailing whitespace
- [ ] `BACKLOG.md` обновлён

---

## Оценка трудоёмкости

| Slice | Оценка | Ключевые файлы |
|---|---|---|
| Slice 1: Config | 0.5d | `config/config.go` |
| Slice 2: Registry wiring | 0.5d | `application/service_registry.go` |
| Slice 3: Cache population | 0.5d | `core/services/alert_processor.go` |
| Slice 4: API endpoint | 0.5d | `application/handlers/inhibitions.go`, `application/router.go` |
| Testing | 0.5d | `*_inhibition_test.go` |
| **Итого** | **~2.5d** | |
