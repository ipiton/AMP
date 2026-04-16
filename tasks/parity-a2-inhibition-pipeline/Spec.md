# Spec: PARITY-A2-INHIBITION-PIPELINE

## Обзор

Задача — подключить уже реализованный inhibition engine в production pipeline.
Все компоненты (`TwoTierAlertCache`, `DefaultInhibitionMatcher`, `DefaultStateManager`) готовы.
Нужно: конфиг, wiring в ServiceRegistry, cache population при ingest.

---

## 1. Конфигурация

### 1.1 Новая секция в `Config`

**Файл**: `go-app/internal/config/config.go`

```go
// InhibitionConfig holds inhibition rules configuration
type InhibitionConfig struct {
    // Rules is the list of inhibition rules (Alertmanager compatible format)
    Rules []InhibitionRuleConfig `mapstructure:"inhibit_rules" yaml:"inhibit_rules"`

    // ConfigFile is an optional path to a separate inhibition rules YAML file
    // If specified, rules from the file are merged with inline Rules
    ConfigFile string `mapstructure:"config_file" yaml:"config_file,omitempty"`
}

// InhibitionRuleConfig holds a single inhibition rule configuration
type InhibitionRuleConfig struct {
    SourceMatch   map[string]string `mapstructure:"source_match"    yaml:"source_match,omitempty"`
    SourceMatchRE map[string]string `mapstructure:"source_match_re" yaml:"source_match_re,omitempty"`
    TargetMatch   map[string]string `mapstructure:"target_match"    yaml:"target_match,omitempty"`
    TargetMatchRE map[string]string `mapstructure:"target_match_re" yaml:"target_match_re,omitempty"`
    Equal         []string          `mapstructure:"equal"           yaml:"equal,omitempty"`
    Name          string            `mapstructure:"name"            yaml:"name,omitempty"`
}
```

Добавить поле в `Config`:
```go
type Config struct {
    // ...existing fields...
    Inhibition InhibitionConfig `mapstructure:"inhibition" yaml:"inhibition,omitempty"`
}
```

### 1.2 Пример `config.yaml`

```yaml
inhibition:
  inhibit_rules:
    - name: "node-down-inhibits-instance-down"
      source_match:
        alertname: "NodeDown"
        severity: "critical"
      target_match:
        alertname: "InstanceDown"
      equal:
        - node
        - cluster

    - name: "critical-inhibits-warnings"
      source_match:
        severity: "critical"
      target_match_re:
        severity: "warning|info"
      equal:
        - cluster
        - namespace
```

---

## 2. Конвертер конфига → inhibition.InhibitionRule

**Файл**: `go-app/internal/config/config.go` (или новый `go-app/internal/config/inhibition_adapter.go`)

```go
// ToInhibitionRules converts config rules to inhibition.InhibitionRule slice.
// Used during ServiceRegistry initialization.
func (c *InhibitionConfig) ToInhibitionRules() []inhibition.InhibitionRule {
    rules := make([]inhibition.InhibitionRule, 0, len(c.Rules))
    for _, r := range c.Rules {
        rules = append(rules, inhibition.InhibitionRule{
            SourceMatch:   r.SourceMatch,
            SourceMatchRE: r.SourceMatchRE,
            TargetMatch:   r.TargetMatch,
            TargetMatchRE: r.TargetMatchRE,
            Equal:         r.Equal,
            Name:          r.Name,
        })
    }
    return rules
}
```

Если `InhibitionConfig.ConfigFile != ""` — дополнительно парсить файл через `inhibition.NewParser().ParseFile(path)` и мержить rules.

---

## 3. ServiceRegistry — новые поля и инициализация

### 3.1 Новые поля в `ServiceRegistry`

**Файл**: `go-app/internal/application/service_registry.go`

```go
type ServiceRegistry struct {
    // ...existing fields...

    // Inhibition subsystem (TN-130, PARITY-A2)
    inhibitionCache   inhibition.ActiveAlertCache      // two-tier cache of firing alerts
    inhibitionMatcher inhibition.InhibitionMatcher     // rule engine
    inhibitionState   inhibition.InhibitionStateManager // active inhibition tracking
}
```

