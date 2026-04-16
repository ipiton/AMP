# Research: PARITY-A2-INHIBITION-PIPELINE

## 1. Что уже реализовано

### 1.1 `internal/infrastructure/inhibition/` — полный inhibition engine

| Файл | Статус | Описание |
|---|---|---|
| `models.go` | ✅ Готов | `InhibitionRule`, `InhibitionConfig`, `ValidationError` |
| `parser.go` | ✅ Готов | YAML парсинг, 100% Alertmanager compatible |
| `matcher.go` | ✅ Готов | Интерфейсы: `InhibitionMatcher`, `ActiveAlertCache`, `MatchResult` |
| `matcher_impl.go` | ✅ Готов | `DefaultInhibitionMatcher` — движок проверки правил |
| `cache.go` | ✅ Готов | `TwoTierAlertCache` — L1 LRU + L2 Redis |
| `state_manager.go` | ✅ Готов | `DefaultStateManager` — sync.Map + Redis persistence |
| `errors.go` | ✅ Готов | `ValidationError`, `ConfigError` |

**137 unit tests, 82.6% coverage** (`*_test.go` файлы).

### 1.2 `internal/core/services/alert_processor.go`

Код ингибиции **присутствует** (lines 117–169), но срабатывает только если `p.inhibitionMatcher != nil`:

```go
// alert_processor.go:118
if p.inhibitionMatcher != nil && alert.Status == core.StatusFiring {
    inhibitionResult, err := p.inhibitionMatcher.ShouldInhibit(ctx, alert)
    // ...
    if inhibitionResult.Matched {
        // Записывает state, метрики, возвращает nil (не публикует)
        return nil
    }
}
```

`AlertProcessorConfig` принимает поля:
```go
InhibitionMatcher inhibition.InhibitionMatcher      // TN-130 Phase 6: optional
InhibitionState   inhibition.InhibitionStateManager // TN-130 Phase 6: optional
BusinessMetrics   *metrics.BusinessMetrics          // TN-130 Phase 6: required if using inhibition
```

---

## 2. Точка разрыва: `service_registry.go`

**Файл**: `internal/application/service_registry.go`, lines 444–466

```go
func (r *ServiceRegistry) initializeAlertProcessor(ctx context.Context) error {
    config := services.AlertProcessorConfig{
        FilterEngine:    r.filterEngine,
        LLMClient:       r.classificationSvc,
        Publisher:       r.publisher,
        Deduplication:   r.deduplicationSvc,
        BusinessMetrics: r.metrics,
        Logger:          r.logger,
        Metrics:         nil, // TODO: MetricsManager
        // ← InhibitionMatcher и InhibitionState НЕ УСТАНОВЛЕНЫ
    }
    // ...
}
```

`ServiceRegistry` struct (lines 41–80) не имеет полей для inhibition компонентов.

---

## 3. Второй разрыв: кэш активных алертов не наполняется

`TwoTierAlertCache` реализован, но `AddFiringAlert()` и `RemoveAlert()` **нигде не вызываются**
из production-кода (только в тестах).

**Где нужно добавить вызовы**:
- При ingest алерта в `StatusFiring` → `cache.AddFiringAlert(ctx, alert)`
- При resolving алерта (или ingest с `StatusResolved`) → `cache.RemoveAlert(ctx, fingerprint)`

Наиболее логичное место — в `alert_processor.go` или в хэндлере `POST /api/v2/alerts`.

**Текущий pipeline в alert_processor.go**:
```
Step 0: Deduplication (dedup.ProcessAlert)
Step 1: Inhibition check (ShouldInhibit) ← тут нужен cache
Step 2: EnrichmentMode selection
Step 3: LLM / Transparent / Enriched processing
Step 4: Publish
```

Кэш должен обновляться **до** inhibition check (Step 1), лучше всего после dedup (Step 0):
- `StatusFiring` → `cache.AddFiringAlert`
- `StatusResolved` → `cache.RemoveAlert`, затем `stateManager.RemoveInhibition`

---

## 4. Конфигурация: inhibition rules

**Текущий** `config.go` не содержит секции для inhibition rules:

```go
// grep -n "inhibit" go-app/internal/config/config.go → нет результатов
```

Нужно добавить поддержку в `Config` struct:

```go
// Вариант A: inline в config.yaml
type Config struct {
    // ...
    InhibitRules []InhibitionRuleConfig `yaml:"inhibit_rules"`
}

// Вариант B: отдельный файл
type Config struct {
    // ...
    InhibitionConfigFile string `yaml:"inhibition_config_file"`
}
```

