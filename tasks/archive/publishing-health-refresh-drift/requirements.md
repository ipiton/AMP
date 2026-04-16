# PUBLISHING-HEALTH-REFRESH-DRIFT — Requirements

## Контекст

После stabilization pass (`REPO-TEST-MATRIX-RED`) пакет `internal/business/publishing`
содержит тесты, которые падают вне sandbox на **logic-level assertions** — не на
инфраструктурных panic/fixture проблемах.

Источник: `docs/06-planning/BUGS.md`, open item.

## Описание проблемы

### Конкретные падающие категории

| Категория | Примеры тестов |
|-----------|---------------|
| HealthMonitor lifecycle/logic | `TestHealthMonitor_*` |
| Error message sanitization | `TestSanitizeErrorMessage` |
| Refresh worker logic | `refresh_*` тесты |
| Error classification | `dns`/`network`/`unknown` случаи |
| Metric count assertions | ожидания по числу метрик |
| Timing-sensitive transitions | `degraded` → `unhealthy` переходы |

### Корень проблемы: drift между health и refresh

Система имеет **три независимых цикла** с разными интервалами, которые не синхронизируются:

1. **Discovery refresh** (RefreshManager) — обновляет список targets из K8s Secrets, интервал 5m
2. **Health checks** (HealthMonitor) — проверяет HTTP-доступность targets, интервал 2m
3. **Status cache** (healthStatusCache) — хранит результаты проверок, TTL 10m

Когда список targets меняется (добавление/удаление), кэши этих трёх систем
расходятся: health-cache содержит orphaned entries для удалённых targets, новые targets
не имеют статуса, а метрики дают неверный count.

### Конкретные drift-точки

**1. Orphaned entries в health cache**
После удаления target из K8s Secret:
- `targetCache.Set(newTargets)` — старый target исчез из discovery
- `statusCache` — старый entry живёт ещё до 10 минут (maxAge)
- `GetStats()` → `statusCache.GetAll()` → включает orphaned entry → TotalTargets неверно

**2. Отсутствие инвалидации при refresh**
`RefreshManager.updateState()` не уведомляет `HealthMonitor` об изменениях в targets.
Health продолжает работать с устаревшим представлением о том, какие targets существуют.

**3. Race condition при параллельном refresh + health check**
`checkAllTargets()` вызывает `discoveryMgr.ListTargets()` → получает snapshot, затем
выполняет HTTP-проверки (~300ms+ на target). Пока checks выполняются, refresh может
полностью заменить `targetCache`. Результаты health checks записываются в `statusCache`
для уже-удалённых targets.

**4. Timing-sensitive state transitions**
`HealthStatusDegraded` → `HealthStatusUnhealthy` переход зависит от
`ConsecutiveFailures >= FailureThreshold`. Тесты с жёсткими time.Sleep ненадёжны:
при медленном CI warmup/check interval дают иной порядок событий.

**5. sanitizeErrorMessage — несоответствие ожиданий теста**
Тест `TestSanitizeErrorMessage` ожидает конкретный формат `[REDACTED]`, но текущая
реализация может давать лишний пробел (`= [REDACTED]` vs `=[REDACTED]`).

**6. Error classification gaps**
`classifyNetworkError`: специфичные ошибки macOS/Linux могут не матчиться на
string-based классификацию. `classifyHTTPError`: wrapped errors через `errors.Unwrap`
не обрабатываются.

## Success Criteria

1. `cd go-app && go test ./internal/business/publishing/... -count=1` — **green** без sandbox
2. Все `TestHealthMonitor_*` тесты проходят
3. `TestSanitizeErrorMessage` — все случаи корректны
4. Refresh и health tests не имеют flaky timing assertions
5. Metric count assertions верны: `GetStats().TotalTargets` отражает актуальный discovery state
6. Orphaned entries не появляются в GetStats/GetHealth после удаления targets из discovery

## Scope

**В scope:**
- Исправление logic-level багов в `internal/business/publishing`
- Синхронизация health cache с discovery state (invalidation при refresh)
- Фикс `sanitizeErrorMessage` для корректного вывода
- Фикс или защита timing-sensitive тестов (fake clock или generous timeout)
- Error classification gaps (wrapped errors)

**Вне scope:**
- Изменение публичных API (интерфейсы `HealthMonitor`, `RefreshManager`)
- Рефакторинг beyond bug fixes
- Runtime handler changes
- Helm/deployment changes

## Ограничения

- Не нарушать контракты интерфейсов `HealthMonitor`, `RefreshManager`, `TargetDiscoveryManager`
- Не добавлять новые публичные типы без необходимости
- Изменения должны быть безопасны для concurrent use (RWMutex паттерны сохраняются)
