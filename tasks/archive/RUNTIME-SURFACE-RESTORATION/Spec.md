# Spec: RUNTIME-SURFACE-RESTORATION

**Status**: Closed as restoration slice  
**Last Verified**: 2026-03-09  
**Implementation Outcome**: endpoints `status`, `receivers`, `alerts/groups` и `reload` смонтированы в active router и покрыты handler-level tests; pre-restoration `internal/application/router_contract_test.go` вынесен в отдельный follow-up bug вместо удержания этой задачи в WIP.

## Overview
Этот slice восстанавливает ключевые API-эндпоинты Alertmanager в активном рантайме AMP, обеспечивая совместимость с внешними инструментами (Grafana, amtool).

## API Endpoints

### 1. GET `/api/v2/status`
**Description**: Возвращает текущее состояние сервера, конфигурацию и информацию о версии.

**Response Schema (JSON)**:
```json
{
  "config.original": "YAML_CONTENT",
  "versionInfo": {
    "version": "0.0.1",
    "revision": "GIT_SHA",
    "branch": "GIT_BRANCH",
    "buildUser": "USER",
    "buildDate": "DATE",
    "goVersion": "go1.21.x"
  },
  "uptime": "2026-03-09T12:00:00Z"
}
```
**Implementation Details**:
- Текст конфига будет считываться из файла, указанного в `AMP_CONFIG_FILE`.
- Данные о версии будут браться из констант в `main.go`.

### 2. GET `/api/v2/receivers`
**Description**: Возвращает список всех настроенных получателей.

**Response Schema (JSON)**:
```json
[
  {
    "name": "pagerduty-critical"
  },
  {
    "name": "slack-warnings"
  }
]
```
**Implementation Details**:
- Список имен будет извлекаться из секции `receivers` (будет добавлена в `Config` или извлечена через `viper`).

### 3. GET `/api/v2/alerts/groups`
**Description**: Возвращает алерты, сгруппированные по заданным правилам.

**Response Schema (JSON)**:
```json
[
  {
    "labels": { "alertname": "HighCpu" },
    "receiver": { "name": "default" },
    "alerts": [ ... ]
  }
]
```
**Implementation Details**:
- Реализовать метод `GroupAlerts` в `AlertStore`, который группирует алерты по `group_by` лейблам.

### 4. POST `/-/reload`
**Description**: Инициирует горячую перезагрузку конфигурации.

**Behavior**:
- Вызывает `ReloadCoordinator.ReloadFromFile`.
- Возвращает `200 OK` при успехе.
- Возвращает `500 Internal Server Error` с деталями ошибки при неудаче.

## Changes in ServiceRegistry
- Добавить поле `startTime time.Time`.
- Добавить метод `ReloadConfig(ctx context.Context) error`.
- Добавить доступ к `ReloadCoordinator`.

## Non-Goals
- Полная поддержка `inhibit_rules` в этом slice.
- Полная реализация `silences` API v2 (они уже частично есть).

## Acceptance Criteria
1. `curl http://localhost:9093/api/v2/status` возвращает валидный JSON с YAML конфигом.
2. `curl -X POST http://localhost:9093/-/reload` успешно обновляет конфиг (проверить по логам).
3. Grafana успешно считывает группы алертов через `/api/v2/alerts/groups`.
4. Если active router contract tests еще не обновлены, это явно оформлено как отдельный follow-up, а не скрыто внутри “почти закрытой” задачи.