Импорт:
```go
inhibitionpkg "github.com/ipiton/AMP/internal/infrastructure/inhibition"
```

### 3.2 Метод `initializeInhibition`

```go
// initializeInhibition initializes the inhibition subsystem.
// Called after cache/redis is available, before initializeAlertProcessor.
func (r *ServiceRegistry) initializeInhibition(ctx context.Context) error {
    rules := r.config.Inhibition.ToInhibitionRules()

    // Если правил нет — inhibition отключён (graceful degradation)
    if len(rules) == 0 {
        r.logger.Info("⚠️ No inhibition rules configured, inhibition disabled")
        return nil
    }

    // Если есть отдельный config file — распарсить и добавить
    if r.config.Inhibition.ConfigFile != "" {
        parser := inhibitionpkg.NewParser()
        cfg, err := parser.ParseFile(r.config.Inhibition.ConfigFile)
        if err != nil {
            r.logger.Warn("Failed to parse inhibition config file, using inline rules only",
                "file", r.config.Inhibition.ConfigFile, "error", err)
        } else {
            rules = append(rules, cfg.Rules...)
        }
    }

    // Validate rules (pre-compile regex)
    parser := inhibitionpkg.NewParser()
    inhibitionCfg := &inhibitionpkg.InhibitionConfig{Rules: rules}
    if err := parser.Validate(inhibitionCfg); err != nil {
        return fmt.Errorf("inhibition rules validation failed: %w", err)
    }

    // Initialize two-tier cache (L1 in-memory + L2 Redis)
    r.inhibitionCache = inhibitionpkg.NewTwoTierAlertCache(r.cache, r.logger)

    // Initialize state manager (sync.Map + Redis)
    r.inhibitionState = inhibitionpkg.NewDefaultStateManager(r.cache, r.logger, r.metrics)

    // Initialize matcher
    r.inhibitionMatcher = inhibitionpkg.NewMatcher(r.inhibitionCache, inhibitionCfg.Rules, r.logger)

    r.logger.Info("✅ Inhibition subsystem initialized",
        "rules_count", len(inhibitionCfg.Rules))
    return nil
}
```

### 3.3 Обновление `Initialize` — порядок инициализации

```go
func (r *ServiceRegistry) Initialize(ctx context.Context) error {
    // ...existing steps...
    // После initializeCache/initializeRedis, ДО initializeAlertProcessor:
    if err := r.initializeInhibition(ctx); err != nil {
        r.logger.Warn("Inhibition initialization failed, continuing without inhibition", "error", err)
        r.degradedReasons = append(r.degradedReasons, "inhibition disabled: "+err.Error())
        // non-fatal: graceful degradation
    }
    // ...
    if err := r.initializeAlertProcessor(ctx); err != nil {
        return err
    }
}
```

### 3.4 Обновление `initializeAlertProcessor` — передать inhibition

```go
func (r *ServiceRegistry) initializeAlertProcessor(ctx context.Context) error {
    config := services.AlertProcessorConfig{
        FilterEngine:      r.filterEngine,
        LLMClient:         r.classificationSvc,
        Publisher:         r.publisher,
        Deduplication:     r.deduplicationSvc,
        BusinessMetrics:   r.metrics,
        Logger:            r.logger,
        Metrics:           nil, // TODO: MetricsManager
        InhibitionMatcher: r.inhibitionMatcher, // PARITY-A2: может быть nil (graceful degradation)
        InhibitionState:   r.inhibitionState,   // PARITY-A2: может быть nil
    }
    // ...
}
```

### 3.5 Обновление `Shutdown` — остановить inhibition cache

```go
func (r *ServiceRegistry) Shutdown(ctx context.Context) error {
    // ...existing...
    if r.inhibitionCache != nil {
        if stopper, ok := r.inhibitionCache.(interface{ Stop() }); ok {
            stopper.Stop()
        }
    }
}
```

---

## 4. Cache population — обновление при ingest

### 4.1 Где вызывать

Самое логичное место: **`alert_processor.go`**, в `ProcessAlert()`, после dedup (Step 0) и до inhibition check (Step 1).

Обновить `AlertProcessor` struct — добавить поле `inhibitionCache`:

