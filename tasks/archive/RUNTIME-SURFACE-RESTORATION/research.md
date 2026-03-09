# Research: RUNTIME-SURFACE-RESTORATION

## Findings

### Current State of API Handlers
- **Health/Status**: `go-app/internal/application/handlers/status.go` содержит только эндпоинты проверки работоспособности (`/health`, `/ready`). Стандартный Alertmanager `/api/v2/status` отсутствует.
- **Alerts**: `go-app/internal/application/handlers/alerts.go` реализует базовые `GET` и `POST` для `/api/v2/alerts`. Группировка (`/api/v2/alerts/groups`) не реализована.
- **Receivers**: Эндпоинт `/api/v2/receivers` полностью отсутствует.
- **Reload**: Эндпоинт `/-/reload` полностью отсутствует.

### ServiceRegistry Capabilities
- `ServiceRegistry` имеет доступ к `appconfig.Config`.
- Конфигурация содержит информацию о ресиверах, которую можно использовать для `/api/v2/receivers`.
- В `futureparity_compat.go` есть функция `configSHA256`, которую можно использовать для формирования ответа `/api/v2/status`.

### Gap Analysis
- Нам не хватает структуры данных для ответа `/api/v2/status`, совместимой с Alertmanager.
- Нужно реализовать логику группировки алертов для `/api/v2/alerts/groups`.
- Для `/-/reload` требуется механизм уведомления сервисов об изменении конфигурации (Reload Coordinator).

## Recommendations

1.  **Status API**: Реализовать `StatusHandler` в новом файле или дополнить `status.go`. Ответ должен включать `config.original`, `versionInfo` и `uptime`.
2.  **Receivers API**: Создать хендлер, который извлекает список ресиверов из `config.Route`.
3.  **Alert Groups API**: Реализовать логику группировки в `memory.AlertStore` или непосредственно в хендлере.
4.  **Reload API**: 
    - Добавить в `ServiceRegistry` метод `ReloadConfig(ctx)`.
    - Реализовать `POST /-/reload`, который вызывает этот метод.
    - Использовать `ReloadCoordinator` (если он есть) или реализовать простой механизм обновления.

## Next Steps
- Перейти к созданию `Spec.md` для фиксации форматов ответов и поведения.
- Подготовить инкрементальный план реализации.
