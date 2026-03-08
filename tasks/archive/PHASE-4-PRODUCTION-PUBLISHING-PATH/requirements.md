# Requirements: PHASE-4-PRODUCTION-PUBLISHING-PATH

## Context
Активный bootstrap в `go-app/internal/application/service_registry.go` все еще поднимает `services.NewSimplePublisher(...)` со статусом `STUB - development only`, а `initializeBusinessServices()` пока не подключает production publishing stack. При этом в репозитории уже есть реальная инфраструктура публикации (`internal/infrastructure/publishing`, `internal/business/publishing`): discovery manager, queue, mode manager, health monitor, stats и HTTP handlers. Сейчас между доступной инфраструктурой и активным runtime есть разрыв: runtime выглядит готовым к доставке, но фактически работает через заглушку.

## Goals
- [x] Убрать `SimplePublisher` из активного production bootstrap-пути.
- [x] Подключить реальный publishing stack в `ServiceRegistry` и runtime bootstrap.
- [x] Сохранить предсказуемую деградацию в `metrics-only`/safe fallback режиме, если targets или зависимости недоступны.
- [x] Обеспечить наблюдаемость: publishing mode, health и metrics должны отражать реальное состояние доставки.

## Constraints
- Перед реализацией нужен отдельный `/spec`: задача затрагивает bootstrap, конфигурационный контракт и внешнюю доставку.
- Нельзя ломать текущие core API/runtime path и режим локальной разработки без production targets.
- Нельзя хардкодить секреты, targets или production-specific значения; использовать существующие config/Kubernetes discovery-паттерны.
- Поведение retries, timeouts и rate limits должно оставаться ограниченным и наблюдаемым.

## Success Criteria (Definition of Done)
- [x] Активный runtime больше не использует `services.NewSimplePublisher(...)` в production path.
- [x] Реальные publishing components инициализируются и корректно останавливаются через `ServiceRegistry`.
- [x] При отсутствии targets или деградации зависимостей runtime не имитирует успешную доставку через stub.
- [x] Dashboard/health/metrics показывают фактическое состояние publishing path.
- [ ] Добавлены тесты на bootstrap/integration path для выбранного production publishing режима.

## Final Status

Задача закрыта как выполненный production slice с зафиксированными внешними blockers.

Реально доставлено:
- active runtime переведен на real publishing path через adapter/coordinator/queue;
- typed `publishing.*` config и Helm/runtime env contract выровнены;
- canonical Kubernetes Secret contract для target discovery внедрен;
- dashboard/publishing stats отражают реальный mode/queue/discovery state.

Незакрытый остаток:
- отдельные integration tests для `ServiceRegistry` в режимах `normal` и `metrics-only` не добавлены;
- full repo quality gate перед `/end-task` остается red на preexisting проблемах вне scope задачи и зафиксирован в planning artifacts.