```go
// В alert_processor.go:
type AlertProcessor struct {
    // ...existing fields...
    inhibitionCache   inhibition.ActiveAlertCache     // PARITY-A2: для обновления кэша
}

type AlertProcessorConfig struct {
    // ...existing fields...
    InhibitionCache   inhibition.ActiveAlertCache     // PARITY-A2: опционально
}
```

### 4.2 Логика обновления кэша в `ProcessAlert`

```go
// После dedup (Step 0), перед inhibition check (Step 1):

// PARITY-A2: Обновить кэш активных алертов
if p.inhibitionCache != nil {
    switch alert.Status {
    case core.StatusFiring:
        if err := p.inhibitionCache.AddFiringAlert(ctx, alert); err != nil {
            p.logger.Warn("Failed to add alert to inhibition cache", "error", err)
            // Non-critical: продолжаем обработку
        }
    case core.StatusResolved:
        if err := p.inhibitionCache.RemoveAlert(ctx, alert.Fingerprint); err != nil {
            p.logger.Warn("Failed to remove alert from inhibition cache", "error", err)
        }
        // Снять ингибицию с алертов, ингибированных данным source
        if p.inhibitionState != nil {
            r.cleanupInhibitionsForSource(ctx, alert.Fingerprint)
        }
    }
}
```

### 4.3 Вспомогательный метод `cleanupInhibitionsForSource`

```go
// cleanupInhibitionsForSource снимает ингибиции с target-алертов,
// ингибированных данным source-алертом (при его resolving).
func (p *AlertProcessor) cleanupInhibitionsForSource(ctx context.Context, sourceFingerprint string) {
    active, err := p.inhibitionState.GetActiveInhibitions(ctx)
    if err != nil {
        p.logger.Warn("Failed to get active inhibitions for cleanup", "error", err)
        return
    }
    for _, state := range active {
        if state.SourceFingerprint == sourceFingerprint {
            if err := p.inhibitionState.RemoveInhibition(ctx, state.TargetFingerprint); err != nil {
                p.logger.Warn("Failed to remove inhibition", 
                    "target", state.TargetFingerprint, "error", err)
            }
        }
    }
}
```

---

## 5. API: `GET /api/v2/inhibitions`

### 5.1 Response schema (Alertmanager parity)

```go
// GettableInhibitions — ответ GET /api/v2/inhibitions
type GettableInhibitions []GettableInhibition

type GettableInhibition struct {
    // ID ингибиции (composite: target_fingerprint)
    ID string `json:"id"`

    // TargetFingerprint — подавленный алерт
    TargetFingerprint string `json:"targetFingerprint"`

    // SourceFingerprint — алерт-источник, вызвавший ингибицию
    SourceFingerprint string `json:"sourceFingerprint"`

    // RuleName — имя сработавшего правила
    RuleName string `json:"ruleName"`

    // InhibitedAt — время начала ингибиции
    InhibitedAt time.Time `json:"inhibitedAt"`

    // ExpiresAt — время истечения (nil = бессрочно)
    ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
```

### 5.2 Handler

**Файл**: `go-app/internal/application/handlers/inhibitions.go` (новый)

```go
package handlers

import (
    "net/http"
    "encoding/json"
    "github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

type InhibitionsHandler struct {
    stateManager inhibition.InhibitionStateManager
}

func NewInhibitionsHandler(sm inhibition.InhibitionStateManager) *InhibitionsHandler {
    return &InhibitionsHandler{stateManager: sm}
}

// GET /api/v2/inhibitions
func (h *InhibitionsHandler) List(w http.ResponseWriter, r *http.Request) {
    if h.stateManager == nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode([]struct{}{})
        return
    }

    active, err := h.stateManager.GetActiveInhibitions(r.Context())
    if err != nil {
        http.Error(w, `{"error":"failed to get inhibitions"}`, http.StatusInternalServerError)
        return
    }

    result := make(GettableInhibitions, 0, len(active))
    for _, s := range active {
        result = append(result, GettableInhibition{
            ID:                s.TargetFingerprint,
            TargetFingerprint: s.TargetFingerprint,
            SourceFingerprint: s.SourceFingerprint,
            RuleName:          s.RuleName,
            InhibitedAt:       s.InhibitedAt,
            ExpiresAt:         s.ExpiresAt,
        })
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(result)
}
```