**Рекомендация**: Вариант A (inline) для простых deployments, с fallback на Вариант B для
совместимости с Alertmanager (где `inhibit_rules` — секция в alertmanager.yaml).

Существующий `InhibitionParser` (`parser.go`) поддерживает оба формата:
```go
parser.ParseFile(path)         // Вариант B
parser.Parse([]byte(yaml))     // Вариант A (сериализовать из Config)
```

---

## 5. State management: что происходит при resolving source alert

**Текущее состояние**: `RemoveInhibition()` не вызывается автоматически при resolving source alert.

**Требуемое поведение** (Alertmanager совместимость):
1. Source alert resolves → ингибиция снимается
2. Target alert, если всё ещё firing, должен быть проверен повторно на следующем ingest

Так как AMP — stateless pipeline (не держит timer-based re-evaluation), поведение:
- При resolving source alert: `cache.RemoveAlert(sourceFingerprint)` + `stateManager.RemoveInhibition(targetFingerprint)`
- На следующем firing target alert: ShouldInhibit вернёт false → алерт пройдёт

**Проблема**: как найти все target алерты, ингибированные данным source?
`InhibitionStateManager.GetActiveInhibitions()` возвращает все active states → фильтровать по `SourceFingerprint`.

---

## 6. Real-time events

`internal/realtime/event.go` уже имеет:
```go
EventTypeAlertInhibited = "alert_inhibited"
```

Нужно отправлять событие при ингибиции (через EventBroadcaster если доступен).

---

## 7. API parity с Alertmanager

Alertmanager API (v0.0.3): `GET /api/v2/inhibitions` — **не реализован** в AMP.

Текущие routes (`application/router.go`) — только alerts, silences, status, receivers, groups.

Для MVP (Phase A parity) достаточно:
- `GET /api/v2/inhibitions` → список `[]{targetFP, sourceFP, ruleName, inhibitedAt}`

Для полного Alertmanager parity (Phase B):
- CRUD для inhibition rules через API (сейчас — только config file)

---

## 8. Метрики (уже объявлены)

`pkg/metrics/metrics.go` содержит (lines ~1043–1048):
```go
InhibitionCacheHits       prometheus.Counter
InhibitionCacheMisses     prometheus.Counter
InhibitionCacheEvictions  prometheus.Counter
InhibitionCacheSize       prometheus.Gauge
InhibitionCacheOperations *prometheus.CounterVec
InhibitionCacheDuration   *prometheus.HistogramVec
```

`BusinessMetrics` (используется в `alert_processor.go`):
```go
p.businessMetrics.RecordInhibitionCheck("inhibited")
p.businessMetrics.RecordInhibitionMatch(inhibitionResult.Rule.Name)
p.businessMetrics.RecordInhibitionDuration("check", ...)
```

Нужно убедиться, что `RecordInhibitionCheck`, `RecordInhibitionMatch`, `RecordInhibitionDuration` реализованы в `BusinessMetrics`.

---

## 9. Существующие таблицы БД

Для inhibition **не требуется новая таблица** (state хранится в Redis + sync.Map).

Если понадобится persistence через PostgreSQL (Phase B), таблица могла бы выглядеть так:

```sql
CREATE TABLE inhibition_states (
    target_fingerprint VARCHAR(64) PRIMARY KEY,
    source_fingerprint VARCHAR(64) NOT NULL,
    rule_name          VARCHAR(255) NOT NULL,
    inhibited_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at         TIMESTAMPTZ
);
```

Для MVP — **не нужна**, Redis достаточно.

---

## 10. Сводная матрица готовности

| Компонент | Готовность | Что делать |
|---|---|---|
| Parser | ✅ 100% | Ничего |
| Matcher (движок) | ✅ 100% | Ничего |
| Cache (TwoTierAlertCache) | ✅ 100% | Подключить в service_registry |
| StateManager | ✅ 100% | Подключить в service_registry |
| alert_processor.go (check) | ✅ 100% | Ничего — код есть |
| Config section | ❌ 0% | Добавить `InhibitRules` в `Config` struct |
| service_registry wiring | ❌ 0% | Создать cache, matcher, state_mgr |
| Cache population (firing) | ❌ 0% | `AddFiringAlert` при ingest |
| Cache cleanup (resolved) | ❌ 0% | `RemoveAlert` при resolve |
| State cleanup (resolved src) | ❌ 0% | `RemoveInhibition` при resolve source |
| API `GET /api/v2/inhibitions` | ❌ 0% | Новый handler (опционально) |
| BusinessMetrics methods | ⚠️ Проверить | Убедиться что методы реализованы |
