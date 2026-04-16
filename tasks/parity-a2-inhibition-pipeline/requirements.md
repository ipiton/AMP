# Requirements: PARITY-A2-INHIBITION-PIPELINE

## Context

AMP является заменой Alertmanager. Одна из ключевых функций Alertmanager — **inhibition rules**:
возможность подавить (заглушить) один алерт, если одновременно активен другой, более важный алерт.

Пример: если `NodeDown` (critical) активен, то `InstanceDown` (warning) на том же узле — шум.
Inhibition rule позволяет подавить такой алерт автоматически.

### Текущее состояние

Inhibition engine **полностью реализован** (`internal/infrastructure/inhibition/`):
- `parser.go` — парсинг YAML, 100% совместимость с Alertmanager
- `matcher_impl.go` — движок проверки правил (<500µs p99)
- `cache.go` — двухуровневый кэш активных алертов (L1 in-memory + L2 Redis)
- `state_manager.go` — трекинг состояния ингибиций (L1 sync.Map + L2 Redis)

Однако в `service_registry.go::initializeAlertProcessor()` **inhibition не подключён**:

```go
// СЕЙЧАС (InhibitionMatcher не установлен → nil guard в alert_processor.go, движок не запускается):
config := services.AlertProcessorConfig{
    FilterEngine:    r.filterEngine,
    LLMClient:       r.classificationSvc,
    Publisher:       r.publisher,
    Deduplication:   r.deduplicationSvc,
    BusinessMetrics: r.metrics,
    // InhibitionMatcher: nil  ← НЕТ
    // InhibitionState:   nil  ← НЕТ
}
```

Дополнительно: `AddFiringAlert` / `RemoveAlert` **не вызываются** из pipeline при смене
статуса алерта — кэш никогда не наполняется, matcher всегда видит пустой список.

---

## Goals

- [ ] **G1** — Inhibition rules работают в production: `ShouldInhibit()` вызывается для каждого firing-алерта
- [ ] **G2** — Кэш активных алертов наполняется при firing и очищается при resolving
- [ ] **G3** — Конфигурация inhibition rules загружается из YAML (inline в config.yaml или отдельный файл)
- [ ] **G4** — State tracking работает: активные ингибиции видны через `InhibitionStateManager`
- [ ] **G5** — Prometheus метрики собираются корректно (inhibited/allowed, match duration, cache hits)
- [ ] **G6** — API endpoint для просмотра активных ингибиций (Alertmanager parity: `GET /api/v2/inhibitions`)

---

## User Stories

**US-1: Оператор, настраивающий подавление алертов**
> Как оператор, я хочу определить inhibition rule, чтобы при алерте `NodeDown` (critical)
> не получать флуд алертов `InstanceDown` (warning) для того же узла.

**US-2: On-call инженер, диагностирующий инцидент**
> Как on-call инженер, я хочу видеть список подавленных алертов и причину подавления,
> чтобы понимать, что алерты не потеряны, а осознанно скрыты.

**US-3: SRE, мигрирующий с Alertmanager**
> Как SRE, я хочу скопировать секцию `inhibit_rules` из alertmanager.yaml в AMP config
> без изменений, чтобы не переписывать конфигурацию.

**US-4: Разработчик, добавляющий мониторинг**
> Как разработчик, я хочу видеть метрики `inhibition_checks_total{status="inhibited"}` и
> `inhibition_match_duration_seconds` в Grafana, чтобы отслеживать эффективность правил.

---

## Constraints

- **Совместимость**: конфигурация должна принимать тот же YAML формат, что и Alertmanager `inhibit_rules`
- **Fail-safe**: ошибка в inhibition engine НЕ должна останавливать доставку алертов
  (graceful degradation уже реализован в `alert_processor.go:120-125`)
- **Производительность**: добавление inhibition check не должно замедлять pipeline >1ms
  (движок работает <500µs p99 на пустом и заполненном кэше)
- **Go + pgx + Redis**: использовать существующую инфраструктуру (Redis через `infrastructurecache.Cache`)
- **Нет новых зависимостей**: все необходимые компоненты уже есть в codebase
- **WIP ≤ 2**: задача делается на feature-branch, не на main

---

## Success Criteria (Definition of Done)

- [ ] `service_registry.go` инициализирует `TwoTierAlertCache`, `DefaultInhibitionMatcher`, `DefaultStateManager`
- [ ] `AlertProcessorConfig.InhibitionMatcher` и `InhibitionState` не nil в production
- [ ] `AddFiringAlert()` вызывается когда алерт переходит в `StatusFiring`
- [ ] `RemoveAlert()` вызывается когда алерт переходит в `StatusResolved`
- [ ] Config section `inhibit_rules` читается из `config.yaml`
- [ ] Тест: алерт с matching source alert в кэше → статус `inhibited`, не доставляется
- [ ] Тест: алерт без matching source alert → доставляется нормально
- [ ] Тест: resolving source alert → target alert разблокирован
- [ ] `GET /api/v2/inhibitions` возвращает список активных ингибиций (опционально, Alertmanager parity)
- [ ] Prometheus метрики `inhibition_checks_total` видны в `/metrics`
- [ ] `go test ./...` проходит без регрессий
- [ ] `BACKLOG.md` обновлён: PARITY-A2 → ✅