### 5.3 Регистрация route

**Файл**: `go-app/internal/application/router.go` — добавить:

```go
// GET /api/v2/inhibitions
if registry.InhibitionState() != nil {
    inhibitionsHandler := handlers.NewInhibitionsHandler(registry.InhibitionState())
    mux.HandleFunc("GET /api/v2/inhibitions", inhibitionsHandler.List)
}
```

Добавить accessor в `ServiceRegistry`:
```go
func (r *ServiceRegistry) InhibitionState() inhibition.InhibitionStateManager {
    return r.inhibitionState
}
```

---

## 6. Модели данных (повторное использование существующих)

Новых таблиц в PostgreSQL не требуется. State хранится в Redis (TTL = 24h по умолчанию).

Ключи Redis:
```
inhibition:state:{target_fingerprint}  → JSON(InhibitionState)
```

---

## 7. Prometheus метрики

Все метрики уже объявлены. После wiring будут собираться автоматически:

| Метрика | Labels | Описание |
|---|---|---|
| `alert_history_inhibition_checks_total` | `result={inhibited,allowed}` | Кол-во проверок |
| `alert_history_inhibition_matches_total` | `rule={rule_name}` | Кол-во совпадений по правилам |
| `alert_history_inhibition_duration_seconds` | `operation={check}` | Время проверки |
| `alert_history_inhibition_cache_hits_total` | `tier={l1,l2}` | Cache hits |
| `alert_history_inhibition_cache_misses_total` | `tier={l1,l2}` | Cache misses |
| `alert_history_inhibition_cache_size` | — | Алертов в L1 кэше |

Рекомендуемый Grafana запрос:
```promql
# Доля ингибированных алертов
rate(alert_history_inhibition_checks_total{result="inhibited"}[5m])
/
rate(alert_history_inhibition_checks_total[5m])
```

---

## 8. Архитектурные решения

### AD-1: Graceful degradation при отсутствии inhibition rules

**Решение**: если `inhibit_rules` не задан в конфиге — `r.inhibitionMatcher == nil`.
`alert_processor.go` уже проверяет `if p.inhibitionMatcher != nil` — pipeline продолжает работать без ингибиции.

**Почему**: не все deployments нуждаются в inhibition. Нет смысла требовать конфигурацию.

### AD-2: Cache population в `alert_processor.go`, не в handler

**Решение**: вызывать `AddFiringAlert` / `RemoveAlert` в `ProcessAlert()`, после dedup.

**Почему**: dedup может изменить алерт (обновить labels). Лучше класть в кэш уже дедуплицированный алерт. Обработчик HTTP (`alerts.go`) не должен знать о деталях inhibition.

### AD-3: Первый matching rule wins (не все правила)

**Решение**: `ShouldInhibit()` возвращает первый match (early exit). Для дебага — `FindInhibitors()`.

**Почему**: соответствует поведению Alertmanager; производительность важна (p99 < 500µs).

### AD-4: Inhibition check — после dedup, до classification

**Решение**: Step 1 в `ProcessAlert()` (текущая позиция в коде).

**Почему**: classification (LLM) дорогая операция (~100ms). Не тратить ресурсы на алерты, которые всё равно будут подавлены.

### AD-5: Нет персистентности inhibition state в PostgreSQL

**Решение**: Redis (TTL 24h) + in-memory (sync.Map). При рестарте сервиса — state сбрасывается.

**Почему**: inhibition state — ephemeral. При рестарте все алерты re-ingested → кэш и state пересоздаются из нового входящего трафика. PostgreSQL для этого избыточен.

---

## 9. Что НЕ входит в скоуп PARITY-A2

- CRUD API для управления inhibition rules (только GET /api/v2/inhibitions)
- Hot-reload правил без рестарта (это задача RELOADABLE-COMPONENT-INTERFACES)
- Кластеризация inhibition cache (это PARITY-C1)
- Inhibition через UI (это отдельная frontend-задача)
